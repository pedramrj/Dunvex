package bot

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

func TestFreeTrial(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fake := &fakeTelegram{members: map[string]string{}}
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
	press := func(chat int64, data string) {
		fake.push(map[string]any{"callback_query": map[string]any{"id": "c", "data": data,
			"message": map[string]any{"message_id": 9, "chat": map[string]any{"id": chat}}}})
	}
	sentTo := func(chat string, what string) map[string]any {
		return fake.waitFor(t, what, func(m map[string]any) bool { return chatOf(m) == chat && strings.Contains(text(m), what) })
	}

	// off: a new person sees how to link a subscription, no trial
	fake.push(msg(700, "/start"))
	m := sentTo("700", "chat ID")
	if js, _ := json.Marshal(m["reply_markup"]); strings.Contains(string(js), `"tr"`) {
		t.Fatal("a trial button while trials are off")
	}

	// on: 512 MB for 6 hours, one device, two protocols, two a day
	set := LoadTrial(st)
	set.On, set.DataMB, set.Hours, set.Devices, set.Inbounds, set.Daily = true, 512, 6, 1, []int64{3, 5}, 2
	if err := set.Check(); err != nil {
		t.Fatal(err)
	}
	SaveTrial(st, set)
	fake.push(msg(700, "/start"))
	m = sentTo("700", "try the service for free")
	if js, _ := json.Marshal(m["reply_markup"]); !strings.Contains(string(js), `"callback_data":"tr"`) || !strings.Contains(text(m), "512") {
		t.Fatalf("offer: %v %s", text(m), js)
	}
	press(700, "tr")
	sentTo("700", "Your trial is ready")
	fake.waitFor(t, "QR", func(m map[string]any) bool { return m["_method"] == "sendPhoto" && chatOf(m) == "700" })
	u, err := st.UserByTG(700)
	if err != nil {
		t.Fatal("no trial user")
	}
	now := time.Now().Unix()
	if !strings.HasPrefix(u.Name, "trial-") || u.DataLimit != 512<<20 || u.IPLimit != 1 || len(u.InboundIDs) != 2 ||
		u.ExpireAt < now+6*3600-60 || u.ExpireAt > now+6*3600+60 || !strings.Contains(u.Note, "trial") {
		t.Fatalf("trial user: %+v", u)
	}

	// right after: no "1 day left" note for a 6-hour trial
	b.warnUsers()
	time.Sleep(200 * time.Millisecond)
	for _, m := range fake.sent {
		if chatOf(m) == "700" && strings.Contains(text(m), "🔔") {
			t.Fatalf("an early warning: %s", text(m))
		}
	}

	// one per Telegram account, even once the trial user is deleted
	st.DeleteUser(u.ID)
	press(700, "tr")
	sentTo("700", "already had your free trial")

	// the daily cap: the second one today, then no more
	press(701, "tr")
	sentTo("701", "Your trial is ready")
	press(702, "tr")
	sentTo("702", "Today's free trials are gone")

	// a channel to join first
	set.Daily, set.Channel = 0, "https://t.me/dvxchan"
	if err := set.Check(); err != nil || set.Channel != "@dvxchan" {
		t.Fatal(set.Channel, err)
	}
	SaveTrial(st, set)
	press(703, "tr")
	m = sentTo("703", "join @dvxchan first")
	if js, _ := json.Marshal(m["reply_markup"]); !strings.Contains(string(js), "https://t.me/dvxchan") {
		t.Fatalf("join button: %s", js)
	}
	fake.mu.Lock()
	fake.members["703"] = "member"
	fake.mu.Unlock()
	press(703, "tr")
	sentTo("703", "Your trial is ready")

	// the end: one note, with no running-out notes before it
	u2, _ := st.UserByTG(701)
	u2.ExpireAt = now - 60
	st.UpdateUser(u2)
	b.warnUsers()
	sentTo("701", "Your free trial has ended")

	// cleanup: ended long ago and not bought → removed; a renewed one stays
	st.SetTrialUser(701, u2.ID, u2.Name, now-4*86400)
	u3, _ := st.UserByTG(703)
	st.SetTrialUser(703, u3.ID, u3.Name, now-4*86400)
	u3.ExpireAt = now + 30*86400 // renewed by hand
	st.UpdateUser(u3)
	b.cleanTrials()
	if _, err := st.UserByID(u2.ID); err == nil {
		t.Fatal("the ended trial was not removed")
	}
	if _, err := st.UserByID(u3.ID); err != nil {
		t.Fatal("a renewed trial was removed")
	}
	if !st.HasTrial(701) {
		t.Fatal("the record went with the user")
	}
	total, day, bought := st.TrialStats(now - 86400)
	if total != 3 || day != 3 || bought != 0 {
		t.Fatalf("stats %d %d %d", total, day, bought)
	}

	// settings that make no sense are refused
	for _, bad := range []TrialSettings{{Hours: 0}, {Hours: 5, DataMB: -1}, {Hours: 5, Channel: "dvxchan"}, {Hours: 24 * 31}} {
		if err := bad.Check(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
