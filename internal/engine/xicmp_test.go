package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// XDNS and xICMP clients appear from fd00::/64 plus a client ID, a new one per
// connection: they are no devices, so they never count toward a device limit.
func TestTunnelIDsAreNoDevices(t *testing.T) {
	d := newDevices()
	now := time.Now().Unix()
	for _, ip := range []string{"fd00::1c2b:3a4d:5e6f:7081", "fd00::a1b2:c3d4:e5f6:0708", "fd00:0000:0000:0000:ffff:ffff:ffff:fffc"} {
		d.see("sara", ip, now)
	}
	d.see("sara", "203.0.113.7", now)
	d.see("sara", "fd12:3456:789a::1", now) // a real ULA network is still a device
	d.see("sara", "2001:db8::5", now)
	if n := d.counts(now - 10)["sara"]; n != 3 {
		t.Fatalf("devices: %d (%+v)", n, d.list("sara", now-10))
	}
}

// One xICMP per server; a server that cannot open raw ICMP sockets holds it
// back with the reason instead of starting Xray with it.
func TestXICMPBuild(t *testing.T) {
	m, st, add := dnsTestManager(t)
	st.CreateUser(&store.User{Name: "alice", UUID: util.UUID(), Password: "pw", SubToken: util.RandomString(20), Enabled: true})
	old := xicmpReady
	defer func() { xicmpReady = old }()
	xicmpReady = func() error { return nil }

	first := add("vless-xicmp", 31000, nil)
	second := add("vless-xicmp", 31001, nil)
	b, err := m.build()
	if err != nil {
		t.Fatal(err)
	}
	if b.errs[first.ID] != "" || !strings.Contains(b.errs[second.ID], "only one xICMP") {
		t.Fatalf("errors: %v", b.errs)
	}
	if !strings.Contains(string(b.xray), `"type": "xicmp"`) || strings.Count(string(b.xray), `"xicmp"`) != 1 {
		t.Fatalf("Xray config: %s", b.xray)
	}

	xicmpReady = func() error { return errors.New("xICMP needs raw ICMP sockets, which need root") }
	b, _ = m.build()
	if !strings.Contains(b.errs[first.ID], "need root") || strings.Contains(string(b.xray), "xicmp") {
		t.Fatalf("without raw sockets: %v %s", b.errs, b.xray)
	}
}
