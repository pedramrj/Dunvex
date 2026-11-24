package protocol

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

func clientUser() *store.User {
	priv, pub := util.WireGuardKeys()
	return &store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(20), WGPrivate: priv, WGPublic: pub, WGIndex: 7, Enabled: true}
}

func at(o any, path ...any) any {
	for _, k := range path {
		switch x := o.(type) {
		case map[string]any:
			o = x[k.(string)]
		case []map[string]any:
			o = x[k.(int)]
		case []any:
			o = x[k.(int)]
		case []string:
			o = x[k.(int)]
		default:
			return nil
		}
	}
	return o
}

// Every link Dunvex makes for an Xray protocol opens in an Xray client, with
// the details the server checks.
func TestXrayOutboundForEveryXrayPreset(t *testing.T) {
	u := clientUser()
	env := Env{Host: "203.0.113.10", Domain: "vpn.example.com", CertFile: "c.pem", KeyFile: "k.pem"}
	n := 0
	for _, p := range All() {
		if p.Engine != "xray" {
			continue
		}
		in := &store.Inbound{ID: 3, Preset: p.ID, Port: 8443, Enabled: true, Params: Defaults(p, nil, env.Domain)}
		if p.transport == "xdns" {
			in.Params["xdnsDomain"] = "t.example.com"
		}
		link := Link(p, in, u, env, "test")
		o, err := XrayOutbound(link, "proxy")
		if err != nil {
			t.Errorf("%s: %v\n%s", p.ID, err, link)
			continue
		}
		n++
		want := map[string]string{"vless": "vless", "vmess": "vmess", "trojan": "trojan", "shadowsocks": "shadowsocks",
			"hysteria": "hysteria", "wireguard": "wireguard", "socks": "socks", "http": "http"}[p.proto]
		if o["protocol"] != want || o["tag"] != "proxy" {
			t.Errorf("%s: protocol %v, tag %v", p.ID, o["protocol"], o["tag"])
		}
		if _, err := json.Marshal(o); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
		switch p.Security {
		case "reality":
			r := at(o, "streamSettings", "realitySettings")
			if at(r, "publicKey") != in.Str("publicKey") || at(r, "shortId") != in.Str("shortId") || at(r, "serverName") != in.Str("sni") {
				t.Errorf("%s: reality %v", p.ID, r)
			}
		case "tls":
			if p.proto != "hysteria" && at(o, "streamSettings", "tlsSettings", "serverName") != "vpn.example.com" {
				t.Errorf("%s: tls %v", p.ID, at(o, "streamSettings"))
			}
		}
		switch p.proto {
		case "vless":
			if at(o, "settings", "vnext", 0, "users", 0, "id") != u.UUID {
				t.Errorf("%s: user %v", p.ID, at(o, "settings"))
			}
			if p.flow != "" && at(o, "settings", "vnext", 0, "users", 0, "flow") != p.flow {
				t.Errorf("%s: flow lost", p.ID)
			}
		case "trojan":
			if at(o, "settings", "servers", 0, "password") != u.Password {
				t.Errorf("%s: password", p.ID)
			}
		case "vmess":
			if at(o, "settings", "vnext", 0, "users", 0, "id") != u.UUID {
				t.Errorf("%s: vmess id", p.ID)
			}
		}
	}
	if n < 15 {
		t.Fatalf("only %d Xray presets", n)
	}
}

func TestXrayOutboundDetails(t *testing.T) {
	u := clientUser()
	env := Env{Host: "203.0.113.10", SelfSigned: true, CertSHA256: strings.Repeat("ab", 32), CertFile: "c", KeyFile: "k"}
	in := func(id string) (*Preset, *store.Inbound) {
		p := Get(id)
		return p, &store.Inbound{ID: 1, Preset: id, Port: 4444, Enabled: true, Params: Defaults(p, nil, "")}
	}

	// Hysteria2 with obfs, pinned self-signed certificate
	p, hy := in("hysteria2")
	hy.Params["obfs"], hy.Params["obfsPassword"] = true, "salt"
	o, err := XrayOutbound(Link(p, hy, u, env, "h"), "x")
	if err != nil {
		t.Fatal(err)
	}
	if at(o, "streamSettings", "hysteriaSettings", "auth") != u.Password || at(o, "streamSettings", "tlsSettings", "pinnedPeerCertSha256") != env.CertSHA256 ||
		at(o, "streamSettings", "finalmask", "udp", 0, "settings", "password") != "salt" || at(o, "settings", "port") != 4444 {
		js, _ := json.Marshal(o)
		t.Fatalf("hysteria2: %s", js)
	}
	// without a fingerprint Xray cannot accept a self-signed certificate
	if _, err := XrayOutbound("hysteria2://pw@203.0.113.10:4444/?sni=a&insecure=1", "x"); err == nil {
		t.Fatal("insecure link without a pin accepted")
	}

	// Shadowsocks 2022: server key and user key, both kept whole
	p, ss := in("shadowsocks-2022")
	o, err = XrayOutbound(Link(p, ss, u, env, "s"), "x")
	if err != nil {
		t.Fatal(err)
	}
	pw := at(o, "settings", "servers", 0, "password").(string)
	if at(o, "settings", "servers", 0, "method") != ss.Str("method") || !strings.HasPrefix(pw, ss.Str("serverKey")+":") {
		t.Fatalf("ss2022: %v", at(o, "settings"))
	}
	// classic Shadowsocks: base64 userinfo
	p, ssc := in("shadowsocks")
	o, err = XrayOutbound(Link(p, ssc, u, env, "s"), "x")
	if err != nil || at(o, "settings", "servers", 0, "password") != u.Password {
		t.Fatalf("ss: %v %v", err, o)
	}

	// SOCKS: v2rayN's base64 "user:pass" with a "/" in it
	cred := base64.StdEncoding.EncodeToString([]byte("??????:pa?ss>>"))
	if !strings.Contains(cred, "/") {
		t.Fatal("the test needs a / in the base64")
	}
	o, err = XrayOutbound("socks://"+cred+"@198.51.100.7:1080#x", "x")
	if err != nil || at(o, "settings", "servers", 0, "users", 0, "user") != "??????" || at(o, "settings", "servers", 0, "users", 0, "pass") != "pa?ss>>" ||
		at(o, "settings", "servers", 0, "port") != 1080 {
		t.Fatalf("socks: %v %v", err, o)
	}

	// WireGuard: keys, address and endpoint
	p, wg := in("wireguard")
	o, err = XrayOutbound(Link(p, wg, u, env, "w"), "x")
	if err != nil || at(o, "settings", "secretKey") != u.WGPrivate || at(o, "settings", "peers", 0, "endpoint") != "203.0.113.10:4444" ||
		at(o, "settings", "address", 0) != "10.66.0.8/32" || at(o, "settings", "noKernelTun") != true {
		t.Fatalf("wireguard: %v %v", err, o)
	}

	// TLS with the self-signed certificate pinned
	p, tr := in("trojan-tls")
	o, err = XrayOutbound(Link(p, tr, u, env, "t"), "x")
	if err != nil || at(o, "streamSettings", "tlsSettings", "pinnedPeerCertSha256") != env.CertSHA256 {
		t.Fatalf("trojan-tls: %v %v", err, at(o, "streamSettings"))
	}

	// xICMP: the finalmask travels in the link
	p, ic := in("vless-xicmp")
	ic.Params["icmpDgram"] = true
	o, err = XrayOutbound(Link(p, ic, u, env, "i"), "x")
	if err != nil || at(o, "streamSettings", "network") != "kcp" || at(o, "streamSettings", "finalmask", "udp", 0, "type") != "xicmp" {
		t.Fatalf("xicmp: %v %v", err, at(o, "streamSettings"))
	}

	for _, bad := range []string{"tg://proxy?server=a", "mieru://x", "vless://id@host", "vless://id@h:1?type=quic", "ss://bm9wZQ@h:1"} {
		if _, err := XrayOutbound(bad, "x"); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
