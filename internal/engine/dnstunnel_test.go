package engine

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/dnstt/dnsttclient"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

func dnsTestManager(t *testing.T) (*Manager, *store.Store, func(preset string, port int, params map[string]any) *store.Inbound) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	add := func(preset string, port int, params map[string]any) *store.Inbound {
		p := protocol.Get(preset)
		in := &store.Inbound{Preset: p.ID, Port: port, Enabled: true, Params: protocol.Defaults(p, params, "")}
		if err := st.CreateInbound(in); err != nil {
			t.Fatal(err)
		}
		return in
	}
	env := protocol.Env{Host: "203.0.113.10", Listen: "127.0.0.1"}
	m := NewManager(st, filepath.Join(dir, "bin"), filepath.Join(dir, "run"), func() protocol.Env { return env })
	return m, st, add
}

// XDNS, dnstt and Slipstream on one port: a shared DNS port sends each its
// domain; the tunnels hand their streams to SOCKS inbounds of Xray.
func TestDNSTunnelsBuild(t *testing.T) {
	m, st, add := dnsTestManager(t)
	st.CreateUser(&store.User{Name: "alice", UUID: util.UUID(), Password: "pw-alice", SubToken: util.RandomString(20), Enabled: true})
	xd := add("vless-xdns", 53, map[string]any{"xdnsDomain": "x.example.com"})
	dt := add("dnstt", 53, map[string]any{"xdnsDomain": "d.example.com"})
	ss := add("slipstream", 53, map[string]any{"xdnsDomain": "s.example.com"})
	alone := add("vless-xdns", 5353, map[string]any{"xdnsDomain": "y.example.com"})
	b, err := m.build()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []*store.Inbound{xd, dt, ss, alone} {
		if e := b.errs[in.ID]; e != "" {
			t.Fatalf("%s: %s", in.Preset, e)
		}
	}
	port := b.dns[53]
	if port == nil || len(port.zones) != 3 || len(b.dns) != 1 || port.listen != "127.0.0.1:53" {
		t.Fatalf("shared port: %+v (all: %v)", port, b.dns)
	}
	var xcfg struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	json.Unmarshal(b.xray, &xcfg)
	byTag := map[string]map[string]any{}
	for _, ib := range xcfg.Inbounds {
		byTag[ib["tag"].(string)] = ib
	}
	x := byTag[xd.Tag()]
	if x["listen"] != "127.0.0.1" || port.zones["x.example.com"] != "127.0.0.1:"+jsonNum(x["port"]) {
		t.Errorf("XDNS behind the shared port: %v:%v, zone %s", x["listen"], x["port"], port.zones["x.example.com"])
	}
	if a := byTag[alone.Tag()]; a["port"].(float64) != 5353 {
		t.Errorf("XDNS alone keeps its port: %v", a["port"])
	}
	socks := func(in *store.Inbound) string {
		s := byTag[in.Tag()]
		set := s["settings"].(map[string]any)
		if s["protocol"] != "socks" || s["listen"] != "127.0.0.1" || set["auth"] != "password" || len(set["accounts"].([]any)) != 1 {
			t.Errorf("SOCKS inbound of %s: %v", in.Preset, s)
		}
		return "127.0.0.1:" + jsonNum(s["port"])
	}
	plan := b.dnstt[dt.ID]
	if plan.domain != "d.example.com" || plan.upstream != socks(dt) || plan.listen != port.zones["d.example.com"] || plan.key != dt.Str("privateKey") {
		t.Errorf("dnstt plan: %+v", plan)
	}
	args := string(b.slip)
	_, back, _ := net.SplitHostPort(port.zones["s.example.com"])
	for _, w := range []string{"--domain=s.example.com", "--target-address=" + socks(ss), "--dns-listen-port=" + back} {
		if !strings.Contains(args, w+"\n") {
			t.Errorf("Slipstream args lack %s:\n%s", w, args)
		}
	}
	if a := slipArgs(writeTemp(t, args)); len(a) != 5 || b.slipIn != ss.ID {
		t.Errorf("Slipstream args read back: %q", a)
	}
	for _, f := range []string{"self.crt", "self.key"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(m.Slip.ConfigPath), "slipstream-s.example.com", f)); err != nil {
			t.Errorf("Slipstream certificate: %v", err)
		}
	}

	// one Slipstream per server; a domain used twice on a port; nobody allowed
	ss2 := add("slipstream", 54, map[string]any{"xdnsDomain": "t.example.com"})
	dup := add("dnstt", 53, map[string]any{"xdnsDomain": "D.example.com."})
	nobody := &store.Inbound{Preset: "dnstt", Port: 55, Enabled: true, Params: protocol.Defaults(protocol.Get("dnstt"), map[string]any{"xdnsDomain": "n.example.com"}, "")}
	st.CreateInbound(nobody)
	u, _ := st.UserByName("alice")
	u.InboundIDs = []int64{xd.ID, dt.ID, ss.ID, alone.ID, ss2.ID, dup.ID}
	st.UpdateUser(u)
	b, _ = m.build()
	if !strings.Contains(b.errs[ss2.ID], "only one Slipstream") || !strings.Contains(b.errs[dup.ID], "already uses the tunnel domain d.example.com") {
		t.Errorf("errors: %v", b.errs)
	}
	if _, on := b.dnstt[nobody.ID]; on || b.errs[nobody.ID] != "" {
		t.Errorf("a tunnel nobody may use stays off quietly: %v", b.errs[nobody.ID])
	}
	// open: it runs without users and signs nobody in
	nobody.Params["signIn"] = "open"
	st.UpdateInbound(nobody)
	b, _ = m.build()
	if _, on := b.dnstt[nobody.ID]; !on || !strings.Contains(string(b.xray), `"auth": "noauth"`) {
		t.Errorf("open tunnel: %v", b.dnstt)
	}
}

func writeTemp(t *testing.T, s string) string {
	p := filepath.Join(t.TempDir(), "args")
	os.WriteFile(p, []byte(s), 0o600)
	return p
}

// Apply opens the shared DNS port and the in-process dnstt server: a real
// dnstt client completes its handshake through it, an unknown name is refused,
// and removing the tunnel frees the port.
func TestDnsttApplyServes(t *testing.T) {
	m, _, add := dnsTestManager(t)
	port := freeUDP(45300)
	dt := add("dnstt", port, map[string]any{"xdnsDomain": "d.example.com", "signIn": "open"})
	defer m.stopDNS()
	m.Apply() // Xray is not installed here; the DNS side does not need it

	if z := m.DNSPorts()[port]; len(z) != 1 || z[0].Zone != "d.example.com" {
		t.Fatalf("shared port %d: %+v (errors %v)", port, z, m.inboundErrs)
	}
	if s, ok := m.dnsttStatus(); !ok || !s.BuiltIn || s.Version != DnsttVersion {
		t.Errorf("dnstt status: %+v", s)
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	dnsttclient.HandshakeTimeout = 15 * time.Second
	c, err := dnsttclient.Dial(addr, "d.example.com", dt.Str("publicKey"))
	if err != nil {
		t.Fatalf("dnstt through the shared port: %v", err)
	}
	c.Close()

	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q, 77)
	binary.BigEndian.PutUint16(q[4:], 1)
	q = append(q, 3, 'w', 'w', 'w', 6, 'g', 'o', 'o', 'g', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1)
	uc, _ := net.Dial("udp", addr)
	uc.Write(q)
	uc.SetReadDeadline(time.Now().Add(2 * time.Second))
	r := make([]byte, 512)
	n, err := uc.Read(r)
	uc.Close()
	if err != nil || n < 12 || r[3]&0x0f != 5 {
		t.Fatalf("an unknown name should be refused: %x %v", r[:n], err)
	}

	dt.Enabled = false
	m.Store.UpdateInbound(dt)
	m.Apply()
	if len(m.DNSPorts()) != 0 || !util.FreeUDPOn("127.0.0.1", port) {
		t.Fatalf("the port stays held after the tunnel was removed: %v", m.DNSPorts())
	}
	if _, ok := m.dnsttStatus(); ok {
		t.Error("dnstt still reported as running")
	}
}
