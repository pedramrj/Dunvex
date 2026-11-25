// Package bridge is the Dunvex Bridge: a resilient, encrypted, multi-carrier
// tunnel between two Dunvex servers we own. One server sits inside Iran (the
// relay), the other abroad (the target). Users keep their normal apps and
// connect to the Iranian relay with the usual protocols; the relay carries
// their bytes across the border through the Bridge, over whichever carriers
// are getting through right now.
//
// The Bridge is a drop-in transport under the reverse relay's smux: instead of
// one pinned-TLS link, it gives smux a net.Conn built as
//
//	smux  ->  kcp (reliability over lossy carriers)  ->  Transport (this package)
//
// The Transport is a net.PacketConn that spreads each kcp packet, encrypted,
// across the healthy carriers and reassembles the answers. Because we own both
// ends, the carriers can be unusual (raw ICMP, our own TCP, DNS) and need no
// support in the user's app.
//
// Roles. The server (the relay inside Iran) listens: it binds its carriers and
// answers the one peer that proves the pre-shared key. The client (the target
// abroad) dials: it opens its carriers toward the relay and keeps the session
// up. The abroad->Iran direction (client->server here) is the one filtering
// leaves alone more often, so most of a user's download rides the easy way;
// the Iran->abroad answers are paced under the reported volume triggers.
package bridge

import (
	"context"
	"crypto/rand"
	"errors"
	"sync/atomic"
	"time"
)

// errNoPeer is returned by a server-side carrier that has not yet learned the
// client's address (no valid frame received from it yet).
var errNoPeer = errors.New("bridge: carrier has no peer yet")

// errCarrierBusy is returned by a carrier whose send queue is full; the
// scheduler treats it as a dropped frame (kcp retransmits).
var errCarrierBusy = errors.New("bridge: carrier send queue full")

// unsupportedCarrier stands in for a raw-socket carrier on an OS that cannot
// provide it (everything but Linux), so the package builds everywhere and the
// panel can show why the carrier is unavailable.
type unsupportedCarrier struct{ name string }

func (u unsupportedCarrier) Name() string { return u.name }
func (u unsupportedCarrier) Up() bool     { return false }
func (u unsupportedCarrier) Start(func([]byte)) error {
	return errors.New("bridge: the " + u.name + " carrier needs Linux with raw sockets")
}
func (u unsupportedCarrier) Send([]byte) error { return errCarrierBusy }
func (u unsupportedCarrier) Close() error      { return nil }

// Carrier is one way to move opaque frames to the single peer. Each driver is
// small and independent: the session's encryption and the scheduler's health,
// pacing and failover all sit above it, so a carrier only has to move bytes
// best-effort, in whatever disguise it wears on the wire.
//
// A carrier is point to point. On the server it is bound and learns its peer
// from the first valid frame; on the client it is aimed at the server. Frames
// may be dropped, reordered or duplicated: the kcp layer above copes.
type Carrier interface {
	// Name is the carrier kind, e.g. "icmp" or "udp".
	Name() string
	// Send moves one frame toward the peer, best-effort. It may block briefly
	// for pacing but must not block forever.
	Send(frame []byte) error
	// Start begins receiving; every inbound frame is handed to recv. It
	// returns once the carrier is ready (listening or connected).
	Start(recv func(frame []byte)) error
	// Up reports whether the carrier can move frames right now (bound/dialed
	// and not in a backoff after an error). The scheduler also tracks liveness
	// with probes; Up is the carrier's own view.
	Up() bool
	// Close stops the carrier and frees its socket.
	Close() error
}

// CarrierStat is one carrier's live state, as the panel shows it.
type CarrierStat struct {
	Name     string `json:"name"`
	Up       bool   `json:"up"`
	RTTMs    int64  `json:"rttMs"`    // last probe round trip; -1 no answer, 0 not measured
	LossPct  int    `json:"lossPct"`  // probe loss over the recent window, 0-100
	SentB    int64  `json:"sentB"`    // bytes handed to Send since start
	RecvB    int64  `json:"recvB"`    // bytes received since start
	RateUp   int64  `json:"rateUp"`   // bytes/s out, recent
	RateDown int64  `json:"rateDown"` // bytes/s in, recent
	LastErr  string `json:"lastErr,omitempty"`
	LastFail int64  `json:"lastFail,omitempty"` // unix seconds of the last error
	CapMBps  int    `json:"capMBps,omitempty"`  // this carrier's send cap, MB/s (0 = none)
}

// counters is the per-carrier accounting shared by every driver.
type counters struct {
	sent     atomic.Int64 // frames
	recv     atomic.Int64
	sentB    atomic.Int64 // bytes
	recvB    atomic.Int64
	rttMs    atomic.Int64
	lossPct  atomic.Int64
	lastErr  atomic.Pointer[errAt]
	rateUp   atomic.Int64
	rateDown atomic.Int64
	lastUpB  int64 // for the rate sampler (scheduler-owned)
	lastDnB  int64
	lastRate time.Time
}

type errAt struct {
	msg string
	at  int64
}

func (c *counters) fail(err error) {
	if err != nil {
		c.lastErr.Store(&errAt{msg: err.Error(), at: time.Now().Unix()})
	}
}

func (c *counters) stat(name string, up bool, cap int) CarrierStat {
	s := CarrierStat{Name: name, Up: up, RTTMs: c.rttMs.Load(), LossPct: int(c.lossPct.Load()),
		SentB: c.sentB.Load(), RecvB: c.recvB.Load(), RateUp: c.rateUp.Load(), RateDown: c.rateDown.Load(), CapMBps: cap}
	if e := c.lastErr.Load(); e != nil {
		s.LastErr, s.LastFail = e.msg, e.at
	}
	return s
}

// maxFrame bounds a single carrier frame. kcp MTU is set well below this; the
// rest is the session header and any padding.
const maxFrame = 1400

// padTo rounds a plaintext frame's length up to the next bucket and appends
// random bytes, so a watcher sees a few fixed sizes instead of the real shape.
// The real length is carried inside the encrypted plaintext, so padding is
// invisible and free to vary.
var padBuckets = []int{64, 128, 256, 512, 1024, 1280}

func padLen(n int) int {
	for _, b := range padBuckets {
		if n <= b {
			return b
		}
	}
	return n // already large: no padding, it is a full data frame
}

// randFill fills b with random bytes (padding and nonces).
func randFill(b []byte) {
	_, _ = rand.Read(b)
}

// probeCtx is a short context for a liveness probe.
func probeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 4*time.Second)
}
