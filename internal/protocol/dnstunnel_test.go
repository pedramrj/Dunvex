package protocol

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/dnstt"
	"github.com/pedramrj/dunvex/internal/store"
)

// slipnetFields decodes a SlipNet link into its fields.
func slipnetFields(t *testing.T, link string) []string {
	t.Helper()
	b64, ok := strings.CutPrefix(link, "slipnet://")
	if !ok {
		t.Fatalf("not a SlipNet link: %s", link)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(raw), "|")
}

func TestDnsttKeysAndLinks(t *testing.T) {
	p := Get("dnstt")
	in := &store.Inbound{ID: 7, Preset: p.ID, Port: 53, Enabled: true, Params: Defaults(p, map[string]any{"xdnsDomain": "T.Example.com."}, "")}
	priv, pub := in.Str("privateKey"), in.Str("publicKey")
	if len(priv) != 64 || len(pub) != 64 || dnstt.PublicKey(priv) != pub {
		t.Fatalf("keys: %q %q", priv, pub)
	}
	if in.Str("signIn") != "user" || in.Str("resolver") != "8.8.8.8" {
		t.Fatalf("defaults: %v", in.Params)
	}
	// the owner's own private key gives its public key; an empty one is replaced
	other, otherPub, _ := dnstt.NewKey()
	if q := Defaults(p, map[string]any{"privateKey": other, "publicKey": "stale"}, ""); q["publicKey"] != otherPub {
		t.Fatalf("own key: %v", q)
	}

	u := &store.User{Name: "sara", Password: "pw|with|bars"}
	env := Env{Host: "203.0.113.10"}
	f := slipnetFields(t, Link(p, in, u, env, "Dunvex · dnstt"))
	if len(f) != 84 {
		t.Fatalf("SlipNet v30 has 84 fields, the link has %d", len(f))
	}
	want := map[int]string{0: "30", 1: "dnstt", 2: "Dunvex · dnstt", 3: "t.example.com", 4: "8.8.8.8:53:0", 5: "0", 7: "bbr",
		8: "1080", 9: "127.0.0.1", 11: pub, 12: "sara", 13: "pwwithbars", 14: "0", 22: "udp", 27: "0", 60: "roundrobin", 71: ""}
	for i, v := range want {
		if f[i] != v {
			t.Errorf("field %d = %q, want %q", i, f[i], v)
		}
	}

	// straight to this server: its address, marked authoritative (faster polling)
	in.Params["resolver"] = "direct"
	f = slipnetFields(t, Link(p, in, u, env, "x"))
	if f[4] != "203.0.113.10:53:1" || f[5] != "1" || f[27] != "1" {
		t.Errorf("direct: resolvers %q, authoritative %q / %q", f[4], f[5], f[27])
	}
	if d := TunnelDetails(p, in, env); d != "" {
		t.Errorf("a tunnel that signs users in has no hand-typed details: %s", d)
	}
	// open: no SOCKS password in the link
	in.Params["signIn"] = "open"
	if f = slipnetFields(t, Link(p, in, u, env, "x")); f[12] != "" || f[13] != "" {
		t.Errorf("open tunnel carries credentials: %q %q", f[12], f[13])
	}
	if d := TunnelDetails(p, in, env); !strings.Contains(d, "Public key: "+pub) || !strings.Contains(d, "no password") || !strings.Contains(d, "203.0.113.10:53") {
		t.Errorf("details: %s", d)
	}
}

func TestSlipstreamLinkAndArgs(t *testing.T) {
	p := Get("slipstream")
	in := &store.Inbound{ID: 9, Preset: p.ID, Port: 53, Enabled: true, Params: Defaults(p, map[string]any{"xdnsDomain": "s.example.com"}, "")}
	if in.Str("privateKey") != "" {
		t.Fatal("Slipstream needs no dnstt key")
	}
	f := slipnetFields(t, Link(p, in, &store.User{Name: "ali", Password: "pw"}, Env{Host: "203.0.113.10"}, "SS"))
	if len(f) != 84 || f[1] != "ss" || f[11] != "" || f[7] != "dcubic" || f[12] != "ali" || f[13] != "pw" {
		t.Fatalf("Slipstream link: %q", f[:14])
	}
	args := strings.Join(SlipstreamArgs(in, 41000, "127.0.0.1:42000", "/run/c.pem", "/run/k.pem"), " ")
	if strings.Contains(args, "--dns-listen-address") {
		t.Error("Slipstream v0.1.1 refuses --dns-listen-address")
	}
	for _, w := range []string{"--dns-listen-port=41000", "--target-address=127.0.0.1:42000", "--domain=s.example.com", "--cert=/run/c.pem", "--key=/run/k.pem"} {
		if !strings.Contains(args, w) {
			t.Errorf("args lack %s: %s", w, args)
		}
	}
}

func TestTunnelSocks(t *testing.T) {
	in := &store.Inbound{ID: 3, Preset: "dnstt", Params: map[string]any{"signIn": "user"}}
	us := []*store.User{{Name: "a", Password: "1"}, {Name: "b", Password: "2"}}
	ib := TunnelSocks(in, us, 41003)
	s := ib["settings"].(obj)
	if ib["tag"] != "in-3" || ib["listen"] != "127.0.0.1" || ib["port"] != 41003 || s["auth"] != "password" || len(s["accounts"].([]obj)) != 2 || s["udp"] != false {
		t.Fatalf("per-user SOCKS: %v", ib)
	}
	in.Params["signIn"] = "open"
	if s := TunnelSocks(in, us, 41003)["settings"].(obj); s["auth"] != "noauth" || s["accounts"] != nil {
		t.Fatalf("open SOCKS: %v", s)
	}
}

// DNS tunnels share a UDP port by domain; nothing else shares it with them.
func TestDNSTunnelsShareAPort(t *testing.T) {
	mk := func(id int64, preset, domain string) *store.Inbound {
		return &store.Inbound{ID: id, Preset: preset, Port: 53, Enabled: true, Params: map[string]any{"xdnsDomain": domain}}
	}
	list := []*store.Inbound{mk(1, "vless-xdns", "x.example.com"), mk(2, "dnstt", "d.example.com")}
	if msg := PortConflict(list, 443, 53, 0, Get("slipstream"), "s.example.com"); msg != "" {
		t.Errorf("a third tunnel with its own domain: %s", msg)
	}
	if msg := PortConflict(list, 443, 53, 0, Get("slipstream"), "D.example.com."); !strings.Contains(msg, "already uses the tunnel domain d.example.com") {
		t.Errorf("same domain: %q", msg)
	}
	if msg := PortConflict(list, 443, 53, 0, Get("dnstt"), ""); !strings.Contains(msg, "tunnel domain") {
		t.Errorf("no domain: %q", msg)
	}
	if msg := PortConflict(list, 443, 53, 0, Get("hysteria2"), ""); !strings.Contains(msg, "already used by") {
		t.Errorf("Hysteria2 on the DNS port: %q", msg)
	}
	if msg := PortConflict(list, 443, 53, 0, Get("vless-reality-vision"), "www.apple.com"); msg != "" {
		t.Errorf("TCP 53 is free: %q", msg)
	}
	if RouteName(list[1]) != "d.example.com" || RouteName(&store.Inbound{Preset: "trusttunnel", Params: map[string]any{"sni": "A.com"}}) != "a.com" {
		t.Error("route names")
	}
}
