package engine

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/edge"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

func freeUDP(from int) int {
	for p := from; p < 65000; p++ {
		if util.FreePort(p) && util.FreeUDPPort(p) {
			return p
		}
	}
	return 0
}

// TestXDNSOnRealXray tunnels a real Xray client through DNS queries to a real
// Xray server (direct mode, no public resolver needed). Needs DUNVEX_TEST_BIN.
func TestXDNSOnRealXray(t *testing.T) {
	src := os.Getenv("DUNVEX_TEST_BIN")
	if src == "" {
		t.Skip("DUNVEX_TEST_BIN not set")
	}
	exe := "xray"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	raw, err := os.ReadFile(filepath.Join(src, exe))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, exe), raw, 0o755)
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sara := &store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20), Enabled: true}
	st.CreateUser(sara)
	p := protocol.Get("vless-xdns")
	port := freeUDP(35300)
	in := &store.Inbound{Preset: p.ID, Port: port, Enabled: true, Params: protocol.Defaults(p, map[string]any{"xdnsDomain": "t.dunvex.test", "resolver": "direct"}, "")}
	st.CreateInbound(in)
	env := protocol.Env{Host: "127.0.0.1"}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.Xray.APIPort = nextFree(port + 1)
	defer m.Xray.Stop()
	if err := m.Apply(); err != nil {
		t.Fatalf("apply: %v | %v", err, m.Xray.Logs())
	}

	// the client is built from the panel's own link (vless + kcp + fm)
	link, _ := url.Parse(protocol.Link(p, in, sara, env, "XDNS"))
	var fm any
	if err := json.Unmarshal([]byte(link.Query().Get("fm")), &fm); err != nil {
		t.Fatalf("fm: %v", err)
	}
	host, lport, _ := net.SplitHostPort(link.Host)
	var linkPort int
	fmt.Sscan(lport, &linkPort)
	socks := nextFree(m.Xray.APIPort + 1)
	client, _ := json.Marshal(map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
		"outbounds": []any{map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{
			"address": host, "port": linkPort, "users": []any{map[string]any{"id": sara.UUID, "encryption": "none"}}}}},
			"streamSettings": map[string]any{"network": "kcp", "security": "none", "finalmask": fm}}},
	})
	cpath := filepath.Join(dir, "client.json")
	os.WriteFile(cpath, client, 0o600)
	cl := exec.Command(filepath.Join(bin, exe), "run", "-c", cpath)
	var clog strings.Builder
	cl.Stdout, cl.Stderr = &clog, &clog
	if err := cl.Start(); err != nil {
		t.Fatal(err)
	}
	defer cl.Process.Kill()
	time.Sleep(1500 * time.Millisecond)
	proxy, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", socks))
	hc := &http.Client{Timeout: 25 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	start := time.Now()
	res, err := hc.Get("http://cp.cloudflare.com/generate_204")
	if err != nil {
		t.Fatalf("through XDNS: %v | server: %v | client: %s", err, m.Xray.Logs(), clog.String())
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("status %d", res.StatusCode)
	}
	t.Logf("a request through the DNS tunnel took %v", time.Since(start).Round(time.Millisecond))
}

// TestTelegramProxyOnRealMTG runs a real mtg behind the shared-port router. A
// TLS hello naming the fake domain must reach mtg (with the PROXY header) and
// come back fronted by the real site; other names still reach the panel.
// Needs DUNVEX_TEST_MTG (folder with mtg) and internet access.
func TestTelegramProxyOnRealMTG(t *testing.T) {
	src := os.Getenv("DUNVEX_TEST_MTG")
	if src == "" {
		t.Skip("DUNVEX_TEST_MTG not set")
	}
	raw, err := os.ReadFile(filepath.Join(src, "mtg"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "mtg"), raw, 0o755)
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	r, err := edge.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	shared := r.Addr().(*net.TCPAddr).Port
	go r.Serve()
	panel := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "panel") })}
	go panel.Serve(r.PanelListener())
	defer panel.Close()

	p := protocol.Get("mtproto")
	in := &store.Inbound{Preset: p.ID, Port: shared, Enabled: true, Params: protocol.Defaults(p, nil, "")}
	st.CreateInbound(in)
	env := protocol.Env{Host: "127.0.0.1"}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.MTG.APIPort = nextFree(36100)
	m.SharedPort = func() int { return shared }
	m.OnRoutes = r.SetRoutes
	defer m.MTG.Stop()
	if err := m.Apply(); err != nil {
		t.Fatalf("apply: %v | %v", err, m.MTG.Logs())
	}
	if _, ok := r.Routes()[protocol.DefaultFakeTLS]; !ok {
		t.Fatalf("routes: %v", r.Routes())
	}
	time.Sleep(1500 * time.Millisecond)
	if !m.MTG.Running() {
		t.Fatalf("mtg should run: %v", m.MTG.Logs())
	}
	if v := m.MTG.Version(); v == "" {
		t.Error("mtg version")
	}

	// a probe naming the fake domain is fronted by the real site
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", r.Addr().String(), &tls.Config{ServerName: protocol.DefaultFakeTLS})
	if err != nil {
		t.Fatalf("TLS through mtg: %v | mtg: %v", err, m.MTG.Logs())
	}
	cert := conn.ConnectionState().PeerCertificates[0]
	conn.Close()
	if err := cert.VerifyHostname(protocol.DefaultFakeTLS); err != nil {
		t.Fatalf("the probe should see %s's own certificate, got %s", protocol.DefaultFakeTLS, cert.Subject)
	}
	// other names still reach the panel
	res, err := http.Get("http://" + r.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "panel" {
		t.Fatalf("panel: %q", body)
	}
	t.Logf("mtg %s fronted %s", m.MTG.Version(), cert.Subject.CommonName)
}
