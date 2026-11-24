package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/bot"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

func TestBroadcastAPI(t *testing.T) {
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
	if err := st.CreateAdmin(seller); err != nil {
		t.Fatal(err)
	}

	// Telegram: every message goes through; record who got what
	var mu sync.Mutex
	got := map[float64][]string{}
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var f map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &f)
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			time.Sleep(20 * time.Millisecond)
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			mu.Lock()
			got[f["chat_id"].(float64)] = append(got[f["chat_id"].(float64)], f["text"].(string))
			mu.Unlock()
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1, "username": "b"}})
	}))
	defer tg.Close()
	st.SetSetting("tg_token", "123:test")
	st.SetSetting("tg_api", tg.URL)
	st.SetSetting("tg_admins", "42")
	for i, name := range []string{"alice", "bob"} {
		st.CreateUser(&store.User{Name: name, UUID: util.UUID(), Password: "p", SubToken: name + "-token-0123456789", Enabled: true, TGChat: int64(500 + i)})
	}
	st.CreateUser(&store.User{Name: "sellers-sam", UUID: util.UUID(), Password: "p", SubToken: "sam-token-0123456789", Enabled: true, TGChat: 600, OwnerID: seller.ID})
	st.CreateUser(&store.User{Name: "carl", UUID: util.UUID(), Password: "p", SubToken: "carl-token-0123456789", Enabled: true})

	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	tgBot := bot.New(bot.Deps{St: st, Mgr: srv.Mgr, Sys: srv.Sys, Version: "test", SubURL: func(u *store.User) string { return "https://x/" + u.SubToken }})
	tgBot.Reload()
	srv.Bot = tgBot
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner, reseller := newClient(t, ts.URL), newClient(t, ts.URL)
	owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"})
	if code, _ := reseller.do("POST", "/login", map[string]string{"username": "seller", "password": "seller-pass-1"}); code != 200 {
		t.Fatal("reseller login")
	}

	_, out := owner.do("GET", "/broadcasts", nil)
	all := out["counts"].(map[string]any)["all"].(map[string]any)
	if all["users"] != 4.0 || all["linked"] != 3.0 || out["bot"].(map[string]any)["configured"] != true {
		t.Fatalf("owner: %v", out)
	}
	_, out = reseller.do("GET", "/broadcasts", nil)
	if all := out["counts"].(map[string]any)["all"].(map[string]any); all["users"] != 1.0 {
		t.Fatalf("reseller sees: %v", out["counts"])
	}

	// a test goes to the bot's admins; resellers cannot send one
	if code, out := owner.do("POST", "/broadcasts", map[string]any{"text": "Hello {name}", "audience": "all", "test": true}); code != 200 || out["sent"] != 1.0 {
		t.Fatalf("test: %d %v", code, out)
	}
	if code, _ := reseller.do("POST", "/broadcasts", map[string]any{"text": "x", "audience": "all", "test": true}); code != 403 {
		t.Fatalf("a reseller's test: %d", code)
	}
	if code, out := owner.do("POST", "/broadcasts", map[string]any{"text": "<b>x", "html": true, "audience": "all"}); code != 400 || !strings.Contains(out["error"].(string), "not closed") {
		t.Fatalf("bad html: %d %v", code, out)
	}
	alice, _ := st.UserByName("alice")
	carl, _ := st.UserByName("carl")
	if code, out := owner.do("POST", "/broadcasts/count", map[string]any{"userIds": []int64{alice.ID, carl.ID}}); code != 200 || out["users"] != 2.0 || out["linked"] != 1.0 {
		t.Fatalf("count: %d %v", code, out)
	}

	// the reseller reaches only their own user
	code, out := reseller.do("POST", "/broadcasts", map[string]any{"text": "Hi {name}", "audience": "all"})
	if code != 202 || out["total"] != 1.0 {
		t.Fatalf("reseller send: %d %v", code, out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got[600])
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	if len(got[600]) != 1 || got[600][0] != "Hi sellers-sam" || len(got[500]) != 0 || len(got[42]) != 1 {
		t.Fatalf("delivered: %v", got)
	}
	mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	_, out = owner.do("GET", "/broadcasts", nil)
	hist := out["history"].([]any)
	if len(hist) != 1 || hist[0].(map[string]any)["by"] != "seller" {
		t.Fatalf("history: %v", hist)
	}
	// nobody can stop what is not running, or someone else's
	if code, _ := reseller.do("POST", "/broadcasts/123/cancel", nil); code != 404 {
		t.Fatalf("cancel: %d", code)
	}
}
