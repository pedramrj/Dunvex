package server

import (
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
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/node"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

func freePort(from int) int {
	for p := from; p < 65000; p++ {
		if util.FreePort(p) {
			return p
		}
	}
	return 0
}

func localIP() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			return n.IP.String()
		}
	}
	return "127.0.0.1"
}

// TestNodeWithRealXray: a panel and a node with a real Xray. A real client
// passes traffic through the node's Reality inbound; the panel must count it
// for the user and see the client's address. Needs DUNVEX_TEST_BIN (xray).
func TestNodeWithRealXray(t *testing.T) {
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

	// --- the node, with real Xray
	ndir := t.TempDir()
	os.MkdirAll(filepath.Join(ndir, "bin"), 0o755)
	os.WriteFile(filepath.Join(ndir, "bin", exe), raw, 0o755)
	nst, err := store.Open(filepath.Join(ndir, "node.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer nst.Close()
	ip := localIP()
	nenv := protocol.Env{Host: ip}
	agent := &node.Agent{Version: "test", St: nst, Sys: sysinfo.NewSampler(), Env: func() protocol.Env { return nenv }}
	agent.Mgr = engine.NewManager(nst, filepath.Join(ndir, "bin"), filepath.Join(ndir, "run"), agent.Env)
	agent.Mgr.Xray.APIPort = freePort(33100)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { agent.Mgr.Run(stop); close(done) }()
	defer func() { close(stop); <-done }()
	ncert, _, _ := node.LoadOrCreateCert(filepath.Join(ndir, "certs"), "agent", "dunvex-node")
	nts := httptest.NewUnstartedServer(agent.Handler())
	nts.TLS = agent.TLSConfig(ncert)
	nts.StartTLS()
	defer nts.Close()
	tok, _ := node.Init(nst, filepath.Join(ndir, "certs"), "127.0.0.1", nts.Listener.Addr().(*net.TCPAddr).Port, false)

	// --- the panel
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	host, port, _ := net.SplitHostPort(tok.Addr)
	var agentPort int
	fmt.Sscan(port, &agentPort)
	n := &store.Node{Name: "Helsinki", Address: host, AgentPort: agentPort, Fingerprint: tok.Fingerprint, Secret: tok.Secret, Enabled: true}
	if err := st.CreateNode(n); err != nil {
		t.Fatal(err)
	}
	// a local TLS 1.3 site stands in for Reality's camouflage target
	cover := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "cover") }))
	defer cover.Close()
	p := protocol.Get("vless-reality-vision")
	in := &store.Inbound{Preset: p.ID, Port: freePort(34100), Enabled: true, NodeID: n.ID,
		Params: protocol.Defaults(p, map[string]any{"sni": "www.example.com", "target": cover.Listener.Addr().String()}, "")}
	if err := st.CreateInbound(in); err != nil {
		t.Fatal(err)
	}
	sara := &store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20), Enabled: true}
	if err := st.CreateUser(sara); err != nil {
		t.Fatal(err)
	}

	h := srv.hub()
	sync := func() {
		fresh, _ := st.NodeByID(n.ID)
		h.sync(fresh)
	}
	sync()
	if l := h.snapshot(n.ID); l.Error != "" || l.Status == nil {
		t.Fatalf("first sync: %+v", l)
	}
	running := false
	for i := 0; i < 20 && !running; i++ {
		running = agent.Mgr.Xray.Running()
		time.Sleep(250 * time.Millisecond)
	}
	if !running {
		t.Fatalf("Xray should run on the node: %v", agent.Mgr.Xray.Logs())
	}

	// --- a real Xray client through the node, built from the panel's own link
	var link string
	for _, c := range srv.configsFor(sara) {
		if c.InboundID == in.ID {
			link = c.Link
		}
	}
	if !strings.HasPrefix(link, "vless://"+sara.UUID+"@") {
		t.Fatalf("link: %q", link)
	}
	socks := freePort(35100)
	client, _ := json.Marshal(map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
		"outbounds": []any{map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{
			"address": ip, "port": in.Port, "users": []any{map[string]any{"id": sara.UUID, "encryption": "none", "flow": "xtls-rprx-vision"}}}}},
			"streamSettings": map[string]any{"network": "raw", "security": "reality", "realitySettings": map[string]any{
				"serverName": in.Str("sni"), "fingerprint": "chrome", "password": in.Str("publicKey"), "shortId": in.Str("shortId")}}}},
	})
	cpath := filepath.Join(dir, "client.json")
	os.WriteFile(cpath, client, 0o600)
	cl := exec.Command(filepath.Join(ndir, "bin", exe), "run", "-c", cpath)
	var clog strings.Builder
	cl.Stdout, cl.Stderr = &clog, &clog
	if err := cl.Start(); err != nil {
		t.Fatal(err)
	}
	defer cl.Process.Kill()
	time.Sleep(1500 * time.Millisecond)
	proxy, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", socks))
	hc := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	for i := 0; i < 3; i++ {
		res, err := hc.Get("http://cp.cloudflare.com/generate_204")
		if err != nil {
			t.Fatalf("through the node: %v | node xray: %v | client: %s", err, agent.Mgr.Xray.Logs(), clog.String())
		}
		res.Body.Close()
	}

	// --- the node counts it (every 5 s), the panel collects it
	var used int64
	for i := 0; i < 30 && used == 0; i++ {
		time.Sleep(time.Second)
		sync()
		u, _ := st.UserByName("sara")
		used = u.Used()
	}
	if used == 0 {
		t.Fatal("the panel should count sara's traffic on the node")
	}
	if !srv.Mgr.OnlineUsers(60)[sara.ID] {
		t.Fatal("sara should be online")
	}
	if ip != "127.0.0.1" {
		seen := false
		for _, d := range srv.Mgr.Devices("sara") {
			seen = seen || d.IP == ip
		}
		if !seen {
			t.Fatalf("the panel should see sara's address %s, got %+v", ip, srv.Mgr.Devices("sara"))
		}
	}
	t.Logf("sara used %d bytes through the node", used)
}
