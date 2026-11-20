package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/geodata"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/routing"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

func TestRoutingPageAndWarp(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	adminHash, _ := HashPassword("admin-pass-1")
	st.CreateAdmin(&store.Admin{Username: "helper", PasswordHash: adminHash, Role: store.RoleAdmin, Enabled: true})
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, filepath.Join(dir, "bin"), filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	// a stand-in Cloudflare
	deleted := false
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/reg":
			w.Write([]byte(`{"id":"dev-9","token":"tok-9","account":{"account_type":"free"},"config":{"client_id":"BAUG",
			"peers":[{"public_key":"bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=","endpoint":{"v4":"162.159.192.5:0","ports":[2408]}}],
			"interface":{"addresses":{"v4":"172.16.0.2","v6":"2606:4700:110:1::2"}}}}`))
		case r.Method == "PUT" && r.URL.Path == "/reg/dev-9/account":
			w.WriteHeader(204)
		case r.Method == "GET" && r.URL.Path == "/reg/dev-9/account":
			w.Write([]byte(`{"account_type":"limited"}`))
		case r.Method == "DELETE" && r.URL.Path == "/reg/dev-9":
			deleted = true
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cf.Close()
	srv.WarpAPI = cf.URL
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	helper := newClient(t, ts.URL)
	helper.do("POST", "/login", map[string]string{"username": "helper", "password": "admin-pass-1"})
	if code, _ := helper.do("GET", "/routing", nil); code != 403 {
		t.Errorf("an admin read the routing: %d", code)
	}

	code, out := owner.do("GET", "/routing", nil)
	set := out["settings"].(map[string]any)
	if code != 200 || set["iran"] != "direct" || set["clientIran"] != true || out["warp"] != nil || out["blockTorrent"] != true || len(out["groups"].([]any)) != len(routing.Groups) {
		t.Fatalf("first look: %d %v", code, out)
	}
	// WARP rules need a WARP device first
	code, out = owner.do("PUT", "/routing", map[string]any{"settings": map[string]any{"warp": "all", "iran": "direct", "clientIran": true}})
	if code != 400 || !strings.Contains(out["error"].(string), "WARP on first") {
		t.Errorf("WARP without a device: %d %v", code, out)
	}
	code, out = owner.do("PUT", "/routing", map[string]any{"settings": map[string]any{"iran": "direct", "warp": "off", "blockDomains": []string{"not a domain!"}}})
	if code != 400 {
		t.Errorf("a bad domain: %d %v", code, out)
	}
	code, out = owner.do("POST", "/routing/warp", nil)
	w, _ := out["warp"].(map[string]any)
	if code != 200 || w == nil || w["addressV4"] != "172.16.0.2" || w["endpoint"] != "162.159.192.5:2408" || w["type"] != "free" {
		t.Fatalf("register: %d %v", code, out)
	}
	if a := routing.Account(st); a == nil || a.Token != "tok-9" {
		t.Fatalf("stored device: %+v", a)
	}
	if raw, _ := json.Marshal(out); strings.Contains(string(raw), "tok-9") || strings.Contains(string(raw), routing.Account(st).PrivateKey) {
		t.Error("the device's token or key reached the page")
	}
	code, out = owner.do("PUT", "/routing", map[string]any{"blockTorrent": false, "settings": map[string]any{
		"blockAds": true, "iran": "warp", "clientIran": false, "warp": "sites", "warpGroups": []string{"ai"}, "warpDomains": []string{"https://grok.com/chat"}}})
	set, _ = out["settings"].(map[string]any)
	if code != 200 || set["iran"] != "warp" || set["warpDomains"].([]any)[0] != "grok.com" || out["blockTorrent"] != false {
		t.Fatalf("save: %d %v", code, out)
	}
	if code, out = owner.do("POST", "/routing/warp/license", map[string]string{"license": "ab12-cd34-ef56"}); code != 200 || out["warp"].(map[string]any)["type"] != "limited" {
		t.Errorf("license: %d %v", code, out)
	}
	// turning WARP off takes its rules away and removes the device
	code, out = owner.do("DELETE", "/routing/warp", nil)
	set, _ = out["settings"].(map[string]any)
	if code != 200 || out["warp"] != nil || set["warp"] != "off" || set["iran"] != "direct" || set["blockAds"] != true || !deleted {
		t.Errorf("remove: %d %v deleted %v", code, out, deleted)
	}
}

func TestIranianSitesDirectInSubscriptions(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	geodata.WriteSite(filepath.Join(bin, "geosite.dat"), map[string][]geodata.Domain{
		"category-ir": {{Kind: geodata.Suffix, Value: "aparat.com"}, {Kind: geodata.Full, Value: "www.digikala.com"}}})
	geodata.WriteIP(filepath.Join(bin, "geoip.dat"), map[string][]string{"ir": {"2.144.0.0/14", "2a01:5ec0::/29"}})
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	p := protocol.Get("vless-reality-vision")
	st.CreateInbound(&store.Inbound{Preset: p.ID, Port: 8443, Enabled: true, Params: protocol.Defaults(p, nil, "")})
	st.CreateUser(&store.User{Name: "sara", UUID: util.UUID(), Password: "sara-pass-0123456789", SubToken: "sara-token-0123456789", Enabled: true, ResetPeriod: "none"})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	get := func(ua string) string {
		req, _ := http.NewRequest("GET", ts.URL+"/sub/sara-token-0123456789", nil)
		req.Header.Set("User-Agent", ua)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}

	clash := get("clash.meta")
	for _, want := range []string{"  - DOMAIN-SUFFIX,ir,DIRECT\n", "  - DOMAIN-SUFFIX,aparat.com,DIRECT\n", "  - DOMAIN,www.digikala.com,DIRECT\n", "  - GEOIP,IR,DIRECT\n  - MATCH,"} {
		if !strings.Contains(clash, want) {
			t.Errorf("clash lacks %q:\n%s", want, clash)
		}
	}
	var sb struct {
		DNS struct {
			Rules []map[string]any `json:"rules"`
		} `json:"dns"`
		Route struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	raw := get("sing-box")
	if err := json.Unmarshal([]byte(raw), &sb); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	rules, _ := json.Marshal(sb.Route.Rules)
	for _, want := range []string{`{"domain":["www.digikala.com"],"domain_suffix":["ir","aparat.com"],"outbound":"direct"}`, `{"ip_cidr":["2.144.0.0/14"],"outbound":"direct"}`} {
		if !strings.Contains(string(rules), want) {
			t.Errorf("sing-box route lacks %s:\n%s", want, rules)
		}
	}
	if len(sb.DNS.Rules) != 1 || sb.DNS.Rules[0]["server"] != "local" {
		t.Errorf("Iranian names are not resolved on the device: %v", sb.DNS.Rules)
	}

	// turned off: everything goes through the VPN again
	routing.Save(st, routing.Settings{Iran: routing.IranDirect, Warp: routing.WarpOff, ClientIran: false})
	if c := get("clash.meta"); strings.Contains(c, "DOMAIN-SUFFIX,ir") || strings.Contains(c, "GEOIP,IR") {
		t.Errorf("clash still sends Iran direct:\n%s", c)
	}
	if s := get("sing-box"); strings.Contains(s, "ip_cidr") {
		t.Error("sing-box still sends Iran direct")
	}
}
