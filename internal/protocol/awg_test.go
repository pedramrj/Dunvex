package protocol

import (
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// hRange reads "a-b" and checks it lies above WireGuard's own message types.
func hRange(t *testing.T, v string) (uint64, uint64) {
	t.Helper()
	a, b, ok := strings.Cut(v, "-")
	lo, e1 := strconv.ParseUint(a, 10, 32)
	hi, e2 := strconv.ParseUint(b, 10, 32)
	if !ok || e1 != nil || e2 != nil || lo < 5 || hi <= lo {
		t.Fatalf("bad header range %q", v)
	}
	return lo, hi
}

func TestAWGDefaults(t *testing.T) {
	p := Get("amneziawg")
	if p == nil || p.Engine != "awg" || p.Network != "udp" {
		t.Fatalf("preset: %+v", p)
	}
	in := &store.Inbound{ID: 7, Preset: p.ID, Port: 51821, Enabled: true, Params: Defaults(p, nil, "")}
	if in.Str("version") != "3.1" || in.Str("mtu") != "1280" || in.Str("dns") == "" {
		t.Fatalf("defaults: %v", in.Params)
	}
	// the public key belongs to the private key
	if pub, err := util.WireGuardPublic(in.Str("privateKey")); err != nil || pub != in.Str("publicKey") {
		t.Fatalf("key pair: %v %s", err, pub)
	}
	// 3.1: one padding for all four messages, at least 12 for header protection
	s1 := in.Str("s1")
	for _, k := range []string{"s2", "s3", "s4"} {
		if in.Str(k) != s1 {
			t.Errorf("3.1 paddings differ: %s=%s s1=%s", k, in.Str(k), s1)
		}
	}
	if n, _ := strconv.Atoi(s1); n < 12 {
		t.Errorf("padding %d is below 12", n)
	}
	if k, err := base64.StdEncoding.DecodeString(in.Str("hpk")); err != nil || len(k) != 32 {
		t.Fatalf("header protection key: %q", in.Str("hpk"))
	}
	// four header ranges that do not overlap
	type span struct{ lo, hi uint64 }
	var hs []span
	for i := 1; i <= 4; i++ {
		lo, hi := hRange(t, in.Str("h"+strconv.Itoa(i)))
		for _, o := range hs {
			if lo <= o.hi && o.lo <= hi {
				t.Fatalf("header ranges overlap: %v", in.Params)
			}
		}
		hs = append(hs, span{lo, hi})
	}

	// saving again keeps everything (clients must not need a new file)
	before := map[string]string{}
	for k := range in.Params {
		before[k] = in.Str(k)
	}
	again := Defaults(p, in.Params, "")
	for k, v := range before {
		if got := (&store.Inbound{Params: again}).Str(k); got != v {
			t.Errorf("%s changed on save: %q → %q", k, v, got)
		}
	}
	// the MTU set by the admin stays, also after a trip through JSON
	in.Params["mtu"] = float64(1380)
	if Defaults(p, in.Params, "")["mtu"] != 1380 {
		t.Errorf("mtu was not kept: %v", in.Params["mtu"])
	}

	// 2.0 gets its own values: no header protection, init and response of different size
	in.Params["version"] = "2.0"
	Defaults(p, in.Params, "")
	if _, ok := in.Params["hpk"]; ok || in.Str("madeFor") != "2.0" {
		t.Fatalf("2.0 params: %v", in.Params)
	}
	a, _ := strconv.Atoi(in.Str("s1"))
	b, _ := strconv.Atoi(in.Str("s2"))
	if a+56 == b {
		t.Errorf("2.0 init and response have the same size: s1=%d s2=%d", a, b)
	}
	if d := AWGDevice(in); d["header_protection_key"] != "" || d["random_trailers"] != "" {
		t.Errorf("2.0 device has 3.1 keys: %v", d)
	}
}

func TestAWGConfigAndDevice(t *testing.T) {
	p := Get("amneziawg")
	in := &store.Inbound{ID: 7, Preset: p.ID, Port: 51821, Enabled: true, Params: Defaults(p, nil, "")}
	priv, pub := util.WireGuardKeys()
	u := &store.User{Name: "sara", WGPrivate: priv, WGPublic: pub, WGIndex: 2}

	conf := Link(p, in, u, Env{Host: "203.0.113.10"}, "AmneziaWG")
	for _, want := range []string{
		"[Interface]\n", "PrivateKey = " + priv + "\n", "Address = 10.67.0.3/32\n", "MTU = 1280\n",
		"Jc = " + in.Str("jc") + "\n", "S1 = " + in.Str("s1") + "\n", "H4 = " + in.Str("h4") + "\n",
		"HeaderProtectionKey = " + in.Str("hpk") + "\n", "RandomTrailers = true\n",
		"[Peer]\n", "PublicKey = " + in.Str("publicKey") + "\n", "Endpoint = 203.0.113.10:51821\n",
		"AllowedIPs = 0.0.0.0/0, ::/0\n", "PersistentKeepalive = 25\n",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("conf misses %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "I1 =") {
		t.Errorf("empty I1 is written:\n%s", conf)
	}
	// IPv6 servers are written in brackets
	if c := AWGConfig(in, u, Env{Host: "2001:db8::1"}); !strings.Contains(c, "Endpoint = [2001:db8::1]:51821\n") {
		t.Errorf("ipv6 endpoint:\n%s", c)
	}

	// the server gets the same obfuscation, the key in hex
	d := AWGDevice(in)
	k, _ := base64.StdEncoding.DecodeString(in.Str("hpk"))
	if d["header_protection_key"] != hex.EncodeToString(k) || d["random_trailers"] != "true" || d["s1"] != in.Str("s1") || d["h1"] != in.Str("h1") {
		t.Fatalf("device: %v", d)
	}

	// every user has an address of their own inside the tunnel
	seen := map[string]bool{AWGServerAddress(): true}
	for i := 2; i < 1200; i++ {
		a := AWGAddress(&store.User{WGIndex: i})
		if seen[a] || strings.HasSuffix(a, ".0") || strings.HasSuffix(a, ".255") {
			t.Fatalf("address %d: %s", i, a)
		}
		seen[a] = true
	}
}
