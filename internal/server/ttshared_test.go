package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
)

// TrustTunnel next to the panel and Reality on 443: it takes the port by
// default, with a hostname of its own, and HTTP/3 only while UDP 443 is free.
func TestTrustTunnelOnThePanelPort(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	st.SetSetting("host", "127.0.0.1")
	st.SetSetting("panel_port", "443")
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	bin := filepath.Join(dir, "bin")
	srv.Mgr = engine.NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	// the endpoint counts as installed, so nothing is downloaded
	os.MkdirAll(bin, 0o755)
	exe := "trusttunnel"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	os.WriteFile(filepath.Join(bin, exe), []byte("stand-in"), 0o755)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	re := protocol.Get("vless-reality-universal")
	st.CreateInbound(&store.Inbound{Preset: re.ID, Port: 443, Enabled: true, Params: protocol.Defaults(re, map[string]any{"sni": protocol.DefaultReality}, "")})
	hy := protocol.Get("hysteria2")
	st.CreateInbound(&store.Inbound{Preset: hy.ID, Port: 443, Enabled: true, Params: protocol.Defaults(hy, nil, "")})

	// HTTP/3 needs UDP 443, which Hysteria2 holds: asked for 443 it is
	// refused; left to choose, it takes its own port, where QUIC works
	if code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "trusttunnel", "port": 443, "params": map[string]any{"protocol": "http3"}}); code != 400 || !strings.Contains(out["error"].(string), "HTTP/2") {
		t.Fatalf("HTTP/3 on 443 next to Hysteria2: %d %v", code, out)
	}
	code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "trusttunnel", "params": map[string]any{"protocol": "http3"}})
	if in, _ := out["inbound"].(map[string]any); code != 200 || in["port"] == float64(443) {
		t.Fatalf("HTTP/3 next to Hysteria2: %d %v", code, out)
	} else {
		owner.do("DELETE", "/inbounds/"+itoa(int64(in["id"].(float64))), nil)
	}
	// by default: 443, and a hostname other than Reality's
	code, out = owner.do("POST", "/inbounds", map[string]any{"preset": "trusttunnel"})
	if code != 200 {
		t.Fatalf("TrustTunnel: %d %v", code, out)
	}
	ins, _ := st.Inbounds()
	tt := ins[len(ins)-1]
	if tt.Port != 443 || tt.Str("sni") == protocol.DefaultReality || tt.Str("sni") == "" || tt.Str("ttCertFor") != tt.Str("sni") {
		t.Fatalf("TrustTunnel got port %d and hostname %q (cert for %q)", tt.Port, tt.Str("sni"), tt.Str("ttCertFor"))
	}
	// the same hostname as Reality is refused
	if code, out := owner.do("PUT", "/inbounds/"+itoa(tt.ID), map[string]any{"params": map[string]any{"sni": "WWW.SPEEDTEST.NET"}}); code != 400 || !strings.Contains(out["error"].(string), "SNI") {
		t.Fatalf("Reality's hostname: %d %v", code, out)
	}
	// the panel's own HTTPS name is refused
	st.SetSetting("domain", "panel.example.com")
	st.SetSetting("panel_tls", "true")
	if code, out := owner.do("PUT", "/inbounds/"+itoa(tt.ID), map[string]any{"params": map[string]any{"sni": "panel.example.com"}}); code != 400 || !strings.Contains(out["error"].(string), "panel answers HTTPS") {
		t.Fatalf("the panel's name: %d %v", code, out)
	}
	st.SetSetting("panel_tls", "false")
	// without Hysteria2 on UDP 443, HTTP/3 is fine; a UDP protocol then cannot take UDP 443
	for _, in := range ins {
		if in.Preset == "hysteria2" {
			st.DeleteInbound(in.ID)
		}
	}
	if code, out := owner.do("PUT", "/inbounds/"+itoa(tt.ID), map[string]any{"params": map[string]any{"protocol": "http3"}}); code != 200 {
		t.Fatalf("HTTP/3 with UDP 443 free: %d %v", code, out)
	}
	if code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "tuic", "port": 443}); code != 400 || !strings.Contains(out["error"].(string), "HTTP/3") {
		t.Fatalf("UDP next to TrustTunnel's QUIC: %d %v", code, out)
	}
	owner.do("PUT", "/inbounds/"+itoa(tt.ID), map[string]any{"params": map[string]any{"protocol": "http2"}})
	if code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "tuic", "port": 443}); code != 200 {
		t.Fatalf("UDP next to TrustTunnel on HTTP/2: %d %v", code, out)
	}
}
