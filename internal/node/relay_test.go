package node

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/relay"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
)

// A relay keeps forwarding after a restart, before its panel calls again.
func TestRelayForwardsSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	env := func() protocol.Env { return protocol.Env{Host: "127.0.0.1"} }
	mk := func() *Agent {
		a := &Agent{Version: "test", St: st, Sys: sysinfo.NewSampler(), Env: env, Relay: relay.New(nil)}
		a.Mgr = engine.NewManager(st, filepath.Join(dir, "bin"), filepath.Join(dir, "run"), env)
		return a
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	a := mk()
	if err := a.Apply(State{Forwards: []relay.Forward{{Name: "Reality", Listen: port, Network: "tcp", Target: "127.0.0.1:9"}}}); err != nil {
		t.Fatal(err)
	}
	if s := a.Status(); len(s.Forwards) != 1 || !s.Forwards[0].Listening {
		t.Fatalf("status: %+v", s.Forwards)
	}
	a.Relay.Close() // the process stops

	b := mk()
	defer b.Relay.Close()
	b.RestoreForwards()
	if st := b.Relay.Stats(); len(st) != 1 || st[0].Listen != port || !st[0].Listening {
		t.Fatalf("after a restart: %+v", st)
	}
	// a state without forwards (the node became a protocol server) stops them
	b.Apply(State{})
	if st := b.Relay.Stats(); len(st) != 0 {
		t.Errorf("forwards left: %+v", st)
	}
}
