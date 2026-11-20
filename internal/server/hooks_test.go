package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/webhook"
)

func TestWebhooksAndMetrics(t *testing.T) {
	var mu sync.Mutex
	type got struct {
		h http.Header
		b []byte
	}
	var hits []got
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits = append(hits, got{r.Header.Clone(), b})
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer recv.Close()
	received := func(kind string) []got {
		mu.Lock()
		defer mu.Unlock()
		var out []got
		for _, h := range hits {
			if h.h.Get("X-Dunvex-Event") == kind {
				out = append(out, h)
			}
		}
		return out
	}
	waitHits := func(kind string, n int) []got {
		for i := 0; i < 200 && len(received(kind)) < n; i++ {
			time.Sleep(10 * time.Millisecond)
		}
		return received(kind)
	}

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	ahash, _ := HashPassword("admin-pass-1")
	st.CreateAdmin(&store.Admin{Username: "helper", PasswordHash: ahash, Role: store.RoleAdmin, Enabled: true})
	st.SetSetting("host", "127.0.0.1")
	st.SetSetting("sub_title", "Test Panel")
	srv := &Server{Home: dir, Version: "v1.19.0", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	srv.Hooks = &webhook.Sender{St: st, Version: srv.Version, Panel: func() string { return st.GetSetting("sub_title", "") }, Backoff: []time.Duration{}}
	st.OnLog = srv.OnEvent
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	helper := newClient(t, ts.URL)
	helper.do("POST", "/login", map[string]string{"username": "helper", "password": "admin-pass-1"})
	if code, _ := helper.do("GET", "/webhooks", nil); code != 403 {
		t.Errorf("an admin read the webhooks: %d", code)
	}

	// a webhook for user events
	if code, _ := owner.do("POST", "/webhooks", map[string]any{"url": "javascript:alert(1)"}); code != 400 {
		t.Error("a bad URL was accepted")
	}
	code, out := owner.do("POST", "/webhooks", map[string]any{"name": "My app", "url": recv.URL + "/in", "kinds": []string{"user"}, "enabled": true})
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	hk := out["hook"].(map[string]any)
	id, secret := hk["id"].(string), hk["secret"].(string)
	if !strings.HasPrefix(secret, "whsec_") || hk["format"] != "json" {
		t.Fatalf("hook: %v", hk)
	}
	owner.do("POST", "/users", map[string]any{"name": "sara"})
	h := waitHits("user", 1)
	if len(h) != 1 {
		t.Fatalf("user event not delivered: %d", len(h))
	}
	var ev webhook.Event
	json.Unmarshal(h[0].b, &ev)
	if ev.Type != "user" || !strings.Contains(ev.Message, "created 1 user") || ev.Panel != "Test Panel" || ev.Version != "v1.19.0" ||
		!webhook.Verify(secret, h[0].h.Get("X-Dunvex-Signature"), h[0].b, time.Minute) {
		t.Fatalf("event: %+v", ev)
	}
	if len(received("settings")) != 0 {
		t.Error("an event of another kind was delivered")
	}
	// test, deliveries, last delivery in the list
	if code, out := owner.do("POST", "/webhooks/"+id+"/test", nil); code != 200 || out["ok"] != true || out["event"] != "test" {
		t.Fatalf("test: %d %v", code, out)
	}
	code, out = owner.do("GET", "/webhooks/"+id+"/deliveries", nil)
	if l := out["list"].([]any); code != 200 || len(l) != 2 || l[0].(map[string]any)["event"] != "test" {
		t.Fatalf("deliveries: %d %v", code, out)
	}
	_, out = owner.do("GET", "/webhooks", nil)
	if hs := out["hooks"].([]any); len(hs) != 1 || hs[0].(map[string]any)["last"] == nil || len(out["kinds"].([]any)) == 0 {
		t.Fatalf("list: %v", out)
	}
	// a new secret; off; removed
	_, out = owner.do("PUT", "/webhooks/"+id, map[string]any{"rotateSecret": true, "enabled": false})
	if nh := out["hook"].(map[string]any); nh["secret"] == secret || nh["enabled"] != false {
		t.Fatalf("update: %v", nh)
	}
	owner.do("POST", "/users", map[string]any{"name": "omid"})
	time.Sleep(100 * time.Millisecond)
	if len(received("user")) != 1 {
		t.Error("a webhook that is off was called")
	}
	if code, out := owner.do("DELETE", "/webhooks/"+id, nil); code != 200 || len(out["hooks"].([]any)) != 0 {
		t.Fatalf("delete: %d %v", code, out)
	}

	// metrics: an API key (read-only is enough), never anonymous
	res, _ := http.Get(ts.URL + "/dunvex/api/metrics")
	if res.StatusCode != 401 && res.StatusCode != 403 {
		t.Fatalf("anonymous metrics: %d", res.StatusCode)
	}
	res.Body.Close()
	_, out = owner.do("POST", "/account/keys", map[string]any{"name": "prometheus", "readOnly": true})
	key, _ := out["token"].(string)
	if key == "" {
		t.Fatalf("key: %v", out)
	}
	p := protocol.Get("vless-reality-vision")
	in := &store.Inbound{Preset: p.ID, Port: 31999, Enabled: true, Remark: `My "fast" one`, Params: protocol.Defaults(p, nil, "")}
	st.CreateInbound(in)
	st.AddTraffic(time.Now(), nil, map[int64]store.Traffic{in.ID: {Up: 1000, Down: 5000}})
	scrape := func(q string) string {
		req, _ := http.NewRequest("GET", ts.URL+"/dunvex/api/metrics"+q, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") {
			t.Fatalf("metrics: %d %s", res.StatusCode, b)
		}
		return string(b)
	}
	m := scrape("")
	for _, want := range []string{
		`dunvex_info{version="v1.19.0",panel="Test Panel"} 1`,
		`dunvex_users{status="pending"} 0`,
		`dunvex_users_online 0`,
		`dunvex_user_traffic_bytes_total{user="sara",direction="up"} 0`,
		`dunvex_inbound_traffic_bytes_total{id="1",protocol="vless-reality-vision",name="My \"fast\" one",server="panel",direction="down"} 5000`,
		`dunvex_server_up{server="panel",role="panel"} 1`,
		`dunvex_events_total{kind="user"} 2`,
		`dunvex_webhook_deliveries_total{result="ok"} 2`,
	} {
		if !strings.Contains(m, want+"\n") {
			t.Errorf("missing %s", want)
		}
	}
	if !strings.Contains(m, `dunvex_users{status="active"} 2`) && !strings.Contains(m, `dunvex_users{status="pending"} 0`) {
		t.Error("user counts")
	}
	// the text format: HELP and TYPE before each family, every family once,
	// every sample well formed
	line := regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*(\{([a-zA-Z_][a-zA-Z0-9_]*="([^"\\]|\\.)*",?)*\})? -?[0-9.e+-]+$`)
	seen := map[string]bool{}
	var current string
	for _, l := range strings.Split(strings.TrimSpace(m), "\n") {
		switch {
		case strings.HasPrefix(l, "# HELP "):
			current = strings.Fields(l)[2]
			if seen[current] {
				t.Errorf("%s appears twice", current)
			}
			seen[current] = true
		case strings.HasPrefix(l, "# TYPE "):
			if f := strings.Fields(l); f[2] != current || (f[3] != "gauge" && f[3] != "counter") {
				t.Errorf("bad TYPE line %q", l)
			}
		default:
			if !line.MatchString(l) || !strings.HasPrefix(l, current) {
				t.Errorf("bad sample %q", l)
			}
		}
	}
	if m := scrape("?users=false"); strings.Contains(m, "dunvex_user_traffic_bytes_total") {
		t.Error("?users=false kept the per-user series")
	}
}
