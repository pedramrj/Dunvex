package bot

import (
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/backup"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
)

// A backup reaches every bot admin as a document; without a bot, nothing is due.
func TestSendFileToAdmins(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mgr := engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return protocol.Env{} })
	b := New(Deps{St: st, Mgr: mgr, Sys: sysinfo.NewSampler(), Version: "test"})
	if _, err := b.SendFileToAdmins("x.db.gz", []byte("x"), "c"); !errors.Is(err, backup.ErrNoTelegram) {
		t.Fatalf("no bot: %v", err)
	}
	fake := &fakeTelegram{}
	tg := httptest.NewServer(fake)
	defer tg.Close()
	st.SetSetting("tg_token", "123:test")
	st.SetSetting("tg_api", tg.URL)
	st.SetSetting("tg_admins", "42, 43")
	b.Reload()
	n, err := b.SendFileToAdmins("dunvex-1.db.gz", []byte("backup bytes"), "💾 Dunvex backup")
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	m := fake.waitFor(t, "document", func(m map[string]any) bool { return m["_method"] == "sendDocument" && chatOf(m) == "43" })
	if !strings.Contains(text(m), "Dunvex backup") {
		t.Fatalf("caption: %v", m)
	}
	if _, err := b.SendFileToAdmins("big", make([]byte, 50<<20), "c"); err == nil || !strings.Contains(err.Error(), "50 MB") {
		t.Fatalf("too big: %v", err)
	}
}
