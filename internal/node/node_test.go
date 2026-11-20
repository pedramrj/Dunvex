package node

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
)

// startNode runs an agent (no cores installed) behind real TLS.
func startNode(t *testing.T) (*Agent, Token, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "node.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	env := protocol.Env{Host: "127.0.0.1"}
	a := &Agent{Version: "test", St: st, Sys: sysinfo.NewSampler(), Env: func() protocol.Env { return env }}
	a.Mgr = engine.NewManager(st, filepath.Join(dir, "bin"), filepath.Join(dir, "run"), a.Env)
	cert, _, err := LoadOrCreateCert(filepath.Join(dir, "certs"), "agent", "dunvex-node")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(a.Handler())
	ts.TLS = a.TLSConfig(cert)
	ts.StartTLS()
	t.Cleanup(ts.Close)
	host, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	_ = port
	tok, err := Init(st, filepath.Join(dir, "certs"), host, ts.Listener.Addr().(*net.TCPAddr).Port, false)
	if err != nil {
		t.Fatal(err)
	}
	return a, tok, dir
}

func TestTokenRoundTrip(t *testing.T) {
	tok := Token{Addr: "203.0.113.5:62050", Fingerprint: strings.Repeat("ab", 32), Secret: strings.Repeat("x", 32)}
	got, err := ParseToken("  " + tok.String() + "\n")
	if err != nil || got != tok {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	for _, bad := range []string{"", "hello", "dvxnode1.!!", Token{Addr: "nohost", Fingerprint: tok.Fingerprint, Secret: tok.Secret}.String(),
		Token{Addr: tok.Addr, Fingerprint: "short", Secret: tok.Secret}.String(), Token{Addr: tok.Addr, Fingerprint: tok.Fingerprint, Secret: "s"}.String()} {
		if _, err := ParseToken(bad); err == nil {
			t.Errorf("should refuse %q", bad)
		}
	}
}

func TestPairingIsPinnedBothWays(t *testing.T) {
	a, tok, dir := startNode(t)
	ctx := context.Background()
	panel, _, _ := LoadOrCreateCert(filepath.Join(dir, "panel"), "panel-client", "dunvex-panel")
	other, _, _ := LoadOrCreateCert(filepath.Join(dir, "other"), "panel-client", "dunvex-panel")

	c := NewClient(tok.Addr, tok.Fingerprint, panel)
	if _, err := c.Status(ctx); err == nil {
		t.Fatal("an unpaired node must not answer")
	}
	if err := c.Pair(ctx, "wrong-secret-wrong-secret-wrong!"); err == nil {
		t.Fatal("a wrong secret must not pair")
	}
	if err := c.Pair(ctx, tok.Secret); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if err := c.Pair(ctx, tok.Secret); err != nil {
		t.Fatalf("pairing again from the same panel is fine: %v", err)
	}
	if !Paired(a.St) || a.St.GetSetting(keySecret, "") != "" {
		t.Fatal("the secret must be spent")
	}
	if _, err := c.Status(ctx); err != nil {
		t.Fatalf("status after pairing: %v", err)
	}

	// another panel, even with the old secret, is refused at the TLS handshake
	thief := NewClient(tok.Addr, tok.Fingerprint, other)
	if err := thief.Pair(ctx, tok.Secret); err == nil {
		t.Fatal("a second panel must not take the node")
	}
	// and the panel refuses a node whose certificate is not the pinned one
	fake := NewClient(tok.Addr, strings.Repeat("00", 32), panel)
	if _, err := fake.Status(ctx); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a wrong node certificate must be refused, got %v", err)
	}

	// a reset makes a new secret and frees the node for a new panel
	tok2, _ := Init(a.St, filepath.Join(dir, "certs"), "127.0.0.1", 1, true)
	if tok2.Secret == "" || tok2.Secret == tok.Secret || Paired(a.St) {
		t.Fatal("reset should issue a fresh secret")
	}
}

func TestStateAndExactlyOnceTraffic(t *testing.T) {
	a, tok, dir := startNode(t)
	ctx := context.Background()
	panel, _, _ := LoadOrCreateCert(filepath.Join(dir, "panel"), "panel-client", "dunvex-panel")
	c := NewClient(tok.Addr, tok.Fingerprint, panel)
	if err := c.Pair(ctx, tok.Secret); err != nil {
		t.Fatal(err)
	}

	p := protocol.Get("vless-reality-vision")
	state := State{
		Inbounds: []*store.Inbound{{ID: 7, Preset: p.ID, Port: 24443, Enabled: true, Params: protocol.Defaults(p, nil, ""), NodeID: 3}},
		Users:    []User{{Name: "sara", UUID: "11111111-1111-1111-1111-111111111111", Password: "pw"}, {Name: "reza", UUID: "22222222-2222-2222-2222-222222222222", Password: "pw2", InboundIDs: []int64{7}}},
		Settings: map[string]string{"block_torrent": "false"},
	}
	st, err := c.Push(ctx, state)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if st.Users != 2 || st.Inbounds != 1 || st.Epoch == "" {
		t.Fatalf("status after push: %+v", st)
	}
	ins, _ := a.St.Inbounds()
	if len(ins) != 1 || ins[0].ID != 7 || ins[0].NodeID != 0 || ins[0].Tag() != "in-7" {
		t.Fatalf("the node keeps the panel's inbound ids: %+v", ins)
	}
	if a.St.GetSetting("block_torrent", "") != "false" {
		t.Fatal("settings")
	}

	// traffic counted on the node
	users, _ := a.St.Users()
	ids := map[string]int64{}
	for _, u := range users {
		ids[u.Name] = u.ID
	}
	a.St.AddTraffic(time.Now(), map[int64]store.Traffic{ids["sara"]: {Up: 100, Down: 1000}}, map[int64]store.Traffic{7: {Up: 100, Down: 1000}})

	b1, err := c.Stats(ctx, 0)
	if err != nil || b1.Seq != 1 || b1.Users["sara"] != (store.Traffic{Up: 100, Down: 1000}) || b1.Inbounds[7].Down != 1000 {
		t.Fatalf("first batch: %+v %v", b1, err)
	}
	// the reply was lost: without our ack the same batch comes again, unchanged by new traffic
	a.St.AddTraffic(time.Now(), map[int64]store.Traffic{ids["sara"]: {Up: 5, Down: 50}}, nil)
	again, _ := c.Stats(ctx, 0)
	if again.Seq != 1 || again.Users["sara"] != b1.Users["sara"] {
		t.Fatalf("unacknowledged batch must repeat: %+v", again)
	}
	// acknowledged: the next batch holds only what came after
	b2, _ := c.Stats(ctx, 1)
	if b2.Seq != 2 || b2.Users["sara"] != (store.Traffic{Up: 5, Down: 50}) {
		t.Fatalf("second batch: %+v", b2)
	}
	b3, _ := c.Stats(ctx, 2)
	if b3.Seq != 3 || len(b3.Users) != 0 {
		t.Fatalf("nothing new: %+v", b3)
	}

	// a user who leaves and comes back starts from zero without a negative delta
	state.Users = state.Users[1:]
	c.Push(ctx, state)
	state.Users = append(state.Users, User{Name: "sara", UUID: "11111111-1111-1111-1111-111111111111", Password: "pw"})
	c.Push(ctx, state)
	users, _ = a.St.Users()
	for _, u := range users {
		if u.Name == "sara" {
			a.St.AddTraffic(time.Now(), map[int64]store.Traffic{u.ID: {Up: 1, Down: 2}}, nil)
		}
	}
	b4, _ := c.Stats(ctx, 3)
	if b4.Users["sara"] != (store.Traffic{Up: 1, Down: 2}) {
		t.Fatalf("returning user: %+v", b4)
	}

	// an unknown protocol is refused instead of half-applied
	bad := State{Inbounds: []*store.Inbound{{ID: 9, Preset: "no-such-preset", Port: 1, Enabled: true}}}
	if _, err := c.Push(ctx, bad); err == nil {
		t.Fatal("unknown preset should fail")
	}
	_ = http.StatusOK
}
