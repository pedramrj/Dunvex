package bot

import (
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

func TestBroadcastByTag(t *testing.T) {
	old := broadcastPace
	broadcastPace = time.Millisecond
	defer func() { broadcastPace = old }()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fake := &tgDouble{limits: map[int64]int{}}
	tg := httptest.NewServer(fake)
	defer tg.Close()
	st.SetSetting("tg_token", "123:test")
	st.SetSetting("tg_api", tg.URL)
	st.SetSetting("tg_admins", "42")
	st.SetSetting("tg_lang", "en")
	now := time.Now().Unix()
	add := func(name string, chat int64, owner int64, tags ...string) {
		u := &store.User{Name: name, UUID: util.UUID(), Password: util.RandomString(16), SubToken: name + "-token-0123456789",
			Enabled: true, ExpireAt: now + 30*86400, TGChat: chat, OwnerID: owner, Tags: tags}
		if err := st.CreateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	add("alice", 501, 0, "VIP", "family")
	add("bob", 0, 0, "vip")        // not linked
	add("carol", 503, 0, "family") // no vip
	add("gina", 504, 7, "vip")     // a reseller's user

	mgr := engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return protocol.Env{} })
	b := New(Deps{St: st, Mgr: mgr, Sys: sysinfo.NewSampler(), Version: "test",
		SubURL: func(u *store.User) string { return "https://example.com/sub/" + u.SubToken }})
	b.Reload()

	tc := b.TagCounts(0)
	if len(tc) != 2 || tc[0].Tag != "VIP" || tc[0].Count != (Count{3, 2}) || tc[1].Tag != "family" || tc[1].Count != (Count{2, 2}) {
		t.Fatalf("tag counts: %+v", tc)
	}
	if tc := b.TagCounts(7); len(tc) != 1 || tc[0].Count != (Count{1, 1}) {
		t.Fatalf("a reseller's tag counts: %+v", tc)
	}
	if _, err := b.StartBroadcast(BroadcastRequest{Text: "hi", Audience: AudienceTag, Tag: "  "}); err == nil || !strings.Contains(err.Error(), "tag") {
		t.Fatalf("no tag: %v", err)
	}
	run, err := b.StartBroadcast(BroadcastRequest{Text: "hi {name}", Audience: AudienceTag, Tag: "vip"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Total != 2 || run.Tag != "vip" {
		t.Fatalf("run: %+v", run)
	}
	if done := waitDone(t, b, run.ID); done.Sent != 2 {
		t.Fatalf("sent: %+v", done)
	}
	if len(fake.to(501)) != 1 || len(fake.to(504)) != 1 || len(fake.to(503)) != 0 {
		t.Fatal("the wrong users got it")
	}
	// a reseller's tag audience stays within their users
	mine, err := b.StartBroadcast(BroadcastRequest{Text: "x", Audience: AudienceTag, Tag: "family", Owner: 7})
	if err == nil {
		t.Fatalf("a reseller reached another's users: %+v", mine)
	}
}
