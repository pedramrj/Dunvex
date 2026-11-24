package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
	"github.com/pedramrj/dunvex/internal/webhook"
)

func TestSupportAPI(t *testing.T) {
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

	// Telegram: record what goes where
	var mu sync.Mutex
	var sent []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			time.Sleep(20 * time.Millisecond)
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
			return
		}
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var chat, body string
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			r.ParseMultipartForm(8 << 20)
			chat, body = r.FormValue("chat_id"), r.FormValue("caption")
		} else {
			var f map[string]any
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &f)
			if c, ok := f["chat_id"].(float64); ok {
				chat = strconv.FormatInt(int64(c), 10)
			}
			body, _ = f["text"].(string)
			if e, ok := f["reaction"]; ok {
				js, _ := json.Marshal(e)
				body = string(js)
			}
		}
		mu.Lock()
		sent = append(sent, method+" "+chat+" "+body)
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1, "username": "b"}})
	}))
	defer tg.Close()
	saw := func(what string) bool {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			for _, s := range sent {
				if strings.Contains(s, what) {
					mu.Unlock()
					return true
				}
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
		}
		return false
	}
	st.SetSetting("tg_token", "123:test")
	st.SetSetting("tg_api", tg.URL)
	st.SetSetting("tg_admins", "42")
	st.SetSetting("tg_lang", "en")
	alice := &store.User{Name: "alice", UUID: util.UUID(), Password: "p", SubToken: "alice-token-0123456789", Enabled: true, TGChat: 500, DataLimit: 10 << 30}
	sam := &store.User{Name: "sam", UUID: util.UUID(), Password: "p", SubToken: "sam-token-0123456789", Enabled: true, TGChat: 600, OwnerID: seller.ID}
	st.CreateUser(alice)
	st.CreateUser(sam)
	now := time.Now().Unix()
	ta, _, _ := st.SupportIncoming(store.SupportIn{Chat: 500, UserID: alice.ID, Name: "Alice", Text: "help", TGMsg: 31, At: now - 60})
	ts1, _, _ := st.SupportIncoming(store.SupportIn{Chat: 600, UserID: sam.ID, OwnerID: seller.ID, Name: "Sam", Text: "slow", Photo: "sam.jpg", TGMsg: 41, At: now - 30})
	tgst, _, _ := st.SupportIncoming(store.SupportIn{Chat: 700, Name: "Guest", Text: "price?", At: now})
	os.MkdirAll(filepath.Join(dir, "support"), 0o700)
	os.WriteFile(filepath.Join(dir, "support", "sam.jpg"), []byte{0xFF, 0xD8, 0xFF, 0xE0, 1, 2, 3}, 0o600)

	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner, reseller := newClient(t, ts.URL), newClient(t, ts.URL)
	owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"})
	reseller.do("POST", "/login", map[string]string{"username": "seller", "password": "seller-pass-1"})
	path := func(id int64, rest string) string { return "/support/threads/" + strconv.FormatInt(id, 10) + rest }

	// without the bot: the inbox still reads, answers cannot go
	if code, out := owner.do("GET", "/support", nil); code != 200 || len(out["threads"].([]any)) != 3 {
		t.Fatalf("list without a bot: %d %v", code, out)
	}
	if code, _ := owner.do("POST", path(ta.ID, "/reply"), map[string]string{"text": "hi"}); code != 409 {
		t.Fatalf("reply without a bot: %d", code)
	}

	tgBot := bot.New(bot.Deps{St: st, Mgr: srv.Mgr, Sys: srv.Sys, Version: "test", SupportDir: srv.SupportDir(),
		SubURL: func(u *store.User) string { return "https://x/" + u.SubToken }})
	tgBot.Reload()
	srv.Bot = tgBot

	// the owner sees every thread, with the user beside it; the reseller only theirs
	_, out := owner.do("GET", "/support", nil)
	threads := out["threads"].([]any)
	first := threads[0].(map[string]any)
	if first["chat"] != 700.0 || first["user"] != nil || first["waiting"] != true {
		t.Fatalf("newest first, a guest: %v", first)
	}
	if c := out["counts"].(map[string]any); c["open"] != 3.0 || c["unread"] != 3.0 || out["bot"].(map[string]any)["configured"] != true {
		t.Fatalf("owner counts: %v", out)
	}
	al := threads[2].(map[string]any)["user"].(map[string]any)
	if al["name"] != "alice" || al["status"] != "active" || al["dataLimit"] != float64(10<<30) {
		t.Fatalf("alice beside her thread: %v", al)
	}
	if third := threads[1].(map[string]any)["user"].(map[string]any); third["owner"] != "seller" {
		t.Fatalf("the reseller's name for the owner: %v", third)
	}
	_, out = reseller.do("GET", "/support", nil)
	if l := out["threads"].([]any); len(l) != 1 || l[0].(map[string]any)["chat"] != 600.0 {
		t.Fatalf("reseller list: %v", l)
	}
	if code, out := reseller.do("GET", "/support/unread", nil); code != 200 || out["unread"] != 1.0 {
		t.Fatalf("reseller badge: %v", out)
	}
	if code, _ := reseller.do("GET", path(ta.ID, ""), nil); code != 404 {
		t.Fatalf("reseller opened another's thread: %d", code)
	}
	if code, _ := reseller.do("POST", path(tgst.ID, "/reply"), map[string]string{"text": "x"}); code != 404 {
		t.Fatalf("reseller answered a guest: %d", code)
	}
	_, out = owner.do("GET", "/support?q=sam", nil)
	if l := out["threads"].([]any); len(l) != 1 {
		t.Fatalf("search by user: %d", len(l))
	}

	// the reseller opens their thread: seen, and the seen receipt to the person
	code, out := reseller.do("GET", path(ts1.ID, ""), nil)
	if code != 200 || len(out["messages"].([]any)) != 1 || out["thread"].(map[string]any)["user"].(map[string]any)["name"] != "sam" {
		t.Fatalf("reseller thread: %d %v", code, out)
	}
	if code, out := reseller.do("POST", path(ts1.ID, "/seen"), nil); code != 200 || out["seen"] != 1.0 {
		t.Fatalf("seen: %d %v", code, out)
	}
	if !saw("setMessageReaction 600 ") || !saw(bot.ReceiptSeen) {
		t.Fatal("no seen receipt")
	}

	// photos: the reseller gets their own, not another's
	req, _ := http.NewRequest("GET", ts.URL+"/dunvex/api/support/photos/sam.jpg", nil)
	if res, err := reseller.hc.Do(req); err != nil || res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("photo: %v %v", res, err)
	}
	if code, _ := owner.do("GET", "/support/photos/..%2Fdata.db", nil); code != 404 {
		t.Fatalf("a path out of the folder: %d", code)
	}

	// answers: text, then a photo in a form; the person sees the support line, the admin's name stays inside
	if code, out := reseller.do("POST", path(ts1.ID, "/reply"), map[string]string{"text": "restart the app"}); code != 200 ||
		out["message"].(map[string]any)["by"] != "seller" || out["thread"].(map[string]any)["waiting"] != false {
		t.Fatalf("reply: %d %v", code, out)
	}
	if !saw("sendMessage 600 📡 <b>Support line</b>\n\nrestart the app") || !saw(bot.ReceiptAnswered) {
		t.Fatal("the answer did not reach the person")
	}
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	mw.WriteField("text", "like this")
	fw, _ := mw.CreateFormFile("photo", "x.png")
	fw.Write(append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...))
	mw.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/dunvex/api"+path(ta.ID, "/reply"), &form)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Dunvex", "1")
	res, err := owner.hc.Do(req)
	if err != nil || res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("photo reply: %v %s", err, b)
	}
	if !saw("sendPhoto 500 📡 <b>Support line</b>\n\nlike this") {
		t.Fatal("the photo answer did not go")
	}
	if code, out := owner.do("POST", path(ta.ID, "/reply"), map[string]string{"text": "  "}); code != 400 || !strings.Contains(out["error"].(string), "write the answer") {
		t.Fatalf("empty reply: %d %v", code, out)
	}

	// typing, close and reopen, delete
	if code, _ := owner.do("POST", path(ta.ID, "/typing"), nil); code != 204 || !saw("sendChatAction 500") {
		t.Fatalf("typing: %d", code)
	}
	if code, out := owner.do("PUT", path(ta.ID, ""), map[string]bool{"open": false}); code != 200 || out["open"] != false || out["closedBy"] != "boss" {
		t.Fatalf("close: %d %v", code, out)
	}
	if _, out := owner.do("GET", "/support?status=closed", nil); len(out["threads"].([]any)) != 1 {
		t.Fatalf("closed filter: %v", out["threads"])
	}
	if code, _ := owner.do("PUT", path(ta.ID, ""), map[string]any{}); code != 400 {
		t.Fatalf("put without open: %d", code)
	}
	if code, _ := reseller.do("DELETE", path(ts1.ID, ""), nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "support", "sam.jpg")); !os.IsNotExist(err) {
		t.Fatal("the photo stayed after the thread was deleted")
	}

	// settings: owner only, checked
	if code, _ := reseller.do("GET", "/support/settings", nil); code != 403 {
		t.Fatalf("reseller settings: %d", code)
	}
	if code, out := owner.do("PUT", "/support/settings", map[string]any{"on": true, "maxLen": 5}); code != 400 || !strings.Contains(out["error"].(string), "50 to 4000") {
		t.Fatalf("bad settings: %d %v", code, out)
	}
	code, out = owner.do("PUT", "/support/settings", map[string]any{"on": true, "guests": true, "alert": false, "rate": 5, "maxLen": 500, "keep": 50, "days": 30, "note": " 9 to 21 "})
	if set := out["settings"].(map[string]any); code != 200 || set["note"] != "9 to 21" || set["rate"] != 5.0 || set["guests"] != true {
		t.Fatalf("settings: %d %v", code, out)
	}
	if s := bot.LoadSupport(st); s.Days != 30 || s.Keep != 50 || s.Alert {
		t.Fatalf("stored: %+v", s)
	}

	// support is an event kind for webhooks
	found := false
	for _, k := range webhook.Kinds {
		found = found || k == "support"
	}
	if !found {
		t.Fatal("no support kind for webhooks")
	}
}
