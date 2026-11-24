package bot

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// a tiny JPEG start: enough for the content check
var jpeg = append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0}, bytes.Repeat([]byte{1}, 200)...)

func TestSupportInbox(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fake := &fakeTelegram{}
	// around the fake: files to download, and a chat that blocked the bot
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/file/") {
			w.Write(jpeg)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/getFile") {
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"file_path": "photos/p.jpg", "file_size": len(jpeg)}})
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.HasSuffix(r.URL.Path, "/sendMessage") && strings.Contains(string(body), `"chat_id":900`) {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "Forbidden: bot was blocked by the user"})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		fake.ServeHTTP(w, r)
	}))
	defer tg.Close()
	st.SetSetting("tg_token", "123:test")
	st.SetSetting("tg_api", tg.URL)
	st.SetSetting("tg_admins", "42")
	st.SetSetting("tg_lang", "en")
	ali := &store.User{Name: "ali", UUID: util.UUID(), Password: "p", SubToken: "ali-token-0123456789", Enabled: true, TGChat: 77, OwnerID: 5}
	st.CreateUser(ali)
	mgr := engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return protocol.Env{} })
	b := New(Deps{St: st, Mgr: mgr, Sys: sysinfo.NewSampler(), Version: "test", SupportDir: filepath.Join(dir, "support"),
		NewUser: func(name string) *store.User {
			return &store.User{Name: name, UUID: util.UUID(), SubToken: util.RandomString(20), Enabled: true}
		},
		SubURL: func(u *store.User) string { return "https://example.com/sub/" + u.SubToken }})
	b.Start()

	press := func(chat int64, data string) {
		fake.push(map[string]any{"callback_query": map[string]any{"id": "c", "data": data,
			"message": map[string]any{"message_id": 9, "chat": map[string]any{"id": chat}}}})
	}
	say := func(chat, id int64, s string) {
		fake.push(map[string]any{"message": map[string]any{"message_id": id, "text": s,
			"chat": map[string]any{"id": chat, "type": "private"}, "from": map[string]any{"first_name": "Ali", "username": "ali_tg"}}})
	}
	mark := func() int {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return len(fake.sent)
	}
	since := func(from int, match func(m map[string]any) bool) []map[string]any {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		var out []map[string]any
		for _, m := range fake.sent[from:] {
			if match(m) {
				out = append(out, m)
			}
		}
		return out
	}
	to := func(chat, what string) func(m map[string]any) bool {
		return func(m map[string]any) bool { return chatOf(m) == chat && strings.Contains(text(m), what) }
	}
	buttons := func(m map[string]any) string {
		switch v := m["reply_markup"].(type) {
		case string:
			return v
		default:
			js, _ := json.Marshal(v)
			return string(js)
		}
	}
	reaction := func(chat string, emoji string) func(m map[string]any) bool {
		return func(m map[string]any) bool {
			js, _ := json.Marshal(m["reaction"])
			return m["_method"] == "setMessageReaction" && chatOf(m) == chat && strings.Contains(string(js), emoji)
		}
	}
	quiet := func(what string, from int, match func(m map[string]any) bool) {
		t.Helper()
		time.Sleep(400 * time.Millisecond)
		if got := since(from, match); len(got) > 0 {
			t.Fatalf("%s: %v", what, got)
		}
	}

	// a stranger: support is for subscribers
	fake.push(msg(99, "/support"))
	fake.waitFor(t, "subscribers only", to("99", "for subscribers"))

	// the user's card has Support; it opens the support screen
	fake.push(msg(77, "/start"))
	card := fake.waitFor(t, "card", func(m map[string]any) bool { return m["_method"] == "sendPhoto" && chatOf(m) == "77" })
	if !strings.Contains(buttons(card), `"hp"`) {
		t.Fatalf("no Support button on the card: %s", buttons(card))
	}
	press(77, "hp")
	fake.waitFor(t, "support screen", to("77", "Type your message"))

	// a message: the arrived receipt on it, one "sent" note, the admins hear about it with Answer
	n := mark()
	say(77, 501, "my config does not connect")
	fake.waitFor(t, "arrived receipt", reaction("77", ReceiptArrived))
	fake.waitFor(t, "delivered", to("77", "Sent to the team"))
	alert := fake.waitFor(t, "admin alert", to("42", "my config does not connect"))
	th, err := st.SupportThreadByChat(77)
	if err != nil || th.UserID != ali.ID || th.OwnerID != 5 || th.Unread != 1 || th.Username != "ali_tg" {
		t.Fatalf("thread: %+v %v", th, err)
	}
	if !strings.Contains(text(alert), "ali") || !strings.Contains(buttons(alert), "hpr:"+strconv.FormatInt(th.ID, 10)) {
		t.Fatalf("alert: %s %s", text(alert), buttons(alert))
	}
	if ev := st.Events(5); len(ev) == 0 || ev[0].Kind != "support" || !strings.Contains(ev[0].Message, "ali wrote to support: my config") {
		t.Fatalf("event: %+v", ev)
	}

	// a second one right after: stored, no second note
	say(77, 502, "I use v2rayNG")
	fake.waitFor(t, "second alert", to("42", "I use v2rayNG"))
	if got := since(n, to("77", "Sent to the team")); len(got) != 1 {
		t.Fatalf("%d delivered notes", len(got))
	}

	// a photo with a caption: kept on disk, sent to the admins as a photo
	fake.push(map[string]any{"message": map[string]any{"message_id": 503, "caption": "the error",
		"photo": []map[string]any{{"file_id": "small"}, {"file_id": "big"}},
		"chat":  map[string]any{"id": 77, "type": "private"}, "from": map[string]any{"first_name": "Ali"}}})
	fake.waitFor(t, "photo to the admins", func(m map[string]any) bool {
		return m["_method"] == "sendPhoto" && chatOf(m) == "42" && strings.Contains(text(m), "the error")
	})
	msgs, _, _ := st.SupportMessages(th.ID, 0, 10)
	if len(msgs) != 3 || msgs[2].Photo == "" || msgs[2].Text != "the error" {
		t.Fatalf("messages: %+v", msgs)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "support", msgs[2].Photo)); err != nil || !bytes.Equal(data, jpeg) {
		t.Fatalf("photo file: %v", err)
	}

	// limits: too long, then too many in an hour
	set := LoadSupport(st)
	set.MaxLen, set.Rate = 60, 4
	if err := set.Check(); err != nil {
		t.Fatal(err)
	}
	SaveSupport(st, set)
	say(77, 504, strings.Repeat("long ", 20))
	fake.waitFor(t, "too long", to("77", "the line takes 60"))
	say(77, 505, "fourth")
	fake.waitFor(t, "fourth", to("42", "fourth"))
	n = mark()
	say(77, 506, "fifth")
	fake.waitFor(t, "slow down", to("77", "sent a lot this hour"))
	say(77, 507, "sixth")
	quiet("an alert over the rate", n, func(m map[string]any) bool { return to("42", "sixth")(m) || to("42", "fifth")(m) })
	if got := since(n, to("77", "sent a lot this hour")); len(got) != 1 {
		t.Fatalf("%d slow-down notes", len(got))
	}
	if msgs, _, _ := st.SupportMessages(th.ID, 0, 20); len(msgs) != 4 {
		t.Fatalf("over the rate kept: %d", len(msgs))
	}

	// the admin answers from Telegram: the person gets it from the support line, with the answered receipt
	n = mark()
	press(42, "hpr:"+strconv.FormatInt(th.ID, 10))
	fake.waitFor(t, "ask the answer", to("42", "Your answer to"))
	fake.push(msg(42, "try the second config"))
	answer := fake.waitFor(t, "answer", to("77", "try the second config"))
	if !strings.Contains(text(answer), "📡 <b>Support line</b>") || strings.Contains(text(answer), "bot (42)") || !strings.Contains(buttons(answer), "hpw") {
		t.Fatalf("answer: %s %s", text(answer), buttons(answer))
	}
	fake.waitFor(t, "answered receipt", reaction("77", ReceiptAnswered))
	fake.waitFor(t, "sent note", to("42", "Delivered to ali"))
	th, _ = st.SupportThreadByID(th.ID)
	if th.Waiting() || th.Unread != 0 || !th.LastStaff {
		t.Fatalf("after the answer: %+v", th)
	}
	for i := 0; i < 100 && len(since(n, reaction("77", ReceiptAnswered))) < 4; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if got := since(n, reaction("77", ReceiptAnswered)); len(got) != 4 {
		t.Fatalf("answered receipt on %d messages", len(got))
	}

	// from the panel: an answer with a photo
	if _, err := b.SupportReply(th.ID, "boss", "", nil); err == nil {
		t.Fatal("an empty answer went")
	}
	if _, err := b.SupportReply(th.ID, "boss", strings.Repeat("x", MaxSupportAnswer+1), nil); err == nil {
		t.Fatal("a too long answer went")
	}
	m, err := b.SupportReply(th.ID, "boss", "here is how", jpeg)
	if err != nil || !m.Staff || m.By != "boss" || m.Photo == "" {
		t.Fatalf("photo answer: %+v %v", m, err)
	}
	fake.waitFor(t, "photo answer", func(m map[string]any) bool {
		return m["_method"] == "sendPhoto" && chatOf(m) == "77" && strings.Contains(text(m), "here is how")
	})

	// the admin's own views: the list and a conversation
	press(42, "hpl")
	fake.waitFor(t, "list", func(m map[string]any) bool {
		return chatOf(m) == "42" && strings.Contains(text(m), "Waiting for an answer") && strings.Contains(buttons(m), "hpv:")
	})

	// Done ends writing: what follows is not for support
	press(77, "hpx")
	fake.waitFor(t, "done", to("77", "Line closed"))
	n = mark()
	say(77, 510, "hello?")
	fake.waitFor(t, "a card instead", func(m map[string]any) bool { return chatOf(m) == "77" && m["_method"] == "sendPhoto" })
	quiet("support took a message after Done", n, to("42", "hello?"))

	// Write back under an answer opens writing again; the admin opening it gives the seen receipt
	press(77, "hpw")
	fake.waitFor(t, "screen again", func(m map[string]any) bool {
		return to("77", "Type your message")(m) && strings.Contains(text(m), "here is how") && strings.Contains(text(m), "Answered")
	})
	SaveSupport(st, SupportSettings{On: true, Alert: false, Rate: 0, MaxLen: 2000, Keep: 300, Days: 180})
	n = mark()
	say(77, 511, "thanks, works now")
	time.Sleep(300 * time.Millisecond)
	quiet("an alert with alerts off", n, to("42", "thanks, works now"))
	press(42, "hpv:"+strconv.FormatInt(th.ID, 10))
	fake.waitFor(t, "seen receipt", reaction("77", ReceiptSeen))

	// guests may write when the owner allows it; a blocked chat gets nothing
	SaveSupport(st, SupportSettings{On: true, Guests: true, MaxLen: 2000})
	fake.push(msg(99, "/support"))
	fake.waitFor(t, "guest screen", to("99", "Type your message"))
	say(99, 600, "how much is a plan?")
	fake.waitFor(t, "guest delivered", to("99", "Sent to the team"))
	guest, err := st.SupportThreadByChat(99)
	if err != nil || guest.UserID != 0 || guest.OwnerID != 0 {
		t.Fatalf("guest thread: %+v %v", guest, err)
	}
	fake.push(map[string]any{"message": map[string]any{"message_id": 700, "text": "/support", "chat": map[string]any{"id": 900, "type": "private"}, "from": map[string]any{}}})
	say(900, 701, "hi")
	deadline := time.Now().Add(3 * time.Second)
	var blockedTh *store.SupportThread
	for time.Now().Before(deadline) && blockedTh == nil {
		blockedTh, _ = st.SupportThreadByChat(900)
		time.Sleep(20 * time.Millisecond)
	}
	if blockedTh == nil {
		t.Fatal("no thread for chat 900")
	}
	if _, err := b.SupportReply(blockedTh.ID, "boss", "hello", nil); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("answer to a blocked chat: %v", err)
	}
	if msgs, _, _ := st.SupportMessages(blockedTh.ID, 0, 10); len(msgs) != 1 {
		t.Fatalf("the undelivered answer was kept: %d", len(msgs))
	}

	// off: the button goes and /support says so
	SaveSupport(st, SupportSettings{On: false, MaxLen: 2000})
	fake.push(msg(77, "/support"))
	fake.waitFor(t, "off", to("77", "closed for now"))
	if kb := b.withSupport(77, nil); len(kb) != 0 {
		t.Fatal("a Support button while support is off")
	}

	// retention takes the photos of removed messages off the disk too
	if photos := must(os.ReadDir(filepath.Join(dir, "support"))); len(photos) != 2 {
		t.Fatalf("%d photos kept", len(photos))
	}
	SaveSupport(st, SupportSettings{On: true, MaxLen: 2000, Keep: 1})
	b.pruneSupport()
	if left := must(os.ReadDir(filepath.Join(dir, "support"))); len(left) != 0 {
		t.Fatalf("photos left: %d", len(left))
	}

	// the settings check
	for _, bad := range []SupportSettings{{MaxLen: 10}, {MaxLen: 100, Rate: -1}, {MaxLen: 100, Keep: 3}, {MaxLen: 100, Days: 99999}, {MaxLen: 100, Note: strings.Repeat("n", 301)}} {
		if bad.Check() == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func must[T any](v T, _ error) T { return v }
