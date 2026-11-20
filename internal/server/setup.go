package server

import (
	"crypto/subtle"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// First-run setup: a fresh install has no admin. The installer prints a one-time
// link with a secret token; whoever opens it chooses the owner's username and
// password. Without the token nobody can claim the panel, and the token is
// deleted as soon as the owner exists.

var setupMu sync.Mutex

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{3,32}$`)

// SetupPending tells whether the owner account still has to be created.
func (s *Server) SetupPending() bool {
	return s.St.GetSetting("setup_token", "") != "" && s.St.AdminCount() == 0
}

func (s *Server) setupState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"pending": s.SetupPending()})
}

func (s *Server) firstSetup(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.tooManyFailures(ip) {
		fail(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	var in struct{ Token, Username, Password string }
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	setupMu.Lock()
	defer setupMu.Unlock()
	token := s.St.GetSetting("setup_token", "")
	if token == "" || s.St.AdminCount() > 0 {
		fail(w, http.StatusConflict, "setup is already done; sign in instead")
		return
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(in.Token)), []byte(token)) != 1 {
		s.recordFailure(ip)
		s.logFailure(ip, "setup", "wrong setup link")
		time.Sleep(600 * time.Millisecond)
		fail(w, http.StatusForbidden, "this setup link is not valid; run `dunvex info` on the server for the right one")
		return
	}
	name := strings.TrimSpace(in.Username)
	if !usernameRe.MatchString(name) {
		fail(w, 400, "username: 3 to 32 letters, digits, dots, dashes or underscores")
		return
	}
	if len(in.Password) < 8 {
		fail(w, 400, "password: at least 8 characters")
		return
	}
	if strings.EqualFold(in.Password, name) {
		fail(w, 400, "the password must not be the username")
		return
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err := s.St.UpsertAdmin(name, hash); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.St.SetSetting("setup_token", "")
	a, err := s.St.AdminByName(name)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.issueSession(w, r, a)
	s.St.Log("auth", "owner account %s created from %s", name, ip)
	writeJSON(w, 200, a)
}
