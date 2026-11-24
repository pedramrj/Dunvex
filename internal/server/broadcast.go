package server

import (
	"net/http"
	"strconv"

	"github.com/pedramrj/dunvex/internal/bot"
	"github.com/pedramrj/dunvex/internal/store"
)

// Broadcast: a message from the panel to users through the Telegram bot (see
// bot/broadcast.go). Resellers reach only their own users and see only their
// own messages.

// Broadcaster is the bot's broadcasting side.
type Broadcaster interface {
	AudienceCounts(owner int64, days int) map[string]bot.Count
	SelectedCount(owner int64, ids []int64) bot.Count
	TestBroadcast(r bot.BroadcastRequest) (int, error)
	StartBroadcast(r bot.BroadcastRequest) (*bot.Broadcast, error)
	CancelBroadcast(id int64) bool
	Broadcasts(owner int64) []bot.Broadcast
}

// broadcastOwner is the reseller whose users a request may reach (0 = all).
func broadcastOwner(a *store.Admin) int64 {
	if a.Role == store.RoleReseller {
		return a.ID
	}
	return 0
}

func (s *Server) broadcaster(w http.ResponseWriter) Broadcaster {
	if bc, ok := s.Bot.(Broadcaster); ok && s.Bot != nil {
		return bc
	}
	fail(w, 409, "the Telegram bot is not available")
	return nil
}

func (s *Server) getBroadcasts(w http.ResponseWriter, r *http.Request) {
	bc := s.broadcaster(w)
	if bc == nil {
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	owner := broadcastOwner(adminOf(r))
	out := map[string]any{"counts": bc.AudienceCounts(owner, days), "history": bc.Broadcasts(owner),
		"userBot": s.St.GetSetting("tg_user_bot", "true") == "true"}
	if s.Bot != nil {
		st := s.Bot.Status()
		out["bot"] = map[string]any{"configured": st["configured"], "running": st["running"], "username": st["username"], "admins": st["admins"]}
	}
	writeJSON(w, 200, out)
}

func (s *Server) countBroadcast(w http.ResponseWriter, r *http.Request) {
	bc := s.broadcaster(w)
	if bc == nil {
		return
	}
	var in struct {
		UserIDs []int64 `json:"userIds"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	writeJSON(w, 200, bc.SelectedCount(broadcastOwner(adminOf(r)), in.UserIDs))
}

func (s *Server) postBroadcast(w http.ResponseWriter, r *http.Request) {
	bc := s.broadcaster(w)
	if bc == nil {
		return
	}
	var in struct {
		bot.BroadcastRequest
		Test bool `json:"test"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	a := adminOf(r)
	req := in.BroadcastRequest
	req.By, req.Owner = a.Username, broadcastOwner(a)
	if in.Test {
		if a.Role == store.RoleReseller {
			fail(w, 403, "test messages go to the bot's admins; send it to one of your users instead")
			return
		}
		n, err := bc.TestBroadcast(req)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]int{"sent": n})
		return
	}
	run, err := bc.StartBroadcast(req)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 202, run)
}

func (s *Server) cancelBroadcast(w http.ResponseWriter, r *http.Request) {
	bc := s.broadcaster(w)
	if bc == nil {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	owner := broadcastOwner(adminOf(r))
	mine := false
	for _, x := range bc.Broadcasts(owner) {
		mine = mine || x.ID == id
	}
	if !mine || !bc.CancelBroadcast(id) {
		fail(w, 404, "no message is being sent with that id")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
