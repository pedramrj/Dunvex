package bridge

import (
	"errors"
	"net"
	"time"

	"github.com/xtaci/kcp-go/v5"
)

// CarrierOpt adds one carrier to a Bridge side, with a scheduling priority
// (higher is preferred while healthy) and an optional send cap in MB/s.
type CarrierOpt struct {
	Carrier  Carrier
	Priority int
	CapMBps  int
}

// Config is one Bridge side.
type Config struct {
	PSK       []byte   // the pre-shared secret both sides hold
	Static    KeyPair  // server (relay) only: its static X25519 key
	ServerPub [32]byte // client (target) only: the relay's pinned static public key
	Carriers  []CarrierOpt
}

func (cfg Config) states() []*carrierState {
	out := make([]*carrierState, 0, len(cfg.Carriers))
	for _, o := range cfg.Carriers {
		out = append(out, &carrierState{c: o.Carrier, priority: o.Priority, cap: newBucket(o.CapMBps * 1000 * 1000), capMBps: o.CapMBps})
	}
	return out
}

func tune(s *kcp.UDPSession) {
	s.SetStreamMode(true)
	s.SetNoDelay(1, 10, 2, 1) // fast: nodelay on, 10ms tick, resend 2, no congestion window
	s.SetWindowSize(1024, 1024)
	s.SetMtu(kcpMTU)
	s.SetACKNoDelay(true)
	s.SetWriteDelay(false)
}

// ---------------------------------------------------------------- server side (Iran relay)

// Listener accepts Bridge sessions from the one target abroad. Each accepted
// net.Conn is a reliable byte stream (kcp over the carriers) the caller runs
// smux over, exactly as the reverse tunnel runs smux over its TLS link.
type Listener struct {
	tr *Transport
	kl *kcp.Listener
}

// Listen starts the carriers and waits for the target to establish a session.
func Listen(cfg Config) (*Listener, error) {
	if len(cfg.Carriers) == 0 {
		return nil, errors.New("bridge: no carriers configured")
	}
	tr := newTransport(roleServer, cfg.PSK, cfg.Static, [32]byte{}, cfg.states())
	if err := tr.start(); err != nil {
		tr.Close()
		return nil, err
	}
	kl, err := kcp.ServeConn(nil, 0, 0, tr)
	if err != nil {
		tr.Close()
		return nil, err
	}
	return &Listener{tr: tr, kl: kl}, nil
}

// Accept returns the next session the target establishes (one at a time: a
// reconnecting target starts a new kcp session, like a reconnecting tunnel link).
func (l *Listener) Accept() (net.Conn, error) {
	s, err := l.kl.AcceptKCP()
	if err != nil {
		return nil, err
	}
	tune(s)
	return s, nil
}

// Stats is every carrier's live state.
func (l *Listener) Stats() []CarrierStat { return l.tr.Stats() }

// ReorderDepth is the largest out-of-order gap the replay window has accepted,
// for tuning the window against real carrier reordering.
func (l *Listener) ReorderDepth() int64 { return l.tr.ReorderDepth() }

// Close stops the listener and the carriers.
func (l *Listener) Close() error {
	l.kl.Close()
	return l.tr.Close()
}

// ---------------------------------------------------------------- client side (target abroad)

// Conn is one established Bridge session on the client side: the kcp stream
// the caller runs smux over, plus live carrier stats.
type Conn struct {
	net.Conn
	tr *Transport
}

// Stats is every carrier's live state.
func (c *Conn) Stats() []CarrierStat { return c.tr.Stats() }

// Dial opens the carriers toward the relay, does the handshake, and returns a
// reliable byte stream. The caller runs smux over it (as a server, to match
// the relay opening streams).
func Dial(cfg Config, handshakeTimeout time.Duration) (*Conn, error) {
	if len(cfg.Carriers) == 0 {
		return nil, errors.New("bridge: no carriers configured")
	}
	if handshakeTimeout <= 0 {
		handshakeTimeout = 20 * time.Second
	}
	tr := newTransport(roleClient, cfg.PSK, KeyPair{}, cfg.ServerPub, cfg.states())
	if err := tr.start(); err != nil {
		tr.Close()
		return nil, err
	}
	if err := tr.clientHandshake(handshakeTimeout); err != nil {
		tr.Close()
		return nil, err
	}
	s, err := kcp.NewConn2(bridgeAddr{}, nil, 0, 0, tr)
	if err != nil {
		tr.Close()
		return nil, err
	}
	tune(s)
	return &Conn{Conn: s, tr: tr}, nil
}
