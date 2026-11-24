package engine

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// The check's user is on every inbound it has a client for, even one no real
// user may use yet, and nowhere it would count.
func TestHealthUserInConfigs(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if HealthUser(st) != nil {
		t.Fatal("a health user before the key exists")
	}
	key := EnsureHealthKey(st)
	if EnsureHealthKey(st) != key || len(key) != 64 {
		t.Fatal("the key changed")
	}
	h1, h2 := HealthUser(st), HealthUser(st)
	if h1 == nil || h1.UUID != h2.UUID || h1.Password != h2.Password || h1.WGPublic != h2.WGPublic || h1.Name != HealthName {
		t.Fatalf("not stable: %+v %+v", h1, h2)
	}
	if pub, _ := util.X25519Public(strings.NewReplacer("+", "-", "/", "_", "=", "").Replace(h1.WGPrivate)); pub == "" {
		t.Fatal("WireGuard key")
	}

	vless := &store.Inbound{Preset: "vless-reality-vision", Port: 31001, Enabled: true, Params: protocol.Defaults(protocol.Get("vless-reality-vision"), nil, "")}
	tuic := &store.Inbound{Preset: "tuic", Port: 31002, Enabled: true, Params: protocol.Defaults(protocol.Get("tuic"), nil, "")}
	for _, in := range []*store.Inbound{vless, tuic} {
		if err := st.CreateInbound(in); err != nil {
			t.Fatal(err)
		}
	}
	// alice may use only the VLESS inbound: TUIC has no real user
	st.CreateUser(&store.User{Name: "alice", UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20), Enabled: true, InboundIDs: []int64{vless.ID}})

	cert, ckey, err := util.SelfSignedCert(filepath.Join(dir, "certs"), "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	env := protocol.Env{Host: "203.0.113.10", CertFile: cert, KeyFile: ckey, SelfSigned: true}
	m := NewManager(st, filepath.Join(dir, "bin"), filepath.Join(dir, "run"), func() protocol.Env { return env })
	x, s, errs, active, err := m.Configs()
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	if !strings.Contains(string(x), `"email": "~health"`) || !strings.Contains(string(x), `"email": "alice"`) || !strings.Contains(string(x), h1.UUID) {
		t.Fatalf("Xray inbound without the check's user:\n%s", x)
	}
	if !strings.Contains(string(s), `"name": "~health"`) {
		t.Fatalf("TUIC (no real user) did not start for the check:\n%s", s)
	}
	var sb map[string]any
	json.Unmarshal(s, &sb)
	if exp, _ := json.Marshal(sb["experimental"]); strings.Contains(string(exp), "~health") {
		t.Fatalf("the check's user in the traffic stats: %s", exp)
	}
	if active[HealthName] {
		t.Fatal("the check's user counted as an active user")
	}

	// its connections are no device
	m.dev.see(HealthName, "203.0.113.10", time.Now().Unix())
	if m.DeviceCounts()[HealthName] != 0 {
		t.Fatal("the check's user has devices")
	}

	// a restored database with a real "~health" user keeps that user as it is
	st.CreateUser(&store.User{Name: HealthName, UUID: util.UUID(), Password: "x", SubToken: util.RandomString(20), Enabled: true})
	x, _, _, _, _ = m.Configs()
	if strings.Contains(string(x), h1.UUID) {
		t.Fatal("the check's user was added next to a real user of the same name")
	}
}
