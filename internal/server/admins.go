package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

type ctxKey struct{}

// sessionKey carries the request's session id (empty for API keys).
type sessionKey struct{}

func sessionOf(r *http.Request) string {
	s, _ := r.Context().Value(sessionKey{}).(string)
	return s
}

// adminOf returns the signed-in admin of an authenticated request.
func adminOf(r *http.Request) *store.Admin {
	a, _ := r.Context().Value(ctxKey{}).(*store.Admin)
	return a
}

// need wraps a handler with login, role and CSRF checks.
func (s *Server) need(role string, h handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, sid := s.cookieAdmin(r)
		var key *store.APIKey
		if a == nil {
			a, key = s.keyAdmin(r)
		}
		if a == nil {
			fail(w, http.StatusUnauthorized, "login required")
			return
		}
		if !a.Can(role) {
			fail(w, http.StatusForbidden, "your role cannot do this")
			return
		}
		if key != nil {
			// a key works the panel, but cannot change who may sign in
			// (a restore would replace the admins too)
			if strings.HasPrefix(r.URL.Path, "/account") || r.URL.Path == "/logout" || strings.HasPrefix(r.URL.Path, "/admins") || r.URL.Path == "/restore" {
				fail(w, http.StatusForbidden, "API keys cannot manage accounts, sessions, keys or restores")
				return
			}
			if key.ReadOnly && r.Method != http.MethodGet {
				fail(w, http.StatusForbidden, "this API key is read-only")
				return
			}
		} else if r.Method != http.MethodGet && r.Header.Get("X-Dunvex") != "1" {
			fail(w, http.StatusForbidden, "missing request header")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, a)
		h(w, r.WithContext(context.WithValue(ctx, sessionKey{}, sid)))
	})
}

// owns reports whether the admin may manage the user.
func owns(a *store.Admin, u *store.User) bool {
	return a.Role != store.RoleReseller || u.OwnerID == a.ID
}

// scopedUsers returns the users an admin may see.
func (s *Server) scopedUsers(a *store.Admin) ([]*store.User, error) {
	users, err := s.St.Users()
	if err != nil || a.Role != store.RoleReseller {
		return users, err
	}
	out := users[:0:0]
	for _, u := range users {
		if u.OwnerID == a.ID {
			out = append(out, u)
		}
	}
	return out, nil
}

// userFor loads a path user the admin may manage, or writes 404.
func (s *Server) userFor(w http.ResponseWriter, r *http.Request) *store.User {
	u, err := s.St.UserByID(pathID(r))
	if err != nil || !owns(adminOf(r), u) {
		fail(w, 404, "user not found")
		return nil
	}
	return u
}

// Quota is a reseller's allowance and what is already handed out.
type Quota struct {
	Users     int   `json:"users"`
	MaxUsers  int   `json:"maxUsers"`
	Allocated int64 `json:"allocated"`
	MaxData   int64 `json:"maxData"`
}

func (s *Server) quota(a *store.Admin, except int64) Quota {
	q := Quota{MaxUsers: a.MaxUsers, MaxData: a.MaxData}
	users, _ := s.St.Users()
	for _, u := range users {
		if u.OwnerID != a.ID || u.ID == except {
			continue
		}
		q.Users++
		q.Allocated += u.DataLimit
	}
	return q
}

// checkQuota refuses a reseller change that goes over the allowance:
// add new users, each with dataLimit bytes, replacing user `except`.
func (s *Server) checkQuota(a *store.Admin, add int, dataLimit int64, except int64) error {
	if a.Role != store.RoleReseller {
		return nil
	}
	q := s.quota(a, except)
	if q.MaxUsers > 0 && q.Users+add > q.MaxUsers {
		return fmt.Errorf("your quota allows %d users; you have %d", q.MaxUsers, q.Users)
	}
	if q.MaxData > 0 {
		if dataLimit <= 0 {
			return fmt.Errorf("set a data limit: your quota does not allow unlimited users")
		}
		if q.Allocated+int64(add)*dataLimit > q.MaxData {
			return fmt.Errorf("not enough data left in your quota (%s free)", util.HumanBytes(q.MaxData-q.Allocated))
		}
	}
	return nil
}

// ---------------------------------------------------------------- admins (owner)

type adminView struct {
	*store.Admin
	UserCount int   `json:"userCount"`
	Allocated int64 `json:"allocated"`
}

func (s *Server) listAdmins(w http.ResponseWriter, r *http.Request) {
	admins, err := s.St.Admins()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	out := []adminView{}
	for _, a := range admins {
		q := s.quota(a, 0)
		out = append(out, adminView{Admin: a, UserCount: q.Users, Allocated: q.Allocated})
	}
	writeJSON(w, 200, out)
}

type adminInput struct {
	Username  *string `json:"username"`
	Password  *string `json:"password"`
	Role      *string `json:"role"`
	MaxUsers  *int    `json:"maxUsers"`
	MaxData   *int64  `json:"maxData"`
	Enabled   *bool   `json:"enabled"`
	ResetTOTP bool    `json:"resetTotp"`
}

func (in *adminInput) apply(a *store.Admin) error {
	if in.Username != nil {
		n := util.SanitizeName(*in.Username)
		if n == "" {
			return fmt.Errorf("username may only contain letters, digits, - _ .")
		}
		a.Username = n
	}
	if in.Password != nil && *in.Password != "" {
		if len(*in.Password) < 8 {
			return fmt.Errorf("the password needs at least 8 characters")
		}
		a.PasswordHash, _ = HashPassword(*in.Password)
	}
	if in.Role != nil {
		switch *in.Role {
		case store.RoleOwner, store.RoleAdmin, store.RoleReseller:
			a.Role = *in.Role
		default:
			return fmt.Errorf("unknown role")
		}
	}
	if in.MaxUsers != nil {
		if *in.MaxUsers < 0 {
			return fmt.Errorf("max users cannot be negative")
		}
		a.MaxUsers = *in.MaxUsers
	}
	if in.MaxData != nil {
		if *in.MaxData < 0 {
			return fmt.Errorf("max data cannot be negative")
		}
		a.MaxData = *in.MaxData
	}
	if in.Enabled != nil {
		a.Enabled = *in.Enabled
	}
	if in.ResetTOTP {
		a.TOTPSecret = ""
	}
	return nil
}

func (s *Server) createAdmin(w http.ResponseWriter, r *http.Request) {
	var in adminInput
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	if in.Username == nil || in.Password == nil || *in.Password == "" {
		fail(w, 400, "username and password are required")
		return
	}
	a := &store.Admin{Role: store.RoleReseller, Enabled: true}
	if err := in.apply(a); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := s.St.CreateAdmin(a); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.St.Log("auth", "%s added admin %s (%s)", adminOf(r).Username, a.Username, a.Role)
	writeJSON(w, 200, a)
}

func (s *Server) lastOwner(id int64) bool {
	admins, _ := s.St.Admins()
	n := 0
	for _, a := range admins {
		if a.Role == store.RoleOwner && a.Enabled && a.ID != id {
			n++
		}
	}
	return n == 0
}

func (s *Server) updateAdminH(w http.ResponseWriter, r *http.Request) {
	a, err := s.St.AdminByID(pathID(r))
	if err != nil {
		fail(w, 404, "admin not found")
		return
	}
	var in adminInput
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	wasOwner := a.Role == store.RoleOwner && a.Enabled
	if err := in.apply(a); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if wasOwner && (a.Role != store.RoleOwner || !a.Enabled) && s.lastOwner(a.ID) {
		fail(w, 400, "keep at least one active owner")
		return
	}
	if err := s.St.SaveAdmin(a); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) deleteAdminH(w http.ResponseWriter, r *http.Request) {
	a, err := s.St.AdminByID(pathID(r))
	if err != nil {
		fail(w, 404, "admin not found")
		return
	}
	if a.ID == adminOf(r).ID {
		fail(w, 400, "you cannot delete yourself")
		return
	}
	if a.Role == store.RoleOwner && s.lastOwner(a.ID) {
		fail(w, 400, "keep at least one active owner")
		return
	}
	s.St.DeleteAdmin(a.ID)
	s.St.Log("auth", "%s removed admin %s", adminOf(r).Username, a.Username)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------- two-factor

func (s *Server) totpSetup(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	secret := util.TOTPSecret()
	s.limMu.Lock()
	if s.totpPending == nil {
		s.totpPending = map[int64]string{}
	}
	s.totpPending[a.ID] = secret
	s.limMu.Unlock()
	uri := util.TOTPURI(secret, a.Username, "Dunvex")
	writeJSON(w, 200, map[string]string{"secret": secret, "uri": uri, "qr": QRSVG(uri)})
}

// totpOK checks a code and refuses one that was already used.
func (s *Server) totpOK(a *store.Admin, secret, code string) bool {
	step := util.TOTPCheck(secret, code, time.Now())
	if step < 0 {
		return false
	}
	s.limMu.Lock()
	defer s.limMu.Unlock()
	if s.totpUsed == nil {
		s.totpUsed = map[int64]int64{}
	}
	if s.totpUsed[a.ID] >= step {
		return false
	}
	s.totpUsed[a.ID] = step
	return true
}

func (s *Server) totpEnable(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	var in struct{ Code string }
	readJSON(r, &in)
	s.limMu.Lock()
	secret := s.totpPending[a.ID]
	s.limMu.Unlock()
	if secret == "" {
		fail(w, 400, "start the setup again")
		return
	}
	if !s.totpOK(a, secret, in.Code) {
		fail(w, 400, "the code is not right; check the time on your phone")
		return
	}
	a.TOTPSecret = secret
	if err := s.St.SaveAdmin(a); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.limMu.Lock()
	delete(s.totpPending, a.ID)
	s.limMu.Unlock()
	s.St.Log("auth", "%s turned on two-step login", a.Username)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	var in struct{ Password, Code string }
	readJSON(r, &in)
	if bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(in.Password)) != nil {
		fail(w, 400, "current password is wrong")
		return
	}
	if a.TOTPSecret != "" && !s.totpOK(a, a.TOTPSecret, in.Code) {
		fail(w, 400, "the code is not right")
		return
	}
	a.TOTPSecret = ""
	s.St.SaveAdmin(a)
	s.St.Log("auth", "%s turned off two-step login", a.Username)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// resellerOverview is the dashboard of a reseller: only their own users.
func (s *Server) resellerOverview(w http.ResponseWriter, r *http.Request, a *store.Admin) {
	now := time.Now()
	users, _ := s.scopedUsers(a)
	online := s.Mgr.OnlineUsers(60)
	counts := map[string]int{}
	on, devices := 0, 0
	dc := s.Mgr.DeviceCounts()
	var lifetime int64
	for _, u := range users {
		counts[u.Status(now.Unix())]++
		if online[u.ID] {
			on++
		}
		devices += dc[u.Name]
		lifetime += u.LifetimeUsed
	}
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()
	tu, td := s.St.OwnerTotal(a.ID, dayStart)
	mu, md := s.St.OwnerTotal(a.ID, monthStart)
	series, _ := s.St.OwnerUsage(a.ID, now.Add(-time.Duration(rangeDays(r))*24*time.Hour).Unix())
	top, _ := s.St.OwnerTop(a.ID, now.Add(-30*24*time.Hour).Unix(), 8)
	writeJSON(w, 200, map[string]any{
		"role": a.Role, "users": len(users), "online": on, "devices": devices, "statuses": counts,
		"today": map[string]int64{"up": tu, "down": td}, "month": map[string]int64{"up": mu, "down": md},
		"lifetime": lifetime, "series": series, "top": top, "quota": s.quota(a, 0), "version": s.Version,
	})
}

func (s *Server) telegramTest(w http.ResponseWriter, r *http.Request) {
	if s.Bot == nil {
		fail(w, 400, "the Telegram bot is not available")
		return
	}
	if err := s.Bot.Test(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
