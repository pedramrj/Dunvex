package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"rsc.io/qr"

	"github.com/pedramrj/dunvex/internal/billing"
	"github.com/pedramrj/dunvex/internal/coremgr"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/tlsmgr"
	"github.com/pedramrj/dunvex/internal/util"
)

type handler func(w http.ResponseWriter, r *http.Request)

func (s *Server) apiMux() http.Handler {
	mux := http.NewServeMux()
	R, A, O := store.RoleReseller, store.RoleAdmin, store.RoleOwner
	h := func(pattern, role string, fn handler) { mux.Handle(pattern, s.need(role, fn)) }

	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("GET /setup-state", s.setupState)
	mux.HandleFunc("POST /setup", s.firstSetup)
	h("POST /logout", R, s.logout)
	h("GET /me", R, s.me)
	h("PUT /account", R, s.updateAccount)
	h("POST /account/2fa/setup", R, s.totpSetup)
	h("POST /account/2fa/enable", R, s.totpEnable)
	h("POST /account/2fa/disable", R, s.totpDisable)
	h("GET /account/sessions", R, s.listSessions)
	h("DELETE /account/sessions/{id}", R, s.endSession)
	h("POST /account/sessions/others", R, s.endOtherSessions)
	h("GET /account/keys", R, s.listKeys)
	h("POST /account/keys", R, s.createKey)
	h("DELETE /account/keys/{id}", R, s.deleteKey)

	h("GET /overview", R, s.overview)
	h("GET /status", R, s.status)
	h("GET /events", A, s.events)
	h("GET /network/geo", A, s.geo)
	h("GET /network/clients", A, s.clients)

	h("GET /nodes", A, s.listNodes)
	h("POST /nodes", O, s.createNode)
	h("PUT /nodes/{id}", O, s.updateNode)
	h("DELETE /nodes/{id}", O, s.deleteNode)
	h("POST /nodes/{id}/sync", A, s.syncNodeNow)

	h("GET /users", R, s.listUsers)
	h("POST /users", R, s.createUsers)
	h("POST /users/bulk", R, s.bulkUsers)
	h("GET /users/{id}", R, s.getUser)
	h("PUT /users/{id}", R, s.updateUser)
	h("DELETE /users/{id}", R, s.deleteUser)
	h("POST /users/{id}/reset", R, s.resetUser)
	h("POST /users/{id}/regenerate", R, s.regenerateUser)
	h("GET /users/{id}/usage", R, s.userUsage)
	h("GET /users/{id}/configs", R, s.userConfigs)
	h("POST /users/{id}/renew", R, s.renewUser)

	h("GET /billing", A, s.billingSummary)
	h("PUT /billing/settings", O, s.putBillingSettings)
	h("GET /billing/plans", R, s.listPlans)
	h("POST /billing/plans", A, s.savePlan)
	h("PUT /billing/plans/{id}", A, s.savePlan)
	h("DELETE /billing/plans/{id}", A, s.deletePlan)
	h("GET /billing/orders", R, s.listOrders)
	h("POST /billing/orders/{id}/approve", A, s.approveOrder)
	h("POST /billing/orders/{id}/reject", A, s.rejectOrder)
	h("POST /billing/orders/{id}/cancel", A, s.cancelOrder)
	h("GET /billing/orders/{id}/receipt", A, s.orderReceipt)
	h("GET /billing/wallets", O, s.listWallets)
	h("GET /billing/wallets/{id}", O, s.walletHistory)
	h("POST /billing/wallets/{id}", O, s.adjustWallet)
	h("GET /wallet", R, s.myWallet)
	h("POST /wallet/topup", R, s.topUp)

	h("GET /presets", A, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, protocol.All()) })
	h("GET /inbounds", A, s.listInbounds)
	h("POST /inbounds", A, s.createInbound)
	h("PUT /inbounds/{id}", A, s.updateInbound)
	h("DELETE /inbounds/{id}", A, s.deleteInbound)
	h("POST /tools/sni", A, s.testSNI)
	h("GET /tools/qr", R, s.qrCode)

	h("GET /admins", O, s.listAdmins)
	h("POST /admins", O, s.createAdmin)
	h("PUT /admins/{id}", O, s.updateAdminH)
	h("DELETE /admins/{id}", O, s.deleteAdminH)

	h("GET /settings", O, s.getSettings)
	h("PUT /settings", O, s.putSettings)
	h("POST /settings/cert", O, s.obtainCert)
	h("POST /settings/telegram/test", O, s.telegramTest)

	h("GET /routing", O, s.getRouting)
	h("PUT /routing", O, s.putRouting)
	h("POST /routing/warp", O, s.registerWarp)
	h("DELETE /routing/warp", O, s.removeWarp)
	h("POST /routing/warp/license", O, s.warpLicense)
	h("POST /routing/warp/check", O, s.checkWarp)

	h("GET /cores", A, s.cores)
	h("POST /cores/{name}/install", O, s.installCore)
	h("POST /cores/apply", A, s.applyCores)
	h("GET /cores/{name}/logs", A, s.coreLogs)
	h("GET /cores/{name}/config", O, s.coreConfig)

	h("GET /backup", O, s.backup)
	h("POST /restore", O, s.restore)
	h("GET /update", O, s.checkUpdate)
	h("POST /update", O, s.selfUpdate)
	return mux
}

// ---------------------------------------------------------------- auth

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.tooManyFailures(ip) {
		fail(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	var in struct{ Username, Password, Code string }
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	if s.SetupPending() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "finish the first-run setup: open the setup link from the install (dunvex info shows it)", "needSetup": true})
		return
	}
	a, err := s.St.AdminByName(strings.TrimSpace(in.Username))
	if err != nil || !a.Enabled || bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(in.Password)) != nil {
		s.recordFailure(ip)
		s.logFailure(ip, strings.TrimSpace(in.Username), "wrong password")
		time.Sleep(600 * time.Millisecond)
		fail(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	if a.TOTPSecret != "" {
		if strings.TrimSpace(in.Code) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "enter the 6-digit code from your authenticator app", "need2fa": true})
			return
		}
		if !s.totpOK(a, a.TOTPSecret, in.Code) {
			s.recordFailure(ip)
			s.logFailure(ip, a.Username, "wrong two-step code")
			time.Sleep(600 * time.Millisecond)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "the code is not right", "need2fa": true})
			return
		}
	}
	s.issueSession(w, r, a)
	s.St.Log("auth", "admin %s signed in from %s", a.Username, ip)
	writeJSON(w, 200, a)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if sid := sessionOf(r); sid != "" {
		s.St.DeleteSession(sid)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: s.PanelPath(), MaxAge: -1, HttpOnly: true})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	out := map[string]any{"admin": a, "version": s.Version}
	if a.Role == store.RoleReseller {
		out["quota"] = s.quota(a, 0)
	}
	writeJSON(w, 200, out)
}

func (s *Server) updateAccount(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	var in struct{ Username, CurrentPassword, NewPassword string }
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(in.CurrentPassword)) != nil {
		fail(w, 400, "current password is wrong")
		return
	}
	name := strings.TrimSpace(in.Username)
	if name == "" {
		name = a.Username
	}
	hash := a.PasswordHash
	if in.NewPassword != "" {
		if len(in.NewPassword) < 8 {
			fail(w, 400, "the new password needs at least 8 characters")
			return
		}
		hash, _ = HashPassword(in.NewPassword)
	}
	if err := s.St.UpdateAdmin(a.ID, name, hash); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a.Username, a.PasswordHash = name, hash
	s.issueSession(w, r, a)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------- overview

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	a := adminOf(r)
	if a.Role == store.RoleReseller {
		s.resellerOverview(w, r, a)
		return
	}
	users, _ := s.St.Users()
	online := s.Mgr.OnlineUsers(60)
	counts := map[string]int{}
	for _, u := range users {
		counts[u.Status(now.Unix())]++
	}
	devices := 0
	for _, n := range s.Mgr.DeviceCounts() {
		devices += n
	}
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()
	tu, td := s.St.TotalSince(dayStart)
	mu, md := s.St.TotalSince(monthStart)
	series, _ := s.St.UsageSeries(0, now.Add(-time.Duration(rangeDays(r))*24*time.Hour).Unix())
	top, _ := s.St.TopUsers(now.Add(-30*24*time.Hour).Unix(), 8)
	var lifetime int64
	for _, u := range users {
		lifetime += u.LifetimeUsed
	}
	inbounds, _ := s.St.Inbounds()
	ierr, cerr := s.Mgr.Errors()
	enabledIn := 0
	for _, in := range inbounds {
		if in.Enabled {
			enabledIn++
		}
	}
	lines := []map[string]any{}
	for _, in := range inbounds {
		p := protocol.Get(in.Preset)
		if p == nil {
			continue
		}
		r := s.Mgr.InboundRate(in.ID)
		lines = append(lines, map[string]any{"id": in.ID, "preset": in.Preset, "name": p.Name, "group": p.Group,
			"remark": in.Remark, "port": in.Port, "enabled": in.Enabled, "error": ierr[in.ID],
			"up": in.Up, "down": in.Down, "rateUp": r.Up, "rateDown": r.Down})
	}
	writeJSON(w, 200, map[string]any{
		"lines":       lines,
		"users":       len(users),
		"online":      len(online),
		"devices":     devices,
		"statuses":    counts,
		"today":       map[string]int64{"up": tu, "down": td},
		"month":       map[string]int64{"up": mu, "down": md},
		"lifetime":    lifetime,
		"series":      series,
		"top":         top,
		"live":        s.Mgr.Live(),
		"system":      s.Sys.Get(),
		"history":     s.Sys.History(),
		"cores":       s.Mgr.CoreStatuses(),
		"coreErrors":  cerr,
		"inboundErrs": len(ierr),
		"inbounds":    len(inbounds),
		"inboundsOn":  enabledIn,
		"version":     s.Version,
	})
}

// ---------------------------------------------------------------- users

type userView struct {
	*store.User
	Status   string `json:"status"`
	Online   bool   `json:"online"`
	Used     int64  `json:"used"`
	SubURL   string `json:"subUrl"`
	SpeedUp  int64  `json:"speedUp"`
	SpeedDn  int64  `json:"speedDown"`
	DaysLeft *int   `json:"daysLeft"`
	Devices  int    `json:"devices"`
	Owner    string `json:"owner,omitempty"`
}

func (s *Server) view(r *http.Request, u *store.User, online map[int64]bool) userView {
	now := time.Now().Unix()
	v := userView{User: u, Status: u.Status(now), Online: online[u.ID], Used: u.Used(), SubURL: s.SubURL(r, u)}
	v.Devices = len(s.Mgr.Devices(u.Name))
	rate := s.Mgr.UserRate(u.ID)
	v.SpeedUp, v.SpeedDn = rate.Up, rate.Down
	if u.ExpireAt > 0 {
		d := int((u.ExpireAt - now + 86399) / 86400)
		if d < 0 {
			d = 0
		}
		v.DaysLeft = &d
	} else if u.DurationDays > 0 {
		d := u.DurationDays
		v.DaysLeft = &d
	}
	return v
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.scopedUsers(adminOf(r))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	online := s.Mgr.OnlineUsers(60)
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	status := r.URL.Query().Get("status")
	owners := map[int64]string{}
	if adminOf(r).Role != store.RoleReseller {
		admins, _ := s.St.Admins()
		for _, a := range admins {
			if a.Role == store.RoleReseller {
				owners[a.ID] = a.Username
			}
		}
	}
	out := []userView{}
	for i := len(users) - 1; i >= 0; i-- {
		v := s.view(r, users[i], online)
		v.Owner = owners[users[i].OwnerID]
		if q != "" && !strings.Contains(strings.ToLower(v.Name+" "+v.Note), q) {
			continue
		}
		if status == "online" && !v.Online {
			continue
		}
		if status != "" && status != "online" && v.Status != status {
			continue
		}
		out = append(out, v)
	}
	writeJSON(w, 200, out)
}

type userInput struct {
	Name         *string  `json:"name"`
	Count        int      `json:"count"`
	DataLimit    *int64   `json:"dataLimit"`
	ExpireAt     *int64   `json:"expireAt"`
	DurationDays *int     `json:"durationDays"`
	StartOnFirst *bool    `json:"startOnFirstUse"`
	ResetPeriod  *string  `json:"resetPeriod"`
	InboundIDs   *[]int64 `json:"inboundIds"`
	Note         *string  `json:"note"`
	Enabled      *bool    `json:"enabled"`
	UsedReset    bool     `json:"resetUsage"`
	IPLimit      *int     `json:"ipLimit"`
	Resume       bool     `json:"resume"` // end a device-limit pause now
	PlanID       int64    `json:"planId"` // new users from a plan (resellers' wallets pay for it)
}

func (in *userInput) apply(u *store.User) error {
	if in.Name != nil {
		n := util.SanitizeName(*in.Name)
		if n == "" {
			return fmt.Errorf("name may only contain letters, digits, - _ .")
		}
		u.Name = n
	}
	if in.DataLimit != nil {
		if *in.DataLimit < 0 {
			return fmt.Errorf("data limit cannot be negative")
		}
		u.DataLimit = *in.DataLimit
	}
	if in.ResetPeriod != nil {
		switch *in.ResetPeriod {
		case "none", "daily", "weekly", "monthly":
			u.ResetPeriod = *in.ResetPeriod
		default:
			return fmt.Errorf("unknown reset period")
		}
	}
	if in.InboundIDs != nil {
		u.InboundIDs = *in.InboundIDs
	}
	if in.Note != nil {
		u.Note = *in.Note
	}
	if in.Enabled != nil {
		u.Enabled = *in.Enabled
	}
	// expiry: either an absolute date, or N days (now or on first use)
	if in.DurationDays != nil {
		days := *in.DurationDays
		if days < 0 {
			return fmt.Errorf("days cannot be negative")
		}
		switch {
		case days == 0:
			u.DurationDays, u.ExpireAt = 0, 0
		case in.StartOnFirst != nil && *in.StartOnFirst && u.FirstUseAt == 0:
			u.DurationDays, u.ExpireAt = days, 0
		default:
			u.DurationDays = days
			u.ExpireAt = time.Now().Unix() + int64(days)*86400
		}
	}
	if in.ExpireAt != nil {
		u.ExpireAt = *in.ExpireAt
		if u.ExpireAt > 0 {
			u.DurationDays = 0
		}
	}
	if in.UsedReset {
		u.UsedUp, u.UsedDown, u.LastResetAt, u.ResetUsage = 0, 0, time.Now().Unix(), true
	}
	if in.UsedReset || in.DataLimit != nil || in.DurationDays != nil || in.ExpireAt != nil {
		u.Notified = "" // plan changed: warn again when it runs low
	}
	if in.IPLimit != nil {
		if *in.IPLimit < 0 || *in.IPLimit > 100 {
			return fmt.Errorf("device limit must be between 0 and 100")
		}
		u.IPLimit = *in.IPLimit
	}
	if in.Resume {
		u.PausedUntil = 0
	}
	return nil
}

// NewUser returns a fresh user with new credentials (not yet saved).
func (s *Server) NewUser(name string) *store.User { return s.newUser(name) }

func (s *Server) newUser(name string) *store.User {
	priv, pub := util.WireGuardKeys()
	return &store.User{Name: name, UUID: util.UUID(), Password: util.RandomString(20), SubToken: util.RandomString(22),
		Enabled: true, ResetPeriod: "none", WGPrivate: priv, WGPublic: pub, WGIndex: s.St.NextWGIndex()}
}

func (s *Server) createUsers(w http.ResponseWriter, r *http.Request) {
	var in userInput
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	base := ""
	if in.Name != nil {
		base = util.SanitizeName(*in.Name)
	}
	count := in.Count
	if count < 1 {
		count = 1
	}
	if count > 500 {
		fail(w, 400, "at most 500 users at once")
		return
	}
	if base == "" {
		base = "user" + util.RandomLower(4)
	}
	a := adminOf(r)
	if a.Role == store.RoleReseller {
		in.InboundIDs = nil // resellers hand out every protocol
	}
	var limit int64
	if in.DataLimit != nil {
		limit = *in.DataLimit
	}
	p, err := s.planFor(a, in.PlanID)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if p != nil {
		limit = p.DataLimit
	}
	if err := s.checkQuota(a, count, limit, 0); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var unitCost int64 // what each user cost the reseller's wallet
	if p != nil && a.Role == store.RoleReseller && billing.LoadSettings(s.St).Wallet {
		cost, err := s.bill().WalletBuy(a, p, count, nil)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		unitCost = cost / int64(count)
	}
	refund := func(missing int) {
		if unitCost > 0 && missing > 0 {
			s.St.WalletMove(a.ID, unitCost*int64(missing), 0, fmt.Sprintf("refund: %d user(s) not created", missing))
		}
	}
	online := map[int64]bool{}
	var created []userView
	for i := 1; i <= count; i++ {
		name := base
		if count > 1 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		u := s.newUser(name)
		u.OwnerID = a.ID
		in2 := in
		in2.Name = nil
		if err := in2.apply(u); err != nil {
			refund(count - i + 1)
			fail(w, 400, err.Error())
			return
		}
		if p != nil {
			billing.ApplyPlan(u, p, time.Now().Unix(), false)
			if a.Role == store.RoleReseller {
				u.InboundIDs = nil // resellers hand out every protocol
			}
		}
		if err := s.St.CreateUser(u); err != nil {
			refund(count - i + 1)
			fail(w, 400, err.Error())
			return
		}
		created = append(created, s.view(r, u, online))
	}
	s.St.Log("user", "%s created %d user(s) (%s)", a.Username, count, base)
	s.Mgr.RequestApply()
	writeJSON(w, 200, created)
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	u := s.userFor(w, r)
	if u == nil {
		return
	}
	writeJSON(w, 200, struct {
		userView
		DeviceList []engine.Device `json:"deviceList"`
	}{s.view(r, u, s.Mgr.OnlineUsers(60)), s.Mgr.Devices(u.Name)})
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	u := s.userFor(w, r)
	if u == nil {
		return
	}
	var in userInput
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	a := adminOf(r)
	if a.Role == store.RoleReseller {
		in.InboundIDs = nil
	}
	if err := in.apply(u); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := s.checkQuota(a, 1, u.DataLimit, u.ID); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := s.St.UpdateUser(u); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.Mgr.RequestApply()
	writeJSON(w, 200, s.view(r, u, s.Mgr.OnlineUsers(60)))
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	u := s.userFor(w, r)
	if u == nil {
		return
	}
	s.St.DeleteUser(u.ID)
	s.St.Log("user", "user %s deleted", u.Name)
	s.Mgr.RequestApply()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) resetUser(w http.ResponseWriter, r *http.Request) {
	u := s.userFor(w, r)
	if u == nil {
		return
	}
	u.UsedUp, u.UsedDown, u.LastResetAt, u.PausedUntil, u.Notified, u.ResetUsage = 0, 0, time.Now().Unix(), 0, "", true
	s.St.UpdateUser(u)
	s.Mgr.RequestApply()
	writeJSON(w, 200, s.view(r, u, s.Mgr.OnlineUsers(60)))
}

func (s *Server) regenerateUser(w http.ResponseWriter, r *http.Request) {
	u := s.userFor(w, r)
	if u == nil {
		return
	}
	u.UUID, u.Password, u.SubToken = util.UUID(), util.RandomString(20), util.RandomString(22)
	s.St.UpdateUser(u)
	s.St.Log("user", "credentials of %s regenerated", u.Name)
	s.Mgr.RequestApply()
	writeJSON(w, 200, s.view(r, u, s.Mgr.OnlineUsers(60)))
}

func (s *Server) bulkUsers(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
		Days   int     `json:"days"`
		Bytes  int64   `json:"bytes"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	n := 0
	a := adminOf(r)
	for _, id := range in.IDs {
		u, err := s.St.UserByID(id)
		if err != nil || !owns(a, u) {
			continue
		}
		switch in.Action {
		case "enable":
			u.Enabled = true
		case "disable":
			u.Enabled = false
		case "reset":
			u.UsedUp, u.UsedDown, u.LastResetAt, u.PausedUntil, u.Notified, u.ResetUsage = 0, 0, time.Now().Unix(), 0, "", true
		case "extend":
			if in.Days > 0 {
				base := time.Now().Unix()
				if u.ExpireAt > base {
					base = u.ExpireAt
				}
				if u.ExpireAt == 0 && u.DurationDays > 0 && u.FirstUseAt == 0 {
					u.DurationDays += in.Days
				} else {
					u.ExpireAt = base + int64(in.Days)*86400
				}
			}
			if in.Bytes > 0 && u.DataLimit > 0 {
				if err := s.checkQuota(a, 1, u.DataLimit+in.Bytes, u.ID); err != nil {
					fail(w, 400, err.Error())
					return
				}
				u.DataLimit += in.Bytes
			}
			u.Notified = ""
		case "delete":
			s.St.DeleteUser(u.ID)
			n++
			continue
		default:
			fail(w, 400, "unknown action")
			return
		}
		s.St.UpdateUser(u)
		n++
	}
	s.St.Log("user", "%s: bulk %s on %d user(s)", a.Username, in.Action, n)
	s.Mgr.RequestApply()
	writeJSON(w, 200, map[string]int{"affected": n})
}

func (s *Server) userUsage(w http.ResponseWriter, r *http.Request) {
	if s.userFor(w, r) == nil {
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 400 {
		days = 30
	}
	pts, err := s.St.UsageSeries(pathID(r), time.Now().Add(-time.Duration(days)*24*time.Hour).Unix())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, pts)
}

// ConfigEntry is one ready-to-import config of a user.
type ConfigEntry struct {
	InboundID int64  `json:"inboundId"`
	Preset    string `json:"preset"`
	Name      string `json:"name"`
	Link      string `json:"link"`
	// a config that is a whole file (AmneziaWG .conf) rather than a link:
	// its file name, and its QR inline so the private key stays out of URLs
	File string `json:"file,omitempty"`
	QR   string `json:"qr,omitempty"`
}

// confName is a .conf file name that is also a valid interface name in the
// apps (at most 15 letters, digits or -_.=+), unique per inbound.
func confName(title string, id int64) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	base := b.String()
	if base == "" {
		base = "dunvex"
	}
	suffix := fmt.Sprintf("-%d", id)
	if len(base)+len(suffix) > 15 {
		base = base[:15-len(suffix)]
	}
	return base + suffix + ".conf"
}

func (s *Server) configsFor(u *store.User) []ConfigEntry {
	inbounds, _ := s.St.Inbounds()
	local, nodes := s.Env(), s.nodeMap()
	title := s.St.GetSetting("sub_title", "Dunvex")
	out := []ConfigEntry{}
	for _, in := range inbounds {
		if !in.Enabled || !u.AllowsInbound(in.ID) {
			continue
		}
		p := protocol.Get(in.Preset)
		if p == nil {
			continue
		}
		if in.NodeID != 0 && nodes[in.NodeID] == nil {
			continue // its server was removed
		}
		name := remarkFor(in, p, u, title, nodes)
		if l := protocol.Link(p, in, u, s.envFor(in, local, nodes), name); l != "" {
			c := ConfigEntry{InboundID: in.ID, Preset: in.Preset, Name: name, Link: l}
			if strings.HasPrefix(l, "[Interface]") {
				c.File, c.QR = confName(title, in.ID), QRSVG(l)
			}
			out = append(out, c)
		}
	}
	return out
}

func (s *Server) userConfigs(w http.ResponseWriter, r *http.Request) {
	u := s.userFor(w, r)
	if u == nil {
		return
	}
	writeJSON(w, 200, map[string]any{"subUrl": s.SubURL(r, u), "configs": s.configsFor(u)})
}

// ---------------------------------------------------------------- inbounds

type inboundView struct {
	*store.Inbound
	Name   string `json:"name"`
	Engine string `json:"engine"`
	Error  string `json:"error"`
}

func (s *Server) listInbounds(w http.ResponseWriter, r *http.Request) {
	list, err := s.St.Inbounds()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	ierr, _ := s.Mgr.Errors()
	nodes := s.nodeMap()
	out := []inboundView{}
	for _, in := range list {
		v := inboundView{Inbound: in, Error: ierr[in.ID]}
		if in.NodeID != 0 {
			v.Error = s.nodeError(in, nodes)
		}
		if p := protocol.Get(in.Preset); p != nil {
			v.Name, v.Engine = p.Name, p.Engine
		}
		out = append(out, v)
	}
	writeJSON(w, 200, out)
}

func (s *Server) panelPort() int {
	pp, _ := strconv.Atoi(s.St.GetSetting("panel_port", "0"))
	return pp
}

// inboundsOn lists the inbounds of one server (0 = this one).
func (s *Server) inboundsOn(nodeID int64) []*store.Inbound {
	all, _ := s.St.Inbounds()
	out := all[:0:0]
	for _, in := range all {
		if in.NodeID == nodeID {
			out = append(out, in)
		}
	}
	return out
}

// portTaken explains why an inbound of preset p (Reality server name sni) cannot
// use port on server nodeID, or returns "". This server's panel port is shared
// with Reality and UDP; on another server the Dunvex agent's port is reserved.
func (s *Server) portTaken(nodeID int64, port int, except int64, p *protocol.Preset, sni string) string {
	if nodeID == 0 {
		return protocol.PortConflict(s.inboundsOn(0), s.panelPort(), port, except, p, sni)
	}
	if n, err := s.St.NodeByID(nodeID); err == nil && port == n.AgentPort {
		return fmt.Sprintf("port %d is used by the Dunvex agent on %s", port, n.Name)
	}
	return protocol.PortConflict(s.inboundsOn(nodeID), 0, port, except, p, sni)
}

// portBusy reports a port held by another program on this server. Ports Dunvex
// itself holds (the panel's, or one of its inbounds') are not busy for this purpose.
func (s *Server) portBusy(nodeID int64, port int) bool {
	if nodeID != 0 || port == s.panelPort() {
		return false // another server's ports are checked there, when it applies
	}
	if protocol.UsesPort(s.inboundsOn(0), port) {
		return false
	}
	return !util.FreePort(port)
}

// pickPort chooses a port for a new inbound: the preset's default when it is
// free (or can share the panel's port), otherwise a random free one.
func (s *Server) pickPort(nodeID int64, p *protocol.Preset, sni string) int {
	if s.portTaken(nodeID, p.DefaultPort, 0, p, sni) == "" && !s.portBusy(nodeID, p.DefaultPort) {
		return p.DefaultPort
	}
	for i := 0; i < 200; i++ {
		port := 20000 + int(util.RandomBytes(2)[0])*150 + int(util.RandomBytes(1)[0])
		if s.portTaken(nodeID, port, 0, p, sni) == "" && (nodeID != 0 || util.FreePort(port)) {
			return port
		}
	}
	return p.DefaultPort
}

// checkServer validates the server an inbound is put on.
func (s *Server) checkServer(nodeID int64, p *protocol.Preset) string {
	if nodeID == 0 {
		return ""
	}
	if _, err := s.St.NodeByID(nodeID); err != nil {
		return "that server does not exist"
	}
	if p.NeedsDomain {
		return p.Name + " needs a domain and a certificate; on other servers use Reality or another protocol without a domain"
	}
	return ""
}

func (s *Server) createInbound(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Preset string         `json:"preset"`
		Port   int            `json:"port"`
		Remark string         `json:"remark"`
		Params map[string]any `json:"params"`
		NodeID int64          `json:"nodeId"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	p := protocol.Get(in.Preset)
	if p == nil {
		fail(w, 400, "unknown protocol")
		return
	}
	if msg := s.checkServer(in.NodeID, p); msg != "" {
		fail(w, 400, msg)
		return
	}
	if p.Engine == "mtg" {
		for _, x := range s.inboundsOn(in.NodeID) {
			if x.Preset == p.ID {
				fail(w, 400, "this server already has a Telegram proxy; one per server")
				return
			}
		}
		// mtg arrives with the first Telegram proxy (older installs do not have it)
		if in.NodeID == 0 && !s.Mgr.MTG.Installed() {
			if _, err := coremgr.InstallMTG(s.Mgr.MTG.BinDir); err != nil {
				fail(w, 502, "could not download mtg (the Telegram proxy): "+err.Error())
				return
			}
		}
	}
	if p.Engine == "trusttunnel" {
		for _, x := range s.inboundsOn(in.NodeID) {
			if x.Preset == p.ID {
				fail(w, 400, "this server already has a TrustTunnel; one per server, because it serves every user on its port")
				return
			}
		}
		// the endpoint arrives with the first TrustTunnel inbound
		if in.NodeID == 0 && !s.Mgr.TT.Installed() {
			if _, err := coremgr.InstallTrustTunnel(s.Mgr.TT.BinDir); err != nil {
				fail(w, 502, "could not download the TrustTunnel endpoint: "+err.Error())
				return
			}
		}
	}
	if p.Engine == "mita" {
		for _, x := range s.inboundsOn(in.NodeID) {
			if q := protocol.Get(x.Preset); protocol.IsMieru(q) {
				fail(w, 400, "this server already has a Mieru ("+q.Name+"); one per server, because it serves every user on its port")
				return
			}
		}
		// mita arrives with the first Mieru inbound
		if in.NodeID == 0 && !s.Mgr.Mita.Installed() {
			if _, err := coremgr.InstallMita(s.Mgr.Mita.BinDir); err != nil {
				fail(w, 502, "could not download mita (the Mieru server): "+err.Error())
				return
			}
		}
	}
	if p.Engine == "awg" {
		for _, x := range s.inboundsOn(in.NodeID) {
			if x.Preset == p.ID {
				fail(w, 400, "this server already has an AmneziaWG; one per server (it serves every user)")
				return
			}
		}
		// amneziawg-go arrives with the first AmneziaWG inbound
		if in.NodeID == 0 && !s.Mgr.AWG.Installed() {
			if _, err := coremgr.InstallAWG(s.Mgr.AWG.BinDir); err != nil {
				fail(w, 502, "could not download amneziawg-go: "+err.Error())
				return
			}
		}
	}
	if in.NodeID == 0 && p.NeedsDomain && strings.TrimSpace(s.St.GetSetting("domain", "")) == "" {
		fail(w, 400, p.Name+" needs a domain. Set your domain in Settings first.")
		return
	}
	ib := &store.Inbound{Preset: p.ID, Remark: strings.TrimSpace(in.Remark), Enabled: true, NodeID: in.NodeID,
		Params: protocol.Defaults(p, in.Params, s.St.GetSetting("domain", ""))}
	sni := protocol.SharedSNI(ib)
	port := in.Port
	if port == 0 {
		port = s.pickPort(in.NodeID, p, sni)
	}
	if msg := s.portTaken(in.NodeID, port, 0, p, sni); msg != "" {
		fail(w, 400, msg)
		return
	}
	if in.Port != 0 && s.portBusy(in.NodeID, port) {
		fail(w, 400, fmt.Sprintf("port %d is used by another program on this server", port))
		return
	}
	ib.Port = port
	if err := s.St.CreateInbound(ib); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.St.Log("inbound", "%s added on port %d", p.Name, port)
	s.KickNodes()
	if err := s.Mgr.Apply(); err != nil {
		writeJSON(w, 200, map[string]any{"inbound": ib, "warning": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"inbound": ib})
}

func (s *Server) updateInbound(w http.ResponseWriter, r *http.Request) {
	ib, err := s.St.InboundByID(pathID(r))
	if err != nil {
		fail(w, 404, "inbound not found")
		return
	}
	var in struct {
		Port    *int           `json:"port"`
		Remark  *string        `json:"remark"`
		Enabled *bool          `json:"enabled"`
		Params  map[string]any `json:"params"`
		NodeID  *int64         `json:"nodeId"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	oldPort, oldSNI, oldNode := ib.Port, protocol.SharedSNI(ib), ib.NodeID
	if in.Port != nil {
		ib.Port = *in.Port
	}
	if in.NodeID != nil && *in.NodeID != ib.NodeID {
		if msg := s.checkServer(*in.NodeID, protocol.Get(ib.Preset)); msg != "" {
			fail(w, 400, msg)
			return
		}
		ib.NodeID = *in.NodeID
	}
	if in.Remark != nil {
		ib.Remark = strings.TrimSpace(*in.Remark)
	}
	if in.Enabled != nil {
		ib.Enabled = *in.Enabled
	}
	if in.Params != nil {
		for k, v := range in.Params {
			ib.Params[k] = v
		}
		if p := protocol.Get(ib.Preset); p != nil {
			if pk, ok := in.Params["privateKey"]; ok && pk == "" {
				delete(ib.Params, "privateKey") // empty = regenerate
			}
			ib.Params = protocol.Defaults(p, ib.Params, s.St.GetSetting("domain", ""))
		}
	}
	if sni := protocol.SharedSNI(ib); ib.Port != oldPort || ib.NodeID != oldNode || (sni != oldSNI && ib.NodeID == 0 && ib.Port == s.panelPort()) {
		if msg := s.portTaken(ib.NodeID, ib.Port, ib.ID, protocol.Get(ib.Preset), sni); msg != "" {
			fail(w, 400, msg)
			return
		}
		if (ib.Port != oldPort || ib.NodeID != oldNode) && s.portBusy(ib.NodeID, ib.Port) {
			fail(w, 400, fmt.Sprintf("port %d is used by another program on this server", ib.Port))
			return
		}
	}
	if err := s.St.UpdateInbound(ib); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.Mgr.ClearPortErrors()
	if err := s.Mgr.Apply(); err != nil {
		writeJSON(w, 200, map[string]any{"inbound": ib, "warning": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"inbound": ib})
}

func (s *Server) deleteInbound(w http.ResponseWriter, r *http.Request) {
	ib, err := s.St.InboundByID(pathID(r))
	if err != nil {
		fail(w, 404, "inbound not found")
		return
	}
	s.St.DeleteInbound(ib.ID)
	s.St.Log("inbound", "inbound on port %d removed", ib.Port)
	s.Mgr.Apply()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------- settings

var editableSettings = []string{"host", "domain", "sub_title", "sub_update_hours", "support_url", "sub_base_url",
	"block_torrent", "panel_port", "panel_path", "sub_path", "cert_mode", "cert_file", "key_file", "acme_email",
	"sub_info_entry", "announce", "panel_tls", "ip_pause_minutes", "sub_accent",
	"tg_token", "tg_admins", "tg_lang", "tg_daily_hour", "tg_backup", "tg_user_bot", "tg_api"}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	all := s.St.AllSettings()
	out := map[string]string{}
	for _, k := range editableSettings {
		out[k] = all[k]
	}
	defaults := map[string]string{"sub_title": "Dunvex", "sub_update_hours": "12", "block_torrent": "true",
		"cert_mode": "self", "panel_path": s.PanelPath(), "sub_path": s.SubPath(), "sub_info_entry": "true",
		"ip_pause_minutes": "10", "tg_lang": "fa", "tg_daily_hour": "9", "tg_backup": "true", "tg_user_bot": "true"}
	for k, v := range defaults {
		if out[k] == "" {
			out[k] = v
		}
	}
	env := s.Env()
	info := map[string]any{"publicIp": s.Sys.PublicIP(), "effectiveHost": env.Host, "selfSigned": env.SelfSigned}
	if env.CertFile != "" {
		if t, err := tlsmgr.Expiry(env.CertFile); err == nil {
			info["certExpiry"] = t.Unix()
		}
	}
	if t := out["tg_token"]; len(t) > 10 {
		out["tg_token"] = t[:6] + strings.Repeat("•", 8) + t[len(t)-4:] // never sent back in full
	}
	if s.Bot != nil {
		info["telegram"] = s.Bot.Status()
	}
	writeJSON(w, 200, map[string]any{"settings": out, "info": info})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	allowed := map[string]bool{}
	for _, k := range editableSettings {
		allowed[k] = true
	}
	needRestart := false
	for k, v := range in {
		if !allowed[k] {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "tg_token":
			if strings.Contains(v, "•") {
				continue // the masked value came back unchanged
			}
		case "sub_accent":
			if v != "" && !accentRe.MatchString(v) {
				fail(w, 400, "accent colour must look like #397bff")
				return
			}
		case "ip_pause_minutes", "tg_daily_hour":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || (k == "tg_daily_hour" && n > 23) || n > 1440 {
				fail(w, 400, "invalid number for "+k)
				return
			}
		case "panel_port":
			p, err := strconv.Atoi(v)
			if err != nil || p < 1 || p > 65535 {
				fail(w, 400, "invalid panel port")
				return
			}
			if v != s.St.GetSetting("panel_port", "") {
				list := s.inboundsOn(0)
				if msg := protocol.PanelPortConflict(list, p); msg != "" {
					fail(w, 400, msg)
					return
				}
				if !protocol.UsesPort(list, p) && !util.FreePort(p) {
					fail(w, 400, fmt.Sprintf("port %d is used by another program on this server", p))
					return
				}
			}
			needRestart = needRestart || v != s.St.GetSetting("panel_port", "")
		case "panel_tls":
			if v != "true" {
				v = "false"
			}
			needRestart = needRestart || v != s.St.GetSetting("panel_tls", "false")
		case "panel_path", "sub_path":
			v = normPath(v)
			if len(v) < 3 {
				fail(w, 400, "path is too short")
				return
			}
		case "domain":
			v = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(v, "https://"), "http://"), "/"))
		case "cert_file", "key_file":
			if v != "" && !util.FileExists(v) {
				fail(w, 400, "file not found: "+v)
				return
			}
		}
		s.St.SetSetting(k, v)
	}
	if in["panel_path"] != "" && normPath(in["panel_path"]) == s.SubPath() {
		fail(w, 400, "panel path and subscription path must differ")
		return
	}
	s.St.Log("settings", "settings updated")
	s.Mgr.RequestApply()
	if s.Bot != nil {
		s.Bot.Reload()
	}
	if needRestart && s.Restart != nil {
		go func() { time.Sleep(time.Second); s.Restart() }()
	}
	writeJSON(w, 200, map[string]any{"ok": true, "restarting": needRestart})
}

func (s *Server) obtainCert(w http.ResponseWriter, r *http.Request) {
	domain := strings.TrimSpace(s.St.GetSetting("domain", ""))
	if domain == "" {
		fail(w, 400, "set a domain first")
		return
	}
	cert, key, err := tlsmgr.Obtain(s.CertDir(), domain, s.St.GetSetting("acme_email", ""))
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.St.SetSetting("cert_file", cert)
	s.St.SetSetting("key_file", key)
	s.St.SetSetting("cert_mode", "acme")
	s.St.Log("tls", "certificate issued for %s", domain)
	s.Mgr.Apply()
	writeJSON(w, 200, map[string]string{"cert": cert, "key": key})
}

// ---------------------------------------------------------------- cores

func (s *Server) cores(w http.ResponseWriter, r *http.Request) {
	_, cerr := s.Mgr.Errors()
	writeJSON(w, 200, map[string]any{
		"cores":  s.Mgr.CoreStatuses(),
		"errors": cerr,
		"stats":  map[string]bool{"sing-box": s.Mgr.SingBox.HasTag("with_v2ray_api"), "xray": true},
	})
}

func (s *Server) installCore(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var ver string
	var err error
	binDir := s.Mgr.Xray.BinDir
	switch name {
	case "xray":
		s.Mgr.Xray.Stop()
		ver, err = coremgr.InstallXray(binDir)
		s.Mgr.Xray.ResetVersion()
	case "sing-box":
		s.Mgr.SingBox.Stop()
		ver, err = coremgr.InstallSingBox(binDir)
		s.Mgr.SingBox.ResetVersion()
	case "mtg":
		s.Mgr.MTG.Stop()
		ver, err = coremgr.InstallMTG(binDir)
		s.Mgr.MTG.ResetVersion()
	case "amneziawg":
		s.Mgr.AWG.Stop()
		ver, err = coremgr.InstallAWG(s.Mgr.AWG.BinDir)
		s.Mgr.AWG.ResetVersion()
	case "mita":
		s.Mgr.Mita.Stop()
		ver, err = coremgr.InstallMita(binDir)
		s.Mgr.Mita.ResetVersion()
	case "trusttunnel":
		s.Mgr.TT.Stop()
		ver, err = coremgr.InstallTrustTunnel(binDir)
		s.Mgr.TT.ResetVersion()
	default:
		fail(w, 404, "unknown core")
		return
	}
	s.Mgr.Apply()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.St.Log("core", "%s updated to %s", name, ver)
	writeJSON(w, 200, map[string]string{"version": ver})
}

func (s *Server) applyCores(w http.ResponseWriter, r *http.Request) {
	s.Mgr.ClearPortErrors()
	for _, c := range s.Mgr.Cores() {
		c.Restart()
	}
	if err := s.Mgr.Apply(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) core(name string) interface{ Logs() []string } {
	if name == "amneziawg" {
		return s.Mgr.AWG
	}
	return s.Mgr.CoreByName(name)
}

func (s *Server) coreLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.core(r.PathValue("name")).Logs())
}

func (s *Server) coreConfig(w http.ResponseWriter, r *http.Request) {
	x, sb, _, _, err := s.Mgr.Configs()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.PathValue("name") {
	case "sing-box":
		w.Write(orEmpty(sb))
	case "mtg":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(s.Mgr.MTGConfig())
	case "amneziawg":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(s.Mgr.AWGConfig())
	case "mita":
		w.Write(s.Mgr.MitaConfig())
	case "trusttunnel":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(s.Mgr.TTConfig())
	default:
		w.Write(orEmpty(x))
	}
}

func orEmpty(b []byte) []byte {
	if b == nil {
		return []byte("{}")
	}
	return b
}

// ---------------------------------------------------------------- tools

func (s *Server) testSNI(w http.ResponseWriter, r *http.Request) {
	var in struct{ Domains []string }
	readJSON(r, &in)
	if len(in.Domains) == 0 {
		in.Domains = []string{"www.speedtest.net", "www.yahoo.com", "dl.google.com", "www.lovelive-anime.jp", "addons.mozilla.org",
			"discord.com", "www.samsung.com", "www.nvidia.com", "www.amd.com", "www.oracle.com", "www.tesla.com", "www.mozilla.org"}
	}
	if len(in.Domains) > 30 {
		in.Domains = in.Domains[:30]
	}
	res := make([]tlsmgr.SNIResult, len(in.Domains))
	done := make(chan struct{})
	for i, d := range in.Domains {
		go func(i int, d string) {
			res[i] = tlsmgr.TestSNI(strings.TrimSpace(d))
			done <- struct{}{}
		}(i, d)
	}
	for range in.Domains {
		<-done
	}
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].OK != res[j].OK {
			return res[i].OK
		}
		return res[i].LatencyMs < res[j].LatencyMs
	})
	writeJSON(w, 200, res)
}

func (s *Server) qrCode(w http.ResponseWriter, r *http.Request) {
	text := r.URL.Query().Get("text")
	if text == "" || len(text) > 2900 {
		fail(w, 400, "bad text")
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write([]byte(QRSVG(text)))
}

// QRSVG renders text as a compact SVG QR code.
func QRSVG(text string) string {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return `<svg xmlns="http://www.w3.org/2000/svg"/>`
	}
	n := code.Size
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="-2 -2 %d %d" shape-rendering="crispEdges"><rect x="-2" y="-2" width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n+4, n+4, n+4, n+4)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if !code.Black(x, y) {
				continue
			}
			run := 1 // one rectangle per run of dark modules keeps big codes (a .conf) small
			for x+run < n && code.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", x, y, run, run)
			x += run - 1
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	ensureDir(s.BackupDir())
	name := "dunvex-" + time.Now().Format("20060102-150405") + ".db"
	p := filepath.Join(s.BackupDir(), name)
	if err := s.St.Backup(p); err != nil {
		fail(w, 500, err.Error())
		return
	}
	defer os.Remove(p)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, p)
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<20)
	f, _, err := r.FormFile("file")
	if err != nil {
		fail(w, 400, "upload a .db backup file")
		return
	}
	defer f.Close()
	ensureDir(s.BackupDir())
	tmp := filepath.Join(s.BackupDir(), "restore-upload.db")
	out, err := os.Create(tmp)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	head := make([]byte, 16)
	n, _ := io.ReadFull(f, head)
	if n < 16 || string(head[:15]) != "SQLite format 3" {
		out.Close()
		os.Remove(tmp)
		fail(w, 400, "this is not a Dunvex backup")
		return
	}
	out.Write(head[:n])
	io.Copy(out, f)
	out.Close()
	// keep a safety copy of the current database, then swap on restart
	s.St.Backup(filepath.Join(s.BackupDir(), "before-restore-"+time.Now().Format("20060102-150405")+".db"))
	os.WriteFile(filepath.Join(s.Home, "restore.pending"), []byte(tmp), 0o600)
	writeJSON(w, 200, map[string]bool{"ok": true})
	if s.Restart != nil {
		go func() { time.Sleep(time.Second); s.Restart() }()
	}
}

func (s *Server) checkUpdate(w http.ResponseWriter, r *http.Request) {
	latest, err := coremgr.LatestDunvex()
	xr, _ := coremgr.LatestVersion("xray")
	sb, _ := coremgr.LatestVersion("sing-box")
	resp := map[string]any{"current": s.Version, "latest": latest, "xray": xr, "singbox": sb}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, 200, resp)
}

func (s *Server) selfUpdate(w http.ResponseWriter, r *http.Request) {
	tag, err := coremgr.UpdateSelf()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.St.Log("update", "Dunvex updated to %s", tag)
	writeJSON(w, 200, map[string]string{"version": tag})
	if s.Restart != nil {
		go func() { time.Sleep(time.Second); s.Restart() }()
	}
}
