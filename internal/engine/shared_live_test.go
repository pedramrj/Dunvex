package engine

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/edge"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// hostIP is a non-loopback IPv4 address of this machine, so the router sees a
// real client address (127.0.0.1 is ignored by the device list).
func hostIP() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			return n.IP.String()
		}
	}
	return "127.0.0.1"
}

// TestSharedRealityOnRealXray runs a real Xray Reality inbound behind the edge
// router on one port with the panel: traffic must pass, the panel must still
// answer, and Xray must see the client's own address from the PROXY header.
// Needs DUNVEX_TEST_BIN (folder with xray).
func TestSharedRealityOnRealXray(t *testing.T) {
	src := os.Getenv("DUNVEX_TEST_BIN")
	if src == "" {
		t.Skip("DUNVEX_TEST_BIN not set")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	exe := "xray"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	raw, err := os.ReadFile(filepath.Join(src, exe))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(bin, exe), raw, 0o755)

	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bob := &store.User{Name: "bob", UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20), Enabled: true}
	if err := st.CreateUser(bob); err != nil {
		t.Fatal(err)
	}

	r, err := edge.Listen(":0")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	shared := r.Addr().(*net.TCPAddr).Port
	go r.Serve()
	panel := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "panel") })}
	go panel.Serve(r.PanelListener())
	defer panel.Close()

	// a local TLS 1.3 site stands in for Reality's camouflage target
	cover := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "cover") }))
	defer cover.Close()
	p := protocol.Get("vless-reality-vision")
	in := &store.Inbound{Preset: p.ID, Port: shared, Enabled: true, Params: protocol.Defaults(p, map[string]any{
		"sni": "www.example.com", "target": cover.Listener.Addr().String()}, "")}
	if err := st.CreateInbound(in); err != nil {
		t.Fatal(err)
	}
	env := protocol.Env{Host: "127.0.0.1"}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.Xray.APIPort = nextFree(32000)
	m.SharedPort = func() int { return shared }
	m.OnRoutes = r.SetRoutes
	m.ClientOf = r.ClientOf
	defer m.Xray.Stop()
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	if len(r.Routes()) != 2 { // the SNI and the fallback
		t.Fatalf("routes: %v", r.Routes())
	}

	// the panel still answers on the shared port
	res, err := http.Get("http://127.0.0.1:" + strconv.Itoa(shared) + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "panel" {
		t.Fatalf("panel: %q", body)
	}

	// a prober that names an unknown SNI gets the camouflage site, not the panel:
	// the router hands it to Reality, which forwards it to its target
	pc, err := tls.Dial("tcp", "127.0.0.1:"+strconv.Itoa(shared), &tls.Config{ServerName: "random-probe.example", InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("TLS probe with an unknown SNI: %v | server: %v", err, m.Xray.Logs())
	}
	got := pc.ConnectionState().PeerCertificates[0].Raw
	pc.Close()
	if !bytes.Equal(got, cover.Certificate().Raw) {
		t.Fatal("the probe should see the camouflage target's certificate")
	}

	// a real Xray client dials the shared port with the Reality SNI
	ip := hostIP()
	socks := nextFree(m.Xray.APIPort + 1)
	client, _ := json.Marshal(map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
		"outbounds": []any{map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{
			"address": ip, "port": shared, "users": []any{map[string]any{"id": bob.UUID, "encryption": "none", "flow": "xtls-rprx-vision"}}}}},
			"streamSettings": map[string]any{"network": "raw", "security": "reality", "realitySettings": map[string]any{
				"serverName": in.Str("sni"), "fingerprint": "chrome", "password": in.Str("publicKey"), "shortId": in.Str("shortId")}}}},
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
	hc := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	res, err = hc.Get("http://cp.cloudflare.com/generate_204")
	if err != nil {
		t.Fatalf("through shared Reality: %v | server: %v | client: %s", err, m.Xray.Logs(), clog.String())
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("status %d", res.StatusCode)
	}

	// Xray took the client's address from the PROXY header
	if ip == "127.0.0.1" {
		t.Log("no non-loopback address on this machine; skipping the client address check")
		return
	}
	var seen []string
	for i := 0; i < 10; i++ {
		online, err := xrayOnlineIPs(m.xrayAPI())
		if err == nil {
			seen = seen[:0]
			for _, o := range online["bob"] {
				seen = append(seen, o.IP)
				if o.IP == ip {
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("Xray should see bob from %s, saw %v", ip, seen)
}
