package server

// The Domains page's certificate against Pebble, Let's Encrypt's test
// authority (see the pebbletest package); skipped without DUNVEX_PEBBLE.

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/domains"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/tlsmgr"
	"github.com/pedramrj/dunvex/internal/tlsmgr/pebbletest"
	"github.com/pedramrj/dunvex/internal/util"
)

func TestPebbleDomainsCertificate(t *testing.T) {
	p := pebbletest.Start(t)
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	st.SetSetting("domain", "vpn.dunvex.test")
	tp := protocol.Get("trojan-tls")
	st.CreateInbound(&store.Inbound{Preset: tp.ID, Port: tp.DefaultPort, Enabled: true, Params: protocol.Defaults(tp, nil, "vpn.dunvex.test")})
	st.CreateUser(&store.User{Name: "sara", UUID: util.UUID(), Password: "sara-pass-0123456789", SubToken: "sara-token-0123456789", Enabled: true, ResetPeriod: "none"})

	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	srv.dom.acmeDir, srv.dom.acmeHTTP, srv.dom.httpAddr, srv.dom.noKick = p.Dir, p.HTTP, p.HTTP01, true
	srv.InitDomains()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner := newClient(t, ts.URL)
	owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"})

	// a second name, both in the certificate (still self-signed: links pin it)
	code, out := owner.do("PUT", "/domains", map[string]any{"domains": []map[string]any{
		{"name": "vpn.dunvex.test", "role": "direct", "cert": true},
		{"name": "vpn2.dunvex.test", "role": "direct", "cert": true, "configs": true},
	}})
	if code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	u, _ := st.UserByToken("sara-token-0123456789")
	if l := strings.Join(srv.subLinks(u, time.Now().Unix()), "\n"); !strings.Contains(l, "pcs=") {
		t.Fatalf("before: %s", l)
	}
	wait := func() map[string]any {
		deadline := time.Now().Add(90 * time.Second)
		for {
			_, c := owner.do("GET", "/domains/cert", nil)
			if c["running"] != true {
				return c
			}
			if time.Now().After(deadline) {
				t.Fatal("the issuance did not end")
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	if code, out = owner.do("POST", "/domains/cert", nil); code != 200 {
		t.Fatalf("ask: %d %v", code, out)
	}
	c := wait()
	if last, _ := c["last"].(map[string]any); last == nil || last["ok"] != true {
		t.Fatalf("issuance: %v", c)
	}
	if st.GetSetting("cert_mode", "") != "acme" {
		t.Error("cert_mode")
	}
	env := srv.Env()
	info, _ := tlsmgr.Read(env.CertFile)
	if env.SelfSigned || info == nil || !info.Covers("vpn.dunvex.test") || !info.Covers("vpn2.dunvex.test") {
		t.Fatalf("certificate in use: %+v %+v", env, info)
	}
	links := strings.Join(srv.subLinks(u, time.Now().Unix()), "\n")
	if strings.Contains(links, "pcs=") || !strings.Contains(links, "@vpn2.dunvex.test:") || !strings.Contains(links, "sni=vpn2.dunvex.test") {
		t.Fatalf("links with the real certificate: %s", links)
	}
	// nothing is due now
	if srv.startCert(false) {
		t.Error("an issuance started with nothing to do")
	}

	// a third name whose DNS fails: the certificate stays as it is, the name is reported
	p.Do("/set-servfail", map[string]any{"host": "broken.dunvex.test."})
	owner.do("PUT", "/domains", map[string]any{"domains": []map[string]any{
		{"name": "vpn.dunvex.test", "role": "direct", "cert": true},
		{"name": "vpn2.dunvex.test", "role": "direct", "cert": true, "configs": true},
		{"name": "broken.dunvex.test", "role": "sub", "cert": true},
	}})
	before := info.NotBefore
	if !srv.startCert(false) {
		t.Fatal("the new name was not asked for")
	}
	wait()
	info2, _ := tlsmgr.Read(srv.Env().CertFile)
	if info2 == nil || !info2.NotBefore.Equal(before) {
		t.Errorf("the certificate was made again without the new name: %+v", info2)
	}
	_, out = owner.do("GET", "/domains", nil)
	for _, r := range out["domains"].([]any) {
		row := r.(map[string]any)
		cs := row["certState"].(map[string]any)
		switch row["name"] {
		case "broken.dunvex.test":
			if cs["covered"] != false || cs["code"] == nil || cs["retryAt"] == nil {
				t.Errorf("broken: %v", cs)
			}
		default:
			if cs["covered"] != true {
				t.Errorf("%s: %v", row["name"], cs)
			}
		}
	}
	// it waits before the next try (unless asked)
	if srv.startCert(false) {
		t.Error("a failed name was tried again at once")
	}
	if l := domains.Load(st); l.Find("broken.dunvex.test") == nil {
		t.Error("list")
	}
}
