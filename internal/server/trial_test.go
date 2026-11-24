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

func TestTrialAPI(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	ahash, _ := HashPassword("admin-pass-1")
	st.CreateAdmin(&store.Admin{Username: "helper", PasswordHash: ahash, Role: store.RoleAdmin, Enabled: true})
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner, helper := newClient(t, ts.URL), newClient(t, ts.URL)
	owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"})
	helper.do("POST", "/login", map[string]string{"username": "helper", "password": "admin-pass-1"})
	if code, _ := helper.do("GET", "/trial", nil); code != 403 {
		t.Fatalf("an admin read the trial: %d", code)
	}
	_, out := owner.do("GET", "/trial", nil)
	s := out["settings"].(map[string]any)
	if s["on"] != false || s["dataMB"] != 1024.0 || s["hours"] != 24.0 || s["devices"] != 1.0 || s["cleanupDays"] != 3.0 {
		t.Fatalf("defaults: %v", s)
	}
	if code, _ := owner.do("PUT", "/trial", map[string]any{"on": true, "hours": 0}); code != 400 {
		t.Fatal("0 hours accepted")
	}
	code, out := owner.do("PUT", "/trial", map[string]any{"on": true, "dataMB": 500, "hours": 12, "devices": 2, "daily": 10,
		"channel": "https://t.me/dunvexnews", "cleanupDays": 7, "inbounds": []int{4}})
	if code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	s = out["settings"].(map[string]any)
	if s["on"] != true || s["channel"] != "@dunvexnews" || s["hours"] != 12.0 || len(s["inbounds"].([]any)) != 1 {
		t.Fatalf("saved: %v", s)
	}
	if st := out["stats"].(map[string]any); st["total"] != 0.0 {
		t.Fatalf("stats: %v", st)
	}
}
