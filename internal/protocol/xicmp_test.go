package protocol

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/store"
)

func TestXICMP(t *testing.T) {
	p := Get("vless-xicmp")
	u := &store.User{Name: "sara", UUID: "11111111-1111-1111-1111-111111111111"}
	in := &store.Inbound{ID: 6, Preset: p.ID, Port: 31000, Enabled: true, Params: Defaults(p, nil, "")}
	if !in.Bool("icmpDgram") {
		t.Fatal("unprivileged pings should be the default (phones)")
	}
	ib, err := XrayInbound(p, in, []*store.User{u}, Env{Host: "203.0.113.10"})
	if err != nil {
		t.Fatal(err)
	}
	ss := ib["streamSettings"].(obj)
	fm, _ := json.Marshal(ss["finalmask"])
	if ss["network"] != "kcp" || string(fm) != `{"udp":[{"settings":{},"type":"xicmp"}]}` {
		t.Fatalf("server stream: %v %s", ss["network"], fm)
	}

	// the link dials the server's address with the mask in "fm"
	link, err := url.Parse(Link(p, in, u, Env{Host: "203.0.113.10"}, "ICMP"))
	if err != nil {
		t.Fatal(err)
	}
	q := link.Query()
	if link.Scheme != "vless" || link.Host != "203.0.113.10:31000" || q.Get("type") != "kcp" || q.Get("security") != "none" {
		t.Fatalf("link: %s", link)
	}
	if got := q.Get("fm"); got != `{"udp":[{"settings":{"dgram":true},"type":"xicmp"}]}` {
		t.Fatalf("mask: %s", got)
	}

	// a server known by name: pings go to its addresses, pinned in the mask
	old := lookupIPs
	lookupIPs = func(h string) []string {
		if h == "vpn.example.com" {
			return []string{"203.0.113.10"}
		}
		return nil
	}
	defer func() { lookupIPs = old }()
	in.Params["icmpDgram"] = false
	link, _ = url.Parse(Link(p, in, u, Env{Host: "vpn.example.com"}, "ICMP"))
	if got := link.Query().Get("fm"); got != `{"udp":[{"settings":{"ips":["203.0.113.10"]},"type":"xicmp"}]}` {
		t.Fatalf("mask for a name: %s", got)
	}

	// Xray-only: not in Clash or sing-box profiles; never through a relay
	if c := Clash(p, in, u, Env{Host: "203.0.113.10"}, "x"); c != nil {
		t.Errorf("Clash entry: %v", c)
	}
	if kind, items := SingboxOutbound(p, in, u, Env{Host: "203.0.113.10"}, "x"); kind != "" || items != nil {
		t.Errorf("sing-box outbound: %s %v", kind, items)
	}
}

// xICMP binds no port: it never clashes with what does.
func TestXICMPHoldsNoPort(t *testing.T) {
	list := []*store.Inbound{{ID: 1, Preset: "hysteria2", Port: 31000, Enabled: true, Params: map[string]any{}}}
	if msg := PortConflict(list, 443, 31000, 0, Get("vless-xicmp"), ""); msg != "" {
		t.Errorf("xICMP next to Hysteria2's port: %s", msg)
	}
	list = append(list, &store.Inbound{ID: 2, Preset: "vless-xicmp", Port: 40000, Enabled: true, Params: map[string]any{}})
	if msg := PortConflict(list, 443, 40000, 0, Get("tuic"), "x.com"); msg != "" {
		t.Errorf("TUIC on the number xICMP shows in links: %s", msg)
	}
	if !strings.Contains(Get("vless-xicmp").Desc, "One per server") {
		t.Error("the description should say it is one per server")
	}
}
