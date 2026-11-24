package protocol

import (
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/store"
)

// A new TrustTunnel hides from probes: the endpoint admits only the apps with
// its key, the link carries the key, and a request with no sign-in gets 404.
func TestTrustTunnelHides(t *testing.T) {
	p := Get("trusttunnel")
	u := &store.User{Name: "sara", Password: "pw-0123456789"}
	env := Env{Host: "203.0.113.10"}
	in := &store.Inbound{ID: 5, Preset: p.ID, Port: 8443, Enabled: true, Params: Defaults(p, map[string]any{"sni": "www.apple.com"}, "")}
	if !in.Bool("ttHide") || len(TTAuthKey(in)) != 16 || !TTHide(in, env) {
		t.Fatalf("defaults: %v", in.Params)
	}
	key := in.Str("ttAuthKey")
	if again := Defaults(p, in.Params, ""); again["ttAuthKey"] != key {
		t.Fatal("the key changed on a second Defaults")
	}

	s := TrustTunnelServer(in, []*store.User{u}, env, "/etc/dvx", 9000, "", "")
	main := string(s.Main)
	rules := string(s.Files[ttRulesFile])
	if !strings.Contains(main, `rules_file = "/etc/dvx/tt-rules.toml"`) || !strings.Contains(main, "non_connect_auth_failure_status_code = 404") {
		t.Fatalf("main settings:\n%s", main)
	}
	if !strings.Contains(rules, `client_random_auth_key = "`+key+`"`) || !strings.Contains(rules, "action = \"allow\"") ||
		strings.Index(rules, `action = "allow"`) > strings.Index(rules, `action = "deny"`) {
		t.Fatalf("rules (the key allowed first, then everyone else denied):\n%s", rules)
	}
	f := ttFields(t, Link(p, in, u, env, "TT"))
	if len(f[0x0F]) != 1 || string(f[0x0F][0]) != key {
		t.Fatalf("link key: %q", f[0x0F])
	}

	// a new key is a new endpoint config (it restarts)
	other := &store.Inbound{ID: 5, Preset: p.ID, Port: 8443, Enabled: true, Params: Defaults(p, map[string]any{"sni": "www.apple.com", "ttCert": in.Str("ttCert"), "ttKey": in.Str("ttKey"), "ttCertFor": "www.apple.com"}, "")}
	if string(TrustTunnelServer(other, []*store.User{u}, env, "/etc/dvx", 9000, "", "").Main) == main {
		t.Fatal("another key gave the same endpoint config")
	}

	// off: no rules and no key in the link, still a plain 404
	in.Params["ttHide"] = false
	s = TrustTunnelServer(in, []*store.User{u}, env, "/etc/dvx", 9000, "", "")
	if _, ok := s.Files[ttRulesFile]; ok || strings.Contains(string(s.Main), "rules_file") || !strings.Contains(string(s.Main), "non_connect_auth_failure_status_code = 404") {
		t.Fatalf("off:\n%s", s.Main)
	}
	if f := ttFields(t, Link(p, in, u, env, "TT")); len(f[0x0F]) != 0 {
		t.Fatal("the key is in the link while hiding is off")
	}

	// on this server's own domain (a real certificate): nothing to hide behind
	in.Params["ttHide"] = true
	in.Params["sni"] = "vpn.example.com"
	real := Env{Host: "203.0.113.10", Domain: "vpn.example.com", CertFile: "/c.pem", KeyFile: "/k.pem"}
	if TTHide(in, real) {
		t.Fatal("hiding on the panel's own domain")
	}
}
