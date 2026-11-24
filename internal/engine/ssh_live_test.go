package engine

import (
	"context"
	"encoding/binary"
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

	"golang.org/x/crypto/ssh"

	"github.com/pedramrj/dunvex/internal/health"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/routing"
	"github.com/pedramrj/dunvex/internal/store"
)

// TestSSHOnRealCores runs Dunvex's SSH server in front of a real Xray and
// sends a real sing-box client through it to the internet, with the outbound
// the users' sing-box profile carries. It checks the sign-in (a wrong
// password and a disabled user are refused), the usage counted per user and
// per inbound, the routing (a blocked site stays blocked), the config check
// with its own user, UDP over TCP through Xray, and that a user who loses
// access is cut off. Needs DUNVEX_TEST_BIN (xray and sing-box) and internet.
func TestSSHOnRealCores(t *testing.T) {
	src := os.Getenv("DUNVEX_TEST_BIN")
	if src == "" {
		t.Skip("DUNVEX_TEST_BIN not set")
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	for _, name := range []string{"xray" + ext, "sing-box" + ext} {
		raw, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(bin, name), raw, 0o755)
	}
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	alice := sshUser(t, st, "alice", nil)
	bob := sshUser(t, st, "bob", func(u *store.User) { u.Enabled = false })
	EnsureHealthKey(st)
	routing.Save(st, routing.Settings{BlockDomains: []string{"example.com"}, Iran: routing.IranDirect, Warp: routing.WarpOff})

	p := protocol.Get("ssh")
	port := nextFree(36600)
	in := &store.Inbound{Preset: p.ID, Port: port, Enabled: true, Params: protocol.Defaults(p, nil, "")}
	if err := st.CreateInbound(in); err != nil {
		t.Fatal(err)
	}
	env := protocol.Env{Host: "127.0.0.1", Listen: "127.0.0.1"}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.Xray.APIPort = nextFree(port + 1)
	m.SingBox.APIPort = nextFree(m.Xray.APIPort + 1)
	defer m.Xray.Stop()
	defer m.stopSSH()
	if err := m.Apply(); err != nil {
		t.Fatalf("apply: %v | %v", err, m.Xray.Logs())
	}
	if ie, _ := m.Errors(); ie[in.ID] != "" {
		t.Fatalf("inbound: %s", ie[in.ID])
	}
	if s, ok := m.sshStatus(); !ok || !s.Running || !s.BuiltIn {
		t.Fatalf("status %+v", s)
	}

	// a sing-box client with the user's own profile outbound
	runner := &health.Runner{BinDir: bin, Dir: dir, Timeout: 40 * time.Second}
	client := func(u *store.User) (socks string, stop func()) {
		t.Helper()
		_, items := protocol.SingboxOutbound(p, in, u, env, "proxy")
		if len(items) != 1 || items[0]["type"] != "ssh" {
			t.Fatalf("sing-box outbound %v", items)
		}
		lp := nextFree(37000)
		cfg, err := health.ClientConfig(health.Target{Kind: health.KindSingbox, Singbox: items}, lp)
		if err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(dir, fmt.Sprintf("client-%s-%d.json", u.Name, lp))
		os.WriteFile(f, cfg, 0o600)
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, filepath.Join(bin, "sing-box"+ext), "run", "-c", f)
		var out strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		if !waitPortT(lp) {
			t.Fatalf("sing-box client did not start: %s", out.String())
		}
		return fmt.Sprintf("127.0.0.1:%d", lp), func() { cancel(); cmd.Wait() }
	}
	wait := 30 * time.Second
	get := func(socks, u string) (int, int64, error) {
		pu, _ := url.Parse("socks5h://" + socks)
		hc := &http.Client{Timeout: wait, Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableKeepAlives: true}}
		res, err := hc.Get(u)
		if err != nil {
			return 0, 0, err
		}
		defer res.Body.Close()
		n, err := io.Copy(io.Discard, res.Body)
		return res.StatusCode, n, err
	}

	socks, stop := client(alice)
	defer stop()
	code, _, err := get(socks, "http://cp.cloudflare.com/generate_204")
	if err != nil || code != 204 {
		t.Fatalf("through SSH: %d %v | ssh log: %v | xray: %v", code, err, SSHLogger.Logs(), m.Xray.Logs())
	}
	const size = 5 << 20
	begin := time.Now()
	code, n, err := get(socks, fmt.Sprintf("http://speed.cloudflare.com/__down?bytes=%d", size))
	if err != nil || code != 200 || n != size {
		t.Fatalf("download: %d %d %v", code, n, err)
	}
	t.Logf("5 MB through the SSH server in %v", time.Since(begin).Round(time.Millisecond))

	// the routing applies: a blocked site does not load
	if code, _, err := get(socks, "http://example.com/"); err == nil && code == 200 {
		t.Error("a blocked site loaded through SSH: the routing does not apply")
	}

	// usage per user (from the SSH server) and per inbound (from Xray)
	m.collect()
	got, _ := st.UserByID(alice.ID)
	if got.UsedDown < size || got.UsedUp <= 0 {
		t.Errorf("alice's usage up %d down %d, want at least %d down", got.UsedUp, got.UsedDown, size)
	}
	if on := m.OnlineUsers(60); !on[alice.ID] {
		t.Error("alice is not online")
	}
	inUse := inboundUsage(t, st, in.ID)
	if inUse < size {
		t.Errorf("the inbound counted %d bytes, want at least %d", inUse, size)
	}
	t.Logf("alice: up %d, down %d; inbound %d", got.UsedUp, got.UsedDown, inUse)

	// wrong password and a disabled user are refused
	wait = 10 * time.Second
	wrong := *alice
	wrong.Password = "not-her-password"
	for _, u := range []*store.User{&wrong, bob} {
		s, stop := client(u)
		if code, _, err := get(s, "http://cp.cloudflare.com/generate_204"); err == nil && code == 204 {
			t.Errorf("%s (%q) got through", u.Name, u.Password)
		}
		stop()
	}

	// the config check gets through with its own user
	hu := HealthUser(st)
	_, items := protocol.SingboxOutbound(p, in, hu, env, "proxy")
	res := runner.Run(context.Background(), []health.Target{{Config: health.Config{InboundID: in.ID, Preset: p.ID, Name: "SSH"}, Kind: health.KindSingbox, Singbox: items}})
	if res[0].Status != health.StatusOK {
		t.Errorf("config check: %+v", res[0])
	}

	// UDP over TCP (the Hiddify app's) goes out through Xray: a DNS question to
	// 1.1.1.1, when this machine can send UDP to the internet at all
	pk, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(protocol.SSHHostKey(in)))
	sc, err := ssh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), &ssh.ClientConfig{User: alice.Name, Auth: []ssh.AuthMethod{ssh.Password(alice.Password)}, HostKeyCallback: ssh.FixedHostKey(pk), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()
	q := dnsQuery("cloudflare.com")
	if !udpEgress(q) {
		t.Log("this machine sends no UDP to the internet: UDP over TCP is tested by sshproxy's TestThroughRealXray only")
	} else {
		testUoTDNS(t, sc, q, m)
	}

	// alice loses access: her open session is cut, and she cannot sign in again
	alice.Enabled = false
	st.UpdateUser(alice)
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sc.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("a disabled user's open SSH session was not cut")
	}
	if code, _, err := get(socks, "http://cp.cloudflare.com/generate_204"); err == nil && code == 204 {
		t.Error("a disabled user still gets through")
	}
	if !m.Xray.Running() {
		t.Error("Xray stopped")
	}
}

// udpEgress reports whether a DNS question sent straight to 1.1.1.1 over UDP is answered.
func udpEgress(q []byte) bool {
	c, err := net.Dial("udp", "1.1.1.1:53")
	if err != nil {
		return false
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write(q)
	b := make([]byte, 512)
	n, err := c.Read(b)
	return err == nil && n > 12
}

func testUoTDNS(t *testing.T, sc *ssh.Client, q []byte, m *Manager) {
	t.Helper()
	uc, err := sc.Dial("tcp", "sp.v2.udp-over-tcp.arpa:0")
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()
	uc.SetDeadline(time.Now().Add(15 * time.Second))
	frame := []byte{0, 1, 1, 1, 1, 1, 0, 53} // not connected; request header names 1.1.1.1:53
	frame = append(frame, 0, 1, 1, 1, 1, 0, 53)
	frame = binary.BigEndian.AppendUint16(frame, uint16(len(q)))
	uc.Write(append(frame, q...))
	head := make([]byte, 1+4+2+2)
	if _, err := io.ReadFull(uc, head); err != nil {
		t.Fatalf("no DNS answer over UDP over TCP: %v | xray: %v", err, m.Xray.Logs())
	}
	ans := make([]byte, binary.BigEndian.Uint16(head[7:]))
	io.ReadFull(uc, ans)
	if len(ans) < 12 || ans[0] != q[0] || ans[1] != q[1] || ans[3]&0x0f != 0 || binary.BigEndian.Uint16(ans[6:]) == 0 {
		t.Fatalf("DNS answer %x", ans)
	}
	t.Log("a DNS question went out as UDP through UDP over TCP and Xray")
}

func inboundUsage(t *testing.T, st *store.Store, id int64) int64 {
	t.Helper()
	ins, _ := st.Inbounds()
	for _, in := range ins {
		if in.ID == id {
			return in.Up + in.Down
		}
	}
	return 0
}

// dnsQuery is a DNS question for the A record of name.
func dnsQuery(name string) []byte {
	b := []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, l := range strings.Split(name, ".") {
		b = append(append(b, byte(len(l))), l...)
	}
	return append(b, 0, 0, 1, 0, 1)
}

func waitPortT(port int) bool {
	for i := 0; i < 100; i++ {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond); err == nil {
			c.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
