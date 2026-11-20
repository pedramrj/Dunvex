package protocol

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/pedramrj/dunvex/internal/dnstt"
	"github.com/pedramrj/dunvex/internal/store"
)

// DNS tunnels: dnstt (in Dunvex itself) and Slipstream (its own server) carry
// each app's TCP streams inside DNS queries and hand them to a SOCKS inbound
// of Xray on loopback. That inbound signs users in by name and password, so
// usage is counted per user; an open tunnel signs nobody in (for apps that
// cannot send a SOCKS password, such as DNSTT.XYZ). Several DNS tunnels and
// XDNS can share a UDP port (53): each has its own tunnel domain.

// TunnelSignIns are the sign-in choices of a DNS tunnel.
var TunnelSignIns = []string{"user", "open"}

// IsDNSTunnel reports whether p is dnstt or Slipstream.
func IsDNSTunnel(p *Preset) bool {
	return p != nil && (p.Engine == "dnstt" || p.Engine == "slipstream")
}

// DNSRouted reports whether p is told apart on a shared UDP port by its tunnel
// domain: XDNS, dnstt and Slipstream.
func DNSRouted(p *Preset) bool { return p != nil && (p.transport == "xdns" || IsDNSTunnel(p)) }

// RouteName is what tells an inbound apart on a shared port: a DNS tunnel's
// domain, otherwise its TLS server name.
func RouteName(in *store.Inbound) string {
	if DNSRouted(Get(in.Preset)) {
		return XDNSDomain(in)
	}
	return SharedSNI(in)
}

// OpenSignIn reports whether a DNS tunnel lets anyone in without a password.
func OpenSignIn(in *store.Inbound) bool { return in.Str("signIn") == "open" }

func dnsttKeys(params map[string]any) {
	if pub := dnstt.PublicKey(str(params["privateKey"])); pub != "" {
		params["publicKey"] = pub
		return
	}
	if priv, pub, err := dnstt.NewKey(); err == nil {
		params["privateKey"], params["publicKey"] = priv, pub
	}
}

// TunnelSocks is the SOCKS inbound of Xray a DNS tunnel hands its streams to,
// on loopback port: users sign in with their name and password, nobody when
// the tunnel is open. Its tag is the inbound's, so usage counts there.
func TunnelSocks(in *store.Inbound, users []*store.User, port int) map[string]any {
	settings := obj{"auth": "noauth", "udp": false}
	if !OpenSignIn(in) {
		accts := []obj{}
		for _, u := range users {
			accts = append(accts, obj{"user": u.Name, "pass": u.Password})
		}
		settings = obj{"auth": "password", "accounts": accts, "udp": false}
	}
	return obj{"tag": in.Tag(), "listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": settings,
		"sniffing": obj{"enabled": true, "destOverride": []string{"http", "tls"}, "routeOnly": true}}
}

// SlipstreamArgs is the Slipstream server's command line: DNS on listenPort
// (behind the shared port), streams to the SOCKS inbound at target, QUIC with
// the certificate in cert and key. Only flags v0.1.1 knows (it refuses any
// other): it has no --dns-listen-address yet, so it listens on every address
// of that port.
func SlipstreamArgs(in *store.Inbound, listenPort int, target, cert, key string) []string {
	return []string{
		"--dns-listen-port=" + strconv.Itoa(listenPort),
		"--target-address=" + target,
		"--domain=" + XDNSDomain(in),
		"--cert=" + cert,
		"--key=" + key,
	}
}

// directTunnel reports whether the apps query this server itself (no resolver).
func directTunnel(in *store.Inbound) bool {
	r := strings.TrimSpace(in.Str("resolver"))
	return r == "" || r == "direct"
}

// slipnetLink is the profile link of the SlipNet app (format v30):
// "slipnet://" and the base64 of its fields joined by "|", in the order
// SlipNet's ConfigExporter writes them (github.com/anonvector/SlipNet).
func slipnetLink(p *Preset, in *store.Inbound, u *store.User, env Env, remark string) string {
	clean := func(s string) string { return strings.ReplaceAll(s, "|", "") }
	h, port := xdnsTarget(in, env)
	b := func(ok bool) string {
		if ok {
			return "1"
		}
		return "0"
	}
	direct := directTunnel(in)
	kind, cc, pub := "dnstt", "bbr", in.Str("publicKey")
	if p.Engine == "slipstream" {
		kind, pub = "ss", ""
		if !direct {
			cc = "dcubic" // Slipstream's default through resolvers; BBR is faster straight to the server
		}
	}
	user, pass := "", ""
	if !OpenSignIn(in) && u != nil {
		user, pass = u.Name, u.Password
	}
	f := []string{
		"30", kind, clean(remark), clean(XDNSDomain(in)),
		clean(h) + ":" + strconv.Itoa(port) + ":" + b(direct), // resolvers: host:port:authoritative
		b(direct), "5000", cc, "1080", "127.0.0.1", "0", // authoritative mode, keep-alive (ms), congestion, local SOCKS port and host, GSO
		pub, clean(user), clean(pass),
		"0", "", "", "22", "0", "127.0.0.1", "0", // SSH: off
		"", "udp", "password", "", "", "", // DoH URL, DNS transport, SSH auth, SSH key, passphrase, Tor bridges
		b(direct && kind == "dnstt"),         // dnstt's faster query rate for its own server
		"443", "", "", "0", "", "0", "0", "", // Naive; not locked, no expiry, no sharing, no device
		"0", "", "0", "0", "1080", // resolvers shown, NoizDNS stealth off, full payload, SOCKS5 port
		"0", "txt", "101", "0.0", "0", "0", "0", "0", "0", // VayDNS defaults
		"0", "", "", "8080", "", "0", "/", "1", "", "", // SSH over TLS / HTTP proxy / WebSocket: off
		"roundrobin", "3",
		"", "tls", "ws", "/", "", "443", "0", "micro", "300", "", // VLESS: unused
		"0", "0", "0", "8", "", "0", "", // DPI options: off
		"0", "", "auto", "soar", "", // Soar: off
	}
	return "slipnet://" + base64.StdEncoding.EncodeToString([]byte(strings.Join(f, "|")))
}

// TunnelDetails is what an app without SlipNet links needs to set an open
// DNS tunnel up by hand (DNSTT.XYZ, the dnstt command line): "" for others,
// and for a tunnel that signs users in (those apps cannot send a SOCKS password).
func TunnelDetails(p *Preset, in *store.Inbound, env Env) string {
	if !IsDNSTunnel(p) || !OpenSignIn(in) {
		return ""
	}
	h, port := xdnsTarget(in, env)
	s := fmt.Sprintf("Domain: %s\nDNS: %s", XDNSDomain(in), hostPort(h, port))
	if p.Engine == "dnstt" {
		s += "\nPublic key: " + in.Str("publicKey")
	}
	return s + "\nTunnel type: SOCKS5 (no password)"
}
