package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/routing"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

func sshUser(t *testing.T, st *store.Store, name string, f func(*store.User)) *store.User {
	t.Helper()
	u := &store.User{Name: name, UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20), Enabled: true, ResetPeriod: "none"}
	if f != nil {
		f(u)
	}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	return u
}

// xrayInbound finds an inbound of a built Xray config by tag.
func xrayInbound(t *testing.T, cfg []byte, tag string) map[string]any {
	t.Helper()
	var c struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatalf("xray config: %v", err)
	}
	for _, ib := range c.Inbounds {
		if ib["tag"] == tag {
			return ib
		}
	}
	return nil
}

func TestSSHBuild(t *testing.T) {
	m, st := routingSetup(t)
	now := time.Now().Unix()
	alice := sshUser(t, st, "alice", nil)
	sshUser(t, st, "bob", func(u *store.User) { u.Enabled = false })
	sshUser(t, st, "carol", func(u *store.User) { u.ExpireAt = now - 60 })
	sshUser(t, st, "dave", func(u *store.User) { u.PausedUntil = now + 600 })
	sshUser(t, st, "erin", func(u *store.User) { u.DataLimit, u.UsedDown = 1000, 2000 })
	EnsureHealthKey(st)
	p := protocol.Get("ssh")
	in := &store.Inbound{Preset: p.ID, Port: 2222, Enabled: true, Params: protocol.Defaults(p, nil, "")}
	if err := st.CreateInbound(in); err != nil {
		t.Fatal(err)
	}
	b, err := m.build()
	if err != nil {
		t.Fatal(err)
	}
	if b.ssh == nil {
		t.Fatalf("no SSH server planned: %v", b.errs)
	}
	if b.ssh.listen != ":2222" || b.ssh.hostKey != in.Str("hostKey") || b.ssh.inbound != in.ID {
		t.Errorf("plan %+v", b.ssh)
	}
	if len(b.ssh.users) != 2 || b.ssh.users["alice"] != alice.Password || b.ssh.users[HealthName] == "" {
		t.Errorf("users %v: only alice and the config check may sign in (not the disabled, expired, paused or over-limit ones)", b.ssh.users)
	}
	ib := xrayInbound(t, b.xray, in.Tag())
	if ib == nil {
		t.Fatalf("no SOCKS inbound for the SSH server in Xray:\n%s", b.xray)
	}
	if ib["protocol"] != "socks" || ib["listen"] != "127.0.0.1" || !strings.HasSuffix(b.ssh.upstream, ":"+js(ib["port"])) || !strings.HasPrefix(b.ssh.upstream, "127.0.0.1:") {
		t.Errorf("SOCKS inbound %s, upstream %s", js(ib), b.ssh.upstream)
	}
	if set, _ := ib["settings"].(map[string]any); set["auth"] != "noauth" || set["udp"] != true || set["accounts"] != nil {
		t.Errorf("SOCKS settings %s: no accounts (the SSH server signs users in), UDP on", js(set))
	}

	// the routing rules are those of every Xray inbound
	routing.Save(st, routing.Settings{BlockDomains: []string{"blocked.example"}, Iran: routing.IranDirect, Warp: routing.WarpOff})
	if b, err = m.build(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b.xray), `"domain:blocked.example"`) {
		t.Errorf("the block list is not in Xray's routing:\n%s", b.xray)
	}

	// a change of users reaches the SSH server only: Xray's config stays the same
	alice.Enabled = false
	st.UpdateUser(alice)
	b2, err := m.build()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b2.ssh.users["alice"]; ok {
		t.Error("a disabled user is still among the SSH users")
	}
	if string(b2.xray) != string(b.xray) {
		t.Error("a change of users changed Xray's config (Xray would restart)")
	}

	// one per server
	p2 := &store.Inbound{Preset: p.ID, Port: 2223, Enabled: true, Params: protocol.Defaults(p, nil, "")}
	st.CreateInbound(p2)
	if b, _ = m.build(); !strings.Contains(b.errs[p2.ID], "only one SSH") {
		t.Errorf("a second SSH: %q", b.errs[p2.ID])
	}
	st.DeleteInbound(p2.ID)

	// nobody may use it: it stays off, and so does its SOCKS inbound
	st.SetSetting(HealthKeySetting, "")
	if b, _ = m.build(); b.ssh != nil || (b.xray != nil && xrayInbound(t, b.xray, in.Tag()) != nil) {
		t.Errorf("an SSH server without users: %+v", b.ssh)
	}
}

func TestSSHDefaultsKeepTheHostKey(t *testing.T) {
	p := protocol.Get("ssh")
	params := protocol.Defaults(p, nil, "")
	key, pub := params["hostKey"].(string), params["hostKeyPub"].(string)
	if key == "" || !strings.HasPrefix(pub, "ssh-ed25519 ") {
		t.Fatalf("defaults %v", params)
	}
	again := protocol.Defaults(p, map[string]any{"hostKey": key, "hostKeyPub": "tampered"}, "")
	if again["hostKey"] != key || again["hostKeyPub"] != pub {
		t.Errorf("the key changed: %v", again)
	}
	fresh := protocol.Defaults(p, map[string]any{"hostKey": ""}, "")
	if fresh["hostKey"] == key || fresh["hostKeyPub"] == pub {
		t.Error("clearing the key did not make a new one")
	}
}
