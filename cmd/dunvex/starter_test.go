package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

func starterStore(t *testing.T, panelPort string) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.SetSetting("panel_port", panelPort)
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	for _, n := range []string{"mtg", "trusttunnel", "mita", "amneziawg-go"} {
		if runtime.GOOS == "windows" && n != "amneziawg-go" {
			n += ".exe"
		}
		os.WriteFile(filepath.Join(bin, n), []byte("stand-in"), 0o755)
	}
	return st, bin
}

func seqPorts(start int) func() int {
	n := start
	return func() int { n += 7; return n }
}

// On a fresh server with the panel on 443: every protocol that needs no
// domain, the TCP ones on 443 by server name, and nothing the engine refuses.
func TestStarterSet(t *testing.T) {
	st, bin := starterStore(t, "443")
	items := starterInbounds(st, starterEnv{
		binDir:   bin,
		free:     func(int, bool) bool { return true },
		goodSNI:  func(h string) bool { return h == "www.apple.com" }, // microsoft "fails" the test
		icmpOK:   func() error { return nil },
		randPort: seqPorts(30000),
	})
	ins, _ := st.Inbounds()
	if len(ins) != 8 || len(items) != 8 {
		t.Fatalf("starter set: %d inbounds, %v", len(ins), items)
	}
	by := map[string]*store.Inbound{}
	for _, in := range ins {
		by[in.Preset] = in
	}
	for _, p := range []string{"vless-reality-universal", "vless-reality-xhttp", "mtproto", "trusttunnel"} {
		if by[p] == nil || by[p].Port != 443 {
			t.Fatalf("%s should share 443: %+v", p, by[p])
		}
	}
	if by["hysteria2"].Port != 443 || !by["hysteria2"].Bool("obfs") {
		t.Errorf("Hysteria2: %+v", by["hysteria2"].Params)
	}
	if s := by["vless-reality-xhttp"].Str("sni"); s != "www.apple.com" {
		t.Errorf("XHTTP took %q; only a site that passed the test may be used", s)
	}
	if tt := by["trusttunnel"]; tt.Str("sni") == "www.apple.com" || tt.Str("sni") == protocol.DefaultReality || tt.Str("protocol") != "http2" {
		t.Errorf("TrustTunnel: %s %s", tt.Str("sni"), tt.Str("protocol"))
	}
	if by["amneziawg"].Port != protocol.Get("amneziawg").DefaultPort || by["mieru"].Port < 20000 || by["vless-xicmp"] == nil {
		t.Errorf("own ports: AmneziaWG %d, Mieru %d", by["amneziawg"].Port, by["mieru"].Port)
	}
	line := starterLine(items)
	if !strings.HasPrefix(line, "5 protocols (port 443) + AmneziaWG (port 51821) + Mieru (port ") || !strings.HasSuffix(line, " + xICMP") {
		t.Errorf("summary: %s", line)
	}
	if d := starterDetails(items); !strings.Contains(d, "Hysteria2: UDP 443") || !strings.Contains(d, "xICMP: pings") {
		t.Errorf("details: %s", d)
	}

	// the engine takes all of them on the shared port (with a user to serve)
	st.CreateUser(&store.User{Name: "alice", UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20), Enabled: true})
	cert, key, err := util.SelfSignedCert(filepath.Join(t.TempDir(), "certs"), "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	env := protocol.Env{Host: "203.0.113.10", CertFile: cert, KeyFile: key, SelfSigned: true}
	m := engine.NewManager(st, bin, filepath.Join(t.TempDir(), "run"), func() protocol.Env { return env })
	m.SharedPort = func() int { return 443 }
	_, _, errs, _, err := m.Configs()
	if err != nil {
		t.Fatal(err)
	}
	for id, e := range errs {
		if id == by["vless-xicmp"].ID && strings.Contains(e, "raw ICMP") {
			continue // a test runner without root cannot open raw sockets
		}
		t.Errorf("inbound %d: %s", id, e)
	}
}

// 443 held by another program: the panel and Reality move, and the protocols
// that would share 443 get ports of their own instead.
func TestStarterSetWithout443(t *testing.T) {
	st, bin := starterStore(t, "2096")
	items := starterInbounds(st, starterEnv{
		binDir:   bin,
		free:     func(p int, udp bool) bool { return p != 443 },
		goodSNI:  func(string) bool { return true },
		icmpOK:   func() error { return os.ErrPermission }, // no raw sockets: no xICMP
		randPort: seqPorts(40000),
	})
	ins, _ := st.Inbounds()
	if len(ins) != 7 || len(items) != 7 {
		t.Fatalf("starter set: %v", items)
	}
	seen := map[int]string{}
	for _, in := range ins {
		if in.Port == 443 || in.Port == 2096 {
			t.Errorf("%s on %d", in.Preset, in.Port)
		}
		key := in.Port
		if protocol.Get(in.Preset).Network == "udp" {
			key = -in.Port
		}
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s on %d", in.Preset, other, in.Port)
		}
		seen[key] = in.Preset
	}
}
