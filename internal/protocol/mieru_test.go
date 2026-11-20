package protocol

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/pedramrj/dunvex/internal/store"
)

func TestMieru(t *testing.T) {
	sara := &store.User{Name: "sara", Password: "p@ss/word+1"}
	for _, c := range []struct {
		id, transport string
	}{{"mieru", "TCP"}, {"mieru-udp", "UDP"}} {
		p := Get(c.id)
		if !IsMieru(p) {
			t.Fatalf("%s: preset %+v", c.id, p)
		}
		in := &store.Inbound{ID: 3, Preset: p.ID, Port: 31234, Enabled: true, Params: Defaults(p, nil, "")}
		if in.Str("mtu") != "1400" || in.Str("multiplexing") != "MULTIPLEXING_LOW" || in.Str("tpSeed") == "" || in.Str("tpSeed") == "0" {
			t.Fatalf("%s defaults: %v", c.id, in.Params)
		}
		seed := in.Str("tpSeed")
		in.Params["mtu"] = float64(1300) // as it comes back from the database
		Defaults(p, in.Params, "")
		if in.Str("tpSeed") != seed || in.Str("mtu") != "1300" {
			t.Errorf("%s: saving changed the seed or the MTU: %v", c.id, in.Params)
		}

		// the server: one port, the users with a password, the traffic pattern seed
		raw, err := MieruServer(p, in, []*store.User{sara, {Name: "nopass"}}, "203.0.113.5", "")
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			PortBindings []struct {
				Port     int    `json:"port"`
				Protocol string `json:"protocol"`
			} `json:"portBindings"`
			Users []struct{ Name, Password string } `json:"users"`
			MTU   int                               `json:"mtu"`
			TP    struct{ Seed int }                `json:"trafficPattern"`
			IP    string                            `json:"listenIPAddress"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		if len(cfg.PortBindings) != 1 || cfg.PortBindings[0].Port != 31234 || cfg.PortBindings[0].Protocol != c.transport ||
			len(cfg.Users) != 1 || cfg.Users[0].Name != "sara" || cfg.Users[0].Password != sara.Password ||
			cfg.MTU != 1300 || cfg.TP.Seed == 0 || cfg.IP != "203.0.113.5" {
			t.Fatalf("%s server config:\n%s", c.id, raw)
		}

		// the link the apps import
		link := Link(p, in, sara, Env{Host: "203.0.113.10"}, "Dunvex Mieru")
		u, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		pw, _ := u.User.Password()
		q := u.Query()
		if u.Scheme != "mierus" || u.User.Username() != "sara" || pw != sara.Password || u.Host != "203.0.113.10" || u.Fragment != "Dunvex Mieru" ||
			q.Get("profile") != "Dunvex Mieru" || q.Get("port") != "31234" || q.Get("protocol") != c.transport ||
			q.Get("mtu") != "1300" || q.Get("multiplexing") != "MULTIPLEXING_LOW" {
			t.Errorf("%s link: %s", c.id, link)
		}
		if l := Link(p, in, sara, Env{Host: "2001:db8::1"}, "x"); mustHost(t, l) != "[2001:db8::1]" {
			t.Errorf("ipv6 link: %s", l)
		}

		// Clash.Meta (mihomo)
		cl := Clash(p, in, sara, Env{Host: "203.0.113.10"}, "Dunvex Mieru")
		if cl["type"] != "mieru" || cl["transport"] != c.transport || cl["username"] != "sara" || cl["password"] != sara.Password ||
			cl["port"] != 31234 || cl["server"] != "203.0.113.10" || cl["multiplexing"] != "MULTIPLEXING_LOW" {
			t.Errorf("%s clash: %v", c.id, cl)
		}
		// not in sing-box profiles: official sing-box has no Mieru
		if _, items := SingboxOutbound(p, in, sara, Env{Host: "203.0.113.10"}, "m"); items != nil {
			t.Errorf("%s in sing-box: %v", c.id, items)
		}
	}
}

func mustHost(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}
