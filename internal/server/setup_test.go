package server

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
)

func TestFirstRunSetup(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetSetting("setup_token", "tok-0123456789abcdefghijklmnopqr")
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c := newClient(t, ts.URL)
	if _, out := c.do("GET", "/setup-state", nil); out["pending"] != true {
		t.Fatalf("setup should be pending: %v", out)
	}
	if code, out := c.do("POST", "/login", map[string]string{"username": "admin", "password": "whatever1"}); code != 409 || out["needSetup"] != true {
		t.Fatalf("login before setup: %d %v", code, out)
	}
	if code, _ := c.do("POST", "/setup", map[string]string{"token": "wrong", "username": "pedram", "password": "longenough1"}); code != 403 {
		t.Fatalf("wrong token: %d", code)
	}
	if code, _ := c.do("POST", "/setup", map[string]string{"token": "tok-0123456789abcdefghijklmnopqr", "username": "pedram", "password": "short"}); code != 400 {
		t.Fatalf("short password: %d", code)
	}
	if code, _ := c.do("POST", "/setup", map[string]string{"token": "tok-0123456789abcdefghijklmnopqr", "username": "a b", "password": "longenough1"}); code != 400 {
		t.Fatalf("bad username: %d", code)
	}
	if code, out := c.do("POST", "/setup", map[string]string{"token": "tok-0123456789abcdefghijklmnopqr", "username": "pedram", "password": "longenough1"}); code != 200 || out["username"] != "pedram" || out["role"] != "owner" {
		t.Fatalf("setup: %d %v", code, out)
	}
	// the setup signs the owner in
	if code, out := c.do("GET", "/me", nil); code != 200 || out["admin"] == nil {
		t.Fatalf("me after setup: %d %v", code, out)
	}
	// the link works once
	other := newClient(t, ts.URL)
	if code, _ := other.do("POST", "/setup", map[string]string{"token": "tok-0123456789abcdefghijklmnopqr", "username": "intruder", "password": "longenough2"}); code != 409 {
		t.Fatalf("second setup: %d", code)
	}
	if _, out := other.do("GET", "/setup-state", nil); out["pending"] != false {
		t.Fatalf("setup still pending: %v", out)
	}
	if st.GetSetting("setup_token", "") != "" {
		t.Fatal("token not cleared")
	}
	if code, _ := other.do("POST", "/login", map[string]string{"username": "pedram", "password": "longenough1"}); code != 200 {
		t.Fatalf("login with the chosen password: %d", code)
	}
}
