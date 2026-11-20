package protocol

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/store"
)

// ttFields decodes a tt:// link into tag → values.
func ttFields(t *testing.T, link string) map[byte][][]byte {
	t.Helper()
	raw, ok := strings.CutPrefix(link, "tt://?")
	if !ok {
		t.Fatalf("not a tt:// link: %s", link)
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
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

func TestTrustTunnel(t *testing.T) {
	p := Get("trusttunnel")
	if p == nil || p.Engine != "trusttunnel" || !p.alsoUDP {
		t.Fatalf("preset: %+v", p)
	}
	// no domain: the camouflage hostname, with a certificate made for it
	in := &store.Inbound{ID: 5, Preset: p.ID, Port: 8443, Enabled: true, Params: Defaults(p, nil, "")}
	if in.Str("sni") != DefaultReality || in.Str("protocol") != "http2" || in.Str("ttCertFor") != DefaultReality {
		t.Fatalf("defaults: %v", in.Params)
	}
	blk, _ := pem.Decode([]byte(in.Str("ttCert")))
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil || cert.VerifyHostname(DefaultReality) != nil {
		t.Fatalf("certificate: %v", err)
	}
	certPEM := in.Str("ttCert")
	Defaults(p, in.Params, "")
	if in.Str("ttCert") != certPEM {
		t.Error("saving made a new certificate (links would break)")
	}
	in.Params["sni"] = "cdn.example.org"
	Defaults(p, in.Params, "")
	if in.Str("ttCert") == certPEM || in.Str("ttCertFor") != "cdn.example.org" {
		t.Error("a new hostname kept the old certificate")
	}
	if d := Defaults(p, map[string]any{}, "vpn.example.com"); d["sni"] != "vpn.example.com" {
		t.Errorf("with a domain the hostname is the domain: %v", d["sni"])
	}
	if d := Defaults(p, map[string]any{"sni": "203.0.113.9"}, ""); d["sni"] != DefaultReality {
		t.Errorf("an IP cannot be a TLS server name: %v", d["sni"])
	}

	sara := &store.User{Name: "sara", Password: `p"ss\word`}
	reza := &store.User{Name: "reza", Password: "r-pass"}
	env := Env{Host: "203.0.113.10"}

	// without users the endpoint would let anyone in: it must not run
	if s := TrustTunnelServer(in, nil, env, "/run/dvx", 62792, ""); s != nil {
		t.Fatal("a server config with no users")
	}
	if s := TrustTunnelServer(in, []*store.User{{Name: "x"}}, env, "/run/dvx", 62792, ""); s != nil {
		t.Fatal("a server config with only password-less users")
	}
	s := TrustTunnelServer(in, []*store.User{sara, reza}, env, "/run/dvx", 62792, "")
	creds := string(s.Files["tt-credentials.toml"])
	if !strings.Contains(creds, `username = "sara"`) || !strings.Contains(creds, `password = "p\"ss\\word"`) || !strings.Contains(creds, `username = "reza"`) {
		t.Errorf("credentials:\n%s", creds)
	}
	hosts := string(s.Files[TTHostsFile])
	if !strings.Contains(hosts, `hostname = "cdn.example.org"`) || !strings.Contains(hosts, `"/run/dvx/tt-cert.pem"`) || string(s.Files["tt-cert.pem"]) != in.Str("ttCert") {
		t.Errorf("hosts:\n%s", hosts)
	}
	main := string(s.Main)
	for _, want := range []string{`listen_address = "0.0.0.0:8443"`, `credentials_file = "/run/dvx/tt-credentials.toml"`, `address = "127.0.0.1:62792"`, "per_client_metrics = true", "[listen_protocols.quic]"} {
		if !strings.Contains(main, want) {
			t.Errorf("main settings miss %q:\n%s", want, main)
		}
	}
	// a change of users changes the main settings, so the endpoint restarts
	if s2 := TrustTunnelServer(in, []*store.User{sara}, env, "/run/dvx", 62792, ""); bytes.Equal(s2.Main, s.Main) {
		t.Error("removing a user does not restart the endpoint")
	}

	// the link
	in.Params["protocol"], in.Params["antiDpi"], in.Params["dns"] = "http3", true, "tls://1.1.1.1, 9.9.9.9"
	f := ttFields(t, ttLink(in, sara, env, "Dunvex TT"))
	one := func(tag byte) string {
		if len(f[tag]) != 1 {
			t.Fatalf("tag %#x: %q", tag, f[tag])
		}
		return string(f[tag][0])
	}
	if one(0x00) != "\x02" || one(0x01) != "cdn.example.org" || one(0x02) != "203.0.113.10:8443" || one(0x04) != "\x00" ||
		one(0x05) != "sara" || one(0x06) != sara.Password || one(0x09) != "\x02" || one(0x0A) != "\x01" || one(0x0C) != "Dunvex TT" {
		t.Errorf("link fields: %q", f)
	}
	nb, _ := pem.Decode([]byte(in.Str("ttCert")))
	if one(0x08) != string(nb.Bytes) || len(nb.Bytes) < 64 {
		t.Error("the link does not carry the certificate (its length needs a 2-byte varint)")
	}
	if one(0x0D) != "\x0dtls://1.1.1.1\x079.9.9.9" {
		t.Errorf("dns list: %q", one(0x0D))
	}
	if l := ttLink(in, sara, Env{Host: "2001:db8::1"}, "x"); string(ttFields(t, l)[0x02][0]) != "[2001:db8::1]:8443" {
		t.Errorf("ipv6 address in the link: %s", l)
	}

	// the panel's domain with a real certificate: that certificate, none in the link
	in.Params["sni"] = "vpn.example.com"
	Defaults(p, in.Params, "vpn.example.com")
	real := Env{Host: "vpn.example.com", Domain: "vpn.example.com", CertFile: "/etc/dvx/live.crt", KeyFile: "/etc/dvx/live.key"}
	s = TrustTunnelServer(in, []*store.User{sara}, real, "/run/dvx", 62792, "")
	if !strings.Contains(string(s.Files[TTHostsFile]), `"/etc/dvx/live.crt"`) || s.Files["tt-cert.pem"] != nil {
		t.Errorf("real certificate:\n%s", s.Files[TTHostsFile])
	}
	if _, ok := ttFields(t, ttLink(in, sara, real, "x"))[0x08]; ok {
		t.Error("a real certificate was put in the link")
	}

	// TCP and UDP on its port: a UDP protocol on the same port clashes too
	list := []*store.Inbound{{ID: 9, Preset: "hysteria2", Port: 8443}}
	if got := PortConflict(list, 443, 8443, 0, p, ""); !strings.Contains(got, "already used") {
		t.Errorf("TrustTunnel next to Hysteria2 on one port: %q", got)
	}
	if got := PortConflict([]*store.Inbound{{ID: 5, Preset: "trusttunnel", Port: 8443}}, 443, 8443, 0, Get("hysteria2"), ""); !strings.Contains(got, "already used") {
		t.Errorf("Hysteria2 next to TrustTunnel on one port: %q", got)
	}
}
