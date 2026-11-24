package protocol

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/cryptobyte"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// goECHKeys reads server keys the way Xray (ConvertToGoECHKeys) and sing-box
// (UnmarshalECHKeys) do.
func goECHKeys(t *testing.T, b64 string) []tls.EncryptedClientHelloKey {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	var keys []tls.EncryptedClientHelloKey
	s := cryptobyte.String(raw)
	for !s.Empty() {
		var k tls.EncryptedClientHelloKey
		if !s.ReadUint16LengthPrefixed((*cryptobyte.String)(&k.PrivateKey)) || !s.ReadUint16LengthPrefixed((*cryptobyte.String)(&k.Config)) {
			t.Fatal("broken ECH keys")
		}
		keys = append(keys, k)
	}
	return keys
}

// The keys NewECHKeys makes work in a real TLS handshake: the server takes
// the hidden name, and the name on the wire is the public one.
func TestECHKeysHandshake(t *testing.T) {
	e, err := NewECHKeys("www.speedtest.net")
	if err != nil {
		t.Fatal(err)
	}
	if name, err := ECHPublicName(e.Config); err != nil || name != "www.speedtest.net" {
		t.Fatalf("public name %q %v", name, err)
	}
	certFile, keyFile, err := util.SelfSignedCert(filepath.Join(t.TempDir(), "c"), "vpn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	var outer string
	srv := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13, EncryptedClientHelloKeys: goECHKeys(t, e.Keys),
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) { outer = h.ServerName; return nil, nil }}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srv)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.(*tls.Conn).Handshake()
			io.WriteString(c, "ok")
			c.Close()
		}
	}()
	pool := x509.NewCertPool()
	pool.AddCert(cert.Leaf)
	list, _ := base64.StdEncoding.DecodeString(e.Config)
	c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{ServerName: "vpn.example.com", RootCAs: pool, EncryptedClientHelloConfigList: list})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if st := c.ConnectionState(); !st.ECHAccepted || st.ServerName != "vpn.example.com" {
		t.Fatalf("ECH not taken: %+v", st)
	}
	if outer != "vpn.example.com" { // the server sees the inner name once it decrypted it
		t.Logf("server saw %q", outer)
	}
	if _, err := ECHPublicName("bm90IGVjaA=="); err == nil {
		t.Error("junk read as an ECH config")
	}
}

func tricksEnv(t *testing.T) Env {
	ech, err := NewECHKeys("www.speedtest.net")
	if err != nil {
		t.Fatal(err)
	}
	return Env{Host: "203.0.113.10", Domain: "vpn.example.com", CertFile: "c", KeyFile: "k", SelfSigned: true, CertSHA256: strings.Repeat("ab", 32),
		Fragment: &Fragment{FragmentPackets, FragmentLength, FragmentDelay},
		Mux:      &Mux{Protocol: "smux", Streams: 6, Padding: true, Brutal: &Brutal{Up: 30, Down: 90}, Singbox: true},
		Noise:    &Noise{Count: 4, Size: "40-80", Delay: "5-9"},
		ECH:      ech}
}

func trickInbound(p *Preset, env Env) *store.Inbound {
	in := &store.Inbound{ID: 1, Preset: p.ID, Port: 2053, Enabled: true, Params: Defaults(p, nil, env.Domain)}
	if p.transport == "xdns" {
		in.Params["xdnsDomain"] = "t.example.com"
	}
	return in
}

// Every option travels in the links of the configs whose apps read it, and an
// Xray app reads it back from the link.
func TestTricksInLinks(t *testing.T) {
	u, env := clientUser(), tricksEnv(t)
	ech := 0
	for _, p := range All() {
		in := trickInbound(p, env)
		link := Link(p, in, u, env, "x")
		q := url.Values{}
		if l, err := url.Parse(link); err == nil {
			q = l.Query()
		}
		vm := map[string]any{}
		if strings.HasPrefix(link, "vmess://") {
			b, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
			json.Unmarshal(b, &vm)
		}
		hasECH := q.Get("ech") == env.ECH.Config || vm["ech"] == env.ECH.Config
		if want := ECHable(p) && p.proto != "anytls"; hasECH != want { // no app reads ECH from an AnyTLS link
			t.Errorf("%s: ech in link %v, want %v", p.ID, hasECH, want)
		}
		if (Fragmentable(p) && p.proto != "vmess" || p.proto == "anytls") && q.Get("fragment") != "100-200,10-20,tlshello" {
			t.Errorf("%s: Hiddify's fragment %q", p.ID, q.Get("fragment"))
		}
		if mux := q.Get("muxtype") == "smux" && q.Get("muxsmax") == "6" && q.Get("muxup") == "30" && q.Get("muxdown") == "90"; mux != (p.ID == "vless-reality-universal") {
			t.Errorf("%s: sing-box multiplex in link %v", p.ID, mux)
		}
		o, err := XrayOutbound(link, "proxy")
		if err != nil {
			continue // not an Xray link (TUIC, AnyTLS, ...)
		}
		if hasECH {
			ech++
			if at(o, "streamSettings", "tlsSettings", "echConfigList") != env.ECH.Config {
				t.Errorf("%s: an Xray app would not read the ECH: %v", p.ID, o["streamSettings"])
			}
		}
		if p.proto == "wireguard" {
			if q.Get("ifp") != "4" || q.Get("ifps") != "40-80" || q.Get("ifpd") != "5-9" {
				t.Errorf("wireguard: Hiddify's noise %v", q)
			}
			m := at(o, "streamSettings", "finalmask", "udp", 0)
			if at(m, "type") != "noise" || len(at(m, "settings", "noise").([]any)) != 4 || at(m, "settings", "noise", 0, "rand") != "40-80" {
				t.Errorf("wireguard: noise %v", m)
			}
		}
	}
	if ech < 7 {
		t.Fatalf("only %d Xray links carry the ECH", ech)
	}
	// off: none of it
	p := Get("vless-ws-tls")
	in := trickInbound(p, env)
	plain := Env{Host: env.Host, Domain: env.Domain, CertFile: "c", KeyFile: "k"}
	if l := Link(p, in, u, plain, "x"); strings.Contains(l, "ech=") || strings.Contains(l, "fragment=") {
		t.Fatalf("tricks while off: %s", l)
	}
	if l := Link(Get("wireguard"), trickInbound(Get("wireguard"), plain), u, plain, "w"); strings.Contains(l, "fm=") || strings.Contains(l, "ifp=") {
		t.Fatalf("noise while off: %s", l)
	}
}

// An XHTTP config whose downloads take another way carries Xray's
// downloadSettings in its link's "extra", and an Xray app reads it.
func TestXHTTPDownloadRoute(t *testing.T) {
	u, env := clientUser(), tricksEnv(t)
	p := Get("vless-xhttp-tls")
	in := trickInbound(p, env)
	cdnIn := *in
	cdnIn.Params = map[string]any{}
	for k, v := range in.Params {
		cdnIn.Params[k] = v
	}
	cdnIn.Params["address"], cdnIn.Params["sni"], cdnIn.Params["host"] = "104.16.1.1", "cdn.example.com", "cdn.example.com"
	cdnEnv := Env{Host: "cdn.example.com", Domain: "cdn.example.com", Fragment: env.Fragment}
	cdnEnv.Download = &Route{In: in, Env: env} // uploads through the CDN, downloads straight
	link := Link(p, &cdnIn, u, cdnEnv, "split")
	l, _ := url.Parse(link)
	if l.Hostname() != "104.16.1.1" || l.Query().Get("sni") != "cdn.example.com" {
		t.Fatalf("uploads: %s", link)
	}
	o, err := XrayOutbound(link, "proxy")
	if err != nil {
		t.Fatal(err)
	}
	d := at(o, "streamSettings", "xhttpSettings", "extra", "downloadSettings")
	if at(d, "address") != "203.0.113.10" || at(d, "port") != 2053.0 || at(d, "network") != "xhttp" || at(d, "security") != "tls" ||
		at(d, "tlsSettings", "serverName") != "vpn.example.com" || at(d, "tlsSettings", "pinnedPeerCertSha256") != env.CertSHA256 ||
		at(d, "tlsSettings", "echConfigList") != env.ECH.Config || at(d, "xhttpSettings", "path") != in.Str("path") ||
		at(d, "finalmask", "tcp", 0, "type") != "fragment" {
		t.Fatalf("downloadSettings: %v", d)
	}
	// stream-one sends both ways in one request: no split
	in.Params["mode"] = "stream-one"
	cdnIn.Params["mode"] = "stream-one"
	if strings.Contains(Link(p, &cdnIn, u, cdnEnv, "x"), "extra=") {
		t.Fatal("a split with stream-one")
	}
}

// sing-box and Clash profiles carry what their apps read.
func TestTricksInProfiles(t *testing.T) {
	u, env := clientUser(), tricksEnv(t)
	sb := func(id string) obj {
		p := Get(id)
		_, items := SingboxOutbound(p, trickInbound(p, env), u, env, "x")
		if len(items) == 0 {
			t.Fatalf("%s: no sing-box outbound", id)
		}
		return items[0]
	}
	for _, id := range []string{"vless-ws-tls", "trojan-tls", "vless-grpc-tls", "anytls"} {
		o := sb(id)
		if at(o, "tls", "fragment") != true || at(o, "tls", "record_fragment") != true {
			t.Errorf("%s: sing-box fragment %v", id, o["tls"])
		}
		if c := at(o, "tls", "ech", "config"); c == nil || c.([]string)[1] != env.ECH.Config || at(o, "tls", "ech", "enabled") != true {
			t.Errorf("%s: sing-box ECH %v", id, o["tls"])
		}
	}
	for _, id := range []string{"tuic", "hysteria2-sb", "vless-reality-universal"} {
		if o := sb(id); at(o, "tls", "fragment") != nil || at(o, "tls", "ech") != nil {
			t.Errorf("%s: fragment or ECH where sing-box ignores or breaks on it: %v", id, o["tls"])
		}
	}
	for _, id := range []string{"vless-reality-universal", "shadowtls"} {
		m := sb(id)["multiplex"]
		if at(m, "protocol") != "smux" || at(m, "max_streams") != 6 || at(m, "padding") != true || at(m, "brutal", "up_mbps") != 30 || at(m, "brutal", "down_mbps") != 90 {
			t.Errorf("%s: sing-box multiplex %v", id, m)
		}
	}
	if m := sb("vless-ws-tls")["multiplex"]; m != nil {
		t.Errorf("sing-box multiplex towards an Xray server: %v", m)
	}
	cl := func(id string) obj {
		p := Get(id)
		return Clash(p, trickInbound(p, env), u, env, "x")
	}
	if e := cl("trojan-ws-tls")["ech-opts"]; at(e, "enable") != true || at(e, "config") != env.ECH.Config {
		t.Errorf("Clash ECH %v", e)
	}
	for _, id := range []string{"vless-reality-universal", "shadowtls"} {
		if m := cl(id)["smux"]; at(m, "protocol") != "smux" || at(m, "max-streams") != 6 || at(m, "brutal-opts", "up") != 30 {
			t.Errorf("%s: Clash smux %v", id, m)
		}
	}
	if w := cl("wireguard")["amnezia-wg-option"]; at(w, "jc") != 4 || at(w, "jmin") != 40 || at(w, "jmax") != 80 || at(w, "s1") != nil || at(w, "h1") != nil {
		t.Errorf("Clash junk packets %v", w)
	}
	// another server's configs: no sing-box multiplex (that server's inbounds have none)
	node := env
	m := *env.Mux
	m.Singbox = false
	node.Mux = &m
	p := Get("vless-reality-universal")
	if _, items := SingboxOutbound(p, trickInbound(p, node), u, node, "x"); items[0]["multiplex"] != nil {
		t.Error("sing-box multiplex for another server")
	}
}

// The server side: this server's ECH keys on its TLS inbounds over TCP, and
// sing-box's multiplex (with Brutal) on its sing-box VLESS and ShadowTLS.
func TestTricksOnServer(t *testing.T) {
	u, env := clientUser(), tricksEnv(t)
	for _, p := range All() {
		in := trickInbound(p, env)
		switch p.Engine {
		case "xray":
			ib, err := XrayInbound(p, in, []*store.User{u}, env)
			if err != nil {
				continue
			}
			keys := at(ib, "streamSettings", "tlsSettings", "echServerKeys")
			if (keys == env.ECH.Keys) != ECHable(p) {
				t.Errorf("%s: echServerKeys %v", p.ID, keys)
			}
		case "singbox":
			ibs, err := SingboxInbounds(p, in, []*store.User{u}, env)
			if err != nil {
				t.Fatal(p.ID, err)
			}
			last := ibs[len(ibs)-1] // ShadowTLS: the Shadowsocks behind it
			m := last["multiplex"]
			if (m != nil) != SingMuxable(p) {
				t.Errorf("%s: multiplex %v", p.ID, m)
			}
			if m != nil && (at(m, "padding") != true || at(m, "brutal", "up_mbps") != 90 || at(m, "brutal", "down_mbps") != 30) {
				t.Errorf("%s: server multiplex %v", p.ID, m)
			}
			if k := at(ibs[0], "tls", "ech", "key"); (k != nil) != (p.proto == "anytls") || (k != nil && k.([]string)[1] != env.ECH.Keys) {
				t.Errorf("%s: sing-box ECH key %v", p.ID, k)
			}
		}
	}
	// a CDN's ECH has no keys: no server takes it
	cdnEnv := env
	cdnEnv.ECH = &ECH{Config: env.ECH.Config}
	p := Get("vless-ws-tls")
	ib, _ := XrayInbound(p, trickInbound(p, cdnEnv), []*store.User{u}, cdnEnv)
	if at(ib, "streamSettings", "tlsSettings", "echServerKeys") != nil {
		t.Error("server keys from a CDN's ECH")
	}
}

func TestMuxInXrayJSON(t *testing.T) {
	u, env := clientUser(), tricksEnv(t)
	want := map[string]bool{"vless-ws-tls": true, "vless-httpupgrade-tls": true, "trojan-ws-tls": true, "vmess-ws-tls": true, "vmess-ws": true,
		"trojan-tls": true, "trojan-reality": true, "shadowsocks-2022": true, "shadowsocks": true, "vmess-kcp": true}
	for _, p := range All() {
		o, err := XrayOutbound(Link(p, trickInbound(p, env), u, env, "x"), "proxy")
		if err != nil {
			continue
		}
		XrayExtras(o, p, env)
		if has := o["mux"] != nil; has != want[p.ID] {
			t.Errorf("%s: Mux.Cool %v", p.ID, has)
		}
		if o["mux"] != nil && (at(o, "mux", "concurrency") != 6 || at(o, "mux", "xudpProxyUDP443") != "reject") {
			t.Errorf("%s: %v", p.ID, o["mux"])
		}
	}
}

func TestTrickChecks(t *testing.T) {
	if (Mux{Protocol: "h2mux", Streams: 8}).Check() != nil || (Mux{Protocol: "quic", Streams: 8}).Check() == nil ||
		(Mux{Protocol: "smux", Streams: 0}).Check() == nil || (Mux{Protocol: "smux", Streams: 4, Brutal: &Brutal{0, 10}}).Check() == nil {
		t.Error("mux checks")
	}
	if (Noise{5, "50-100", "10-20"}).Check() != nil || (Noise{0, "50", "1"}).Check() == nil || (Noise{3, "0-10", "1"}).Check() == nil ||
		(Noise{3, "10", "2000"}).Check() == nil {
		t.Error("noise checks")
	}
	if !Splittable(Get("vless-xhttp-tls"), &store.Inbound{Params: map[string]any{"mode": "auto"}}) || Splittable(Get("vless-reality-xhttp"), &store.Inbound{}) {
		t.Error("splittable")
	}
}
