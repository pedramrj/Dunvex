package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/bot"
	"github.com/pedramrj/dunvex/internal/store"
)

// Support inbox: what people write to the bot's support, one thread per
// Telegram chat, and the answers that go back through the bot (see
// bot/support.go). The owner and admins see every thread; resellers only
// their own users'.

// Supporter is the bot's support side.
type Supporter interface {
	SupportReply(thread int64, by, text string, photo []byte) (*store.SupportMessage, error)
	SupportReact(chat int64, msgs []int64, emoji string)
	SupportTyping(thread int64)
}

// SupportDir is where support photos are kept.
func (s *Server) SupportDir() string { return filepath.Join(s.Home, "support") }

func (s *Server) supporter() Supporter {
	if sp, ok := s.Bot.(Supporter); ok && s.Bot != nil {
		return sp
	}
	return nil
}

// supportUser is the person's subscription beside the thread.
type supportUser struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Online    bool   `json:"online"`
	Used      int64  `json:"used"`
	DataLimit int64  `json:"dataLimit"`
	ExpireAt  int64  `json:"expireAt"`
	DaysLeft  *int   `json:"daysLeft"`
	Devices   int    `json:"devices"`
	IPLimit   int    `json:"ipLimit"`
	Note      string `json:"note"`
	Owner     string `json:"owner,omitempty"`
}

type threadView struct {
	*store.SupportThread
	Waiting bool         `json:"waiting"`
	User    *supportUser `json:"user"` // nil: no subscription (a guest) or it was deleted
}

func (s *Server) threadView(r *http.Request, th *store.SupportThread, online map[int64]bool, owners map[int64]string) threadView {
	v := threadView{SupportThread: th, Waiting: th.Waiting()}
	if th.UserID == 0 {
		return v
	}
	u, err := s.St.UserByID(th.UserID)
	if err != nil {
		return v
	}
	uv := s.view(r, u, online)
	v.User = &supportUser{ID: u.ID, Name: u.Name, Status: uv.Status, Online: uv.Online, Used: uv.Used, DataLimit: u.DataLimit,
		ExpireAt: u.ExpireAt, DaysLeft: uv.DaysLeft, Devices: uv.Devices, IPLimit: u.IPLimit, Note: u.Note, Owner: owners[u.OwnerID]}
	return v
}

func (s *Server) adminNames(a *store.Admin) map[int64]string {
	out := map[int64]string{}
	if a.Role == store.RoleReseller {
		return out
	}
	admins, _ := s.St.Admins()
	for _, x := range admins {
		if x.Role == store.RoleReseller {
			out[x.ID] = x.Username
		}
	}
	return out
}

// supportThreadFor loads the thread in the path, if the admin may see it.
func (s *Server) supportThreadFor(w http.ResponseWriter, r *http.Request) *store.SupportThread {
	th, err := s.St.SupportThreadByID(pathID(r))
	if err == nil {
		if owner := broadcastOwner(adminOf(r)); owner == 0 || s.St.SupportOwnerOf(th) == owner {
			return th
		}
	}
	fail(w, 404, "conversation not found")
	return nil
}

func (s *Server) botInfo() map[string]any {
	if s.Bot == nil {
		return map[string]any{"configured": false}
	}
	st := s.Bot.Status()
	return map[string]any{"configured": st["configured"], "running": st["running"], "username": st["username"]}
}

// getSupport lists the threads (?q= the person, ?status=open|waiting|closed).
func (s *Server) getSupport(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	owner := broadcastOwner(a)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.St.SupportThreads(store.SupportFilter{Owner: owner, Q: r.URL.Query().Get("q"), Status: r.URL.Query().Get("status"), Limit: limit})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	online, owners := s.Mgr.OnlineUsers(60), s.adminNames(a)
	out := make([]threadView, 0, len(list))
	for _, th := range list {
		out = append(out, s.threadView(r, th, online, owners))
	}
	set := bot.LoadSupport(s.St)
	writeJSON(w, 200, map[string]any{"threads": out, "counts": s.St.SupportCount(owner), "bot": s.botInfo(),
		"on": set.On, "maxAnswer": bot.MaxSupportAnswer})
}

// supportUnread is the menu badge: threads with something nobody opened.
func (s *Server) supportUnread(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.St.SupportCount(broadcastOwner(adminOf(r))))
}

// getSupportThread is one thread and its messages (?before= a message id for older ones).
func (s *Server) getSupportThread(w http.ResponseWriter, r *http.Request) {
	th := s.supportThreadFor(w, r)
	if th == nil {
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	msgs, more, err := s.St.SupportMessages(th.ID, before, 80)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	a := adminOf(r)
	writeJSON(w, 200, map[string]any{"thread": s.threadView(r, th, s.Mgr.OnlineUsers(60), s.adminNames(a)), "messages": msgs, "more": more})
}

// seenSupport marks the person's messages seen (they get 👀 in Telegram).
func (s *Server) seenSupport(w http.ResponseWriter, r *http.Request) {
	th := s.supportThreadFor(w, r)
	if th == nil {
		return
	}
	ids := s.St.SupportMarkSeen(th.ID)
	if sp := s.supporter(); sp != nil && len(ids) > 0 {
		go sp.SupportReact(th.Chat, ids, "👀")
	}
	writeJSON(w, 200, map[string]int{"seen": len(ids)})
}

// replySupport sends an answer: JSON {"text"} or a form with text and a photo.
func (s *Server) replySupport(w http.ResponseWriter, r *http.Request) {
	th := s.supportThreadFor(w, r)
	if th == nil {
		return
	}
	sp := s.supporter()
	if sp == nil {
		fail(w, 409, "the Telegram bot is not available")
		return
	}
	var text string
	var photo []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
		if err := r.ParseMultipartForm(6 << 20); err != nil {
			fail(w, 400, "the photo is larger than 5 MB")
			return
		}
		text = r.FormValue("text")
		if f, _, err := r.FormFile("photo"); err == nil {
			photo, err = io.ReadAll(io.LimitReader(f, 5<<20+1))
			f.Close()
			if err != nil || len(photo) > 5<<20 {
				fail(w, 400, "the photo is larger than 5 MB")
				return
			}
			if bot.SupportPhotoType(photo) == "" {
				fail(w, 400, "only JPEG, PNG, WebP and GIF pictures")
				return
			}
		}
	} else {
		var in struct {
			Text string `json:"text"`
		}
		if err := readJSON(r, &in); err != nil {
			fail(w, 400, "bad request")
			return
		}
		text = in.Text
	}
	msg, err := sp.SupportReply(th.ID, adminOf(r).Username, text, photo)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	th, _ = s.St.SupportThreadByID(th.ID)
	writeJSON(w, 200, map[string]any{"message": msg, "thread": s.threadView(r, th, s.Mgr.OnlineUsers(60), s.adminNames(adminOf(r)))})
}

// typingSupport shows "typing…" to the person while an admin writes.
func (s *Server) typingSupport(w http.ResponseWriter, r *http.Request) {
	th := s.supportThreadFor(w, r)
	if th == nil {
		return
	}
	if sp := s.supporter(); sp != nil {
		go sp.SupportTyping(th.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// putSupportThread closes a thread or opens it again: {"open": false}.
func (s *Server) putSupportThread(w http.ResponseWriter, r *http.Request) {
	th := s.supportThreadFor(w, r)
	if th == nil {
		return
	}
	var in struct {
		Open *bool `json:"open"`
	}
	if err := readJSON(r, &in); err != nil || in.Open == nil {
		fail(w, 400, `send {"open": true} or {"open": false}`)
		return
	}
	if err := s.St.SetSupportOpen(th.ID, *in.Open, adminOf(r).Username, time.Now().Unix()); err != nil {
		fail(w, 500, err.Error())
		return
	}
	th, _ = s.St.SupportThreadByID(th.ID)
	writeJSON(w, 200, s.threadView(r, th, s.Mgr.OnlineUsers(60), s.adminNames(adminOf(r))))
}

// deleteSupportThread removes a thread, its messages and photos.
func (s *Server) deleteSupportThread(w http.ResponseWriter, r *http.Request) {
	th := s.supportThreadFor(w, r)
	if th == nil {
		return
	}
	photos, err := s.St.DeleteSupportThread(th.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	for _, p := range photos {
		os.Remove(filepath.Join(s.SupportDir(), filepath.Base(p)))
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// supportPhoto serves a photo of a thread the admin may see.
func (s *Server) supportPhoto(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || filepath.Base(name) != name || strings.HasPrefix(name, ".") {
		fail(w, 404, "no photo")
		return
	}
	tid, err := s.St.SupportPhotoThread(name)
	if err == nil {
		var th *store.SupportThread
		if th, err = s.St.SupportThreadByID(tid); err == nil {
			if owner := broadcastOwner(adminOf(r)); owner != 0 && s.St.SupportOwnerOf(th) != owner {
				err = store.ErrNotFound
			}
		}
	}
	if err != nil {
		fail(w, 404, "no photo")
		return
	}
	data, err := os.ReadFile(filepath.Join(s.SupportDir(), name))
	if err != nil {
		fail(w, 404, "the photo file is missing")
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Write(data)
}

func (s *Server) getSupportSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"settings": bot.LoadSupport(s.St), "bot": s.botInfo()})
}

func (s *Server) putSupportSettings(w http.ResponseWriter, r *http.Request) {
	var in bot.SupportSettings
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	if err := in.Check(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	bot.SaveSupport(s.St, in)
	s.St.Log("settings", "%s changed the support inbox (%s)", adminOf(r).Username, map[bool]string{true: "on", false: "off"}[in.On])
	s.getSupportSettings(w, r)
}
