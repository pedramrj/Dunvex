package protocol

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/pedramrj/dunvex/internal/sshproxy"
	"github.com/pedramrj/dunvex/internal/store"
)

// SSH: Dunvex's own SSH server (internal/sshproxy), port forwarding only.
// Each user signs in with their name and password. The server's Ed25519 host
// key is made once and kept in the inbound ("hostKey", its seed); the links
// and profiles pin its public half ("hostKeyPub"), so an app that checks it
// cannot be fooled by another server. Every forward goes to a SOCKS inbound
// of Xray on loopback (SSHSocks), so the routing rules apply.
//
// What apps read (checked in their code):
//   - Hiddify app: ssh://user:pass@host:port/?hk=<host key> (hiddify/ray2sing
//     ssh.go reads the user, the password and hk), from the link or the
//     subscription; it turns UDP over TCP on, which the server speaks.
//   - SlipNet: its own slipnet:// profile, tunnel type "ssh" (SSH-only).
//   - Clash.Meta (mihomo): type ssh; sing-box apps: the ssh outbound.
//   - NetMod, HTTP Injector and other SSH tunnel apps import only their own
//     encrypted files: users type the details in (SSHDetails).

// IsSSH reports whether p is the SSH preset.
func IsSSH(p *Preset) bool { return p != nil && p.Engine == "ssh" }

func sshDefaults(params map[string]any) {
	if sshproxy.PublicKey(str(params["hostKey"])) == "" {
		params["hostKey"] = sshproxy.NewHostKey()
	}
	params["hostKeyPub"] = sshproxy.PublicKey(str(params["hostKey"]))
}

// SSHHostKey is the server's public host key as apps pin it ("ssh-ed25519 AAAA…").
func SSHHostKey(in *store.Inbound) string { return sshproxy.PublicKey(in.Str("hostKey")) }

// SSHSocks is the SOCKS inbound of Xray on loopback that the SSH server hands
// every forward to, TCP and UDP. The SSH server signs users in and counts
// their usage itself, so this inbound has no accounts (it changes, and Xray
// restarts, only when the inbound itself changes). Its tag is the inbound's,
// so the inbound's usage counts there.
func SSHSocks(in *store.Inbound, port int) map[string]any {
	return obj{"tag": in.Tag(), "listen": "127.0.0.1", "port": port, "protocol": "socks",
		"settings": obj{"auth": "noauth", "udp": true, "ip": "127.0.0.1"},
		"sniffing": obj{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}}
}

// sshLink is the ssh:// link of the Hiddify app.
func sshLink(in *store.Inbound, u *store.User, env Env, remark string) string {
	q := url.Values{}
	q.Set("hk", SSHHostKey(in))
	// a + is legal in the userinfo, but apps that decode it as a form value read a space
	ui := strings.ReplaceAll(url.UserPassword(u.Name, u.Password).String(), "+", "%2B")
	return "ssh://" + ui + "@" + hostPort(LinkHost(in, env), in.Port) +
		"/?" + strings.ReplaceAll(q.Encode(), "+", "%20") + "#" + url.PathEscape(remark)
}

// SSHSlipNetLink is the SlipNet profile (format v30, tunnel type "ssh") of an
// SSH inbound: the fields in the order SlipNet's ConfigExporter writes them
// (github.com/anonvector/SlipNet). For SSH-only it dials the "domain" field
// with the SSH port, signs in with the SSH name and password, and asks its
// DNS (8.8.8.8, then 1.1.1.1) over TCP through the tunnel. SlipNet does not
// check host keys.
func SSHSlipNetLink(in *store.Inbound, u *store.User, env Env, remark string) string {
	clean := func(s string) string { return strings.ReplaceAll(s, "|", "") }
	host := clean(LinkHost(in, env))
	f := []string{
		"30", "ssh", clean(remark), host, "", // version, tunnel type, name, server, resolvers (none for SSH)
		"0", "5000", "bbr", "1080", "127.0.0.1", "0", // authoritative, keep-alive (ms), congestion, local SOCKS port and host, GSO
		"", "", "", // dnstt key, SOCKS name and password: unused
		"1", clean(u.Name), clean(u.Password), strconv.Itoa(in.Port), "0", host, // SSH on, name, password, port, DNS through SSH (off: its own), SSH host
		"0", "", "udp", "password", "", "", "", // DoH URL, DNS transport, SSH auth, SSH key, passphrase, Tor bridges
		"0",                                  // dnstt's faster query rate: unused
		"443", "", "", "0", "", "0", "0", "", // Naive; not locked, no expiry, no sharing, no device
		"0", "", "0", "0", "1080", // resolvers shown, NoizDNS stealth off, full payload, SOCKS5 port
		"0", "txt", "101", "0.0", "0", "0", "0", "0", "0", // VayDNS defaults
		"0", "", "", "8080", "", "0", "/", "1", "", "", // SSH over TLS / HTTP proxy / WebSocket: off, no payload
		"roundrobin", "3",
		"", "tls", "ws", "/", "", "443", "0", "micro", "300", "", // VLESS: unused
		"0", "0", "0", "8", "", "0", "", // DPI options: off
		"0", "", "auto", "soar", "", // Soar: off
	}
	return "slipnet://" + base64.StdEncoding.EncodeToString([]byte(strings.Join(f, "|")))
}

// SSHDetails is what an SSH app without a link of its own (NetMod, HTTP
// Injector, Termius, ssh -D) needs typed in.
func SSHDetails(in *store.Inbound, u *store.User, env Env) string {
	return fmt.Sprintf("Server: %s\nPort: %d\nUsername: %s\nPassword: %s\nHost key: %s (ssh-ed25519)",
		LinkHost(in, env), in.Port, u.Name, u.Password, sshproxy.Fingerprint(in.Str("hostKey")))
}

func sshClash(in *store.Inbound, u *store.User, c obj) {
	c["type"], c["username"], c["password"] = "ssh", u.Name, u.Password
	c["host-key"] = []string{SSHHostKey(in)}
	c["host-key-algorithms"] = []string{"ssh-ed25519"}
	delete(c, "udp") // mihomo's SSH carries no UDP
}

func sshSingbox(in *store.Inbound, u *store.User, o obj) {
	o["type"], o["user"], o["password"] = "ssh", u.Name, u.Password
	o["host_key"] = []string{SSHHostKey(in)}
	o["host_key_algorithms"] = []string{"ssh-ed25519"}
}
