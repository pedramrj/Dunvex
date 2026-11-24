package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/health"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// Config health: every config the users get (each protocol, on every server,
// and its CDN and relay copies) is tested from this server with a real client
// core, on a schedule and on demand. The check signs in as its own user (see
// engine.HealthUser), which every server holds.

const (
	healthLastSetting  = "health_last"  // the last report (JSON)
	healthHoursSetting = "health_hours" // the schedule; 0 = only when asked
	healthDefaultHours = "3"
)

// healthHourChoices are the schedules the panel offers.
var healthHourChoices = map[int]bool{0: true, 1: true, 2: true, 3: true, 6: true, 12: true, 24: true}

// healthReport is what GET /health returns, and what is kept between checks.
type healthReport struct {
	At      int64           `json:"at"`   // when the last check finished (0 = never)
	Took    int             `json:"took"` // seconds it took
	Results []health.Result `json:"results"`

	// filled in for the API
	Running bool         `json:"running"`
	Started int64        `json:"started,omitempty"` // when the running check began
	Hours   int          `json:"hours"`
	Next    int64        `json:"next,omitempty"` // when the next scheduled check is due
	Summary healthCounts `json:"summary"`
}

type healthCounts struct {
	Total int `json:"total"` // configs tested (skipped ones left out)
	OK    int `json:"ok"`
	Fail  int `json:"fail"`
	Skip  int `json:"skip"`
}

func countHealth(res []health.Result) healthCounts {
	var c healthCounts
	for _, r := range res {
		switch r.Status {
		case health.StatusOK:
			c.OK++
		case health.StatusFail:
			c.Fail++
		default:
			c.Skip++
		}
	}
	c.Total = c.OK + c.Fail
	return c
}

func (s *Server) healthHours() int {
	n, err := strconv.Atoi(s.St.GetSetting(healthHoursSetting, healthDefaultHours))
	if err != nil || !healthHourChoices[n] {
		return 3
	}
	return n
}

// healthLast is the last finished report.
func (s *Server) healthLast() healthReport {
	var r healthReport
	if v := s.St.GetSetting(healthLastSetting, ""); v != "" {
		json.Unmarshal([]byte(v), &r)
	}
	return r
}

func (s *Server) healthView() healthReport {
	r := s.healthLast()
	s.healthMu.Lock()
	r.Running, r.Started = s.healthRunning, s.healthStarted
	s.healthMu.Unlock()
	if !r.Running {
		r.Started = 0
	}
	r.Hours = s.healthHours()
	if r.Hours > 0 {
		r.Next = max(r.At+int64(r.Hours)*3600, s.healthReady.Unix())
	}
	if r.Results == nil {
		r.Results = []health.Result{}
	}
	r.Summary = countHealth(r.Results)
	return r
}

// ---------------------------------------------------------------- what to test

// healthTargets is every config of the check's user, with the client to test it.
func (s *Server) healthTargets(hu *store.User) []health.Target {
	nodes := s.nodeMap()
	var out []health.Target
	for _, it := range s.items(hu) {
		t := health.Target{Config: health.Config{InboundID: it.in.ID, Preset: it.p.ID, Name: it.name, Via: it.via}}
		if n := nodes[it.in.NodeID]; it.in.NodeID != 0 && n != nil {
			t.Server = n.Name
		}
		healthClient(&t, it, hu)
		if it.p.Network == "tcp" && it.p.Engine != "dnstt" {
			t.Addr = net.JoinHostPort(protocol.LinkHost(it.in, it.env), strconv.Itoa(it.in.Port))
		}
		out = append(out, t)
	}
	return out
}

// healthClient picks the client a user's app would use for the config.
func healthClient(t *health.Target, it profileItem, hu *store.User) {
	p, in := it.p, it.in
	switch p.Engine {
	case "xray":
		// the exact link the users import, read the way an Xray app reads it
		o, err := protocol.XrayOutbound(protocol.Link(p, in, hu, it.env, it.name), "proxy")
		if err != nil {
			t.Broken = "the link cannot be opened: " + err.Error()
			return
		}
		t.Kind, t.Xray = health.KindXray, o
		t.Slow = protocol.DNSRouted(p) || protocol.IsXICMP(p)
		if protocol.IsXICMP(p) {
			rawPings(o) // the panel runs as root: no need for unprivileged ping sockets
		}
	case "singbox":
		kind, items := protocol.SingboxOutbound(p, in, hu, it.env, "proxy")
		if len(items) == 0 {
			t.Skip = "no client on this server can open it"
			return
		}
		t.Kind = health.KindSingbox
		if kind == "endpoint" {
			t.Endpoints = items
		} else {
			t.Singbox = items
		}
	case "dnstt":
		d := &health.DNSTT{Domain: protocol.XDNSDomain(in), PublicKey: in.Str("publicKey"), Resolver: dnsttResolver(in, it.env)}
		if !protocol.OpenSignIn(in) {
			d.User, d.Pass = hu.Name, hu.Password
		}
		t.Kind, t.DNSTT, t.Slow = health.KindDNSTT, d, true
	default:
		t.Skip = fmt.Sprintf("the server has no %s client to test it with yet", p.Name)
	}
}

// rawPings makes an xICMP client send its pings on a raw socket. Phones use
// unprivileged ping sockets (the link's dgram), which many servers do not
// allow (net.ipv4.ping_group_range); the tunnel server takes both alike.
func rawPings(o map[string]any) {
	ss, _ := o["streamSettings"].(map[string]any)
	fm, _ := ss["finalmask"].(map[string]any)
	udp, _ := fm["udp"].([]any)
	for _, m := range udp {
		if mask, ok := m.(map[string]any); ok && mask["type"] == "xicmp" {
			if set, ok := mask["settings"].(map[string]any); ok {
				delete(set, "dgram")
			}
		}
	}
}

// dnsttResolver is where the app sends its queries: the inbound's resolver, or
// the server itself when it is set to "direct".
func dnsttResolver(in *store.Inbound, env protocol.Env) string {
	r := strings.TrimSpace(in.Str("resolver"))
	switch r {
	case "":
		r = "8.8.8.8"
	case "direct":
		return net.JoinHostPort(protocol.LinkHost(in, env), strconv.Itoa(in.Port))
	}
	if _, _, err := net.SplitHostPort(r); err != nil {
		r = net.JoinHostPort(strings.Trim(r, "[]"), "53")
	}
	return r
}

// ---------------------------------------------------------------- running it

// healthRunner is the checker; tests replace it.
func (s *Server) healthRunner() *health.Runner {
	if s.HealthRunner != nil {
		return s.HealthRunner
	}
	return &health.Runner{BinDir: s.Mgr.Xray.BinDir}
}

func (s *Server) healthRetryWait() time.Duration {
	if s.HealthRetry > 0 {
		return s.HealthRetry
	}
	return 8 * time.Second
}

// CheckConfigs starts a check unless one is running; it reports whether it started one.
func (s *Server) CheckConfigs() bool {
	s.healthMu.Lock()
	if s.healthRunning {
		s.healthMu.Unlock()
		return false
	}
	s.healthRunning, s.healthStarted = true, time.Now().Unix()
	s.healthMu.Unlock()
	go func() {
		defer func() {
			s.healthMu.Lock()
			s.healthRunning = false
			s.healthMu.Unlock()
		}()
		s.runHealth()
	}()
	return true
}

func (s *Server) runHealth() {
	begin := time.Now()
	var res []health.Result
	if hu := engine.HealthUser(s.St); hu != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		targets := s.healthTargets(hu)
		r := s.healthRunner()
		res = r.Run(ctx, targets)
		// a config that failed gets one more try a little later: a core that
		// was restarting (a change just applied) is no broken config
		var again []health.Target
		var at []int
		for i, x := range res {
			if x.Status == health.StatusFail && targets[i].Broken == "" {
				again, at = append(again, targets[i]), append(at, i)
			}
		}
		if len(again) > 0 {
			select {
			case <-time.After(s.healthRetryWait()):
			case <-ctx.Done():
			}
			for j, x := range r.Run(ctx, again) {
				res[at[j]] = x
			}
		}
		cancel()
	}
	if res == nil {
		res = []health.Result{}
	}
	prev := s.healthLast()
	rep := healthReport{At: time.Now().Unix(), Took: int(time.Since(begin).Seconds()), Results: res}
	b, _ := json.Marshal(rep)
	s.St.SetSetting(healthLastSetting, string(b))
	s.healthEvents(prev, rep)
}

func healthKey(r health.Result) string { return strconv.FormatInt(r.InboundID, 10) + "|" + r.Name }

// healthEvents logs what changed since the last check: configs that stopped
// working, and those that work again. The events reach webhooks and the bot.
func (s *Server) healthEvents(prev, now healthReport) {
	before := map[string]string{}
	for _, r := range prev.Results {
		before[healthKey(r)] = r.Status
	}
	var broke, back []string
	for _, r := range now.Results {
		was := before[healthKey(r)]
		switch {
		case r.Status == health.StatusFail && was != health.StatusFail:
			broke = append(broke, r.Name+" ("+r.Error+")")
		case r.Status == health.StatusOK && was == health.StatusFail:
			back = append(back, r.Name)
		}
	}
	c := countHealth(now.Results)
	if len(broke) > 0 {
		s.St.Log("health", "config check: %d of %d configs fail: %s", c.Fail, c.Total, joinFew(broke, 5))
	}
	if len(back) > 0 {
		s.St.Log("health", "config check: working again: %s", joinFew(back, 8))
	}
}

func joinFew(list []string, n int) string {
	if len(list) <= n {
		return strings.Join(list, "; ")
	}
	return strings.Join(list[:n], "; ") + fmt.Sprintf("; and %d more", len(list)-n)
}

// RunHealthChecks checks the configs on the schedule until stop closes. The
// first check waits for the cores to settle after a start.
func (s *Server) RunHealthChecks(stop <-chan struct{}) {
	s.healthReady = time.Now().Add(2 * time.Minute)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		h := s.healthHours()
		if h == 0 || time.Now().Before(s.healthReady) {
			continue
		}
		if last := s.healthLast(); time.Since(time.Unix(last.At, 0)) >= time.Duration(h)*time.Hour {
			s.CheckConfigs()
		}
	}
}

// ---------------------------------------------------------------- API

func (s *Server) getHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.healthView())
}

func (s *Server) postHealthCheck(w http.ResponseWriter, r *http.Request) {
	if engine.HealthUser(s.St) == nil {
		fail(w, 409, "the config check is not set up yet: restart the panel")
		return
	}
	s.CheckConfigs()
	writeJSON(w, 202, s.healthView())
}

// healthByInbound is the worst result of each inbound (fail over ok over skip),
// for the Protocols page and the metrics.
func healthByInbound(res []health.Result) map[int64]health.Result {
	rank := map[string]int{health.StatusSkip: 0, health.StatusOK: 1, health.StatusFail: 2}
	out := map[int64]health.Result{}
	for _, r := range res {
		if cur, ok := out[r.InboundID]; !ok || rank[r.Status] > rank[cur.Status] {
			out[r.InboundID] = r
		}
	}
	return out
}

// healthMetrics adds the last check to the Prometheus output.
func (s *Server) healthMetrics(p *prom) {
	rep := s.healthLast()
	if rep.At == 0 {
		return
	}
	var up, lat []sample
	res := append([]health.Result(nil), rep.Results...)
	sort.SliceStable(res, func(i, j int) bool { return res[i].InboundID < res[j].InboundID })
	for _, r := range res {
		if r.Status == health.StatusSkip {
			continue
		}
		server := r.Server
		if server == "" {
			server = "panel"
		}
		via := r.Via
		if via == "" {
			via = "direct"
		}
		l := []string{"id", strconv.FormatInt(r.InboundID, 10), "protocol", r.Preset, "name", r.Name, "server", server, "via", via}
		up = append(up, gauge(b2f(r.Status == health.StatusOK), l...))
		if r.Status == health.StatusOK {
			lat = append(lat, gauge(float64(r.MS), l...))
		}
	}
	p.family("dunvex_config_up", "gauge", "Whether each config passed the last health check (configs that cannot be tested are left out).", up...)
	p.family("dunvex_config_latency_milliseconds", "gauge", "How long a page took through each working config in the last check.", lat...)
	p.family("dunvex_config_check_timestamp_seconds", "gauge", "When the last config health check finished.", gauge(float64(rep.At)))
}
