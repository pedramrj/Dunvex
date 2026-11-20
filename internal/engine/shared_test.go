package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/edge"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestSharedPanelPort checks what the engine builds when Reality shares the
// panel's port: loopback listeners, a PROXY header for Xray only, one SNI route
// each, and a clear error for a protocol that cannot share.
func TestSharedPanelPort(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateUser(&store.User{Name: "alice", UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	add := func(preset string, port int, params map[string]any) *store.Inbound {
		p := protocol.Get(preset)
		in := &store.Inbound{Preset: p.ID, Port: port, Enabled: true, Params: protocol.Defaults(p, params, "example.com")}
		if err := st.CreateInbound(in); err != nil {
			t.Fatal(err)
		}
		return in
	}
	sb := add("vless-reality-universal", 443, map[string]any{"sni": "www.microsoft.com"})
	xr := add("vless-reality-xhttp", 443, map[string]any{"sni": "dl.google.com"})
	ws := add("vless-ws-tls", 443, nil)
	hy := add("hysteria2", 443, nil)
	other := add("vless-reality-vision", 2443, map[string]any{"sni": "www.apple.com"})

	cert, key, err := util.SelfSignedCert(filepath.Join(dir, "certs"), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	env := protocol.Env{Host: "203.0.113.10", Domain: "example.com", CertFile: cert, KeyFile: key, SelfSigned: true}
	m := NewManager(st, filepath.Join(dir, "bin"), filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.SharedPort = func() int { return 443 }
	b, err := m.build()
	if err != nil {
		t.Fatal(err)
	}

	if e := b.errs[ws.ID]; !strings.Contains(e, "only Reality") {
		t.Errorf("TLS inbound on the panel port should be refused, got %q", e)
	}
	for _, in := range []*store.Inbound{sb, xr, hy, other} {
		if e := b.errs[in.ID]; e != "" {
			t.Errorf("inbound %s: unexpected error %q", in.Preset, e)
		}
	}
	if len(b.routes) != 3 {
		t.Fatalf("routes (two SNIs and the fallback): %v", b.routes)
	}
	if b.routes[edge.Fallback] != b.routes["www.microsoft.com"] {
		t.Errorf("unknown names should go to the first shared Reality inbound, got %+v", b.routes[edge.Fallback])
	}

	var xcfg struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	json.Unmarshal(b.xray, &xcfg)
	byTag := map[string]map[string]any{}
	for _, ib := range xcfg.Inbounds {
		byTag[ib["tag"].(string)] = ib
	}
	x := byTag[xr.Tag()]
	if x["listen"] != "127.0.0.1" || x["port"].(float64) == 443 {
		t.Errorf("shared Xray inbound should be on loopback, got %v:%v", x["listen"], x["port"])
	}
	if pp := x["streamSettings"].(map[string]any)["sockopt"].(map[string]any)["acceptProxyProtocol"]; pp != true {
		t.Errorf("shared Xray inbound should accept the PROXY header")
	}
	if r := b.routes["dl.google.com"]; !r.ProxyHeader || !strings.HasSuffix(r.Addr, ":"+jsonNum(x["port"])) {
		t.Errorf("xray route: %+v", r)
	}
	if h := byTag[hy.Tag()]; h["port"].(float64) != 443 {
		t.Errorf("UDP inbound keeps the public port, got %v", h["port"])
	}
	if o := byTag[other.Tag()]; o["port"].(float64) != 2443 || o["listen"] == "127.0.0.1" {
		t.Errorf("an inbound on another port is untouched, got %v:%v", o["listen"], o["port"])
	}

	var scfg struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	json.Unmarshal(b.singbox, &scfg)
	s := scfg.Inbounds[0]
	if s["listen"] != "127.0.0.1" || s["listen_port"].(float64) == 443 {
		t.Errorf("shared sing-box inbound should be on loopback, got %v:%v", s["listen"], s["listen_port"])
	}
	if _, has := s["proxy_protocol"]; has {
		t.Errorf("sing-box removed PROXY protocol; the field must not be set")
	}
	if r := b.routes["www.microsoft.com"]; r.ProxyHeader || r.Addr != "127.0.0.1:"+jsonNum(s["listen_port"]) {
		t.Errorf("sing-box route: %+v", r)
	}

	// the router hears every apply
	var got map[string]edge.Target
	m.OnRoutes = func(r map[string]edge.Target) { got = r }
	m.Apply()
	if len(got) != 3 {
		t.Errorf("OnRoutes: %v", got)
	}

	// with real cores, both configs must still be accepted
	if bin := os.Getenv("DUNVEX_TEST_BIN"); bin != "" {
		m.Xray.BinDir, m.SingBox.BinDir = bin, bin
		if err := m.Xray.Test(b.xray); err != nil {
			t.Errorf("xray: %v", err)
		}
		if err := m.SingBox.Test(b.singbox); err != nil {
			t.Errorf("sing-box: %v", err)
		}
	}
}

// sing-box only sees the router: its connection lines are translated back to the client.
func TestSingboxLineThroughRouter(t *testing.T) {
	d := newDevices()
	d.resolve = func(addr string) string {
		if addr == "127.0.0.1:51000" {
			return "198.51.100.7"
		}
		return ""
	}
	d.singboxLine("+0330 2026-01-01 INFO [11 0ms] inbound/vless[in-1]: inbound connection from 127.0.0.1:51000")
	d.singboxLine("+0330 2026-01-01 INFO [11 0ms] inbound/vless[in-1]: [alice] inbound connection to example.com:443")
	if l := d.list("alice", 0); len(l) != 1 || l[0].IP != "198.51.100.7" {
		t.Fatalf("devices: %+v", l)
	}
}

func jsonNum(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
