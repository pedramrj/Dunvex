package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestXraySubscription: /xray gives one whole Xray config per config Xray can
// open, and the fragment setting reaches those configs and the plain links.
func TestXraySubscription(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	st.SetSetting("host", "203.0.113.7")
	st.SetSetting("domain", "vpn.dunvex.invalid")
	for _, id := range []string{"vless-reality-vision", "vmess-ws-tls", "trojan-tls", "hysteria2", "tuic", "mieru"} {
		p := protocol.Get(id)
		if err := st.CreateInbound(&store.Inbound{Preset: id, Port: 31000 + len(id), Enabled: true, Params: protocol.Defaults(p, nil, "vpn.dunvex.invalid")}); err != nil {
			t.Fatal(err)
		}
	}
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, filepath.Join(dir, "bin"), filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	st.CreateUser(&store.User{Name: "sara", UUID: util.UUID(), Password: "sara-pass-0123456789", SubToken: "sara-token-0123456789", Enabled: true, ResetPeriod: "none"})
	get := func(path string) (string, string) {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b), res.Header.Get("Content-Type")
	}
	type cfg struct {
		Remarks   string           `json:"remarks"`
		Outbounds []map[string]any `json:"outbounds"`
		Routing   map[string]any   `json:"routing"`
		Inbounds  []map[string]any `json:"inbounds"`
	}
	read := func() ([]cfg, string) {
		body, ct := get("/sub/sara-token-0123456789/xray")
		var list []cfg
		if err := json.Unmarshal([]byte(body), &list); err != nil || !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("not a JSON array (%s): %v\n%s", ct, err, body)
		}
		// what v2rayNG looks for before it reads a body as Xray configs
		for _, w := range []string{"inbounds", "outbounds", "routing"} {
			if !strings.Contains(body, w) {
				t.Fatalf("v2rayNG would not take it: no %q", w)
			}
		}
		return list, body
	}

	list, body := read()
	// the usage line first, then Reality, VMess, Trojan and Hysteria2; no TUIC, no Mieru
	if len(list) != 5 || !strings.Contains(list[0].Remarks, "📊") {
		names := []string{}
		for _, c := range list {
			names = append(names, c.Remarks)
		}
		t.Fatalf("configs: %q", names)
	}
	protos := map[string]bool{}
	for _, c := range list[1:] {
		if c.Outbounds[0]["tag"] != "proxy" || c.Outbounds[1]["protocol"] != "freedom" || c.Inbounds[0]["port"] != 10808.0 {
			t.Errorf("%s: %v", c.Remarks, c.Outbounds)
		}
		protos[c.Outbounds[0]["protocol"].(string)] = true
	}
	if !protos["vless"] || !protos["vmess"] || !protos["trojan"] || !protos["hysteria"] || strings.Contains(body, "tuic") || strings.Contains(body, "mieru") {
		t.Fatalf("protocols: %v", protos)
	}
	if strings.Contains(body, `"fragment"`) {
		t.Fatal("a fragment while it is off")
	}

	// the owner turns the fragment on
	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	if code, out := owner.do("PUT", "/settings", map[string]string{"fragment": "true", "fragment_length": "500-100"}); code != 400 || !strings.Contains(out["error"].(string), "length") {
		t.Fatalf("a bad length: %d %v", code, out)
	}
	if code, out := owner.do("PUT", "/settings", map[string]string{"fragment": "true", "fragment_packets": "1-3", "fragment_length": "40-80", "fragment_delay": "5-15"}); code != 200 {
		t.Fatalf("fragment on: %d %v", code, out)
	}
	if _, out := owner.do("GET", "/settings", nil); out["settings"].(map[string]any)["fragment_length"] != "40-80" {
		t.Fatalf("settings: %v", out["settings"])
	}
	list, _ = read()
	for _, c := range list[1:] {
		js, _ := json.Marshal(c.Outbounds[0])
		has := strings.Contains(string(js), `"type":"fragment"`)
		if want := c.Outbounds[0]["protocol"] != "hysteria"; has != want {
			t.Errorf("%s: fragment %v, want %v: %s", c.Remarks, has, want, js)
		}
		if has && !strings.Contains(string(js), `"length":"40-80"`) {
			t.Errorf("%s: %s", c.Remarks, js)
		}
	}
	// plain links of TLS and Reality configs carry it as "fm"
	raw, _ := get("/sub/sara-token-0123456789")
	links, _ := base64.StdEncoding.DecodeString(raw)
	n := 0
	for _, l := range strings.Split(string(links), "\n") {
		if u, err := url.Parse(l); err == nil && strings.Contains(u.Query().Get("fm"), `"packets":"1-3"`) {
			n++
		}
	}
	if n != 2 { // vless Reality and trojan TLS; vmess links have no place for it, Hysteria2 is QUIC
		t.Fatalf("%d links with the fragment:\n%s", n, links)
	}

	// a blocked user gets one config that says why
	u, _ := st.UserByName("sara")
	u.Enabled = false
	st.UpdateUser(u)
	if list, _ = read(); len(list) != 1 || !strings.Contains(list[0].Remarks, "disabled") {
		t.Fatalf("blocked: %+v", list)
	}

	// the user page offers it to v2rayNG
	if page, _ := get("/sub/sara-token-0123456789?format=html"); !strings.Contains(page, url.QueryEscape("/sub/sara-token-0123456789/xray")) {
		t.Error("no Xray JSON button on the user page")
	}
}
