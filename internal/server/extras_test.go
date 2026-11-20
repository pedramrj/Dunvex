package server

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
)

func TestStatusEventsAndBrand(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// a wrong password is logged once, however often it is retried
	bad := newClient(t, ts.URL)
	for i := 0; i < 3; i++ {
		if code, _ := bad.do("POST", "/login", map[string]string{"username": "boss", "password": "nope"}); code != 401 {
			t.Fatalf("bad login: %d", code)
		}
	}

	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatalf("owner login: %d", code)
	}
	owner.do("POST", "/users", map[string]any{"name": "alice"})

	code, out := owner.do("GET", "/status", nil)
	if code != 200 || out["users"].(float64) != 1 || out["version"] != "test" {
		t.Fatalf("status: %d %v", code, out)
	}
	if cores, _ := out["cores"].([]any); len(cores) != 2 {
		t.Fatalf("status cores: %v", out["cores"])
	}

	_, ev := owner.do("GET", "/events?limit=500&q=failed+sign-in", nil)
	list, _ := ev["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("want one failed sign-in event, got %d", len(list))
	}
	if msg := list[0].(map[string]any)["message"].(string); !strings.Contains(msg, "boss") {
		t.Fatalf("event message: %q", msg)
	}

	// portal accent accepts only #rrggbb
	if code, _ := owner.do("PUT", "/settings", map[string]any{"sub_accent": "red;}body{x"}); code != 400 {
		t.Fatalf("bad accent accepted: %d", code)
	}
	if code, out := owner.do("PUT", "/settings", map[string]any{"sub_accent": "#25c99a"}); code != 200 {
		t.Fatalf("accent: %d %v", code, out)
	}
	if got := st.GetSetting("sub_accent", ""); got != "#25c99a" {
		t.Fatalf("accent stored as %q", got)
	}
	if accentOr("javascript:x") != "#397bff" || accentOr("#ABCDEF") != "#ABCDEF" {
		t.Fatal("accentOr")
	}

	// resellers see their own status but not the event feed
	owner.do("POST", "/admins", map[string]any{"username": "rs", "password": "reseller-1", "role": "reseller", "maxUsers": 5})
	rs := newClient(t, ts.URL)
	rs.do("POST", "/login", map[string]string{"username": "rs", "password": "reseller-1"})
	if code, out := rs.do("GET", "/status", nil); code != 200 || out["users"].(float64) != 0 || out["problems"].(float64) != 0 {
		t.Fatalf("reseller status: %d %v", code, out)
	}
	if code, _ := rs.do("GET", "/events", nil); code != 403 {
		t.Fatalf("reseller events: %d", code)
	}
}

func TestRangeDays(t *testing.T) {
	for q, want := range map[string]int{"": 30, "1": 2, "7": 7, "90": 90, "401": 30, "-3": 30, "x": 30} {
		r := httptest.NewRequest("GET", "/x?days="+q, nil)
		if got := rangeDays(r); got != want {
			t.Errorf("days=%q: got %d want %d", q, got, want)
		}
	}
}
