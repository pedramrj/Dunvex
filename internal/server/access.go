package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// API keys look like "dvx_" + 40 random characters. Only their SHA-256 is stored.
const keyPrefix = "dvx_"

const maxKeysPerAdmin = 20

func keyHash(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// deviceName turns a user agent into "Chrome on Windows".
func deviceName(ua string) string {
	l := strings.ToLower(ua)
	browser := "Browser"
	switch {
	case ua == "":
		return "Unknown device"
	case strings.Contains(l, "curl/"), strings.Contains(l, "python"), strings.Contains(l, "go-http-client"), strings.Contains(l, "wget"):
		return "Script"
	case strings.Contains(l, "edg/"):
		browser = "Edge"
	case strings.Contains(l, "opr/"), strings.Contains(l, "opera"):
		browser = "Opera"
	case strings.Contains(l, "firefox/"), strings.Contains(l, "fxios"):
		browser = "Firefox"
	case strings.Contains(l, "chrome/"), strings.Contains(l, "crios"):
		browser = "Chrome"
	case strings.Contains(l, "safari/"):
		browser = "Safari"
	}
	os := ""
	switch {
	case strings.Contains(l, "android"):
		os = "Android"
	case strings.Contains(l, "iphone"), strings.Contains(l, "ipad"):
		os = "iOS"
	case strings.Contains(l, "windows"):
		os = "Windows"
	case strings.Contains(l, "mac os"), strings.Contains(l, "macintosh"):
		os = "macOS"
	case strings.Contains(l, "linux"):
		os = "Linux"
	}
	if os == "" {
		return browser
	}
	return browser + " on " + os
}

type sessionView struct {
	*store.Session
	Device  string `json:"device"`
	Current bool   `json:"current"`
}

// listSessions: the owner sees every admin's sessions, others their own.
func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	scope := a.ID
	if a.Role == store.RoleOwner {
		scope = 0
	}
	list, err := s.St.Sessions(scope, time.Now().Unix())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	cur := sessionOf(r)
	out := make([]sessionView, 0, len(list))
	for _, x := range list {
		out = append(out, sessionView{x, deviceName(x.Agent), x.ID == cur})
	}
	writeJSON(w, 200, out)
}

func (s *Server) endSession(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	x, err := s.St.SessionByID(r.PathValue("id"))
	if err != nil || (x.AdminID != a.ID && a.Role != store.RoleOwner) {
		fail(w, 404, "session not found")
		return
	}
	s.St.DeleteSession(x.ID)
	if x.AdminID == a.ID {
		s.St.Log("auth", "%s signed out a session (%s, %s)", a.Username, deviceName(x.Agent), x.IP)
	} else {
		s.St.Log("auth", "%s signed out a session of %s (%s, %s)", a.Username, x.Admin, deviceName(x.Agent), x.IP)
	}
	writeJSON(w, 200, map[string]bool{"ok": true, "self": x.ID == sessionOf(r)})
}

func (s *Server) endOtherSessions(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	n, err := s.St.DeleteSessions(a.ID, sessionOf(r))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if n > 0 {
		s.St.Log("auth", "%s signed out %d other session(s)", a.Username, n)
	}
	writeJSON(w, 200, map[string]int64{"ended": n})
}

// listKeys: the owner sees every key, others their own.
func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	scope := a.ID
	if a.Role == store.RoleOwner {
		scope = 0
	}
	list, err := s.St.APIKeys(scope)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	var in struct {
		Name     string `json:"name"`
		ReadOnly bool   `json:"readOnly"`
		Days     int    `json:"days"` // 0 = never expires
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 40 {
		fail(w, 400, "give the key a name of up to 40 characters")
		return
	}
	if in.Days < 0 || in.Days > 3650 {
		fail(w, 400, "expiry must be between 0 and 3650 days")
		return
	}
	if mine, _ := s.St.APIKeys(a.ID); len(mine) >= maxKeysPerAdmin {
		fail(w, 400, "you already have "+strconv.Itoa(maxKeysPerAdmin)+" keys; delete one first")
		return
	}
	now := time.Now().Unix()
	tok := keyPrefix + util.RandomString(40)
	k := &store.APIKey{AdminID: a.ID, Admin: a.Username, Name: in.Name, Prefix: tok[:10], Hash: keyHash(tok),
		ReadOnly: in.ReadOnly, CreatedAt: now}
	if in.Days > 0 {
		k.ExpiresAt = now + int64(in.Days)*86400
	}
	if err := s.St.CreateAPIKey(k); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.St.Log("auth", "%s created the API key %q", a.Username, k.Name)
	// the only time the key itself leaves the server
	writeJSON(w, 200, map[string]any{"key": k, "token": tok})
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	k, err := s.St.APIKeyByID(pathID(r))
	if err != nil || (k.AdminID != a.ID && a.Role != store.RoleOwner) {
		fail(w, 404, "key not found")
		return
	}
	s.St.DeleteAPIKey(k.ID)
	s.St.Log("auth", "%s deleted the API key %q", a.Username, k.Name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
