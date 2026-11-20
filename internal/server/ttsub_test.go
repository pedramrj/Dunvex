package server

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestTrustTunnelSubscription: the tt:// link is in the plain list (and on
// the user page), Clash and sing-box profiles leave it out, one per server.
func TestTrustTunnelSubscription(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, filepath.Join(dir, "bin"), filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	os.WriteFile(srv.Mgr.TT.Bin(), []byte("stand-in"), 0o755) // present, so nothing is downloaded
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	if code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "trusttunnel"}); code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	if code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "trusttunnel", "port": 9443}); code != 400 || !strings.Contains(out["error"].(string), "one per server") {
		t.Errorf("a second TrustTunnel: %d %v", code, out)
	}
	st.CreateUser(&store.User{Name: "sara", UUID: util.UUID(), Password: "sara-pass-0123456789", SubToken: "sara-token-0123456789", Enabled: true, ResetPeriod: "none"})
	get := func(ua string) string {
		req, _ := http.NewRequest("GET", ts.URL+"/sub/sara-token-0123456789", nil)
		req.Header.Set("User-Agent", ua)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	links, _ := base64.StdEncoding.DecodeString(get("Shadowrocket"))
	if !strings.Contains(string(links), "\ntt://?") {
		t.Errorf("links:\n%s", links)
	}
	if c := get("clash.meta"); strings.Contains(c, "trusttunnel") {
		t.Errorf("clash got TrustTunnel:\n%s", c)
	}
	if s := get("sing-box"); strings.Contains(s, "trusttunnel") {
		t.Errorf("sing-box got TrustTunnel:\n%s", s)
	}
}
