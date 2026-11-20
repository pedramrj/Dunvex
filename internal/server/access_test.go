package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
)

func TestSessionsAndAPIKeys(t *testing.T) {
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
	login := func() *client {
		c := newClient(t, ts.URL)
		if code, _ := c.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
			t.Fatalf("login: %d", code)
		}
		return c
	}

	// two browsers: each sees both sessions, its own marked current
	laptop, phone := login(), login()
	_, out := laptop.do("GET", "/account/sessions", nil)
	list := out["list"].([]any)
	if len(list) != 2 {
		t.Fatalf("sessions: %v", list)
	}
	var phoneID string
	current := 0
	for _, x := range list {
		m := x.(map[string]any)
		if m["current"] == true {
			current++
		} else {
			phoneID = m["id"].(string)
		}
	}
	if current != 1 || phoneID == "" {
		t.Fatalf("exactly one current session expected: %v", list)
	}

	// the laptop signs the phone out; the phone's cookie stops working at once
	if code, _ := laptop.do("DELETE", "/account/sessions/"+phoneID, nil); code != 200 {
		t.Fatalf("end session: %d", code)
	}
	if code, _ := phone.do("GET", "/me", nil); code != 401 {
		t.Fatalf("signed-out phone should get 401, got %d", code)
	}
	if code, _ := laptop.do("GET", "/me", nil); code != 200 {
		t.Fatalf("laptop should stay signed in, got %d", code)
	}

	// "sign out everywhere else"
	tablet := login()
	_, out = laptop.do("POST", "/account/sessions/others", nil)
	if out["ended"].(float64) != 1 {
		t.Fatalf("others: %v", out)
	}
	if code, _ := tablet.do("GET", "/me", nil); code != 401 {
		t.Fatalf("tablet should be signed out, got %d", code)
	}

	// logout removes the session row
	if code, _ := laptop.do("POST", "/logout", nil); code != 200 {
		t.Fatal("logout")
	}
	if l, _ := st.Sessions(0, 0); len(l) != 0 {
		t.Fatalf("no session should be left, got %d", len(l))
	}

	// API keys: shown once, work as Bearer, read-only keys cannot write
	owner := login()
	code, out := owner.do("POST", "/account/keys", map[string]any{"name": "billing script"})
	if code != 200 {
		t.Fatalf("create key: %d %v", code, out)
	}
	full := out["token"].(string)
	_, out = owner.do("POST", "/account/keys", map[string]any{"name": "dashboard", "readOnly": true, "days": 30})
	ro := out["token"].(string)
	if !strings.HasPrefix(full, keyPrefix) || len(full) != len(keyPrefix)+40 {
		t.Fatalf("token shape: %q", full)
	}
	bearer := func(tok, method, path, body string) int {
		req, _ := http.NewRequest(method, ts.URL+"/dunvex/api"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res.StatusCode
	}
	if c := bearer(full, "GET", "/users", ""); c != 200 {
		t.Fatalf("key GET: %d", c)
	}
	if c := bearer(full, "POST", "/users", `{"name":"from-script"}`); c != 200 {
		t.Fatalf("key POST without the browser header: %d", c)
	}
	if c := bearer(ro, "GET", "/overview", ""); c != 200 {
		t.Fatalf("read-only GET: %d", c)
	}
	if c := bearer(ro, "POST", "/users", `{"name":"nope"}`); c != 403 {
		t.Fatalf("read-only POST should be 403, got %d", c)
	}
	if c := bearer(full, "POST", "/account/keys", `{"name":"escalate"}`); c != 403 {
		t.Fatalf("a key must not make keys, got %d", c)
	}
	if c := bearer(full, "GET", "/admins", ""); c != 403 {
		t.Fatalf("a key must not manage admins, got %d", c)
	}
	if c := bearer(full, "POST", "/restore", ""); c != 403 {
		t.Fatalf("a key must not restore a backup, got %d", c)
	}
	if c := bearer("dvx_"+strings.Repeat("x", 40), "GET", "/users", ""); c != 401 {
		t.Fatalf("unknown key should be 401, got %d", c)
	}

	// the list never shows the key, and deleting one revokes it
	_, out = owner.do("GET", "/account/keys", nil)
	keys := out["list"].([]any)
	if len(keys) != 2 {
		t.Fatalf("keys: %v", keys)
	}
	for _, k := range keys {
		m := k.(map[string]any)
		if _, leaked := m["hash"]; leaked {
			t.Fatal("hash must not be sent")
		}
		if m["name"] == "billing script" {
			if code, _ := owner.do("DELETE", "/account/keys/"+jsonID(m["id"]), nil); code != 200 {
				t.Fatalf("delete key: %d", code)
			}
		}
	}
	if c := bearer(full, "GET", "/users", ""); c != 401 {
		t.Fatalf("deleted key should be 401, got %d", c)
	}

	// only the owner's current browser is left
	if l, _ := st.Sessions(0, 0); len(l) != 1 {
		t.Fatalf("one session expected, got %d", len(l))
	}
	if deviceName("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36") != "Chrome on Windows" ||
		deviceName("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1") != "Safari on iOS" ||
		deviceName("curl/8.5.0") != "Script" {
		t.Error("device names")
	}
}

func jsonID(v any) string {
	f, _ := v.(float64)
	return strconv.FormatInt(int64(f), 10)
}
