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

func TestUserTags(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	rhash, _ := HashPassword("seller-pass-1")
	seller := &store.Admin{Username: "seller", PasswordHash: rhash, Role: store.RoleReseller, Enabled: true, MaxUsers: 10}
	st.CreateAdmin(seller)
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner, reseller := newClient(t, ts.URL), newClient(t, ts.URL)
	owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"})
	reseller.do("POST", "/login", map[string]string{"username": "seller", "password": "seller-pass-1"})

	ids := map[string]float64{}
	// in this order: the first spelling of a tag ("VIP") is the one listed
	for _, x := range []struct {
		name string
		tags []string
	}{{"ali", []string{"VIP", "iran"}}, {"sara", []string{"vip"}}, {"reza", nil}} {
		code, out := owner.do("POST", "/users", map[string]any{"name": x.name, "tags": x.tags})
		if code != 200 {
			t.Fatalf("create %s: %d %v", x.name, code, out)
		}
	}
	code, list := owner.doList("GET", "/users")
	if code != 200 {
		t.Fatal(code)
	}
	for _, u := range list {
		ids[u["name"].(string)] = u["id"].(float64)
	}
	if code, out := reseller.do("POST", "/users", map[string]any{"name": "mine", "tags": []string{"vip", "reseller"}}); code != 200 {
		t.Fatalf("reseller create: %d %v", code, out)
	}

	// the filter: every tag asked for
	if _, l := owner.doList("GET", "/users?tag=vip"); len(l) != 3 {
		t.Fatalf("vip: %d", len(l))
	}
	if _, l := owner.doList("GET", "/users?tag=vip&tag=IRAN"); len(l) != 1 || l[0]["name"] != "ali" {
		t.Fatalf("vip+iran: %v", l)
	}
	// counts, the most used first; a reseller sees only theirs
	if _, l := owner.doList("GET", "/users/tags"); len(l) != 3 || l[0]["tag"] != "VIP" || l[0]["count"] != 3.0 {
		t.Fatalf("tags: %v", l)
	}
	if _, l := reseller.doList("GET", "/users/tags"); len(l) != 2 {
		t.Fatalf("reseller tags: %v", l)
	}

	// bulk: add to three (one already has it), take off one
	code, out := owner.do("POST", "/users/bulk", map[string]any{"ids": []float64{ids["ali"], ids["sara"], ids["reza"]}, "action": "tag", "tag": " family "})
	if code != 200 || out["affected"] != 3.0 {
		t.Fatalf("tag: %d %v", code, out)
	}
	code, out = owner.do("POST", "/users/bulk", map[string]any{"ids": []float64{ids["ali"], ids["reza"]}, "action": "tag", "tag": "FAMILY"})
	if out["affected"] != 0.0 {
		t.Fatalf("tagged twice: %v", out)
	}
	owner.do("POST", "/users/bulk", map[string]any{"ids": []float64{ids["sara"]}, "action": "untag", "tag": "vip"})
	if code, _ := owner.do("POST", "/users/bulk", map[string]any{"ids": []float64{ids["sara"]}, "action": "tag", "tag": "  "}); code != 400 {
		t.Fatal("an empty tag")
	}
	sara, _ := st.UserByName("sara")
	if len(sara.Tags) != 1 || sara.Tags[0] != "family" {
		t.Fatalf("sara: %q", sara.Tags)
	}
	// edit replaces the list
	if code, _ := owner.do("PUT", "/users/"+itoa(int64(ids["ali"])), map[string]any{"tags": []string{"gold"}}); code != 200 {
		t.Fatal("edit")
	}
	if ali, _ := st.UserByName("ali"); len(ali.Tags) != 1 || ali.Tags[0] != "gold" {
		t.Fatalf("ali: %q", ali.Tags)
	}
	// a reseller cannot tag users that are not theirs
	if _, out := reseller.do("POST", "/users/bulk", map[string]any{"ids": []float64{ids["reza"]}, "action": "tag", "tag": "x"}); out["affected"] != 0.0 {
		t.Fatalf("reseller tagged another's user: %v", out)
	}
}

// doList is do for answers that are a JSON list.
func (c *client) doList(method, path string) (int, []map[string]any) {
	code, out := c.do(method, path, nil)
	var list []map[string]any
	raw, _ := out["list"].([]any)
	for _, v := range raw {
		if m, ok := v.(map[string]any); ok {
			list = append(list, m)
		}
	}
	return code, list
}
