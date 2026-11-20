package bot

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// fakeTelegram answers the Bot API and records what the bot sends.
type fakeTelegram struct {
	mu      sync.Mutex
	queue   []map[string]any
	nextID  int64
	sent    []map[string]any // method + fields
	methods []string
}

func (f *fakeTelegram) push(u map[string]any) {
	f.mu.Lock()
	f.nextID++
	u["update_id"] = f.nextID
	f.queue = append(f.queue, u)
	f.mu.Unlock()
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	fields := map[string]any{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		r.ParseMultipartForm(8 << 20)
		for k, v := range r.MultipartForm.Value {
			fields[k] = v[0]
		}
	} else {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &fields)
	}
	var result any = true
	switch method {
	case "getMe":
		result = map[string]any{"id": 1, "username": "dunvex_test_bot"}
	case "getUpdates":
		time.Sleep(30 * time.Millisecond)
		f.mu.Lock()
		result = f.queue
		f.queue = nil
		f.mu.Unlock()
		if result == nil {
			result = []any{}
		}
	default:
		f.mu.Lock()
		fields["_method"] = method
		f.sent = append(f.sent, fields)
		f.mu.Unlock()
		result = map[string]any{"message_id": 1}
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func (f *fakeTelegram) waitFor(t *testing.T, what string, match func(m map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, m := range f.sent {
			if match(m) {
				f.mu.Unlock()
				return m
			}
		}
		f.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no %s; sent: %v", what, f.sent)
	return nil
}

func text(m map[string]any) string {
	s, _ := m["text"].(string)
	if c, ok := m["caption"].(string); ok {
		s += c
	}
	return s
}

func chatOf(m map[string]any) string {
	switch v := m["chat_id"].(type) {
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case string:
		return v
	}
	return ""
}

func jsonNum(v float64) string { b, _ := json.Marshal(v); return string(b) }

func msg(chat int64, s string) map[string]any {
	return map[string]any{"message": map[string]any{"message_id": 5, "text": s,
		"chat": map[string]any{"id": chat, "type": "private"}, "from": map[string]any{"first_name": "t"}}}
}

func TestBotFlows(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fake := &fakeTelegram{}
	tg := httptest.NewServer(fake)
	defer tg.Close()
	st.SetSetting("tg_token", "123:test")
	st.SetSetting("tg_api", tg.URL)
	st.SetSetting("tg_admins", "42")
	st.SetSetting("tg_lang", "en")

	mgr := engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return protocol.Env{} })
	newUser := func(name string) *store.User {
		return &store.User{Name: name, UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20), Enabled: true}
	}
	b := New(Deps{St: st, Mgr: mgr, Sys: sysinfo.NewSampler(), Version: "test", NewUser: newUser,
		SubURL: func(u *store.User) string { return "https://example.com/sub/" + u.SubToken }})
	b.Start()

	// admin opens the bot
	fake.push(msg(42, "/start"))
	fake.waitFor(t, "admin menu", func(m map[string]any) bool {
		return m["_method"] == "sendMessage" && strings.Contains(text(m), "Dunvex")
	})

	// admin creates a user in one line
	fake.push(msg(42, "/new ali 5 30 2"))
	fake.waitFor(t, "new user QR", func(m map[string]any) bool { return m["_method"] == "sendPhoto" && strings.Contains(text(m), "ali") })
	u, err := st.UserByName("ali")
	if err != nil {
		t.Fatal("user was not created")
	}
	if u.DataLimit != 5<<30 || u.IPLimit != 2 || u.ExpireAt == 0 {
		t.Fatalf("plan not applied: %+v", u)
	}

	// status button
	fake.push(map[string]any{"callback_query": map[string]any{"id": "c1", "data": "st",
		"message": map[string]any{"message_id": 9, "chat": map[string]any{"id": 42}}}})
	fake.waitFor(t, "status", func(m map[string]any) bool {
		return m["_method"] == "editMessageText" && strings.Contains(text(m), "CPU")
	})

	// +30 days button
	before := u.ExpireAt
	fake.push(map[string]any{"callback_query": map[string]any{"id": "c2", "data": "ua:" + jsonNum(float64(u.ID)) + ":d30",
		"message": map[string]any{"message_id": 9, "chat": map[string]any{"id": 42}}}})
	time.Sleep(300 * time.Millisecond)
	u2, _ := st.UserByID(u.ID)
	if u2.ExpireAt-before < 29*86400 {
		t.Fatalf("expiry not extended: %d → %d", before, u2.ExpireAt)
	}

	// a user links Telegram from the subscription page
	fake.push(msg(77, "/start "+u.SubToken))
	fake.waitFor(t, "user card", func(m map[string]any) bool { return m["_method"] == "sendPhoto" && chatOf(m) == "77" })
	u3, _ := st.UserByID(u.ID)
	if u3.TGChat != 77 {
		t.Fatal("chat not linked")
	}

	// a stranger gets their chat id, nothing else
	fake.push(msg(99, "/start"))
	fake.waitFor(t, "chat id reply", func(m map[string]any) bool { return chatOf(m) == "99" && strings.Contains(text(m), "99") })

	// a stranger cannot press admin buttons
	fake.push(map[string]any{"callback_query": map[string]any{"id": "c3", "data": "ua:" + jsonNum(float64(u.ID)) + ":delok",
		"message": map[string]any{"message_id": 9, "chat": map[string]any{"id": 99}}}})
	time.Sleep(300 * time.Millisecond)
	if _, err := st.UserByID(u.ID); err != nil {
		t.Fatal("a stranger deleted a user")
	}

	// alerts reach the admin
	st.Log("limit", "ali used 3 devices at once (limit 2) and is paused for 10 minutes")
	fake.waitFor(t, "alert", func(m map[string]any) bool { return chatOf(m) == "42" && strings.Contains(text(m), "devices at once") })

	// running low warns the user once
	u4, _ := st.UserByID(u.ID)
	u4.UsedDown = 0
	st.AddTraffic(time.Now(), map[int64]store.Traffic{u.ID: {Down: 5 << 30 * 9 / 10}}, nil)
	b.warnUsers()
	b.warnUsers()
	n := 0
	fake.mu.Lock()
	for _, m := range fake.sent {
		if chatOf(m) == "77" && strings.Contains(text(m), "80%") {
			n++
		}
	}
	fake.mu.Unlock()
	if n != 1 {
		t.Fatalf("80%% warning sent %d times", n)
	}
	if s := b.Status(); s["username"] != "dunvex_test_bot" || s["running"] != true {
		t.Fatalf("status = %v", s)
	}
}
