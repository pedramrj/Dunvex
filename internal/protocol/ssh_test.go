package protocol

import (
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/pedramrj/dunvex/internal/sshproxy"
	"github.com/pedramrj/dunvex/internal/store"
)

func sshFixture() (*Preset, *store.Inbound, *store.User, Env) {
	p := Get("ssh")
	in := &store.Inbound{ID: 7, Preset: p.ID, Port: 2222, Enabled: true, Params: Defaults(p, nil, "")}
	u := &store.User{Name: "sara", Password: "p@ss:w/rd#?&+ x", UUID: "8f4c2a1e-0000-4000-8000-000000000001"}
	return p, in, u, Env{Host: "203.0.113.9"}
}

func TestSSHPreset(t *testing.T) {
	p := Get("ssh")
	if p == nil || !IsSSH(p) || p.Network != "tcp" || p.DefaultPort == 22 || p.Desc == "" || p.DescFa == "" {
		t.Fatalf("preset %+v", p)
	}
	if CanSharePanelPort(p) || SNIRouted(p) {
		t.Error("SSH has no SNI: it cannot share the panel's port")
	}
}

// The Hiddify app reads ssh:// links with hiddify/ray2sing (ssh.go): the user
// and password from the userinfo, the host key from hk, the name from the
// fragment, all through net/url.
func TestSSHLinkAsHiddifyReadsIt(t *testing.T) {
	p, in, u, env := sshFixture()
	link := Link(p, in, u, env, "Dunvex · SSH")
	pu, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := pu.User.Password()
	if pu.Scheme != "ssh" || pu.User.Username() != u.Name || pass != u.Password || pu.Hostname() != env.Host || pu.Port() != "2222" || pu.Fragment != "Dunvex · SSH" {
		t.Fatalf("link %s read as %s %q %q %s %s %q", link, pu.Scheme, pu.User.Username(), pass, pu.Hostname(), pu.Port(), pu.Fragment)
	}
	hk := pu.Query().Get("hk")
	if hk != SSHHostKey(in) || hk != in.Str("hostKeyPub") {
		t.Fatalf("hk %q, want %q", hk, SSHHostKey(in))
	}
	if strings.Contains(link, "+") {
		t.Error("a + in the link: some apps read it as a space")
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(hk)); err != nil {
		t.Fatalf("host key in the link: %v", err)
	}
	// no private key travels in the link
	if strings.Contains(link, in.Str("hostKey")) || strings.Contains(link, url.QueryEscape(in.Str("hostKey"))) {
		t.Fatal("the server's private host key is in the link")
	}
	// and the key in the link is the one the server signs with
	signer, err := sshproxy.HostKey(in.Str("hostKey"))
	if err != nil {
		t.Fatal(err)
	}
	pk, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(hk))
	if string(pk.Marshal()) != string(signer.PublicKey().Marshal()) {
		t.Fatal("the link pins another key than the server's")
	}
	// Xray has no SSH: the Xray JSON subscription leaves it out
	if _, err := XrayOutbound(link, "proxy"); err == nil {
		t.Error("Xray was given an SSH outbound")
	}
}

func TestSSHProfiles(t *testing.T) {
	p, in, u, env := sshFixture()
	c := Clash(p, in, u, env, "ssh")
	if c["type"] != "ssh" || c["server"] != env.Host || c["port"] != 2222 || c["username"] != u.Name || c["password"] != u.Password {
		t.Fatalf("clash %v", c)
	}
	if hk, _ := c["host-key"].([]string); len(hk) != 1 || hk[0] != SSHHostKey(in) {
		t.Errorf("clash host-key %v", c["host-key"])
	}
	if _, ok := c["udp"]; ok {
		t.Error("mihomo's SSH has no UDP")
	}
	kind, items := SingboxOutbound(p, in, u, env, "ssh-out")
	if kind != "outbound" || len(items) != 1 {
		t.Fatalf("sing-box %s %v", kind, items)
	}
	o := items[0]
	if o["type"] != "ssh" || o["server"] != env.Host || o["server_port"] != 2222 || o["user"] != u.Name || o["password"] != u.Password || o["tag"] != "ssh-out" {
		t.Fatalf("sing-box %v", o)
	}
	if hk, _ := o["host_key"].([]string); len(hk) != 1 || hk[0] != SSHHostKey(in) {
		t.Errorf("sing-box host_key %v", o["host_key"])
	}
	// through a relay: the relay's address, the same key
	relayed := *in
	relayed.Params = map[string]any{}
	for k, v := range in.Params {
		relayed.Params[k] = v
	}
	relayed.Params["address"] = "relay.example"
	if _, items := SingboxOutbound(p, &relayed, u, env, "x"); items[0]["server"] != "relay.example" {
		t.Errorf("relay server %v", items[0]["server"])
	}
}

func TestSSHSlipNetLink(t *testing.T) {
	p, in, u, env := sshFixture()
	u.Name = "sa|ra"
	l := SSHSlipNetLink(in, u, env, "My|SSH")
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(l, "slipnet://"))
	if !strings.HasPrefix(l, "slipnet://") || err != nil {
		t.Fatalf("link %s: %v", l, err)
	}
	f := strings.Split(string(raw), "|")
	// the same 84 fields as the DNS tunnels' SlipNet links
	dp := Get("dnstt")
	din := &store.Inbound{ID: 8, Preset: dp.ID, Port: 53, Params: Defaults(dp, map[string]any{"xdnsDomain": "t.example.com"}, "")}
	draw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(Link(dp, din, &store.User{Name: "a", Password: "b"}, env, "x"), "slipnet://"))
	if want := len(strings.Split(string(draw), "|")); len(f) != want || want != 84 {
		t.Fatalf("%d fields, the DNS tunnel link has %d", len(f), want)
	}
	want := map[int]string{0: "30", 1: "ssh", 2: "MySSH", 3: env.Host, 4: "", 14: "1", 15: "sara", 16: u.Password, 17: strconv.Itoa(in.Port), 19: env.Host, 23: "password"}
	for i, v := range want {
		if f[i] != v {
			t.Errorf("field %d = %q, want %q", i, f[i], v)
		}
	}
	_ = p
}

func TestSSHDetails(t *testing.T) {
	p, in, u, env := sshFixture()
	d := Details(p, in, u, env)
	for _, s := range []string{"Server: 203.0.113.9", "Port: 2222", "Username: sara", "Password: " + u.Password, "Host key: SHA256:"} {
		if !strings.Contains(d, s) {
			t.Errorf("details lack %q:\n%s", s, d)
		}
	}
	if strings.Contains(d, in.Str("hostKey")) {
		t.Error("the private host key is in the details")
	}
	if Details(Get("vless-reality-vision"), in, u, env) != "" {
		t.Error("details for a protocol with a link of its own")
	}
	if got := SSHSocks(in, 41234); got["tag"] != "in-7" || got["listen"] != "127.0.0.1" || got["port"] != 41234 || got["protocol"] != "socks" {
		t.Errorf("SOCKS inbound %v", got)
	}
}
