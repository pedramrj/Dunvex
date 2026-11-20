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

// TestMieruSubscription: the mierus:// link is in the plain list, Clash.Meta
// gets a mieru proxy, sing-box gets none, and a server takes one Mieru only.
func TestMieruSubscription(t *testing.T) {
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
	os.WriteFile(srv.Mgr.Mita.Bin(), []byte("stand-in"), 0o755) // present, so nothing is downloaded
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	if code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "mieru"}); code != 200 {
		t.Fatalf("create mieru: %d %v", code, out)
	}
	if code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "mieru-udp"}); code != 400 || !strings.Contains(out["error"].(string), "one per server") {
		t.Errorf("a second Mieru: %d %v", code, out)
	}

	st.CreateUser(&store.User{Name: "sara", UUID: util.UUID(), Password: "sara-pass-0123456789", SubToken: "sara-token-0123456789", Enabled: true, ResetPeriod: "none"})
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
	links, _ := base64.StdEncoding.DecodeString(get("/sub/sara-token-0123456789", "v2rayN"))
	if !strings.Contains(string(links), "mierus://sara:sara-pass-0123456789@") {
		t.Errorf("links:\n%s", links)
	}
	if clash := get("/sub/sara-token-0123456789", "clash.meta"); !strings.Contains(clash, `"type":"mieru"`) || !strings.Contains(clash, `"transport":"TCP"`) {
		t.Errorf("clash:\n%s", clash)
	}
	if sb := get("/sub/sara-token-0123456789", "sing-box"); strings.Contains(sb, "mieru") {
		t.Errorf("sing-box got Mieru:\n%s", sb)
	}
}
