package server

import (
	"context"
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

// tricksServer is a panel with a few protocols, its owner signed in, and the
// fake DNS and kernel the tricks look at.
func tricksServer(t *testing.T, brutalReady bool) (*Server, *client, func(path string) string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	st.SetSetting("host", "203.0.113.7")
	st.SetSetting("domain", "vpn.dunvex.invalid")
	for _, id := range []string{"vless-reality-universal", "vless-xhttp-tls", "vless-ws-tls", "trojan-tls", "wireguard", "shadowtls", "anytls"} {
		p := protocol.Get(id)
		if err := st.CreateInbound(&store.Inbound{Preset: id, Port: 32000 + len(id), Enabled: true, Params: protocol.Defaults(p, nil, "vpn.dunvex.invalid")}); err != nil {
			t.Fatal(err)
		}
	}
	st.CreateUser(&store.User{Name: "sara", UUID: util.UUID(), Password: "sara-pass-0123456789", SubToken: "sara-token-0123456789", Enabled: true, ResetPeriod: "none"})
	// the kernel and the DNS, as the tests want them
	oldCheck, oldLookup, oldCF := brutalCheck, echLookup, onCloudflare
	brutalCheck = func() brutalState {
		if brutalReady {
			return brutalState{Ready: true}
		}
		return brutalState{Why: "no module"}
	}
	echLookup = func(ctx context.Context, name string) (string, error) {
		if name == cloudflareECHName {
			return "AEX+DQBBbQAgACAY2Y+YjJB4JCzJZpSh+4KZEhCvixWisGKx7+fftZ7lNQAEAAEAAQASY2xvdWRmbGFyZS1lY2guY29tAAA=", nil
		}
		return "", nil // the CDN name has no record of its own
	}
	onCloudflare = func(context.Context, string) bool { return true }
	forgetBrutal()
	forgetCDNECH()
	t.Cleanup(func() {
		brutalCheck, echLookup, onCloudflare = oldCheck, oldLookup, oldCF
		forgetBrutal()
		forgetCDNECH()
	})

	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, filepath.Join(dir, "bin"), filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	get := func(path string) string {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	return srv, owner, get
}

func subLinks(t *testing.T, get func(string) string) []string {
	raw, err := base64.StdEncoding.DecodeString(get("/sub/sara-token-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(raw), "\n")
}

func TestTrickSettings(t *testing.T) {
	srv, owner, get := tricksServer(t, false)
	for _, bad := range []map[string]string{
		{"mux": "true", "mux_streams": "0"},
		{"mux": "true", "mux_protocol": "quic"},
		{"mux_brutal": "true", "mux_up": "fast"},
		{"wg_noise": "true", "wg_noise_size": "0-5"},
		{"wg_noise": "true", "wg_noise_count": "99"},
		{"ech_direct": "true", "ech_name": "not a name"},
		{"ech_direct": "true", "ech_name": "1.2.3.4"},
		{"xhttp_split": "sideways"},
		{"fragment": "true", "fragment_delay": "9999"},
	} {
		if code, out := owner.do("PUT", "/settings", bad); code != 400 {
			t.Errorf("%v accepted: %d %v", bad, code, out)
		}
	}
	if code, out := owner.do("PUT", "/settings", map[string]string{"ech_direct": "true", "ech_name": "Www.Example.COM.", "mux": "true", "mux_protocol": "yamux",
		"mux_streams": "12", "mux_brutal": "true", "wg_noise": "true", "wg_noise_count": "3"}); code != 200 {
		t.Fatalf("settings: %d %v", code, out)
	}
	_, out := owner.do("GET", "/settings", nil)
	s := out["settings"].(map[string]any)
	if s["ech_direct"] != "true" || s["ech_name"] != "www.example.com" || s["mux_protocol"] != "yamux" || s["wg_noise_count"] != "3" || s["xhttp_split"] != "" {
		t.Fatalf("settings: %v", s)
	}
	if _, leaked := s["ech_keys"]; leaked {
		t.Fatal("the ECH keys are sent to the panel")
	}
	info := out["info"].(map[string]any)["tricks"].(map[string]any)
	if at := info["ech"].(map[string]any)["publicName"]; at != "www.example.com" {
		t.Fatalf("ECH info: %v", info)
	}
	if b := info["brutal"].(map[string]any); b["ready"] != false || b["why"] == "" {
		t.Fatalf("brutal info: %v", info)
	}
	keys := srv.St.GetSetting(echKeysSetting, "")
	// saving other things keeps the keys; a new name makes new ones
	owner.do("PUT", "/settings", map[string]string{"sub_title": "Fast", "ech_direct": "true"})
	if srv.St.GetSetting(echKeysSetting, "") != keys {
		t.Fatal("the keys changed without a new name")
	}
	owner.do("PUT", "/settings", map[string]string{"ech_name": "cdn.example.org"})
	if k := srv.St.GetSetting(echKeysSetting, ""); k == keys || k == "" {
		t.Fatal("a new name kept the old keys")
	}
	cfg := srv.St.GetSetting(echConfigSetting, "")
	if n, _ := protocol.ECHPublicName(cfg); n != "cdn.example.org" {
		t.Fatalf("public name %q", n)
	}

	// the links carry it: ECH on the TLS ones (the AnyTLS link has no place for it),
	// noise on WireGuard, sing-box's multiplex (no Brutal: the kernel lacks it) for Hiddify
	links := subLinks(t, get)
	esc := url.QueryEscape(cfg)
	for _, l := range links {
		switch {
		case strings.HasPrefix(l, "vless://") && strings.Contains(l, "type=xhttp"), strings.HasPrefix(l, "vless://") && strings.Contains(l, "type=ws"), strings.HasPrefix(l, "trojan://"):
			if !strings.Contains(l, "ech="+esc) {
				t.Errorf("no ECH: %s", l)
			}
		case strings.HasPrefix(l, "wireguard://"):
			if !strings.Contains(l, "ifp=3") || !strings.Contains(l, url.QueryEscape(`"type":"noise"`)) {
				t.Errorf("no noise: %s", l)
			}
		case strings.HasPrefix(l, "vless://") && strings.Contains(l, "security=reality"):
			if !strings.Contains(l, "muxtype=yamux") || !strings.Contains(l, "muxsmax=12") || strings.Contains(l, "muxup=") {
				t.Errorf("sing-box multiplex: %s", l)
			}
		}
	}
	// the Xray JSON has Mux.Cool on the Xray protocols that take it, none on sing-box's
	var list []struct {
		Remarks   string           `json:"remarks"`
		Outbounds []map[string]any `json:"outbounds"`
	}
	json.Unmarshal([]byte(get("/sub/sara-token-0123456789/xray")), &list)
	for _, c := range list[1:] {
		_, has := c.Outbounds[0]["mux"]
		ss, _ := c.Outbounds[0]["streamSettings"].(map[string]any)
		want := ss != nil && (ss["network"] == "ws" || (ss["network"] == "raw" && ss["security"] == "tls"))
		if has != want {
			t.Errorf("%s: Mux.Cool %v", c.Remarks, has)
		}
	}
	// the server's side: its own cores get the keys and sing-box's multiplex
	xray, sb, _, _, err := srv.Mgr.Configs()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(xray), srv.St.GetSetting(echKeysSetting, "")) || !strings.Contains(string(sb), `"multiplex"`) || strings.Contains(string(sb), `"brutal"`) {
		t.Fatalf("server configs:\n%s\n%s", xray, sb)
	}
}

// With the module in the kernel, Brutal reaches this server's sing-box
// protocols and their apps; another server's configs get neither multiplex nor ECH.
func TestBrutalAndNodes(t *testing.T) {
	srv, owner, get := tricksServer(t, true)
	if code, out := owner.do("PUT", "/settings", map[string]string{"mux": "true", "mux_brutal": "true", "mux_up": "25", "mux_down": "150", "ech_direct": "true"}); code != 200 {
		t.Fatalf("settings: %d %v", code, out)
	}
	_, sb, _, _, _ := srv.Mgr.Configs()
	if !strings.Contains(string(sb), `"brutal"`) || !strings.Contains(string(sb), `"up_mbps": 150`) {
		t.Fatalf("server Brutal:\n%s", sb)
	}
	var prof map[string]any
	json.Unmarshal([]byte(get("/sub/sara-token-0123456789?format=singbox")), &prof)
	js, _ := json.Marshal(prof["outbounds"])
	if !strings.Contains(string(js), `"brutal":{"down_mbps":150,"enabled":true,"up_mbps":25}`) {
		t.Fatalf("app Brutal: %s", js)
	}
	clash := get("/sub/sara-token-0123456789?format=clash")
	if !strings.Contains(clash, `"brutal-opts":{"down":150,"enabled":true,"up":25}`) {
		t.Fatalf("Clash smux: %s", clash)
	}
	local := srv.Env()
	node := srv.envFor(&store.Inbound{NodeID: 5}, local, map[int64]*store.Node{5: {ID: 5, Address: "198.51.100.9"}})
	if node.ECH != nil || node.Mux == nil || node.Mux.Singbox || node.Mux.Brutal != nil {
		t.Fatalf("node env: %+v %+v", node.ECH, node.Mux)
	}
	// an inbound whose links go elsewhere (its own address, often a CDN) leaves this server's ECH out
	p := protocol.Get("vless-ws-tls")
	in := &store.Inbound{Preset: p.ID, Port: 2087, Enabled: true, Params: protocol.Defaults(p, map[string]any{"address": "cdn.example.com"}, "vpn.dunvex.invalid")}
	if e := directEnv(in, local); e.ECH != nil {
		t.Fatal("this server's ECH on a config to another address")
	}
}

// Through the CDN: the CDN's ECH (Cloudflare's shared one when the name has
// none), never this server's; and XHTTP that downloads straight, or uploads straight.
func TestCDNECHAndSplit(t *testing.T) {
	_, owner, get := tricksServer(t, false)
	if code, out := owner.do("PUT", "/cdn", map[string]any{"enabled": true, "domain": "cdn.dunvex.invalid", "addresses": []map[string]string{{"address": "104.16.9.9", "name": "MCI"}}}); code != 200 {
		t.Fatalf("cdn: %d %v", code, out)
	}
	if code, out := owner.do("PUT", "/settings", map[string]string{"ech_cdn": "true", "ech_direct": "true", "xhttp_split": "down"}); code != 200 {
		t.Fatalf("settings: %d %v", code, out)
	}
	_, out := owner.do("GET", "/settings", nil)
	if e := out["info"].(map[string]any)["tricks"].(map[string]any)["echCdn"].(map[string]any); e["publicName"] != "cloudflare-ech.com" || e["source"] != cloudflareECHName {
		t.Fatalf("CDN ECH info: %v", e)
	}
	cfRaw := "AEX+DQBBbQAgACAY2Y+YjJB4JCzJZpSh+4KZEhCvixWisGKx7+fftZ7lNQAEAAEAAQASY2xvdWRmbGFyZS1lY2guY29tAAA="
	cf := url.QueryEscape(cfRaw)
	n := 0
	for _, l := range subLinks(t, get) {
		u, err := url.Parse(l)
		if err != nil || u.Query().Get("sni") != "cdn.dunvex.invalid" {
			continue
		}
		n++
		if !strings.Contains(l, "ech="+cf) {
			t.Errorf("not the CDN's ECH: %s", l)
		}
		if u.Query().Get("type") == "xhttp" {
			o, err := protocol.XrayOutbound(l, "proxy")
			if err != nil {
				t.Fatal(err)
			}
			d := o["streamSettings"].(map[string]any)["xhttpSettings"].(map[string]any)["extra"].(map[string]any)["downloadSettings"].(map[string]any)
			tl := d["tlsSettings"].(map[string]any)
			if d["address"] != "203.0.113.7" || tl["serverName"] != "vpn.dunvex.invalid" || tl["pinnedPeerCertSha256"] == nil || tl["echConfigList"] == nil || tl["echConfigList"] == cfRaw {
				t.Errorf("downloads straight: %v", d)
			}
		}
	}
	if n != 4 { // XHTTP and WebSocket, each to the CDN name and the clean address
		t.Fatalf("%d configs through the CDN", n)
	}
	// uploads straight, downloads through the CDN
	owner.do("PUT", "/settings", map[string]string{"xhttp_split": "up"})
	for _, l := range subLinks(t, get) {
		if !strings.Contains(l, "type=xhttp") || !strings.Contains(l, "extra=") {
			continue
		}
		u, _ := url.Parse(l)
		o, _ := protocol.XrayOutbound(l, "proxy")
		d := o["streamSettings"].(map[string]any)["xhttpSettings"].(map[string]any)["extra"].(map[string]any)["downloadSettings"].(map[string]any)
		if u.Hostname() != "203.0.113.7" || u.Query().Get("pcs") == "" || (d["address"] != "cdn.dunvex.invalid" && d["address"] != "104.16.9.9") ||
			d["tlsSettings"].(map[string]any)["serverName"] != "cdn.dunvex.invalid" {
			t.Errorf("uploads straight: %s\n%v", l, d)
		}
	}
}
