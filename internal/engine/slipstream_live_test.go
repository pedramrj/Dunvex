//go:build linux

package engine

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestSlipstreamOnRealXray runs the official Slipstream server under Dunvex,
// behind the shared DNS port next to dnstt, and the official client set up
// from the panel's SlipNet link: through the user's SOCKS sign-in it reaches
// the internet, a wrong password is refused and the usage is the user's.
// Needs DUNVEX_CORE_DIR (xray), DUNVEX_SLIP_SERVER and DUNVEX_SLIP_CLIENT.
func TestSlipstreamOnRealXray(t *testing.T) {
	cores, srv, cli := os.Getenv("DUNVEX_CORE_DIR"), os.Getenv("DUNVEX_SLIP_SERVER"), os.Getenv("DUNVEX_SLIP_CLIENT")
	if cores == "" || srv == "" || cli == "" {
		t.Skip("needs DUNVEX_CORE_DIR, DUNVEX_SLIP_SERVER and DUNVEX_SLIP_CLIENT")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	copyCores(t, cores, bin)
	raw, err := os.ReadFile(srv)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(bin, "slipstream-server"), raw, 0o755)
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sara := &store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(20), SubToken: util.RandomString(20), Enabled: true}
	st.CreateUser(sara)

	port := freeUDP(45600)
	sp, dp := protocol.Get("slipstream"), protocol.Get("dnstt")
	ss := &store.Inbound{Preset: sp.ID, Port: port, Enabled: true, Params: protocol.Defaults(sp, map[string]any{"xdnsDomain": "s.dunvex.test", "resolver": "direct"}, "")}
	st.CreateInbound(ss)
	st.CreateInbound(&store.Inbound{Preset: dp.ID, Port: port, Enabled: true, Params: protocol.Defaults(dp, map[string]any{"xdnsDomain": "d.dunvex.test"}, "")})

	env := protocol.Env{Host: "127.0.0.1", Listen: "127.0.0.1"}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.Xray.APIPort, m.SingBox.APIPort, m.TT.APIPort = nextFree(45700), nextFree(45710), nextFree(45720)
	m.RouterPort, m.WarpTestPort, m.Usque.APIPort = nextFree(45730), nextFree(45740), nextFree(45750)
	defer m.Xray.Stop()
	defer m.Slip.Stop()
	defer m.stopDNS()
	if err := m.Apply(); err != nil {
		t.Fatalf("apply: %v\nslipstream: %s", err, strings.Join(m.Slip.Logs(), "\n"))
	}
	time.Sleep(time.Second)
	if !m.Slip.Running() {
		t.Fatalf("Slipstream is not running: %s", strings.Join(m.Slip.Logs(), "\n"))
	}
	if ie, _ := m.Errors(); ie[ss.ID] != "" {
		t.Fatalf("inbound error: %s", ie[ss.ID])
	}
	t.Logf("Slipstream %s runs: %s", m.Slip.Version(), strings.ReplaceAll(string(m.SlipConfig()), "\n", " "))

	// the client takes the domain, resolver and congestion control from the link
	b64, _ := strings.CutPrefix(protocol.Link(sp, ss, sara, env, "SS"), "slipnet://")
	rawLink, _ := base64.StdEncoding.DecodeString(b64)
	f := strings.Split(string(rawLink), "|")
	if f[1] != "ss" || f[12] != "sara" {
		t.Fatalf("link: %q", f[:14])
	}
	res := strings.Split(f[4], ":")
	local := nextFree(45760)
	c := exec.Command(cli, "--tcp-listen-port="+strconv.Itoa(local), "--resolver="+res[0]+":"+res[1], "--domain="+f[3], "--congestion-control="+f[7])
	logf, _ := os.Create(filepath.Join(dir, "client.log"))
	c.Stdout, c.Stderr = logf, logf
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { c.Process.Kill(); c.Wait() }()
	clientLog := func() string { b, _ := os.ReadFile(filepath.Join(dir, "client.log")); return lastLines(string(b), 15) }
	if !waitPort(local) {
		t.Fatalf("the client does not listen: %s", clientLog())
	}

	bad, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(local))
	if err != nil {
		t.Fatal(err)
	}
	if code, err := socks5(bad, "sara", "wrong", "cp.cloudflare.com", 80); code != 0xfe {
		t.Fatalf("a wrong password got %x %v\nclient: %s\nserver: %s", code, err, clientLog(), lastLines(strings.Join(m.Slip.Logs(), "\n"), 15))
	}
	bad.Close()
	s, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(local))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Now()
	if code, err := socks5(s, "sara", sara.Password, "cp.cloudflare.com", 80); code != 0 || err != nil {
		t.Fatalf("SOCKS through Slipstream: %x %v\nclient: %s\nserver: %s", code, err, clientLog(), lastLines(strings.Join(m.Slip.Logs(), "\n"), 15))
	}
	fmt.Fprintf(s, "GET /generate_204 HTTP/1.1\r\nHost: cp.cloudflare.com\r\nConnection: close\r\n\r\n")
	r, err := http.ReadResponse(bufio.NewReader(s), nil)
	if err != nil || r.StatusCode != 204 {
		t.Fatalf("through Slipstream: %v %v", r, err)
	}
	t.Logf("Slipstream: a request through Xray's SOCKS took %v", time.Since(start).Round(time.Millisecond))

	// a bigger download, for a speed figure
	s2, _ := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(local))
	if code, _ := socks5(s2, "sara", sara.Password, "speed.cloudflare.com", 80); code == 0 {
		start = time.Now()
		fmt.Fprintf(s2, "GET /__down?bytes=2000000 HTTP/1.1\r\nHost: speed.cloudflare.com\r\nConnection: close\r\n\r\n")
		if r2, err := http.ReadResponse(bufio.NewReader(s2), nil); err == nil {
			n, _ := bufio.NewReader(r2.Body).Discard(1 << 30)
			d := time.Since(start)
			t.Logf("Slipstream (direct, loopback): %d bytes in %v, %.0f KB/s", n, d.Round(time.Millisecond), float64(n)/d.Seconds()/1024)
		}
	}
	s2.Close()

	time.Sleep(1500 * time.Millisecond)
	m.collect()
	u, _ := st.UserByName("sara")
	if u.UsedUp+u.UsedDown == 0 {
		t.Fatal("usage not counted for sara")
	}
	t.Logf("counted for sara: %d up, %d down", u.UsedUp, u.UsedDown)
	t.Logf("shared port %d: %+v", port, m.DNSPorts()[port])
}
