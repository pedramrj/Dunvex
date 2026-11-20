package protocol

import (
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/store"
)

func TestXDNS(t *testing.T) {
	p := Get("vless-xdns")
	u := &store.User{Name: "sara", UUID: "11111111-1111-1111-1111-111111111111"}
	env := Env{Host: "203.0.113.10"}

	// no tunnel domain: the inbound explains what is missing
	in := &store.Inbound{ID: 4, Preset: p.ID, Port: 53, Enabled: true, Params: Defaults(p, nil, "")}
	if _, err := XrayInbound(p, in, []*store.User{u}, env); err == nil || !strings.Contains(err.Error(), "tunnel domain") {
		t.Fatalf("missing domain: %v", err)
	}

	in.Params["xdnsDomain"] = "T.Example.com."
	ib, err := XrayInbound(p, in, []*store.User{u}, env)
	if err != nil {
		t.Fatal(err)
	}
	ss := ib["streamSettings"].(obj)
	if ss["network"] != "kcp" || ss["kcpSettings"].(obj)["mtu"] != 900 {
		t.Fatalf("server stream: %v", ss)
	}
	fm, _ := json.Marshal(ss["finalmask"])
	if !strings.Contains(string(fm), `"type":"xdns"`) || !strings.Contains(string(fm), `"t.example.com"`) {
		t.Fatalf("server mask: %s", fm)
	}

	// through a public resolver: the link dials the resolver, the mask carries the domain
	link := Link(p, in, u, env, "XDNS")
	pu, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if pu.Host != "8.8.8.8:53" || pu.Query().Get("type") != "kcp" || pu.Query().Get("security") != "none" {
		t.Fatalf("resolver link: %s", link)
	}
	var mask map[string]any
	if err := json.Unmarshal([]byte(pu.Query().Get("fm")), &mask); err != nil {
		t.Fatalf("fm is not JSON: %v", err)
	}
	s := pu.Query().Get("fm")
	if !strings.Contains(s, `"names":["t.example.com"]`) || !strings.Contains(s, `udp://8.8.8.8:53`) {
		t.Fatalf("client mask: %s", s)
	}

	// direct: straight to this server
	in.Params["resolver"] = "direct"
	in.Port = 5353
	pu, _ = url.Parse(Link(p, in, u, env, "XDNS"))
	if pu.Host != "203.0.113.10:5353" || !strings.Contains(pu.Query().Get("fm"), "udp://203.0.113.10:5353") {
		t.Fatalf("direct link: %s", pu)
	}
	// Clash and sing-box have no XDNS: they leave it out
	if Clash(p, in, u, env, "x") != nil {
		t.Error("clash should skip XDNS")
	}
	if _, items := SingboxOutbound(p, in, u, env, "x"); len(items) != 0 {
		t.Error("sing-box should skip XDNS")
	}
}

func TestMTProto(t *testing.T) {
	p := Get("mtproto")
	in := &store.Inbound{ID: 9, Preset: p.ID, Port: 443, Enabled: true, Params: Defaults(p, nil, "")}
	key := in.Str("mtKey")
	if len(key) != 32 || in.Str("sni") != DefaultFakeTLS {
		t.Fatalf("defaults: %v", in.Params)
	}
	sec := MTProtoSecret(key, "Storage.GoogleAPIs.com")
	if !strings.HasPrefix(sec, "ee"+key) || !strings.HasSuffix(sec, hex.EncodeToString([]byte("storage.googleapis.com"))) {
		t.Fatalf("secret: %s", sec)
	}
	link := Link(p, in, &store.User{Name: "sara"}, Env{Host: "203.0.113.10"}, "Telegram")
	pu, err := url.Parse(link)
	if err != nil || pu.Scheme != "tg" || pu.Host != "proxy" {
		t.Fatalf("link: %s", link)
	}
	q := pu.Query()
	if q.Get("server") != "203.0.113.10" || q.Get("port") != "443" || q.Get("secret") != MTProtoSecret(key, DefaultFakeTLS) {
		t.Fatalf("link query: %v", q)
	}
	// a second Telegram proxy may share the panel's port only with another fake domain
	list := []*store.Inbound{in}
	if got := PortConflict(list, 443, 443, 0, p, DefaultFakeTLS); !strings.Contains(got, "already uses the SNI") {
		t.Errorf("same fake domain on the shared port: %q", got)
	}
	if got := PortConflict(list, 443, 443, 0, Get("vless-reality-vision"), "www.speedtest.net"); got != "" {
		t.Errorf("Reality next to the Telegram proxy on 443: %q", got)
	}
}
