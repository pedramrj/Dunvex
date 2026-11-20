//go:build linux

package engine

import (
	"bytes"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/edge"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/relay"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestTrustTunnelBehindTheRouter puts TrustTunnel on the panel's port next to
// Reality: the router hands it TCP by its hostname (HTTP/2), its QUIC comes
// from the shared UDP port (HTTP/3), the panel still answers there and an
// unknown name still gets Reality's camouflage site. Needs root,
// DUNVEX_TT_BIN, DUNVEX_TT_CLIENT and DUNVEX_CORE_DIR.
func TestTrustTunnelBehindTheRouter(t *testing.T) {
	ttBin, clientBin, cores := os.Getenv("DUNVEX_TT_BIN"), os.Getenv("DUNVEX_TT_CLIENT"), os.Getenv("DUNVEX_CORE_DIR")
	if ttBin == "" || clientBin == "" || cores == "" || os.Geteuid() != 0 {
		t.Skip("needs root, DUNVEX_TT_BIN, DUNVEX_TT_CLIENT and DUNVEX_CORE_DIR")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	copyCores(t, cores, bin)
	raw, err := os.ReadFile(ttBin)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(bin, "trusttunnel"), raw, 0o755)
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sara := &store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(20), SubToken: util.RandomString(20), Enabled: true, ResetPeriod: "none"}
	st.CreateUser(sara)

	// the shared port: TCP through the router, UDP free for TrustTunnel's QUIC
	shared := freeUDP(44600)
	r, err := edge.Listen(":" + strconv.Itoa(shared))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go r.Serve()
	panel := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "panel") })}
	go panel.Serve(r.PanelListener())
	defer panel.Close()
	quic := relay.New(nil)
	defer quic.Close()

	cover := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "cover") }))
	defer cover.Close()
	re := protocol.Get("vless-reality-vision")
	st.CreateInbound(&store.Inbound{Preset: re.ID, Port: shared, Enabled: true, Params: protocol.Defaults(re, map[string]any{
		"sni": "www.speedtest.net", "target": cover.Listener.Addr().String()}, "")})
	tp := protocol.Get("trusttunnel")
	tt := &store.Inbound{Preset: tp.ID, Port: shared, Enabled: true, Params: protocol.Defaults(tp, map[string]any{"sni": "www.apple.com", "protocol": "http2"}, "")}
	st.CreateInbound(tt)

	host := or(util.OutboundIP(), "127.0.0.1")
	env := protocol.Env{Host: host}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.Xray.APIPort, m.SingBox.APIPort, m.TT.APIPort = nextFree(44700), nextFree(44710), nextFree(44720)
	m.RouterPort, m.WarpTestPort, m.Usque.APIPort = nextFree(44730), nextFree(44740), nextFree(44750)
	m.SharedPort = func() int { return shared }
	m.OnRoutes, m.ClientOf = r.SetRoutes, r.ClientOf
	m.OnUDP = func(f map[int]string) {
		var list []relay.Forward
		for port, addr := range f {
			list = append(list, relay.Forward{Name: "TrustTunnel (HTTP/3)", Listen: port, Network: "udp", Target: addr})
		}
		quic.Apply(list)
	}
	defer m.TT.Stop()
	defer m.Xray.Stop()
	if err := m.Apply(); err != nil {
		t.Fatalf("apply: %v\n%s", err, strings.Join(m.TT.Logs(), "\n"))
	}

	// the panel and Reality's camouflage are unchanged on the shared port
	res, err := http.Get("http://127.0.0.1:" + strconv.Itoa(shared) + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "panel" {
		t.Fatalf("panel: %q", body)
	}
	var pc *tls.Conn
	for i := 0; i < 5; i++ {
		if pc, err = tls.Dial("tcp", "127.0.0.1:"+strconv.Itoa(shared), &tls.Config{ServerName: "random-probe.example", InsecureSkipVerify: true}); err == nil {
			break
		}
		t.Logf("probe %d: %v", i, err)
		time.Sleep(time.Second)
	}
	if err != nil {
		t.Fatalf("probe: %v | routes: %v | xray: %s", err, r.Routes(), strings.Join(m.Xray.Logs(), "\n"))
	}
	got := pc.ConnectionState().PeerCertificates[0].Raw
	pc.Close()
	if !bytes.Equal(got, cover.Certificate().Raw) {
		t.Fatal("an unknown name should get Reality's camouflage site")
	}

	run := func(proto string) {
		t.Helper()
		ins, _ := st.Inbounds()
		var in *store.Inbound
		for _, x := range ins {
			if x.Preset == tp.ID {
				in = x
			}
		}
		f := ttDecode(t, protocol.Link(tp, in, sara, env, "TT"))
		if string(f[0x02][0]) != net.JoinHostPort(host, strconv.Itoa(shared)) || string(f[0x01][0]) != "www.apple.com" {
			t.Fatalf("link: %s %s", f[0x02][0], f[0x01][0])
		}
		socks := nextFree(44800)
		cfg := fmt.Sprintf(`loglevel = "info"
vpn_mode = "general"
killswitch_enabled = false
exclusions = []

[endpoint]
hostname = %q
addresses = [%q]
has_ipv6 = false
username = %q
password = %q
skip_verification = false
certificate = """
%s"""
upstream_protocol = %q
anti_dpi = false

[listener.socks]
address = "127.0.0.1:%d"
`, f[0x01][0], f[0x02][0], f[0x05][0], f[0x06][0], pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f[0x08][0]}), proto, socks)
		cfile := filepath.Join(dir, "client-"+proto+".toml")
		os.WriteFile(cfile, []byte(cfg), 0o600)
		c := exec.Command(clientBin, "--config", cfile)
		logf, _ := os.Create(cfile + ".log")
		c.Stdout, c.Stderr = logf, logf
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { c.Process.Kill(); c.Wait() }()
		waitPort(socks)
		var exit string
		for i := 0; i < 8 && exit == ""; i++ {
			out, _ := exec.Command("curl", "-s", "-m", "10", "--socks5-hostname", fmt.Sprintf("127.0.0.1:%d", socks), "https://1.1.1.1/cdn-cgi/trace").CombinedOutput()
			for _, l := range strings.Split(string(out), "\n") {
				if v, ok := strings.CutPrefix(l, "ip="); ok {
					exit = v
				}
			}
			if exit == "" {
				time.Sleep(2 * time.Second)
			}
		}
		if exit == "" {
			b, _ := os.ReadFile(cfile + ".log")
			t.Fatalf("%s through the shared port: no internet\nclient: %s\nserver: %s", proto, lastLines(string(b), 8), lastLines(strings.Join(m.TT.Logs(), "\n"), 8))
		}
		t.Logf("TrustTunnel %s on the shared port %d: traffic leaves as %s", proto, shared, exit)
	}
	run("http2")

	// HTTP/3: QUIC reaches the endpoint through the shared UDP port
	if len(quic.Stats()) != 1 || !quic.Stats()[0].Listening {
		t.Fatalf("UDP not passed on: %+v", quic.Stats())
	}
	tt.Params["protocol"] = "http3"
	st.UpdateInbound(tt)
	m.Apply()
	run("http3")
	if s := quic.Stats()[0]; s.Up+s.Down < 1000 {
		t.Errorf("QUIC did not go through the shared UDP port: %+v", s)
	}
}
