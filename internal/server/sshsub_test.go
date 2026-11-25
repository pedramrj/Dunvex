package server

import (
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/health"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestSSHSubscription: the ssh:// link is in the plain list (the Hiddify
// app), Clash.Meta and sing-box get an ssh proxy with the host key pinned, the
// Xray JSON leaves it out, the user page adds SlipNet's profile and the
// details to type in, the config check tests it with sing-box, and a server
// takes one SSH only.
func TestSSHSubscription(t *testing.T) {
	t.Setenv("DUNVEX_LISTEN", "127.0.0.1")
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	st.SetSetting("host", "203.0.113.9")
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, filepath.Join(dir, "bin"), filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "ssh"})
	if code != 200 {
		t.Fatalf("create ssh: %d %v", code, out)
	}
	ib, _ := out["inbound"].(map[string]any)
	id := int64(ib["id"].(float64))
	defer owner.do("DELETE", fmt.Sprintf("/inbounds/%d", id), nil) // stops the SSH server
	if port := int(ib["port"].(float64)); port == 22 || port == 0 {
		t.Errorf("port %d", port)
	}
	if code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "ssh"}); code != 400 || !strings.Contains(out["error"].(string), "one per server") {
		t.Errorf("a second SSH: %d %v", code, out)
	}

	u := &store.User{Name: "sara", UUID: util.UUID(), Password: "sara-pass-0123456789", SubToken: "sara-token-0123456789", Enabled: true, ResetPeriod: "none"}
	st.CreateUser(u)
	get := func(path, ua string) string {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("User-Agent", ua)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	in, _ := st.InboundByID(id)
	hk := protocol.SSHHostKey(in)
	links, _ := base64.StdEncoding.DecodeString(get("/sub/sara-token-0123456789", "v2rayN"))
	if !strings.Contains(string(links), "ssh://sara:sara-pass-0123456789@203.0.113.9:") || strings.Contains(string(links), "slipnet://") {
		t.Errorf("links:\n%s", links)
	}
	if clash := get("/sub/sara-token-0123456789", "clash.meta"); !strings.Contains(clash, `"type":"ssh"`) || !strings.Contains(clash, hk) {
		t.Errorf("clash:\n%s", clash)
	}
	if sb := get("/sub/sara-token-0123456789", "sing-box"); !strings.Contains(sb, `"type": "ssh"`) || !strings.Contains(sb, hk) {
		t.Errorf("sing-box:\n%s", sb)
	}
	if xj := get("/sub/sara-token-0123456789/xray", "v2rayNG"); strings.Contains(xj, "ssh") {
		t.Errorf("Xray JSON got SSH:\n%s", xj)
	}

	cs := srv.configsFor(u)
	if len(cs) != 2 || !strings.HasPrefix(cs[0].Link, "ssh://") || !strings.HasPrefix(cs[1].Link, "slipnet://") || !strings.HasSuffix(cs[1].Name, "SlipNet") {
		t.Fatalf("configs %+v", cs)
	}
	if !strings.Contains(cs[0].Note, "Username: sara") || !strings.Contains(cs[0].Note, "Host key: SHA256:") || cs[1].Note != "" {
		t.Errorf("notes %q / %q", cs[0].Note, cs[1].Note)
	}
	page := html.UnescapeString(get("/sub/sara-token-0123456789?format=html", "Mozilla/5.0"))
	if !strings.Contains(page, "Hiddify") || !strings.Contains(page, "SlipNet") || !strings.Contains(page, "Password: sara-pass-0123456789") {
		t.Errorf("user page lacks the SSH hints or details")
	}

	engine.EnsureHealthKey(st)
	var target *health.Target
	for _, x := range srv.healthTargets(engine.HealthUser(st)) {
		if x.InboundID == id {
			target = &x
		}
	}
	if target == nil || target.Kind != health.KindSingbox || len(target.Singbox) != 1 || target.Singbox[0]["type"] != "ssh" || target.Addr == "" {
		t.Errorf("health target %+v", target)
	}
}
