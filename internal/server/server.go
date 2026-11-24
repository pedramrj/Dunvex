// Package server is the HTTP side of Dunvex: admin API, panel UI and subscriptions.
package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/pedramrj/dunvex/internal/backup"
	"github.com/pedramrj/dunvex/internal/billing"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/geoip"
	"github.com/pedramrj/dunvex/internal/health"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/relay"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
	"github.com/pedramrj/dunvex/internal/webhook"
)

// Server bundles everything the HTTP layer needs.
type Server struct {
	Home    string
	Version string
	St      *store.Store
	Mgr     *engine.Manager
	Sys     *sysinfo.Sampler
	UI      fs.FS  // built panel (index.html + assets)
	Restart func() // asks the process to restart (after self-update / restore)

	Bot Bot // Telegram bot (optional)

	WarpAPI       string                 // Cloudflare's WARP API ("" = the real one; tests point it elsewhere)
	CloudflareAPI string                 // Cloudflare's API for the CDN setup ("" = the real one)
	SelfBinary    func() ([]byte, error) // this panel's executable, sent to nodes ("" = os.Executable; tests replace it)
	Hooks         *webhook.Sender        // delivers events to webhooks

	evCount eventCounter // events by kind since the start, for the metrics

	Tunnels *relay.Tunnels // this server's links to reverse relays in front of it

	Bill     *billing.Service // the shop (made on first use when nil)
	billOnce sync.Once
	hits     map[string][]time.Time // shop rate limits (under limMu)

	limMu       sync.Mutex
	fails       map[string][]time.Time
	totpPending map[int64]string // admin id → secret waiting for its first code
	totpUsed    map[int64]int64  // admin id → last accepted time step
	failLog     map[string]time.Time

	geoOnce sync.Once
	geo2    *geoip.Loader // offline country data (see geoData)

	nodeOnce sync.Once
	nodes    *nodeHub // other servers (see hub)

	Backups    *backup.Service // automatic backups (made on first use when nil; see backups.go)
	backupOnce sync.Once

	HealthRunner  *health.Runner // the config check (nil = the real cores; see health.go)
	HealthRetry   time.Duration  // the wait before a failed config is tried again (0 = 8s)
	healthMu      sync.Mutex
	healthRunning bool
	healthStarted int64
	healthReady   time.Time // no scheduled check before this (the cores settle after a start)
}

// Bot is the Telegram side, as seen by the HTTP layer.
type Bot interface {
	Status() map[string]any
	Reload()
	Test() error
}

// Dirs.
func (s *Server) CertDir() string   { return filepath.Join(s.Home, "certs") }
func (s *Server) BackupDir() string { return filepath.Join(s.Home, "backups") }

// PanelPath returns the secret panel prefix, e.g. "/a1b2c3/".
func (s *Server) PanelPath() string { return normPath(s.St.GetSetting("panel_path", "/dunvex/")) }

// SubPath returns the subscription prefix, e.g. "/s/".
func (s *Server) SubPath() string { return normPath(s.St.GetSetting("sub_path", "/sub/")) }

func normPath(p string) string {
	p = "/" + strings.Trim(strings.TrimSpace(p), "/") + "/"
	if p == "//" {
		return "/"
	}
	return p
}

// Env returns the protocol environment derived from settings.
func (s *Server) Env() protocol.Env {
	domain := strings.TrimSpace(s.St.GetSetting("domain", ""))
	host := strings.TrimSpace(s.St.GetSetting("host", ""))
	if host == "" {
		host = domain
	}
	if host == "" {
		host = s.Sys.PublicIP()
	}
	if host == "" {
		host = "127.0.0.1"
	}
	env := protocol.Env{Host: host, Domain: domain, Listen: os.Getenv("DUNVEX_LISTEN")}
	cert, key := s.St.GetSetting("cert_file", ""), s.St.GetSetting("key_file", "")
	if cert != "" && util.FileExists(cert) && util.FileExists(key) {
		env.CertFile, env.KeyFile = cert, key
		env.SelfSigned = s.St.GetSetting("cert_mode", "self") == "self"
	} else {
		cn := host
		if domain != "" {
			cn = domain
		}
		if c, k, err := util.SelfSignedCert(s.CertDir(), cn); err == nil {
			env.CertFile, env.KeyFile, env.SelfSigned = c, k, true
		}
	}
	if env.SelfSigned && env.CertFile != "" {
		env.CertSHA256 = util.CertSHA256(env.CertFile)
	}
	env.Fragment = s.fragment()
	env.Mux, env.Noise, env.ECH = s.mux(), s.noise(), s.ownECH()
	return env
}

// Fragment settings (see protocol.Fragment).
const (
	fragmentSetting        = "fragment"
	fragmentPacketsSetting = "fragment_packets"
	fragmentLengthSetting  = "fragment_length"
	fragmentDelaySetting   = "fragment_delay"
)

// fragmentFrom reads the fragment settings from get (nil when it is off).
func fragmentFrom(get func(k, def string) string) *protocol.Fragment {
	if get(fragmentSetting, "false") != "true" {
		return nil
	}
	f := protocol.Fragment{
		Packets: get(fragmentPacketsSetting, protocol.FragmentPackets),
		Length:  get(fragmentLengthSetting, protocol.FragmentLength),
		Delay:   get(fragmentDelaySetting, protocol.FragmentDelay),
	}
	if f.Check() != nil {
		f = protocol.Fragment{Packets: protocol.FragmentPackets, Length: protocol.FragmentLength, Delay: protocol.FragmentDelay}
	}
	return &f
}

// fragment is the TLS fragment Xray clients get, nil when the owner keeps it off.
func (s *Server) fragment() *protocol.Fragment {
	return fragmentFrom(func(k, def string) string { return s.St.GetSetting(k, def) })
}

// PublicBase returns the base URL the server is reachable at (for subscription links).
func (s *Server) PublicBase(r *http.Request) string {
	if b := strings.TrimRight(s.St.GetSetting("sub_base_url", ""), "/"); b != "" {
		return b
	}
	scheme := "http"
	if (r != nil && r.TLS != nil) || (r == nil && s.St.GetSetting("panel_tls", "false") == "true") {
		scheme = "https"
	}
	host := ""
	if r != nil {
		host = r.Host
	}
	if host == "" {
		h := s.Env().Host
		host = net.JoinHostPort(h, s.St.GetSetting("panel_port", "2096"))
	}
	return scheme + "://" + host
}

// SubURL is the subscription link of a user.
func (s *Server) SubURL(r *http.Request, u *store.User) string {
	return s.PublicBase(r) + s.SubPath() + u.SubToken
}

// ---------------------------------------------------------------- routing

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	if s.fails == nil {
		s.fails = map[string][]time.Time{}
	}
	api := s.apiMux()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		panel, sub := s.PanelPath(), s.SubPath()
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, panel+"api/"):
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/" + strings.TrimPrefix(p, panel+"api/")
			api.ServeHTTP(w, r2)
		case p+"/" == panel:
			http.Redirect(w, r, panel, http.StatusMovedPermanently)
		case strings.HasPrefix(p, panel):
			s.serveUI(w, r, strings.TrimPrefix(p, panel))
		case strings.HasPrefix(p, sub+"_font/"):
			s.serveFont(w, strings.TrimPrefix(p, sub+"_font/"))
		case strings.HasPrefix(p, sub+"_brand/"):
			s.serveBrand(w, strings.TrimPrefix(p, sub+"_brand/"))
		case p == sub+"shop":
			http.Redirect(w, r, sub+"shop/", http.StatusMovedPermanently)
		case strings.HasPrefix(p, sub+"shop/"):
			s.serveShop(w, r, strings.TrimPrefix(p, sub+"shop/"))
		case strings.HasPrefix(p, sub) && len(p) > len(sub):
			s.serveSub(w, r, strings.TrimPrefix(p, sub))
		default:
			decoy(w)
		}
	})
}

// serveFont exposes the panel's embedded fonts to the public user page
// (under the subscription path, so the panel path stays secret).
// serveBrand exposes the logo files to the public user page.
func (s *Server) serveBrand(w http.ResponseWriter, name string) {
	ok := map[string]bool{"dunvex-logo-512.png": true, "favicon-64.png": true, "apple-touch-180.png": true, "icon-192.png": true}
	if !ok[name] || s.UI == nil {
		decoy(w)
		return
	}
	b, err := fs.ReadFile(s.UI, "brand/"+name)
	if err != nil {
		decoy(w)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=2592000")
	w.Write(b)
}

func (s *Server) serveFont(w http.ResponseWriter, name string) {
	prefix := map[string]string{"fa.woff2": "Estedad-Variable", "en.woff2": "Geist-Variable", "en-bold.woff2": "Geist-Variable"}[name]
	if prefix == "" || s.UI == nil {
		decoy(w)
		return
	}
	entries, _ := fs.ReadDir(s.UI, "assets")
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".woff2") {
			b, err := fs.ReadFile(s.UI, "assets/"+e.Name())
			if err != nil {
				break
			}
			w.Header().Set("Content-Type", "font/woff2")
			w.Header().Set("Cache-Control", "public, max-age=2592000")
			w.Write(b)
			return
		}
	}
	decoy(w)
}

func decoy(w http.ResponseWriter) {
	w.Header().Set("Server", "nginx")
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, "<html>\r\n<head><title>404 Not Found</title></head>\r\n<body>\r\n<center><h1>404 Not Found</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n")
}

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request, rel string) {
	if s.UI == nil {
		http.Error(w, "panel UI not built", http.StatusNotFound)
		return
	}
	if rel != "" && rel != "index.html" {
		if f, err := s.UI.Open(rel); err == nil {
			st, _ := f.Stat()
			f.Close()
			if st != nil && !st.IsDir() {
				if strings.HasPrefix(rel, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFileFS(w, r, s.UI, rel)
				return
			}
		}
	}
	b, err := fs.ReadFile(s.UI, "index.html")
	if err != nil {
		http.Error(w, "panel UI not built", http.StatusNotFound)
		return
	}
	cfg, _ := json.Marshal(map[string]string{"base": s.PanelPath(), "version": s.Version})
	html := strings.Replace(string(b), "</head>", "<script>window.__DVX__="+string(cfg)+"</script></head>", 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Write([]byte(html))
}

// ---------------------------------------------------------------- auth

const cookieName = "dvx_session"

func (s *Server) secret() []byte {
	sec := s.St.GetSetting("session_secret", "")
	if sec == "" {
		sec = util.RandomHex(32)
		s.St.SetSetting("session_secret", sec)
	}
	return []byte(sec)
}

func (s *Server) sign(payload string) string {
	m := hmac.New(sha256.New, s.secret())
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, a *store.Admin) {
	now := time.Now()
	exp := now.Add(7 * 24 * time.Hour).Unix()
	// the session is also a row, so it can be listed and signed out elsewhere
	sid := util.RandomString(24)
	s.St.PruneSessions(now.Unix())
	s.St.CreateSession(&store.Session{ID: sid, AdminID: a.ID, IP: clientIP(r), Agent: agentOf(r),
		CreatedAt: now.Unix(), LastSeen: now.Unix(), ExpiresAt: exp})
	payload := fmt.Sprintf("%d.%d.%s.%s", a.ID, exp, pwStamp(a.PasswordHash), sid)
	val := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + s.sign(payload)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: val, Path: s.PanelPath(), HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, Expires: time.Unix(exp, 0)})
}

func pwStamp(hash string) string {
	h := sha256.Sum256([]byte(hash))
	return base64.RawURLEncoding.EncodeToString(h[:6])
}

// agentOf is the browser's user agent, kept short.
func agentOf(r *http.Request) string {
	ua := r.UserAgent()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	return ua
}

func (s *Server) currentAdmin(r *http.Request) *store.Admin {
	a, _ := s.cookieAdmin(r)
	return a
}

// cookieAdmin checks the session cookie and returns its admin and session id.
func (s *Server) cookieAdmin(r *http.Request) (*store.Admin, string) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil, ""
	}
	parts := strings.SplitN(c.Value, ".", 2)
	if len(parts) != 2 {
		return nil, ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ""
	}
	payload := string(raw)
	if subtle.ConstantTimeCompare([]byte(s.sign(payload)), []byte(parts[1])) != 1 {
		return nil, ""
	}
	f := strings.Split(payload, ".")
	if len(f) != 4 {
		return nil, "" // cookies from before session rows: sign in once more
	}
	id, _ := strconv.ParseInt(f[0], 10, 64)
	exp, _ := strconv.ParseInt(f[1], 10, 64)
	now := time.Now().Unix()
	if now > exp {
		return nil, ""
	}
	a, err := s.St.AdminByID(id)
	if err != nil || !a.Enabled || pwStamp(a.PasswordHash) != f[2] {
		return nil, ""
	}
	sess, err := s.St.SessionByID(f[3])
	if err != nil || sess.AdminID != a.ID || sess.ExpiresAt < now {
		return nil, "" // signed out from another device
	}
	if now-sess.LastSeen >= 60 {
		s.St.TouchSession(sess.ID, clientIP(r), now)
	}
	return a, sess.ID
}

// keyAdmin checks an "Authorization: Bearer dvx_…" API key.
func (s *Server) keyAdmin(r *http.Request) (*store.Admin, *store.APIKey) {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || !strings.HasPrefix(tok, keyPrefix) {
		return nil, nil
	}
	k, err := s.St.APIKeyByHash(keyHash(tok))
	if err != nil {
		return nil, nil
	}
	now := time.Now().Unix()
	if k.ExpiresAt > 0 && now > k.ExpiresAt {
		return nil, nil
	}
	a, err := s.St.AdminByID(k.AdminID)
	if err != nil || !a.Enabled {
		return nil, nil
	}
	if now-k.LastUsed >= 60 {
		s.St.TouchAPIKey(k.ID, now)
	}
	return a, k
}

// HashPassword hashes an admin password.
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// tooManyFailures applies a simple per-IP login throttle (8 failures / 10 min).
func (s *Server) tooManyFailures(ip string) bool {
	s.limMu.Lock()
	defer s.limMu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	var keep []time.Time
	for _, t := range s.fails[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	s.fails[ip] = keep
	return len(keep) >= 8
}

func (s *Server) recordFailure(ip string) {
	s.limMu.Lock()
	s.fails[ip] = append(s.fails[ip], time.Now())
	s.limMu.Unlock()
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	return dec.Decode(v)
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func ensureDir(p string) { os.MkdirAll(p, 0o700) }
