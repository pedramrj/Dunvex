//go:build linux

package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/pedramrj/dunvex/internal/cdn"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestCDNProbeWithRealXray runs every CDN protocol in a real Xray and checks
// that the CDN check's probe recognizes each one the way the CDN would reach
// it, and tells a wrong path apart. Needs DUNVEX_CORE_DIR.
func TestCDNProbeWithRealXray(t *testing.T) {
	src := os.Getenv("DUNVEX_CORE_DIR")
	if src == "" {
		t.Skip("needs DUNVEX_CORE_DIR")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	copyCores(t, src, bin)
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.CreateUser(&store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(20), SubToken: util.RandomString(20), Enabled: true, ResetPeriod: "none"})
	cert, key, err := util.SelfSignedCert(filepath.Join(dir, "certs"), "cdn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	env := protocol.Env{Host: "127.0.0.1", Domain: "cdn.example.com", CertFile: cert, KeyFile: key, SelfSigned: true, CertSHA256: util.CertSHA256(cert)}
	presets := []string{"vless-ws-tls", "vless-httpupgrade-tls", "vless-grpc-tls", "vless-xhttp-tls", "trojan-ws-tls", "vmess-ws-tls", "vmess-ws"}
	port := nextFree(44100)
	for _, id := range presets {
		p := protocol.Get(id)
		if err := st.CreateInbound(&store.Inbound{Preset: id, Port: port, Enabled: true, Params: protocol.Defaults(p, nil, "cdn.example.com")}); err != nil {
			t.Fatal(err)
		}
		port = nextFree(port + 1)
	}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.Xray.APIPort, m.SingBox.APIPort = nextFree(44300), nextFree(44310)
	m.RouterPort, m.WarpTestPort, m.Usque.APIPort = nextFree(44320), nextFree(44330), nextFree(44340)
	defer m.Xray.Stop()
	if err := m.Apply(); err != nil {
		t.Fatalf("apply: %v %v", err, m.Xray.Logs())
	}
	ins, _ := st.Inbounds()
	ctx := context.Background()
	for _, in := range ins {
		p := protocol.Get(in.Preset)
		tg := cdn.Target{Transport: p.Transport(), TLS: p.Security == "tls", Dial: "127.0.0.1:" + strconv.Itoa(in.Port), Host: "cdn.example.com",
			Path: in.Str("path"), Service: in.Str("serviceName"), Insecure: true}
		var r cdn.Result
		for i := 0; i < 20; i++ { // Xray may still be starting
			if r = cdn.Probe(ctx, tg); r.Code != "refused" {
				break
			}
			waitPort(in.Port)
		}
		if !r.OK {
			t.Errorf("%s: %+v\n%v", p.Name, r, lastLines(fmtLogs(m), 6))
			continue
		}
		t.Logf("%s on %d: %s in %d ms", p.Name, in.Port, r.Code, r.Ms)
		if p.Transport() == "grpc" {
			continue
		}
		bad := tg
		bad.Path = "/not-the-path"
		if r := cdn.Probe(ctx, bad); r.OK || r.Code == "timeout" {
			t.Errorf("%s with a wrong path: %+v", p.Name, r)
		} else {
			t.Logf("%s with a wrong path: %s %d", p.Name, r.Code, r.Status)
		}
	}
}

func fmtLogs(m *Manager) string { return fmt.Sprint(m.Xray.Logs()) }
