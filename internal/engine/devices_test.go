package engine

import (
	"path/filepath"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"testing"
	"time"
)

func TestSingboxDeviceLines(t *testing.T) {
	d := newDevices()
	lines := []string{
		"+0330 2026-11-20 11:09:23 INFO [1546071910 0ms] inbound/vless[in-1]: inbound connection from 203.0.113.7:50801",
		"+0330 2026-11-20 11:09:23 INFO [1546071910 1ms] inbound/vless[in-1]: [alice] inbound connection to example.com:443",
		"+0330 2026-11-20 11:09:23 INFO [1546071910 2ms] outbound/direct[direct]: outbound connection to example.com:443",
		"+0330 2026-11-20 11:09:24 INFO [4103287332 0ms] inbound/hysteria2[in-2]: inbound packet connection from [2001:db8::5]:63740",
		"+0330 2026-11-20 11:09:24 INFO [4103287332 0ms] inbound/hysteria2[in-2]: [alice] inbound packet connection to 1.1.1.1:53",
	}
	for _, l := range lines {
		if !d.singboxLine(ansiRe.ReplaceAllString("\x1b[36m"+l+"\x1b[0m", "")) {
			t.Errorf("not recognised: %s", l)
		}
	}
	if d.singboxLine("+0330 2026-11-20 11:09:20 INFO sing-box started (0.15s)") {
		t.Error("ordinary lines must stay in the log")
	}
	got := d.list("alice", time.Now().Unix()-10)
	if len(got) != 2 {
		t.Fatalf("devices = %+v", got)
	}
	if c := d.counts(time.Now().Unix() - 10)["alice"]; c != 2 {
		t.Fatalf("count = %d", c)
	}
}

func TestDeviceLimitPauses(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u := &store.User{Name: "carol", UUID: "u", Password: "p", SubToken: "tok", Enabled: true, IPLimit: 1}
	st.CreateUser(u)
	m := NewManager(st, t.TempDir(), t.TempDir(), func() protocol.Env { return protocol.Env{} })
	now := time.Now().Unix()
	seen := func(at int64) {
		m.dev.see("carol", "198.51.100.1", at)
		m.dev.see("carol", "198.51.100.2", at)
	}
	users, _ := st.Users()
	seen(now)
	if m.enforceDevices(users, now) {
		t.Fatal("paused on the first sighting")
	}
	seen(now + 60)
	if m.enforceDevices(users, now+60) {
		t.Fatal("paused before the grace period")
	}
	seen(now + 125)
	if !m.enforceDevices(users, now+125) {
		t.Fatal("not paused after two minutes over the limit")
	}
	got, _ := st.UserByID(u.ID)
	if got.Status(now+130) != store.StatusPaused || got.CanConnect(now+130) {
		t.Fatalf("status = %s", got.Status(now+130))
	}
	if got.Status(got.PausedUntil+1) != store.StatusActive {
		t.Fatal("pause should end by itself")
	}
}
