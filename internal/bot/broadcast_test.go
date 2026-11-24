package bot

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

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// tgDouble is a Telegram that answers like the real one for some chats: one
// user blocked the bot, one hits the rate limit once, one gets an error.
type tgDouble struct {
	mu     sync.Mutex
	sent   []map[string]any
	limits map[int64]int // chat → 429 answers left
}

func (f *tgDouble) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	var fields map[string]any
	b, _ := io.ReadAll(r.Body)
	json.Unmarshal(b, &fields)
	reply := func(ok bool, desc string, result any) {
		json.NewEncoder(w).Encode(map[string]any{"ok": ok, "description": desc, "result": result})
	}
	switch method {
	case "getUpdates":
		time.Sleep(20 * time.Millisecond)
		reply(true, "", []any{})
		return
	case "getMe":
		reply(true, "", map[string]any{"username": "dunvex_test_bot"})
		return
	}
	chat := int64(0)
	if v, ok := fields["chat_id"].(float64); ok {
		chat = int64(v)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Contains(text(fields), "PARSEFAIL") {
		reply(false, "Bad Request: can't parse entities: unsupported start tag", nil)
		return
	}
	switch {
	case chat == 900:
		reply(false, "Forbidden: bot was blocked by the user", nil)
		return
	case chat == 902:
		reply(false, "Bad Request: message is too long", nil)
		return
	case f.limits[chat] > 0:
		f.limits[chat]--
		reply(false, "Too Many Requests: retry after 1", nil)
		return
	}
	fields["_method"] = method
	f.sent = append(f.sent, fields)
	reply(true, "", map[string]any{"message_id": 1})
}

func (f *tgDouble) to(chat int64) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, m := range f.sent {
		if v, _ := m["chat_id"].(float64); int64(v) == chat {
			out = append(out, m)
		}
	}
	return out
}

func waitDone(t *testing.T, b *Bot, id int64) Broadcast {
	t.Helper()
	for i := 0; i < 400; i++ {
		for _, x := range b.Broadcasts(0) {
			if x.ID == id && x.Done != 0 {
				return x
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("the broadcast did not finish")
	return Broadcast{}
}

func TestBroadcast(t *testing.T) {
	old := broadcastPace
	broadcastPace = time.Millisecond
	defer func() { broadcastPace = old }()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fake := &tgDouble{limits: map[int64]int{901: 1}}
	tg := httptest.NewServer(fake)
	defer tg.Close()
	st.SetSetting("tg_token", "123:test")
	st.SetSetting("tg_api", tg.URL)
	st.SetSetting("tg_admins", "42")
	st.SetSetting("tg_lang", "en")
	now := time.Now().Unix()
	add := func(name string, chat int64, expire int64, owner int64) {
		u := &store.User{Name: name, UUID: util.UUID(), Password: util.RandomString(16), SubToken: name + "-token-0123456789",
			Enabled: true, ExpireAt: expire, DataLimit: 10 << 30, TGChat: chat, OwnerID: owner}
		if err := st.CreateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	add("alice", 501, now+30*86400, 0)
	add("bob", 502, now+3*86400, 0)    // running out
	add("carol", 503, now-86400, 0)    // expired
	add("dave", 0, now+30*86400, 0)    // never linked Telegram
	add("erin", 900, now+30*86400, 0)  // blocked the bot
	add("frank", 901, now+30*86400, 0) // the rate limit once
	add("gina", 504, now+30*86400, 7)  // a reseller's user
	add("hank", 902, now+30*86400, 0)  // an error

	mgr := engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return protocol.Env{} })
	b := New(Deps{St: st, Mgr: mgr, Sys: sysinfo.NewSampler(), Version: "test",
		SubURL: func(u *store.User) string { return "https://example.com/sub/" + u.SubToken }})
	b.Reload()

	// audiences
	c := b.AudienceCounts(0, 7)
	if c[AudienceAll] != (Count{8, 7}) || c[AudienceActive] != (Count{7, 6}) || c[AudienceEnding] != (Count{1, 1}) || c[AudienceEnded] != (Count{1, 1}) {
		t.Fatalf("counts: %+v", c)
	}
	if c := b.AudienceCounts(7, 7); c[AudienceAll] != (Count{1, 1}) {
		t.Fatalf("a reseller's counts: %+v", c)
	}
	alice, _ := st.UserByName("alice")
	dave, _ := st.UserByName("dave")
	if c := b.SelectedCount(0, []int64{alice.ID, dave.ID}); c != (Count{2, 1}) {
		t.Fatalf("selected: %+v", c)
	}

	// what is refused before anything is sent
	for _, r := range []BroadcastRequest{
		{Text: "  ", Audience: AudienceAll},
		{Text: strings.Repeat("x", maxText+1), Audience: AudienceAll},
		{Text: "hi", Audience: "friends"},
		{Text: "hi", Audience: AudienceSelected},
		{Text: "<b>open", Audience: AudienceAll, HTML: true},
		{Text: "<script>x</script>", Audience: AudienceAll, HTML: true},
		{Text: "1 < 2", Audience: AudienceAll, HTML: true},
		{Text: "<b>x</i>", Audience: AudienceAll, HTML: true},
	} {
		if _, err := b.StartBroadcast(r); err == nil {
			t.Errorf("accepted: %+v", r)
		}
	}
	if err := (BroadcastRequest{Text: `<b>Hi</b> <a href="https://t.me/x">here</a> <tg-spoiler>s</tg-spoiler> 1 &lt; 2`, Audience: AudienceAll, HTML: true}).Check(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.StartBroadcast(BroadcastRequest{Text: "hi", Audience: AudienceSelected, UserIDs: []int64{dave.ID}}); err == nil || !strings.Contains(err.Error(), "linked") {
		t.Fatalf("nobody to reach: %v", err)
	}

	// a test goes to the bot's admins only, filled in with a real user
	if n, err := b.TestBroadcast(BroadcastRequest{Text: "Hi {name}", Audience: AudienceEnding}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if m := fake.to(42); len(m) != 1 || text(m[0]) != "🧪 Hi bob" {
		t.Fatalf("test: %v", m)
	}

	// everyone: placeholders filled per user, plain text escaped
	run, err := b.StartBroadcast(BroadcastRequest{Text: "Hi {name}: {days} days, {data} left <&> {link}", Audience: AudienceAll, Button: true, By: "boss"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Total != 7 {
		t.Fatalf("total %d", run.Total)
	}
	if run.ID <= 0 || run.ID >= 1<<53 {
		t.Fatalf("id %d does not survive JavaScript numbers", run.ID)
	}
	done := waitDone(t, b, run.ID)
	if done.Sent != 5 || done.Blocked != 1 || done.Failed != 1 || len(done.Errors) != 1 || !strings.Contains(done.Errors[0], "hank") {
		t.Fatalf("result: %+v", done)
	}
	am := fake.to(501)
	if len(am) != 1 || text(am[0]) != "Hi alice: 30 days, "+util.HumanBytes(10<<30)+" left &lt;&amp;&gt; https://example.com/sub/alice-token-0123456789" {
		t.Fatalf("alice got: %v", am)
	}
	if js, _ := json.Marshal(am[0]["reply_markup"]); !strings.Contains(string(js), `"callback_data":"me"`) {
		t.Fatalf("no account button: %s", js)
	}
	if len(fake.to(901)) != 1 {
		t.Fatal("frank did not get it after the rate limit")
	}
	if len(fake.to(0)) != 0 || len(fake.to(504)) != 1 {
		t.Fatal("dave (not linked) or gina")
	}
	if erin, _ := st.UserByName("erin"); erin.TGChat != 0 {
		t.Fatal("erin blocked the bot but is still linked")
	}
	if hank, _ := st.UserByName("hank"); hank.TGChat != 902 {
		t.Fatal("an ordinary error unlinked hank")
	}
	found := false
	for _, e := range st.Events(10) {
		found = found || strings.Contains(e.Message, "boss sent a message to 7 users: 5 delivered, 1 failed, 1 had blocked the bot")
	}
	if !found {
		t.Fatal("no event")
	}

	// one at a time, and it can be stopped
	broadcastPace = 150 * time.Millisecond
	slow, err := b.StartBroadcast(BroadcastRequest{Text: "slow", Audience: AudienceActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.StartBroadcast(BroadcastRequest{Text: "again", Audience: AudienceAll}); err == nil {
		t.Fatal("two at once")
	}
	time.Sleep(200 * time.Millisecond)
	if !b.CancelBroadcast(slow.ID) || b.CancelBroadcast(12345) {
		t.Fatal("cancel")
	}
	if s := waitDone(t, b, slow.ID); !s.Canceled || s.Sent+s.Failed+s.Blocked >= s.Total {
		t.Fatalf("stopped: %+v", s)
	}
	broadcastPace = time.Millisecond

	// a message Telegram cannot parse stops at the first user
	before := len(fake.sent)
	bad, err := b.StartBroadcast(BroadcastRequest{Text: "PARSEFAIL", Audience: AudienceActive})
	if err != nil {
		t.Fatal(err)
	}
	if x := waitDone(t, b, bad.ID); x.Sent != 0 || x.Failed != x.Total || len(fake.sent) != before {
		t.Fatalf("bad message: %+v", x)
	}

	// a reseller reaches only their own users and sees only their own history
	mine, err := b.StartBroadcast(BroadcastRequest{Text: "hi", Audience: AudienceAll, Owner: 7, By: "seller"})
	if err != nil || mine.Total != 1 {
		t.Fatal(mine, err)
	}
	waitDone(t, b, mine.ID)
	if list := b.Broadcasts(7); len(list) != 1 || list[0].By != "seller" {
		t.Fatalf("reseller history: %+v", list)
	}
	if list := b.Broadcasts(0); len(list) != 4 || list[0].ID != mine.ID {
		t.Fatalf("history: %d", len(list))
	}

	// a panel that stopped mid-send marks it interrupted at the next start
	b.saveBroadcast(Broadcast{ID: 1, At: now, Text: "x", Total: 3})
	b.markInterrupted()
	for _, x := range b.Broadcasts(0) {
		if x.ID == 1 && (!x.Interrupted || x.Done == 0) {
			t.Fatalf("not marked: %+v", x)
		}
	}
}

// The admin bot: pick the audience, send the text, see it as users will,
// confirm, and the progress message ends with the result.
func TestBroadcastFromTheBot(t *testing.T) {
	old := broadcastPace
	broadcastPace = time.Millisecond
	defer func() { broadcastPace = old }()
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
	for i, name := range []string{"alice", "bob"} {
		st.CreateUser(&store.User{Name: name, UUID: util.UUID(), Password: "p", SubToken: name + "-token-0123456789", Enabled: true, TGChat: int64(300 + i)})
	}
	mgr := engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return protocol.Env{} })
	b := New(Deps{St: st, Mgr: mgr, Sys: sysinfo.NewSampler(), Version: "test",
		SubURL: func(u *store.User) string { return "https://example.com/sub/" + u.SubToken }})
	b.Start()
	press := func(data string) {
		fake.push(map[string]any{"callback_query": map[string]any{"id": "c", "data": data,
			"message": map[string]any{"message_id": 9, "chat": map[string]any{"id": 42}}}})
	}

	fake.push(msg(42, "/broadcast"))
	menu := fake.waitFor(t, "audiences", func(m map[string]any) bool { return strings.Contains(text(m), "Who gets the message") })
	if js, _ := json.Marshal(menu["reply_markup"]); !strings.Contains(string(js), "Everyone · 2") {
		t.Fatalf("menu: %s", js)
	}
	press("bca:all")
	fake.waitFor(t, "ask for the text", func(m map[string]any) bool { return strings.Contains(text(m), "Send the message text") })
	fake.push(msg(42, "Hello {name}, <new servers> tonight"))
	pv := fake.waitFor(t, "preview", func(m map[string]any) bool { return strings.Contains(text(m), "This is how users get it") })
	if !strings.Contains(text(pv), "Hello alice, &lt;new servers&gt; tonight") {
		t.Fatalf("preview: %s", text(pv))
	}
	if js, _ := json.Marshal(pv["reply_markup"]); !strings.Contains(string(js), "Send to 2") {
		t.Fatalf("send button: %s", js)
	}
	press("bcs")
	fake.waitFor(t, "bob's message", func(m map[string]any) bool {
		return chatOf(m) == "301" && text(m) == "Hello bob, &lt;new servers&gt; tonight"
	})
	fake.waitFor(t, "the result", func(m map[string]any) bool {
		return m["_method"] == "editMessageText" && strings.Contains(text(m), "Delivered: 2 of 2")
	})
	if h := b.Broadcasts(0); len(h) != 1 || h[0].By != "bot (42)" {
		t.Fatalf("history: %+v", h)
	}
	// a user's text never starts a broadcast
	fake.push(msg(300, "/broadcast"))
	time.Sleep(300 * time.Millisecond)
	if len(b.Broadcasts(0)) != 1 {
		t.Fatal("a user started a broadcast")
	}
}
