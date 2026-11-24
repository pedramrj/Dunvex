package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/cdn"
	"github.com/pedramrj/dunvex/internal/domains"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/tlsmgr"
	"github.com/pedramrj/dunvex/internal/util"
)

// tableResolver answers DNS from a table.
type tableResolver map[string][]string

func (r tableResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	l, ok := r[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	var out []net.IPAddr
	for _, s := range l {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}
func (r tableResolver) LookupCNAME(_ context.Context, host string) (string, error) {
	return host + ".", nil
}
func (r tableResolver) LookupNS(_ context.Context, name string) ([]*net.NS, error) {
	if name == "dunvex.invalid" {
		return []*net.NS{{Host: "ada.ns.cloudflare.com."}}, nil
	}
	return nil, errors.New("no NS")
}

func TestDomainsPageAndLinks(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	// the setup before the Domains page: the panel's domain and the CDN helper
	st.SetSetting("domain", "panel.dunvex.invalid")
	st.SetSetting("host", "203.0.113.7")
	old, _ := json.Marshal(cdn.Settings{Enabled: true, Domain: "cdn.dunvex.invalid", Addresses: []cdn.Address{{Address: "104.16.1.1", Name: "MCI"}}})
	st.SetSetting(cdn.Setting, string(old))
	for _, id := range []string{"trojan-tls", "vless-ws-tls", "vless-reality-vision"} {
		p := protocol.Get(id)
		st.CreateInbound(&store.Inbound{Preset: id, Port: p.DefaultPort, Enabled: true, Params: protocol.Defaults(p, nil, "panel.dunvex.invalid")})
	}
	st.CreateUser(&store.User{Name: "sara", UUID: util.UUID(), Password: "sara-pass-0123456789", SubToken: "sara-token-0123456789", Enabled: true, ResetPeriod: "none"})

	var mu sync.Mutex
	var records []map[string]any
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		ok := func(v any) { json.NewEncoder(w).Encode(map[string]any{"success": true, "result": v}) }
		switch p := strings.TrimPrefix(r.URL.Path, "/client/v4"); {
		case p == "/user/tokens/verify" && r.Header.Get("Authorization") == "Bearer good-token-0123456789":
			ok(map[string]string{"status": "active"})
		case p == "/user/tokens/verify":
			w.WriteHeader(401)
			io.WriteString(w, `{"success":false,"errors":[{"code":1000,"message":"Invalid API Token"}]}`)
		case p == "/zones" && r.URL.Query().Get("name") == "dunvex.invalid", p == "/zones" && r.URL.Query().Get("name") == "":
			ok([]map[string]string{{"id": "z1", "name": "dunvex.invalid", "status": "active"}})
		case p == "/zones":
			ok([]any{})
		case p == "/zones/z1/dns_records" && r.Method == "GET":
			ok(records)
		case p == "/zones/z1/dns_records" && r.Method == "POST":
			var rec map[string]any
			json.NewDecoder(r.Body).Decode(&rec)
			rec["id"] = "r" + string(rune('0'+len(records)))
			records = append(records, rec)
			ok(rec)
		default:
			w.WriteHeader(404)
			io.WriteString(w, `{"success":false,"errors":[{"code":7003,"message":"No route"}]}`)
		}
	}))
	defer cf.Close()

	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler(), CloudflareAPI: cf.URL + "/client/v4"}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	srv.dom.noKick = true
	srv.dom.resolver = tableResolver{
		"panel.dunvex.invalid": {"203.0.113.7"},
		"cdn.dunvex.invalid":   {"104.16.5.5"},
		"vpn2.dunvex.invalid":  {"198.51.100.3"},
	}
	u, _ := st.UserByToken("sara-token-0123456789")
	links := func() []string {
		out := []string{}
		for _, l := range srv.subLinks(u, time.Now().Unix()) {
			if !strings.Contains(l, "00000000-0000-0000-0000-000000000000") {
				out = append(out, l)
			}
		}
		return out
	}
	before := links()
	srv.InitDomains()
	if after := links(); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("the migration changed the links:\n%v\n%v", before, after)
	}
	if len(before) != 4 { // trojan, reality, and the WebSocket protocol through the CDN name and MCI
		t.Fatalf("links: %v", before)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	code, out := owner.do("GET", "/domains", nil)
	rows, _ := out["domains"].([]any)
	if code != 200 || len(rows) != 2 {
		t.Fatalf("domains: %d %v", code, out)
	}
	main, cdnRow := rows[0].(map[string]any), rows[1].(map[string]any)
	if main["name"] != "panel.dunvex.invalid" || main["main"] != true || main["role"] != "direct" || cdnRow["role"] != "cdn" || cdnRow["configs"] != true {
		t.Fatalf("rows: %v %v", main, cdnRow)
	}
	kinds := func(row map[string]any) map[string]float64 {
		m := map[string]float64{}
		for _, x := range row["uses"].([]any) {
			u := x.(map[string]any)
			n, _ := u["count"].(float64)
			m[u["kind"].(string)] = n
		}
		return m
	}
	if k := kinds(main); k["tls"] != 1 {
		// host is the IP, so the main name is not the links' address
		t.Errorf("main uses: %v", k)
	}
	if k := kinds(cdnRow); k["cdn"] != 2 || cdnRow["configCount"] != float64(2) {
		t.Errorf("cdn uses: %v %v", k, cdnRow["configCount"])
	}

	// more names: a second direct one (TLS configs), a second CDN name, a subscription name
	list := []map[string]any{
		{"name": "panel.dunvex.invalid", "role": "direct", "configs": true, "cert": false},
		{"name": "cdn.dunvex.invalid", "role": "cdn", "configs": true, "addresses": []map[string]string{{"address": "104.16.1.1", "name": "MCI"}}},
		{"name": "vpn2.dunvex.invalid", "role": "direct", "configs": true, "label": "Backup"},
		{"name": "cdn2.dunvex.invalid", "role": "cdn", "configs": true, "label": "Edge"},
		{"name": "sub.dunvex.invalid", "role": "sub"},
	}
	if code, _ := owner.do("PUT", "/domains", map[string]any{"domains": append(list, map[string]any{"name": "vpn2.dunvex.invalid"})}); code != 400 {
		t.Error("a name twice was accepted")
	}
	if code, out := owner.do("PUT", "/domains", map[string]any{"domains": []map[string]any{{"name": "x.dunvex.invalid", "dns01": true, "cert": true}}}); code != 400 || !strings.Contains(out["error"].(string), "Cloudflare") {
		t.Errorf("DNS validation without a token: %d %v", code, out)
	}
	if code, out = owner.do("PUT", "/domains", map[string]any{"domains": list}); code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	got := links()
	find := func(parts ...string) []string {
		var m []string
	next:
		for _, l := range got {
			for _, p := range parts {
				if !strings.Contains(l, p) {
					continue next
				}
			}
			m = append(m, l)
		}
		return m
	}
	// the TLS protocol at the second name: its own name as SNI (the
	// self-signed certificate is pinned, so any name works)
	if m := find("trojan://", "@vpn2.dunvex.invalid:", "sni=vpn2.dunvex.invalid", "Backup"); len(m) != 1 {
		t.Errorf("mirror: %v", got)
	}
	if m := find("@vpn2.dunvex.invalid:", "security=reality"); len(m) != 0 {
		t.Errorf("Reality got a copy without All: %v", m)
	}
	// the WebSocket protocol through both CDN names (and the clean address of the first)
	if m := find("type=ws", "sni=cdn2.dunvex.invalid", "@cdn2.dunvex.invalid:", "Edge"); len(m) != 1 {
		t.Errorf("second CDN name: %v", got)
	}
	if m := find("type=ws"); len(m) != 3 {
		t.Errorf("CDN configs: %v", m)
	}
	// every protocol at the second name with All
	list[2]["all"] = true
	owner.do("PUT", "/domains", map[string]any{"domains": list})
	got = links()
	if m := find("@vpn2.dunvex.invalid:", "security=reality"); len(m) != 1 {
		t.Errorf("Reality with All: %v", got)
	}
	if m := find("type=ws", "@vpn2.dunvex.invalid:"); len(m) != 0 {
		t.Errorf("a CDN protocol went to the second name while it goes through the CDN: %v", m)
	}
	// the subscription name carries the subscription links
	if b := srv.PublicBase(nil); b != "http://sub.dunvex.invalid:2096" {
		t.Errorf("subscription base: %s", b)
	}
	_, out = owner.do("GET", "/users/1/configs", nil)
	if cs := out["configs"].([]any); len(cs) == 0 {
		t.Error("no configs")
	} else {
		vias := map[string]int{}
		for _, c := range cs {
			v, _ := c.(map[string]any)["via"].(string)
			vias[v]++
		}
		if vias["domain"] != 2 || vias["cdn"] != 2 || vias["104.16.1.1"] != 1 {
			t.Errorf("vias: %v", vias)
		}
	}

	// the DNS check
	code, out = owner.do("POST", "/domains/check", map[string]any{})
	if code != 200 {
		t.Fatalf("check: %d %v", code, out)
	}
	checks := out["checks"].(map[string]any)
	status := func(name string) string {
		c, _ := checks[name].(map[string]any)
		if c == nil {
			return ""
		}
		p := c["problems"].([]any)
		s := c["status"].(string)
		if len(p) > 0 {
			s += ":" + p[0].(string)
		}
		return s
	}
	if s := status("panel.dunvex.invalid"); s != "ok" {
		t.Errorf("panel: %s", s)
	}
	if s := status("cdn.dunvex.invalid"); s != "ok" {
		t.Errorf("cdn: %s", s)
	}
	if s := status("vpn2.dunvex.invalid"); s != "bad:notHere" {
		t.Errorf("vpn2: %s", s)
	}
	if s := status("cdn2.dunvex.invalid"); s != "bad:notFound" {
		t.Errorf("cdn2: %s", s)
	}
	stRows := out["state"].(map[string]any)["domains"].([]any)
	if c := stRows[0].(map[string]any)["check"].(map[string]any); c["onCloudflare"] != true || c["zone"] != "dunvex.invalid" {
		t.Errorf("the check was not kept: %v", c)
	}
	// the warnings follow the check: once per problem
	srv.dnsAlerts()
	srv.dnsAlerts()
	evs := st.Events(50)
	n := 0
	for _, e := range evs {
		if strings.HasPrefix(e.Message, "domain vpn2.dunvex.invalid:") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("DNS warnings: %d", n)
	}

	// the Cloudflare token: checked, kept, never sent back in full
	if code, _ := owner.do("PUT", "/domains/cloudflare", map[string]any{"token": "bad-token"}); code != 400 {
		t.Error("a bad token was kept")
	}
	if code, out = owner.do("PUT", "/domains/cloudflare", map[string]any{"token": "good-token-0123456789"}); code != 200 {
		t.Fatalf("token: %d %v", code, out)
	}
	cfv := out["state"].(map[string]any)["cloudflare"].(map[string]any)
	if cfv["saved"] != true || strings.Contains(cfv["hint"].(string), "token-0123") {
		t.Errorf("kept token: %v", cfv)
	}
	if code, raw := owner.do("GET", "/domains", nil); code != 200 || strings.Contains(mustMarshal(raw), "good-token-0123456789") {
		t.Error("the token was sent back")
	}
	// one click: the second name points here, DNS only
	code, out = owner.do("POST", "/domains/point", map[string]any{"name": "vpn2.dunvex.invalid"})
	if code != 200 || out["result"].(map[string]any)["done"] != true {
		t.Fatalf("point: %d %v", code, out)
	}
	if len(records) != 1 || records[0]["name"] != "vpn2.dunvex.invalid" || records[0]["proxied"] != false || records[0]["content"] != "203.0.113.7" {
		t.Errorf("record: %v", records)
	}
	// DNS validation is now allowed
	list[2]["dns01"], list[2]["cert"] = true, true
	if code, out := owner.do("PUT", "/domains", map[string]any{"domains": list}); code != 200 {
		t.Errorf("DNS validation with a token: %d %v", code, out)
	}
	owner.do("PUT", "/domains/cloudflare", map[string]any{"token": ""})
	if domains.Load(st).Find("vpn2.dunvex.invalid").DNS01 {
		t.Error("forgetting the token kept DNS validation")
	}

	// the older Settings field still sets the main domain
	if code, _ := owner.do("PUT", "/settings", map[string]string{"domain": "Main.dunvex.invalid"}); code != 200 {
		t.Fatal("settings")
	}
	l := domains.Load(st)
	if l.MainName() != "main.dunvex.invalid" || l.Find("panel.dunvex.invalid") == nil || st.GetSetting("domain", "") != "main.dunvex.invalid" {
		t.Errorf("after Settings: %+v", l.Domains)
	}
}

func mustMarshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// testCA makes a certificate authority and certificates it signs.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T) *testCA {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA", Organization: []string{"Test CA"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return &testCA{c, key}
}

func (ca *testCA) issue(t *testing.T, dir string, names []string, notBefore, notAfter time.Time) (string, string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: notBefore, NotAfter: notAfter, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	cert, k := filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
	tlsmgr.WriteFiles(cert, k, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
	return cert, k
}

func TestCertificateChoiceAndPlan(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.dom.noKick = true
	domains.Save(st, domains.List{Domains: []domains.Domain{
		{Name: "vpn.dunvex.invalid", Role: domains.Direct, Cert: true},
		{Name: "vpn2.dunvex.invalid", Role: domains.Direct, Cert: true},
	}})
	ca := newTestCA(t)
	cdir := filepath.Join(dir, "issued")
	os.MkdirAll(cdir, 0o700)
	cert, key := ca.issue(t, cdir, []string{"vpn.dunvex.invalid"}, time.Now().Add(-time.Hour), time.Now().Add(80*24*time.Hour))
	st.SetSetting("cert_mode", "acme")
	st.SetSetting("cert_file", cert)
	st.SetSetting("key_file", key)

	env := srv.Env()
	if env.CertFile != cert || env.SelfSigned {
		t.Fatalf("a real certificate for the main name: %+v", env)
	}
	// a plan: vpn2 is missing, vpn is required to stay
	p := srv.certPlan()
	if p.Due || len(p.Missing) != 1 || p.Missing[0] != "vpn2.dunvex.invalid" {
		t.Fatalf("plan: %+v", p)
	}
	o, ok := p.order(srv.state(), false)
	if !ok || strings.Join(o.Names, ",") != "vpn.dunvex.invalid,vpn2.dunvex.invalid" || strings.Join(o.Require, ",") != "vpn.dunvex.invalid" {
		t.Fatalf("order: %+v %v", o, ok)
	}
	// vpn2 failed a moment ago: it waits (unless asked for), and nothing else is due
	srv.editState(func(s *domState) {
		s.Names["vpn2.dunvex.invalid"] = &nameState{Code: "dns", FailedAt: time.Now().Unix(), Fails: 1}
	})
	if _, ok := p.order(srv.state(), false); ok {
		t.Error("a failed name was tried again at once")
	}
	if o, ok := p.order(srv.state(), true); !ok || len(o.Names) != 2 {
		t.Error("asking now did not try it")
	}
	// the main name moves to one the certificate is not for: pinned, so links keep working
	l := domains.Load(st)
	l.SetMain("other.dunvex.invalid")
	domains.Save(st, l)
	if env := srv.Env(); env.CertFile != cert || !env.SelfSigned || env.CertSHA256 == "" {
		t.Errorf("a certificate not for the name: %+v", env)
	}
	// an expired one gives way to the self-signed one
	cert2, key2 := ca.issue(t, filepath.Join(dir, "old"), []string{"other.dunvex.invalid"}, time.Now().Add(-90*24*time.Hour), time.Now().Add(-time.Hour))
	st.SetSetting("cert_file", cert2)
	st.SetSetting("key_file", key2)
	if env := srv.Env(); env.CertFile == cert2 || !env.SelfSigned {
		t.Errorf("an expired certificate: %+v", env)
	}
	if p := srv.certPlan(); !p.Due {
		t.Error("an expired certificate is not due")
	}

	// the expiry warnings: once per step
	cert3, key3 := ca.issue(t, filepath.Join(dir, "soon"), []string{"other.dunvex.invalid"}, time.Now().Add(-80*24*time.Hour), time.Now().Add(6*24*time.Hour))
	st.SetSetting("cert_file", cert3)
	st.SetSetting("key_file", key3)
	srv.expiryAlerts()
	srv.expiryAlerts()
	evs := st.Events(20)
	n := 0
	for _, e := range evs {
		if strings.Contains(e.Message, "expires in 5 days") || strings.Contains(e.Message, "expires in 6 days") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("expiry warnings: %d %v", n, evs)
	}
}

func TestThreshold(t *testing.T) {
	d := 24 * time.Hour
	for left, want := range map[time.Duration]int{20 * d: -1, 13 * d: 14, 6 * d: 7, 2 * d: 3, 12 * time.Hour: 0, -time.Hour: 0} {
		if got := threshold(left, warnDays); got != want {
			t.Errorf("%v: %d, want %d", left, got, want)
		}
	}
}

func TestRDAP(t *testing.T) {
	rdap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/domain/dunvex.example" {
			w.WriteHeader(404)
			return
		}
		io.WriteString(w, `{"events":[{"eventAction":"registration","eventDate":"2020-01-01T00:00:00Z"},{"eventAction":"expiration","eventDate":"`+
			time.Now().Add(10*24*time.Hour).UTC().Format(time.RFC3339)+`"}],"entities":[{"roles":["registrar"],"vcardArray":["vcard",[["version",{},"text","4.0"],["fn",{},"text","Example Registrar"]]]}]}`)
	}))
	defer rdap.Close()
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "t.db"))
	defer st.Close()
	srv := &Server{Home: dir, St: st, Sys: sysinfo.NewSampler()}
	srv.dom.rdapBase = rdap.URL
	domains.Save(st, domains.List{Domains: []domains.Domain{{Name: "vpn.dunvex.example", Role: domains.Direct}, {Name: "cdn.dunvex.example", Role: domains.CDN}, {Name: "x.other.example", Role: domains.Sub}}})
	srv.checkRDAP()
	srv.checkRDAP()
	s := srv.state()
	keys := []string{}
	for k := range s.RDAP {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "dunvex.example,other.example" || s.RDAP["dunvex.example"].Registrar != "Example Registrar" || s.RDAP["other.example"].Error == "" {
		t.Fatalf("rdap: %v %+v", keys, s.RDAP)
	}
	evs := st.Events(10)
	n := 0
	for _, e := range evs {
		if strings.HasPrefix(e.Message, "the domain dunvex.example expires at its registrar in 9 days") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("registration warnings: %v", evs)
	}
}

func TestSubscriptionLinksStayWithoutDomains(t *testing.T) {
	// a server with no domain at all: the list stays empty and links are unchanged
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "t.db"))
	defer st.Close()
	st.SetSetting("host", "203.0.113.9")
	p := protocol.Get("trojan-tls")
	st.CreateInbound(&store.Inbound{Preset: p.ID, Port: p.DefaultPort, Enabled: true, Params: protocol.Defaults(p, nil, "")})
	st.CreateUser(&store.User{Name: "ali", UUID: util.UUID(), Password: "ali-pass-0123456789", SubToken: "ali-token-0123456789", Enabled: true, ResetPeriod: "none"})
	srv := &Server{Home: dir, St: st, Sys: sysinfo.NewSampler()}
	u, _ := st.UserByToken("ali-token-0123456789")
	before := strings.Join(srv.subLinks(u, time.Now().Unix()), "\n")
	srv.InitDomains()
	after := strings.Join(srv.subLinks(u, time.Now().Unix()), "\n")
	if before != after || !strings.Contains(after, "@203.0.113.9:") {
		t.Fatalf("links:\n%s\n%s", before, after)
	}
	// the IP's certificate: the plan asks for the IP with the short-lived profile
	l := domains.Load(st)
	l.IPCert = true
	domains.Save(st, l)
	pl := srv.certPlan()
	o, ok := pl.order(srv.state(), false)
	if pl.IP != "203.0.113.9" || !ok || len(o.IPs) != 1 || o.Profile != tlsmgr.ShortLived || len(o.Names) != 0 {
		t.Fatalf("IP plan: %+v %+v %v", pl, o, ok)
	}
	_ = base64.StdEncoding
}
