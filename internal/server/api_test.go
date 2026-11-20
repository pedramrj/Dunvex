package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

type client struct {
	t    *testing.T
	base string
	hc   *http.Client
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base + "/dunvex/api", hc: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Dunvex", "1")
	res, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	if json.Unmarshal(raw, &out) != nil {
		var list []any
		json.Unmarshal(raw, &list)
		out["list"] = list
	}
	return res.StatusCode, out
}

func TestRolesQuotaAndTwoFactor(t *testing.T) {
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

	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatalf("owner login: %d", code)
	}
	owner.do("POST", "/users", map[string]any{"name": "ownersuser"})
	if code, out := owner.do("POST", "/admins", map[string]any{"username": "rs", "password": "reseller-1", "role": "reseller",
		"maxUsers": 2, "maxData": 10 << 30}); code != 200 {
		t.Fatalf("create reseller: %d %v", code, out)
	}

	rs := newClient(t, ts.URL)
	if code, _ := rs.do("POST", "/login", map[string]string{"username": "rs", "password": "reseller-1"}); code != 200 {
		t.Fatal("reseller login failed")
	}
	if code, _ := rs.do("GET", "/settings", nil); code != 403 {
		t.Fatalf("reseller read settings: %d", code)
	}
	if code, _ := rs.do("GET", "/inbounds", nil); code != 403 {
		t.Fatalf("reseller read inbounds: %d", code)
	}
	if code, _ := rs.do("POST", "/users", map[string]any{"name": "free"}); code != 400 {
		t.Fatalf("unlimited user allowed for reseller: %d", code)
	}
	if code, out := rs.do("POST", "/users", map[string]any{"name": "r1", "dataLimit": 6 << 30, "durationDays": 30}); code != 200 {
		t.Fatalf("reseller create: %d %v", code, out)
	}
	if code, _ := rs.do("POST", "/users", map[string]any{"name": "r2", "dataLimit": 6 << 30}); code != 400 {
		t.Fatalf("data quota not enforced: %d", code)
	}
	_, list := rs.do("GET", "/users", nil)
	if n := len(list["list"].([]any)); n != 1 {
		t.Fatalf("reseller sees %d users", n)
	}
	own, _ := st.UserByName("ownersuser")
	if code, _ := rs.do("DELETE", "/users/"+itoa(own.ID), nil); code != 404 {
		t.Fatalf("reseller deleted another admin's user: %d", code)
	}
	if code, out := rs.do("GET", "/overview", nil); code != 200 || out["quota"] == nil {
		t.Fatalf("reseller overview: %d %v", code, out)
	}
	_, list = owner.do("GET", "/users", nil)
	if n := len(list["list"].([]any)); n != 2 {
		t.Fatalf("owner sees %d users", n)
	}

	// device limit and pause resume through the API
	r1, _ := st.UserByName("r1")
	if code, out := rs.do("PUT", "/users/"+itoa(r1.ID), map[string]any{"ipLimit": 2}); code != 200 || out["ipLimit"].(float64) != 2 {
		t.Fatalf("set device limit: %d %v", code, out)
	}

	// two-step login
	_, setup := owner.do("POST", "/account/2fa/setup", nil)
	secret := setup["secret"].(string)
	step := time.Now().Unix() / 30
	if code, out := owner.do("POST", "/account/2fa/enable", map[string]string{"code": util.TOTPCode(secret, step)}); code != 200 {
		t.Fatalf("enable 2fa: %d %v", code, out)
	}
	fresh := newClient(t, ts.URL)
	code, out := fresh.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"})
	if code != 401 || out["need2fa"] != true {
		t.Fatalf("login without code: %d %v", code, out)
	}
	if code, _ := fresh.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1", "code": "000000"}); code != 401 {
		t.Fatal("wrong code accepted")
	}
	if code, _ := fresh.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1",
		"code": util.TOTPCode(secret, step+1)}); code != 200 {
		t.Fatalf("login with code: %d", code)
	}
	// SSH reset turns 2FA off
	st.UpsertAdmin("boss", hash)
	a, _ := st.AdminByName("boss")
	if a.TOTP {
		t.Fatal("admin reset should clear 2FA")
	}
	// the last owner cannot be demoted
	if code, _ := owner.do("PUT", "/admins/"+itoa(a.ID), map[string]any{"role": "admin"}); code != 400 {
		t.Fatalf("last owner demoted: %d", code)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
