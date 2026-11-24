package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/health"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/webhook"
)

// socksTo answers SOCKS5 without a password and connects every request to
// target, or refuses them all while broken.
func socksTo(l net.Listener, target string, broken func() bool) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			b := make([]byte, 262)
			if _, err := io.ReadFull(c, b[:2]); err != nil {
				return
			}
			io.ReadFull(c, b[:b[1]])
			c.Write([]byte{5, 0})
			io.ReadFull(c, b[:4])
			switch b[3] {
			case 1:
				io.ReadFull(c, b[:6])
			case 3:
				io.ReadFull(c, b[:1])
				io.ReadFull(c, b[:int(b[0])+2])
			case 4:
				io.ReadFull(c, b[:18])
			}
			if broken() {
				c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
				return
			}
			up, err := net.Dial("tcp", target)
			if err != nil {
				return
			}
			defer up.Close()
			c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
			go io.Copy(up, c)
			io.Copy(c, up)
		}()
	}
}

func TestConfigHealth(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	st.SetSetting("host", "203.0.113.7")
	engine.EnsureHealthKey(st)

	add := func(preset string, params map[string]any) *store.Inbound {
		p := protocol.Get(preset)
		in := &store.Inbound{Preset: preset, Port: 30000 + len(preset), Enabled: true, Params: protocol.Defaults(p, params, "")}
		if err := st.CreateInbound(in); err != nil {
			t.Fatal(err)
		}
		return in
	}
	vless := add("vless-reality-vision", nil)
	tuic := add("tuic", nil)
	add("mieru", nil)
	dns := add("dnstt", map[string]any{"xdnsDomain": "t.example.com"})

	// a 204 page, and client cores that reach it unless the config is TUIC while "down"
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer page.Close()
	var tuicDown atomic.Bool
	tuicDown.Store(true)
	start := func(ctx context.Context, kind string, cfg []byte) (func(), func() string, <-chan struct{}, error) {
		var c struct {
			Inbounds []struct {
				Port       int `json:"port"`
				ListenPort int `json:"listen_port"`
			} `json:"inbounds"`
		}
		json.Unmarshal(cfg, &c)
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(c.Inbounds[0].Port+c.Inbounds[0].ListenPort))
		if err != nil {
			return nil, nil, nil, err
		}
		isTUIC := strings.Contains(string(cfg), `"type":"tuic"`)
		go socksTo(l, page.Listener.Addr().String(), func() bool { return isTUIC && tuicDown.Load() })
		return func() { l.Close() }, func() string { return "" }, make(chan struct{}), nil
	}
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler(),
		HealthRunner: &health.Runner{Probes: []string{page.URL + "/generate_204"}, Timeout: 5 * time.Second, Start: start}, HealthRetry: time.Millisecond}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })

	// every config, with the client its users' apps would use
	hu := engine.HealthUser(st)
	kinds := map[string]health.Target{}
	for _, tg := range srv.healthTargets(hu) {
		kinds[tg.Preset] = tg
	}
	if kinds["vless-reality-vision"].Kind != health.KindXray || kinds["tuic"].Kind != health.KindSingbox || kinds["mieru"].Kind != "" || kinds["mieru"].Skip == "" {
		t.Fatalf("clients: %+v", kinds)
	}
	if d := kinds["dnstt"].DNSTT; kinds["dnstt"].Kind != health.KindDNSTT || d == nil || d.Resolver != "8.8.8.8:53" || d.Domain != "t.example.com" ||
		d.User != engine.HealthName || d.PublicKey != dns.Str("publicKey") || !kinds["dnstt"].Slow {
		t.Fatalf("dnstt: %+v %+v", kinds["dnstt"], d)
	}
	// the DNS tunnel needs real DNS; it is tested in the health package
	dns.Enabled = false
	st.UpdateInbound(dns)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	check := func() map[string]any {
		t.Helper()
		if code, out := owner.do("POST", "/health/check", nil); code != 202 {
			t.Fatalf("check: %d %v", code, out)
		}
		for i := 0; i < 100; i++ {
			if !srv.healthView().Running {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		_, out := owner.do("GET", "/health", nil)
		return out
	}
	events := func() []string {
		var out []string
		for _, e := range st.Events(50) {
			if e.Kind == "health" {
				out = append(out, e.Message)
			}
		}
		return out
	}

	// no real user yet: the configs are tested all the same
	out := check()
	sum := out["summary"].(map[string]any)
	if sum["ok"] != 1.0 || sum["fail"] != 1.0 || sum["skip"] != 1.0 || out["at"] == 0.0 || out["hours"] != 3.0 {
		t.Fatalf("first check: %v", out)
	}
	byPreset := map[string]map[string]any{}
	for _, r := range out["results"].([]any) {
		m := r.(map[string]any)
		byPreset[m["preset"].(string)] = m
	}
	if r := byPreset["vless-reality-vision"]; r["status"] != "ok" || r["ms"].(float64) <= 0 || r["inboundId"] != float64(vless.ID) {
		t.Errorf("VLESS: %v", r)
	}
	if r := byPreset["tuic"]; r["status"] != "fail" || !strings.Contains(r["error"].(string), "the connection through the config failed") {
		t.Errorf("TUIC: %v", r)
	}
	if ev := events(); len(ev) != 1 || !strings.Contains(ev[0], "1 of 2 configs fail") || !strings.Contains(ev[0], "TUIC") {
		t.Fatalf("events after the first check: %q", ev)
	}

	// the same again: nothing new to tell
	check()
	if ev := events(); len(ev) != 1 {
		t.Fatalf("a repeated failure was logged again: %q", ev)
	}
	// TUIC works again
	tuicDown.Store(false)
	out = check()
	if sum := out["summary"].(map[string]any); sum["ok"] != 2.0 || sum["fail"] != 0.0 {
		t.Fatalf("after the fix: %v", out["summary"])
	}
	if ev := events(); len(ev) != 2 || !strings.Contains(ev[0], "working again") {
		t.Fatalf("no recovery event: %q", ev)
	}

	// metrics: one series per tested config
	var p prom
	srv.healthMetrics(&p)
	m := p.b.String()
	if !strings.Contains(m, `dunvex_config_up{id="`+strconv.FormatInt(tuic.ID, 10)+`",protocol="tuic",name="Dunvex · TUIC v5",server="panel",via="direct"} 1`) ||
		!strings.Contains(m, "dunvex_config_latency_milliseconds") || !strings.Contains(m, "dunvex_config_check_timestamp_seconds") || strings.Contains(m, "mieru") {
		t.Fatalf("metrics:\n%s", m)
	}

	// the schedule
	if code, _ := owner.do("PUT", "/settings", map[string]string{"health_hours": "5"}); code != 400 {
		t.Errorf("5 hours accepted: %d", code)
	}
	if code, _ := owner.do("PUT", "/settings", map[string]string{"health_hours": "6"}); code != 200 {
		t.Errorf("6 hours refused: %d", code)
	}
	if _, out := owner.do("GET", "/health", nil); out["hours"] != 6.0 || out["next"] == nil {
		t.Errorf("schedule: %v %v", out["hours"], out["next"])
	}
	found := false
	for _, k := range webhook.Kinds {
		found = found || k == "health"
	}
	if !found {
		t.Error("webhooks cannot follow the check")
	}
}
