package domains

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/cdn"
	"github.com/pedramrj/dunvex/internal/store"
)

func openStore(t *testing.T) *store.Store {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func setCDN(st *store.Store, c cdn.Settings) {
	b, _ := json.Marshal(c)
	st.SetSetting(cdn.Setting, string(b))
}

func TestMigrateFromOlderSettings(t *testing.T) {
	st := openStore(t)
	st.SetSetting("domain", "VPN.Example.com")
	st.SetSetting("cert_mode", "acme")
	setCDN(st, cdn.Settings{Enabled: true, Domain: "cdn.example.com", Direct: true, Addresses: []cdn.Address{{Address: "104.16.1.1", Name: "MCI"}}})

	msg, err := Migrate(st)
	if err != nil || !strings.Contains(msg, "2 domain") {
		t.Fatalf("migrate: %q %v", msg, err)
	}
	l := Load(st)
	if len(l.Domains) != 2 || l.MainName() != "vpn.example.com" || !l.Domains[0].Cert || l.Domains[0].Role != Direct {
		t.Fatalf("list: %+v", l.Domains)
	}
	c := l.Domains[1]
	if c.Role != CDN || !c.Configs || c.Name != "cdn.example.com" || len(c.Addresses) != 1 || c.Cert || !l.CDNDirect {
		t.Fatalf("cdn: %+v", c)
	}
	// the older settings say the same, so older code (and an older version) sees the same setup
	if st.GetSetting("domain", "") != "vpn.example.com" {
		t.Error("domain mirror")
	}
	if o := cdn.Load(st); !o.Enabled || o.Domain != "cdn.example.com" || len(o.Addresses) != 1 || !o.Direct {
		t.Errorf("cdn mirror: %+v", o)
	}
	if msg, _ := Migrate(st); msg != "" {
		t.Errorf("a second migration did something: %q", msg)
	}

	// the command line (or an older version) changes the domain: the list follows
	st.SetSetting("domain", "new.example.com")
	if msg, _ := Migrate(st); msg == "" {
		t.Fatal("the changed domain was not taken over")
	}
	l = Load(st)
	if l.MainName() != "new.example.com" || len(l.Domains) != 3 || l.Domains[1].Name != "vpn.example.com" {
		t.Fatalf("after the change: %+v", l.Domains)
	}
	// an older version changes the CDN settings
	setCDN(st, cdn.Settings{Enabled: false, Domain: "cdn2.example.com"})
	l = Load(st)
	if f := l.FirstCDN(); f == nil || f.Name != "cdn2.example.com" || f.Configs || len(l.CDNs()) != 0 || l.CDNDirect {
		t.Fatalf("cdn change: %+v", l.Domains)
	}
	// clearing the domain removes the main name; the next direct one takes over
	st.SetSetting("domain", "")
	l = Load(st)
	if l.MainName() != "vpn.example.com" {
		t.Errorf("after clearing: %+v", l.Domains)
	}
}

func TestMigrateCDNOnThePanelDomain(t *testing.T) {
	// before the list, an empty CDN name meant the panel's own domain
	st := openStore(t)
	st.SetSetting("domain", "panel.example.com")
	setCDN(st, cdn.Settings{Enabled: true, Addresses: []cdn.Address{{Address: "104.16.2.2"}}})
	Migrate(st)
	l := Load(st)
	if len(l.Domains) != 1 || l.Domains[0].Role != CDN || !l.Domains[0].Configs || l.MainName() != "panel.example.com" || len(l.CDNs()) != 1 {
		t.Fatalf("list: %+v", l.Domains)
	}
	if st.GetSetting("domain", "") != "panel.example.com" {
		t.Error("the main domain changed")
	}
}

func TestMigrateNothing(t *testing.T) {
	st := openStore(t)
	if _, err := Migrate(st); err != nil {
		t.Fatal(err)
	}
	if l := Load(st); len(l.Domains) != 0 || l.Main() != nil || !Saved(st) {
		t.Fatalf("%+v", l)
	}
	if st.GetSetting("domain", "x") != "" {
		t.Error("domain")
	}
}

func TestClean(t *testing.T) {
	l := List{Domains: []Domain{
		{Name: " https://VPN.Example.com:443/panel ", Role: Direct, Label: "  Main   one ", Configs: true, All: true},
		{Name: "cdn.example.com", Role: CDN, Configs: true, All: true, Addresses: []cdn.Address{{Address: " 104.16.1.1 "}, {Address: "104.16.1.1"}}},
		{Name: "sub.example.com", Role: Sub, Configs: true, Addresses: []cdn.Address{{Address: "1.1.1.1"}}},
	}}
	if err := l.Clean(); err != nil {
		t.Fatal(err)
	}
	d := l.Domains
	if d[0].Name != "vpn.example.com" || d[0].Label != "Main one" || !d[0].All || d[0].Added == 0 {
		t.Errorf("direct: %+v", d[0])
	}
	if d[1].All || len(d[1].Addresses) != 1 || d[1].Addresses[0].Address != "104.16.1.1" {
		t.Errorf("cdn: %+v", d[1])
	}
	if d[2].Configs || d[2].Addresses != nil {
		t.Errorf("sub: %+v", d[2])
	}
	for _, bad := range []Domain{{Name: "203.0.113.5"}, {Name: "*.example.com"}, {Name: "localhost"}, {Name: "a..b.com"}, {Name: "x.com", Role: "relay"}, {Name: "x.com", Label: strings.Repeat("ا", 33)}} {
		bl := List{Domains: []Domain{bad}}
		if bl.Clean() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	twice := List{Domains: []Domain{{Name: "a.example.com"}, {Name: "A.example.com."}}}
	if err := twice.Clean(); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("twice: %v", err)
	}
}

func TestOrderAndUses(t *testing.T) {
	l := List{Domains: []Domain{
		{Name: "sub.example.com", Role: Sub, Cert: true},
		{Name: "cdn.example.com", Role: CDN, Configs: true},
		{Name: "vpn.example.com", Role: Direct, Cert: true},
		{Name: "vpn2.example.com", Role: Direct, Cert: true, Configs: true},
		{Name: "vpn3.example.com", Role: Direct},
		{Name: "cdn2.example.com", Role: CDN},
	}}
	if l.MainName() != "vpn.example.com" {
		t.Errorf("main: %s", l.MainName())
	}
	if n := l.CertNames(); strings.Join(n, ",") != "vpn.example.com,sub.example.com,vpn2.example.com" {
		t.Errorf("cert names: %v", n)
	}
	if m := l.Mirrors(); len(m) != 1 || m[0].Name != "vpn2.example.com" {
		t.Errorf("mirrors: %v", m)
	}
	if c := l.CDNs(); len(c) != 1 || c[0].Name != "cdn.example.com" {
		t.Errorf("cdns: %v", c)
	}
	if l.SubName() != "sub.example.com" || l.FirstCDN().Name != "cdn.example.com" {
		t.Error("sub / first cdn")
	}
}

func TestJudge(t *testing.T) {
	here, cf, other := Addr{IP: "203.0.113.5", Here: true}, Addr{IP: "104.16.1.1", Cloudflare: true}, Addr{IP: "198.51.100.9"}
	v6other := Addr{IP: "2001:db8::9"}
	cases := []struct {
		role    string
		a, aaaa []Addr
		points  string
		status  string
		problem string
	}{
		{Direct, []Addr{here}, nil, "here", "ok", ""},
		{Direct, []Addr{cf}, nil, "cloudflare", "bad", ProbProxied},
		{Direct, []Addr{other}, nil, "elsewhere", "bad", ProbNotHere},
		{Direct, []Addr{here, other}, nil, "mixed", "warn", ProbSomeElsewhere},
		{Direct, []Addr{here}, []Addr{v6other}, "mixed", "warn", ProbV6Elsewhere},
		{CDN, []Addr{cf}, nil, "cloudflare", "ok", ""},
		{CDN, []Addr{here}, nil, "here", "bad", ProbNotProxied},
		{CDN, []Addr{other}, nil, "elsewhere", "bad", ProbNotCDN},
		{CDN, []Addr{cf, here}, nil, "mixed", "warn", ProbSomeElsewhere},
		{Sub, []Addr{cf}, nil, "cloudflare", "ok", ""},
		{Sub, []Addr{here}, nil, "here", "ok", ""},
		{Sub, []Addr{other}, nil, "elsewhere", "bad", ProbNotHere},
	}
	for i, c := range cases {
		ch := Check{A: c.a, AAAA: c.aaaa, Problems: []string{}}
		if ch.AAAA == nil {
			ch.AAAA = []Addr{}
		}
		judge(&ch, c.role)
		got := ""
		if len(ch.Problems) > 0 {
			got = ch.Problems[0]
		}
		if ch.Points != c.points || ch.Status != c.status || got != c.problem {
			t.Errorf("case %d (%s): %s %s %v", i, c.role, ch.Points, ch.Status, ch.Problems)
		}
	}
}

// fakeResolver answers from tables.
type fakeResolver struct {
	ips   map[string][]string
	cname map[string]string
	ns    map[string][]string
}

func (f fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	l, ok := f.ips[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	var out []net.IPAddr
	for _, s := range l {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}

func (f fakeResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	if c, ok := f.cname[host]; ok {
		return c + ".", nil
	}
	return host + ".", nil
}

func (f fakeResolver) LookupNS(_ context.Context, name string) ([]*net.NS, error) {
	l, ok := f.ns[name]
	if !ok {
		return nil, errors.New("no NS")
	}
	var out []*net.NS
	for _, h := range l {
		out = append(out, &net.NS{Host: h + "."})
	}
	return out, nil
}

func TestLookup(t *testing.T) {
	r := fakeResolver{
		ips:   map[string][]string{"vpn.example.com": {"203.0.113.5", "2001:db8::5"}, "cdn.example.com": {"104.16.1.1", "2606:4700::1"}},
		cname: map[string]string{"cdn.example.com": "example.com.cdn.cloudflare.net"},
		ns:    map[string][]string{"example.com": {"tia.ns.cloudflare.com", "bob.ns.cloudflare.com"}},
	}
	me := Server{IPv4: []string{"203.0.113.5"}, IPv6: []string{"2001:db8::5"}}
	c := Lookup(context.Background(), r, "vpn.example.com", Direct, me)
	if c.Status != "ok" || c.Points != "here" || len(c.A) != 1 || len(c.AAAA) != 1 || !c.A[0].Here || c.Zone != "example.com" || !c.OnCF || len(c.NS) != 2 {
		t.Errorf("direct: %+v", c)
	}
	c = Lookup(context.Background(), r, "cdn.example.com", CDN, me)
	if c.Status != "ok" || c.Points != "cloudflare" || c.CNAME != "example.com.cdn.cloudflare.net" {
		t.Errorf("cdn: %+v", c)
	}
	c = Lookup(context.Background(), r, "gone.example.com", Direct, me)
	if c.Status != "bad" || len(c.Problems) != 1 || c.Problems[0] != ProbNotFound {
		t.Errorf("not found: %+v", c)
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"Example.COM.":                   "example.com",
		"https://a.example.com:2096/x?y": "a.example.com",
		"http://b.example.com":           "b.example.com",
		"  c.example.com  ":              "c.example.com",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
