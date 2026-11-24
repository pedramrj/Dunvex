package protocol

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/store"
)

func TestFragmentCheck(t *testing.T) {
	good := []Fragment{
		{FragmentPackets, FragmentLength, FragmentDelay},
		{"1-3", "10-100", "0"},
		{"TLSHello", "200", "5-5"},
	}
	for _, f := range good {
		if err := f.Check(); err != nil {
			t.Errorf("%+v: %v", f, err)
		}
	}
	bad := []Fragment{
		{"hello", "100-200", "10-20"},
		{"0-2", "100-200", "10-20"},
		{"tlshello", "0-10", "10-20"},
		{"tlshello", "200-100", "10-20"},
		{"tlshello", "100-200", "5000"},
		{"tlshello", "abc", "10"},
		{"tlshello", "100-200", "-5"},
	}
	for _, f := range bad {
		if err := f.Check(); err == nil {
			t.Errorf("%+v accepted", f)
		}
	}
}

// The fragment travels in the links of TLS and Reality configs over TCP, and
// an Xray client reads it back; UDP, QUIC and plain configs stay as they were.
func TestFragmentInLinks(t *testing.T) {
	u := clientUser()
	frag := &Fragment{FragmentPackets, FragmentLength, FragmentDelay}
	env := Env{Host: "203.0.113.10", Domain: "vpn.example.com", CertFile: "c", KeyFile: "k", Fragment: frag}
	with := 0
	for _, p := range All() {
		in := &store.Inbound{ID: 1, Preset: p.ID, Port: 8443, Enabled: true, Params: Defaults(p, nil, env.Domain)}
		if p.transport == "xdns" {
			in.Params["xdnsDomain"] = "t.example.com"
		}
		link := Link(p, in, u, env, "f")
		has := false
		if l, err := url.Parse(link); err == nil {
			has = strings.Contains(l.Query().Get("fm"), `"fragment"`)
		}
		want := Fragmentable(p) && p.proto != "vmess" // vmess links have no place for it
		if has != want {
			t.Errorf("%s: fragment in link = %v, want %v\n%s", p.ID, has, want, link)
		}
		if !has {
			continue
		}
		with++
		o, err := XrayOutbound(link, "proxy")
		if err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		m := at(o, "streamSettings", "finalmask", "tcp", 0)
		if at(m, "type") != "fragment" || at(m, "settings", "packets") != "tlshello" || at(m, "settings", "length") != "100-200" || at(m, "settings", "delay") != "10-20" {
			t.Errorf("%s: mask %v", p.ID, m)
		}
	}
	if with < 10 {
		t.Fatalf("only %d links carry the fragment", with)
	}
	// off: no fm at all on those links
	env.Fragment = nil
	p := Get("vless-reality-vision")
	in := &store.Inbound{ID: 1, Preset: p.ID, Port: 443, Enabled: true, Params: Defaults(p, nil, "")}
	if strings.Contains(Link(p, in, u, env, "f"), "fm=") {
		t.Fatal("fm without a fragment")
	}
}

func TestWithFragment(t *testing.T) {
	frag := &Fragment{"1-3", "50-90", "5-9"}
	u := clientUser()
	env := Env{Host: "203.0.113.10", Domain: "vpn.example.com", CertFile: "c", KeyFile: "k"}
	p := Get("vmess-ws-tls")
	in := &store.Inbound{ID: 1, Preset: p.ID, Port: 2053, Enabled: true, Params: Defaults(p, nil, env.Domain)}
	o, err := XrayOutbound(Link(p, in, u, env, "v"), "proxy")
	if err != nil {
		t.Fatal(err)
	}
	WithFragment(o, frag)
	WithFragment(o, frag) // once is enough
	tcp := at(o, "streamSettings", "finalmask", "tcp").([]any)
	if len(tcp) != 1 || at(tcp[0], "settings", "packets") != "1-3" {
		t.Fatalf("vmess: %v", tcp)
	}
	// Hysteria2 (QUIC) and plain VMess over WebSocket are left alone
	for _, id := range []string{"hysteria2", "vmess-ws"} {
		p := Get(id)
		in := &store.Inbound{ID: 1, Preset: id, Port: 4444, Enabled: true, Params: Defaults(p, nil, env.Domain)}
		o, err := XrayOutbound(Link(p, in, u, Env{Host: "203.0.113.10", Domain: "vpn.example.com", CertFile: "c", KeyFile: "k", CertSHA256: strings.Repeat("ab", 32), SelfSigned: true}, "x"), "proxy")
		if err != nil {
			t.Fatal(id, err)
		}
		WithFragment(o, frag)
		if js, _ := json.Marshal(o); strings.Contains(string(js), `"fragment"`) {
			t.Errorf("%s got a fragment: %s", id, js)
		}
	}
}

func TestXrayClientConfig(t *testing.T) {
	proxy := map[string]any{"tag": "proxy", "protocol": "vless"}
	c := XrayClientConfig("Dunvex · Reality", proxy, []string{"domain:ir", "full:example.ir"}, []string{"2.144.0.0/14"})
	js, _ := json.Marshal(c)
	s := string(js)
	for _, want := range []string{`"remarks":"Dunvex · Reality"`, `"port":10808`, `"port":10809`, `"routing"`, `"outboundTag":"direct"`,
		`"domain:ir"`, `"2.144.0.0/14"`, `"address":"localhost"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	if strings.Contains(s, "geoip:") || strings.Contains(s, "geosite:") || strings.Contains(s, "blackhole") {
		t.Errorf("geo files or blackhole named: %s", s)
	}
	if outs := c["outbounds"].([]obj); outs[0]["tag"] != "proxy" || outs[1]["tag"] != "direct" {
		t.Errorf("outbounds: %v", outs)
	}
	// without device routing: private networks only
	c = XrayClientConfig("x", proxy, nil, nil)
	if rules := c["routing"].(obj)["rules"].([]obj); len(rules) != 1 {
		t.Errorf("rules: %v", rules)
	}
}
