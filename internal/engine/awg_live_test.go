//go:build linux

package engine

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/awg"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// confToUAPI turns a user's .conf into the control lines of an amneziawg-go
// client, the way the AmneziaWG apps read it. It also returns the address.
func confToUAPI(t *testing.T, conf string) (lines []string, addr, mtu string) {
	t.Helper()
	hexOf := func(b64 string) string {
		b, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatalf("bad base64 %q in conf", b64)
		}
		return hex.EncodeToString(b)
	}
	var peer []string
	section := ""
	for _, l := range strings.Split(conf, "\n") {
		if strings.HasPrefix(l, "[") {
			section = l
			continue
		}
		k, v, ok := strings.Cut(l, " = ")
		if !ok {
			continue
		}
		switch {
		case section == "[Interface]" && k == "PrivateKey":
			lines = append(lines, "private_key="+hexOf(v))
		case section == "[Interface]" && k == "Address":
			addr = v
		case section == "[Interface]" && k == "MTU":
			mtu = v
		case section == "[Interface]" && k == "HeaderProtectionKey":
			lines = append(lines, "header_protection_key="+hexOf(v))
		case section == "[Interface]" && k == "RandomTrailers":
			lines = append(lines, "random_trailers="+v)
		case section == "[Interface]" && k != "DNS":
			lines = append(lines, strings.ToLower(k)+"="+v) // Jc, S1…S4, H1…H4, I1
		case section == "[Peer]" && k == "PublicKey":
			peer = append(peer, "public_key="+hexOf(v))
		case section == "[Peer]" && k == "Endpoint":
			peer = append(peer, "endpoint="+v)
		case section == "[Peer]" && k == "PersistentKeepalive":
			peer = append(peer, "persistent_keepalive_interval="+v)
		case section == "[Peer]" && k == "AllowedIPs":
			for _, ip := range strings.Split(v, ",") {
				peer = append(peer, "allowed_ip="+strings.TrimSpace(ip))
			}
		}
	}
	return append(append(lines, "replace_peers=true"), peer...), addr, mtu
}

func uapi(sock string, lines []string) error {
	c, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "set=1\n"+strings.Join(lines, "\n")+"\n\n")
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "errno="); ok {
			if v != "0" {
				return fmt.Errorf("errno %s", v)
			}
			return nil
		}
	}
	return fmt.Errorf("no answer")
}

// TestAmneziaWGOnRealTunnel runs AmneziaWG through the engine and connects a
// real amneziawg-go client to it from its own network namespace, configured
// from the user's .conf. Needs root and DUNVEX_AWG_BIN (an amneziawg-go).
func TestAmneziaWGOnRealTunnel(t *testing.T) {
	src := os.Getenv("DUNVEX_AWG_BIN")
	if src == "" || os.Geteuid() != 0 {
		t.Skip("needs root and DUNVEX_AWG_BIN")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("needs curl")
	}
	sh := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v %s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "amneziawg-go"), raw, 0o755)
	fwd, _ := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	defer os.WriteFile("/proc/sys/net/ipv4/ip_forward", fwd, 0o644)

	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	priv, pub := util.WireGuardKeys()
	sara := &store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20),
		Enabled: true, ResetPeriod: "none", WGPrivate: priv, WGPublic: pub, WGIndex: 2}
	if err := st.CreateUser(sara); err != nil {
		t.Fatal(err)
	}
	p := protocol.Get("amneziawg")
	port := freeUDP(51890)
	in := &store.Inbound{Preset: p.ID, Port: port, Enabled: true, Params: protocol.Defaults(p, nil, "")}
	if err := st.CreateInbound(in); err != nil {
		t.Fatal(err)
	}
	// DUNVEX_AWG_PLAIN=1 turns the obfuscation off (plain WireGuard): a control
	// run that shows the wire check below does see WireGuard when it is there
	plain := os.Getenv("DUNVEX_AWG_PLAIN") == "1"
	if v := os.Getenv("DUNVEX_AWG_VERSION"); v != "" { // e.g. 2.0
		in.Params["version"] = v
		in.Params = protocol.Defaults(p, in.Params, "")
		st.UpdateInbound(in)
	}
	if plain {
		for k, v := range map[string]any{"version": "2.0", "jc": 0, "jmin": 0, "jmax": 0, "s1": 0, "s2": 0, "s3": 0, "s4": 0,
			"h1": "1", "h2": "2", "h3": "3", "h4": "4"} {
			in.Params[k] = v
		}
		delete(in.Params, "hpk")
		st.UpdateInbound(in)
	}
	ins, _ := st.Inbounds() // as the panel has it: after a trip through the database
	in = ins[0]
	env := protocol.Env{Host: "10.250.0.1"}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.AWG = awg.New(bin, "dvxawgt0")
	m.Xray.APIPort, m.SingBox.APIPort, m.MTG.APIPort = nextFree(41890), nextFree(41900), nextFree(41910)
	defer func() {
		m.AWG.Stop()
		if out, _ := exec.Command("sh", "-c", "iptables -S; iptables -t nat -S").CombinedOutput(); strings.Contains(string(out), "dunvex-awg") {
			t.Errorf("NAT rules left after stop:\n%s", out)
		}
		if exec.Command("ip", "link", "show", "dvxawgt0").Run() == nil {
			t.Error("interface left after stop")
		}
	}()
	if err := m.Apply(); err != nil {
		t.Fatalf("apply: %v | %v", err, m.AWG.Logs())
	}
	if !m.AWG.Running() {
		t.Fatalf("not running: %s", m.AWG.LastError())
	}
	starts := m.AWG.Starts()

	// the client: its own network namespace, joined to the host by a veth pair
	const ns = "dvxawgtest"
	exec.Command("ip", "netns", "del", ns).Run()
	exec.Command("ip", "link", "del", "dvxv0").Run()
	sh("ip", "netns", "add", ns)
	defer exec.Command("ip", "netns", "del", ns).Run()
	sh("ip", "link", "add", "dvxv0", "type", "veth", "peer", "name", "dvxv1")
	defer exec.Command("ip", "link", "del", "dvxv0").Run()
	sh("ip", "link", "set", "dvxv1", "netns", ns)
	sh("ip", "addr", "add", "10.250.0.1/30", "dev", "dvxv0")
	sh("ip", "link", "set", "dvxv0", "up")
	sh("ip", "netns", "exec", ns, "ip", "addr", "add", "10.250.0.2/30", "dev", "dvxv1")
	sh("ip", "netns", "exec", ns, "ip", "link", "set", "dvxv1", "up")
	sh("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")

	sock := filepath.Join(awg.SocketDir, "awgc0.sock")
	os.Remove(sock)
	client := exec.Command("ip", "netns", "exec", ns, filepath.Join(bin, "amneziawg-go"), "-f", "awgc0")
	client.Env = append(os.Environ(), "LOG_LEVEL=error")
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { client.Process.Kill(); client.Wait() }()
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// watch the wire: no packet may look like WireGuard (types 1-4 in the first 4 bytes)
	var dump *exec.Cmd
	dumpOut := filepath.Join(dir, "dump.txt")
	if _, err := exec.LookPath("tcpdump"); err == nil {
		f, _ := os.Create(dumpOut)
		dump = exec.Command("tcpdump", "-i", "dvxv0", "-nn", "-l", "-x", "-c", "40", "udp", "port", fmt.Sprint(port))
		dump.Stdout = f
		dump.Start()
		time.Sleep(time.Second)
	}

	conf := protocol.Link(p, in, sara, env, "AmneziaWG")
	lines, addr, mtu := confToUAPI(t, conf)
	if err := uapi(sock, lines); err != nil {
		t.Fatalf("client refused the .conf: %v\n%s", err, conf)
	}
	sh("ip", "netns", "exec", ns, "ip", "addr", "add", addr, "dev", "awgc0")
	sh("ip", "netns", "exec", ns, "ip", "link", "set", "awgc0", "mtu", mtu, "up")
	sh("ip", "netns", "exec", ns, "ip", "route", "add", "default", "dev", "awgc0")

	trace := func() (string, error) {
		out, err := exec.Command("ip", "netns", "exec", ns, "curl", "-s", "-m", "8", "https://1.1.1.1/cdn-cgi/trace").CombinedOutput()
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
	var exit string
	for i := 0; i < 3 && exit == ""; i++ {
		exit, err = trace()
	}
	if exit == "" {
		t.Fatalf("no internet through the tunnel: %v | server %v", err, m.AWG.Logs())
	}
	t.Logf("traffic leaves the tunnel as %s", exit)

	// usage, online state and the device come from the interface's counters
	m.collect()
	u, _ := st.UserByName("sara")
	if u.UsedUp == 0 || u.UsedDown == 0 {
		t.Errorf("usage not counted: up %d down %d", u.UsedUp, u.UsedDown)
	}
	if !m.OnlineUsers(60)[u.ID] {
		t.Error("sara is not online")
	}
	if d := m.Devices("sara"); len(d) != 1 || d[0].IP != "10.250.0.2" {
		t.Errorf("devices: %+v", d)
	}
	t.Logf("counted up %d B, down %d B", u.UsedUp, u.UsedDown)

	if dump != nil {
		dump.Process.Signal(os.Interrupt)
		dump.Wait()
		b, _ := os.ReadFile(dumpOut)
		seen, wg := 0, 0
		// tcpdump -x prints each IP packet as hex lines "\t0x0010:  4500 00b0 …";
		// the UDP payload starts at byte 28 (20 of IP, 8 of UDP)
		var pkts []string
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if !strings.HasPrefix(l, "\t0x") || len(f) < 2 {
				continue // the summary line of a packet
			}
			if f[0] == "0x0000:" {
				pkts = append(pkts, "")
			}
			if len(pkts) == 0 {
				continue
			}
			for _, w := range f[1:] {
				if _, err := hex.DecodeString(w); err == nil {
					pkts[len(pkts)-1] += w
				}
			}
		}
		for _, h := range pkts {
			if len(h) < 64 {
				continue
			}
			seen++
			switch h[56:64] {
			case "01000000", "02000000", "03000000", "04000000":
				wg++
			}
		}
		t.Logf("wire: %d packets checked, %d looked like plain WireGuard", seen, wg)
		if plain {
			if wg == 0 {
				t.Errorf("control run: plain WireGuard was not recognised on the wire (%d packets)", seen)
			}
		} else if seen == 0 || wg > 0 {
			t.Errorf("obfuscation check: %d packets, %d plain WireGuard", seen, wg)
		}
	}

	// switching sara off removes only her peer, live: no restart
	u.Enabled = false
	st.UpdateUser(u)
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	if _, err := trace(); err == nil {
		t.Error("sara still has internet after being switched off")
	}
	u.Enabled = true
	st.UpdateUser(u)
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	if exit, err = trace(); exit == "" {
		exit, err = trace()
	}
	if exit == "" {
		t.Errorf("sara has no internet after being switched on again: %v", err)
	}
	if m.AWG.Starts() != starts {
		t.Errorf("the interface was restarted for a user change (%d → %d starts)", starts, m.AWG.Starts())
	}
	if !strings.Contains(string(m.AWGConfig()), "# sara") || strings.Contains(string(m.AWGConfig()), in.Str("privateKey")) {
		t.Errorf("config text:\n%s", m.AWGConfig())
	}
}
