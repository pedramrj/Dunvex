package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/store"
)

// status is a cheap summary the panel shell polls every few seconds.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	users, _ := s.scopedUsers(a)
	online := s.Mgr.OnlineUsers(60)
	dc := s.Mgr.DeviceCounts()
	on, devices := 0, 0
	for _, u := range users {
		if online[u.ID] {
			on++
		}
		devices += dc[u.Name]
	}
	type core struct {
		Name      string `json:"name"`
		Installed bool   `json:"installed"`
		Running   bool   `json:"running"`
		Version   string `json:"version"`
	}
	cores := []core{}
	for _, st := range []struct {
		name string
		s    func() (bool, bool, string)
	}{
		{"xray", func() (bool, bool, string) { x := s.Mgr.Xray.Status(); return x.Installed, x.Running, x.Version }},
		{"sing-box", func() (bool, bool, string) { x := s.Mgr.SingBox.Status(); return x.Installed, x.Running, x.Version }},
	} {
		inst, run, ver := st.s()
		cores = append(cores, core{st.name, inst, run, ver})
	}
	ierr, cerr := s.Mgr.Errors()
	problems := 0
	if a.Role != store.RoleReseller {
		problems = len(ierr) + len(cerr)
	}
	writeJSON(w, 200, map[string]any{"online": on, "devices": devices, "users": len(users), "cores": cores, "problems": problems, "version": s.Version})
}

// events lists recent events (?limit=1..1000).
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	list := s.St.Events(limit)
	if q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))); q != "" {
		out := list[:0:0]
		for _, e := range list {
			if strings.Contains(strings.ToLower(e.Message), q) {
				out = append(out, e)
			}
		}
		list = out
	}
	writeJSON(w, 200, list)
}

// Geo is the server's own location, looked up once from its public IP.
type Geo struct {
	IP          string  `json:"ip"`
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	City        string  `json:"city"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	ISP         string  `json:"isp"`
}

// geo returns the server location for the network map. Only the server's own
// public IP is sent to the lookup service; the answer is cached.
func (s *Server) geo(w http.ResponseWriter, r *http.Request) {
	ip := s.Sys.PublicIP()
	if ip == "" {
		fail(w, 503, "the server's public IP is not known yet")
		return
	}
	var g Geo
	if raw := s.St.GetSetting("geo_cache", ""); raw != "" && json.Unmarshal([]byte(raw), &g) == nil && g.IP == ip {
		writeJSON(w, 200, g)
		return
	}
	c := &http.Client{Timeout: 8 * time.Second}
	res, err := c.Get("https://ipwho.is/" + ip)
	if err != nil {
		fail(w, 502, "location lookup failed: "+err.Error())
		return
	}
	defer res.Body.Close()
	var raw struct {
		Success     bool    `json:"success"`
		Message     string  `json:"message"`
		Country     string  `json:"country"`
		CountryCode string  `json:"country_code"`
		City        string  `json:"city"`
		Lat         float64 `json:"latitude"`
		Lon         float64 `json:"longitude"`
		Conn        struct {
			ISP string `json:"isp"`
		} `json:"connection"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&raw); err != nil || !raw.Success {
		fail(w, 502, fmt.Sprintf("location lookup failed %s", raw.Message))
		return
	}
	g = Geo{IP: ip, Country: raw.Country, CountryCode: raw.CountryCode, City: raw.City, Lat: raw.Lat, Lon: raw.Lon, ISP: raw.Conn.ISP}
	if b, err := json.Marshal(g); err == nil {
		s.St.SetSetting("geo_cache", string(b))
	}
	writeJSON(w, 200, g)
}

var accentRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// rangeDays reads ?days= for usage series (2..400, default 30).
func rangeDays(r *http.Request) int {
	d, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if d <= 0 || d > 400 {
		return 30
	}
	if d < 2 {
		return 2 // hourly view needs yesterday too
	}
	return d
}

// logFailure records a failed sign-in, at most once a minute per address.
func (s *Server) logFailure(ip, user, why string) {
	s.limMu.Lock()
	if s.failLog == nil {
		s.failLog = map[string]time.Time{}
	}
	if len(s.failLog) > 4096 { // drop stale entries so the map stays small
		for k, v := range s.failLog {
			if time.Since(v) > time.Minute {
				delete(s.failLog, k)
			}
		}
	}
	last := s.failLog[ip]
	ok := time.Since(last) > time.Minute
	if ok {
		s.failLog[ip] = time.Now()
	}
	s.limMu.Unlock()
	if ok {
		s.St.Log("auth", "failed sign-in for %q from %s (%s)", user, ip, why)
	}
}
