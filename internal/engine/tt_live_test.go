//go:build linux

package engine

import (
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// ttDecode reads a tt:// link into tag → values.
func ttDecode(t *testing.T, link string) map[byte][][]byte {
	t.Helper()
	raw, _ := strings.CutPrefix(strings.TrimSpace(link), "tt://?")
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("link %q: %v", link, err)
	}
	varint := func() uint64 {
		n := 1 << (b[0] >> 6)
		v := uint64(b[0] & 0x3f)
		for i := 1; i < n; i++ {
			v = v<<8 | uint64(b[i])
		}
		b = b[n:]
		return v
	}
	out := map[byte][][]byte{}
	for len(b) > 0 {
		tag := byte(varint())
		n := varint()
		out[tag] = append(out[tag], b[:n])
		b = b[n:]
	}
	return out
}

// TestTrustTunnelOnRealEndpoint runs TrustTunnel through the engine with the
// real endpoint, checks Dunvex's link against the endpoint's own export, and
// connects the official CLI client configured from that link. Needs root,
// DUNVEX_TT_BIN (trusttunnel_endpoint) and DUNVEX_TT_CLIENT (trusttunnel_client);
// DUNVEX_TT_PROTOCOL=http3 tests QUIC.
func TestTrustTunnelOnRealEndpoint(t *testing.T) {
	src, clientBin := os.Getenv("DUNVEX_TT_BIN"), os.Getenv("DUNVEX_TT_CLIENT")
	if src == "" || clientBin == "" || os.Geteuid() != 0 {
		t.Skip("needs root, DUNVEX_TT_BIN and DUNVEX_TT_CLIENT")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("needs curl")
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "trusttunnel"), raw, 0o755)

	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mk := func(name string) *store.User {
		u := &store.User{Name: name, UUID: util.UUID(), Password: util.RandomString(20), SubToken: util.RandomString(20), Enabled: true, ResetPeriod: "none"}
		if err := st.CreateUser(u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	sara, reza := mk("sara"), mk("reza")
	p := protocol.Get("trusttunnel")
	port := freeUDP(34890)
	params := protocol.Defaults(p, map[string]any{"protocol": or(os.Getenv("DUNVEX_TT_PROTOCOL"), "http2")}, "")
	if err := st.CreateInbound(&store.Inbound{Preset: p.ID, Port: port, Enabled: true, Params: params}); err != nil {
		t.Fatal(err)
	}
	ins, _ := st.Inbounds()
	in := ins[0]
	// the client dials the server's public address, so the endpoint sees a real
	// address for the device list (loopback is never counted as a device)
	host := or(util.OutboundIP(), "127.0.0.1")
	env := protocol.Env{Host: host}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.Xray.APIPort, m.SingBox.APIPort, m.MTG.APIPort, m.TT.APIPort = nextFree(41890), nextFree(41900), nextFree(41910), nextFree(41940)
	defer m.TT.Stop()
	if err := m.Apply(); err != nil {
		t.Fatalf("apply: %v\n%s", err, strings.Join(m.TT.Logs(), "\n"))
	}
	t.Logf("trusttunnel %s serves %s on port %d (%s)", m.TT.Version(), in.Str("sni"), port, in.Str("protocol"))
	started := m.TT.StartedAt()

	// Dunvex's link against the one the endpoint exports itself
	link := protocol.Link(p, in, sara, env, "Dunvex TT")
	runDir := filepath.Dir(m.TT.ConfigPath)
	out, err := exec.Command(filepath.Join(bin, "trusttunnel"), m.TT.ConfigPath, filepath.Join(runDir, protocol.TTHostsFile),
		"-c", "sara", "-a", net.JoinHostPort(host, fmt.Sprint(port)), "--name", "Dunvex TT", "--format", "deeplink").CombinedOutput()
	ref := ""
	for _, l := range strings.Fields(string(out)) {
		if strings.HasPrefix(l, "tt://?") {
			ref = l
		}
	}
	if ref == "" {
		t.Fatalf("the endpoint exported no link: %v %s", err, out)
	}
	mine, theirs := ttDecode(t, link), ttDecode(t, ref)
	for _, tag := range []byte{0x00, 0x01, 0x02, 0x05, 0x06, 0x08, 0x0C} {
		if fmt.Sprintf("%q", mine[tag]) != fmt.Sprintf("%q", theirs[tag]) {
			t.Errorf("link field %#x: Dunvex %q, endpoint %q", tag, mine[tag], theirs[tag])
		}
	}
	for tag := range theirs {
		if _, ok := mine[tag]; !ok {
			t.Logf("the endpoint's link also has field %#x = %q", tag, theirs[tag])
		}
	}

	// the official client, set up from Dunvex's link, as a SOCKS proxy
	socks := nextFree(41950)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: mine[0x08][0]})
	proto := "http2"
	if string(mine[0x09][0]) == "\x02" {
		proto = "http3"
	}
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
`, mine[0x01][0], mine[0x02][0], mine[0x05][0], mine[0x06][0], certPEM, proto, socks)
	cfgFile := filepath.Join(dir, "client.toml")
	os.WriteFile(cfgFile, []byte(cfg), 0o600)
	// the CLI client quits when it is refused (a user switched off), so it is
	// started again after each change, as a user would reconnect
	var client *exec.Cmd
	startClient := func() {
		t.Helper()
		if client != nil {
			client.Process.Kill()
			client.Wait()
		}
		client = exec.Command(clientBin, "--config", cfgFile)
		logf, _ := os.Create(filepath.Join(dir, "client.log"))
		client.Stdout, client.Stderr = logf, logf
		if err := client.Start(); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 80; i++ {
			if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", socks)); err == nil {
				c.Close()
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	startClient()
	defer func() { client.Process.Kill(); client.Wait() }()
	clientLog := func() string {
		b, _ := os.ReadFile(filepath.Join(dir, "client.log"))
		var keep []string
		for _, l := range strings.Split(string(b), "\n") {
			if (strings.Contains(l, "ERROR") || strings.Contains(l, "WARN") || strings.Contains(strings.ToLower(l), "fail")) && !strings.Contains(l, "Skipping invalid address") {
				keep = append(keep, l)
			}
		}
		if len(keep) > 12 {
			keep = keep[:12]
		}
		return strings.Join(keep, "\n") + "\n…\n" + lastLines(string(b), 4)
	}
	trace := func() (string, error) {
		out, err := exec.Command("curl", "-s", "-m", "10", "--socks5-hostname", fmt.Sprintf("127.0.0.1:%d", socks), "https://1.1.1.1/cdn-cgi/trace").CombinedOutput()
		for _, l := range strings.Split(string(out), "\n") {
			if v, ok := strings.CutPrefix(l, "ip="); ok {
				return v, nil
			}
		}
		if err == nil {
			err = fmt.Errorf("no ip in %q", out)
		}
		return "", err
	}
	works := func(what string) {
		t.Helper()
		var exit string
		var err error
		for i := 0; i < 8 && exit == ""; i++ {
			if exit, err = trace(); exit == "" {
				time.Sleep(3 * time.Second)
			}
		}
		if exit == "" {
			t.Fatalf("%s: no internet: %v\nclient: %s\nserver: %s", what, err, clientLog(), lastLines(strings.Join(m.TT.Logs(), "\n"), 8))
		}
		t.Logf("%s: traffic leaves as %s", what, exit)
	}
	works("first connection")
	if b, _, err := m.ttGet("/clients"); err == nil {
		t.Logf("/clients: %s", b)
	}

	// usage, online state and the device come from /clients
	m.collect()
	u, _ := st.UserByName("sara")
	if u.UsedUp == 0 || u.UsedDown == 0 {
		t.Errorf("usage not counted: up %d down %d", u.UsedUp, u.UsedDown)
	}
	// one small request, one page with a certificate chain back: more down than up
	if u.UsedDown <= u.UsedUp {
		t.Errorf("upload and download swapped: up %d down %d", u.UsedUp, u.UsedDown)
	}
	if !m.OnlineUsers(60)[u.ID] {
		t.Error("sara is not online")
	}
	if d := m.Devices("sara"); len(d) != 1 || d[0].IP != host {
		t.Errorf("devices: %+v", d)
	}
	t.Logf("counted up %d B, down %d B", u.UsedUp, u.UsedDown)

	set := func(u *store.User, on bool) {
		t.Helper()
		u, _ = st.UserByName(u.Name)
		u.Enabled = on
		st.UpdateUser(u)
		if err := m.Apply(); err != nil {
			t.Fatalf("apply: %v\n%s", err, strings.Join(m.TT.Logs(), "\n"))
		}
	}
	// sara switched off: the endpoint starts again without her, she is cut off
	set(sara, false)
	if m.TT.StartedAt().Equal(started) {
		t.Error("the endpoint was not started again for a change of users")
	}
	if exit, _ := trace(); exit != "" {
		t.Errorf("sara still has internet after being switched off (%s)", exit)
	}
	set(sara, true)
	startClient()
	works("sara switched on again")

	// nobody left: the endpoint must not run (it would let anyone in)
	set(reza, false)
	set(sara, false)
	if m.TT.Running() {
		t.Error("the endpoint runs with no users, open to anyone")
	}
	set(sara, true)
	startClient()
	works("sara back after everyone was off")

	// a restart must not count anything twice
	m.collect()
	before, _ := st.UserByName("sara")
	if err := m.TT.Restart(); err != nil {
		t.Fatal(err)
	}
	if err := m.ttServing(); err != nil {
		t.Fatal(err)
	}
	m.collect()
	after, _ := st.UserByName("sara")
	if after.UsedUp+after.UsedDown != before.UsedUp+before.UsedDown {
		t.Errorf("usage changed across a restart with no traffic: %d → %d", before.UsedUp+before.UsedDown, after.UsedUp+after.UsedDown)
	}
}
