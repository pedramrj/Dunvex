package bridge

// Transport is the net.PacketConn that kcp runs on. It owns the carriers, the
// session, and the scheduler that decides which carrier each packet rides,
// probes them for liveness, paces each under its cap, and fails over the
// instant one dies — all without the kcp or smux layers above noticing more
// than a little loss. A kcp packet handed to WriteTo is wrapped as an inner
// data frame, padded to a size bucket, sealed by the session and sent over the
// healthiest carrier; an inbound frame is opened, and data goes to the kcp
// read queue while probe and pong frames stay here.

import (
	"encoding/binary"
	"errors"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type role int

const (
	roleClient role = iota // the target abroad: dials carriers, sends Init
	roleServer             // the relay in Iran: binds carriers, answers Init
)

// inner frame types (inside the AEAD).
const (
	innerData  = 0x00
	innerProbe = 0x01
	innerPong  = 0x02
)

const (
	kcpMTU     = 1277 // a full inner data frame lands on the top size bucket, so bulk packets are not padded
	probeEvery = 1 * time.Second
	probeMiss  = 2 // probes missed in a row before a carrier is marked down (~2 s failover)
	lossWindow = 16
)

// bridgeAddr is the single fixed peer address kcp keys its one session on.
type bridgeAddr struct{}

func (bridgeAddr) Network() string { return "dvxb" }
func (bridgeAddr) String() string  { return "bridge" }

// Transport carries kcp packets across the carriers.
type Transport struct {
	role role
	psk  []byte

	// server: our static key; client: the pinned server static key.
	static KeyPair
	srvPub [32]byte

	carriers []*carrierState
	sess     atomic.Pointer[session]

	// client handshake: the pending session awaiting a Resp.
	pending   *session
	confirmed chan struct{}
	confirmOK atomic.Bool

	// server: a fresh session was installed (restart the kcp accept side).
	onSession func()

	// server: ephemerals of Inits seen recently, so a replayed Init never
	// installs a second session or (past a short window) draws a Resp.
	hsMu    sync.Mutex
	seenEph map[[32]byte]seenInit

	inbound chan []byte
	closed  chan struct{}
	closeMu sync.Once

	rdl atomic.Int64 // read deadline, unix nano (0 = none)

	probes sync.Map // probe id (uint64) -> probeSent
	nextID atomic.Uint64
}

type probeSent struct {
	carrier int
	at      time.Time
}

// carrierState wraps a Carrier with the scheduler's bookkeeping.
type carrierState struct {
	c        Carrier
	priority int // higher is preferred when healthy
	cap      *bucket
	capMBps  int
	ct       counters
	misses   atomic.Int64 // probes missed in a row
	loss     lossRing
	down     atomic.Bool // scheduler's verdict (probes), separate from the carrier's own Up()
}

func (cs *carrierState) healthy() bool { return cs.c.Up() && !cs.down.Load() }

// seenInit remembers one Init's ephemeral, so replays are recognised.
type seenInit struct {
	sid     [sidLen]byte
	first   time.Time
	carrier int
}

// respFresh bounds how long the server re-answers a client re-sending the same
// Init (it re-sends every 700 ms until it sees the Resp); a later replay is met
// with silence.
const respFresh = 5 * time.Second

// NewTransport builds the transport for one side.
func newTransport(r role, psk []byte, static KeyPair, srvPub [32]byte, carriers []*carrierState) *Transport {
	return &Transport{role: r, psk: psk, static: static, srvPub: srvPub, carriers: carriers,
		inbound: make(chan []byte, 2048), closed: make(chan struct{}), confirmed: make(chan struct{}),
		seenEph: map[[32]byte]seenInit{}}
}

// ReorderDepth is the largest out-of-order gap the live session's replay window
// has accepted, for tuning the window against real carrier reordering.
func (tr *Transport) ReorderDepth() int64 {
	s := tr.sess.Load()
	if s == nil {
		return 0
	}
	return int64(s.reorderDepth())
}

// start begins every carrier and the scheduler loops.
func (tr *Transport) start() error {
	for i, cs := range tr.carriers {
		i := i
		if err := cs.c.Start(func(frame []byte, src any) { tr.feed(i, frame, src) }); err != nil {
			return err
		}
	}
	go tr.probeLoop()
	go tr.rateLoop()
	return nil
}

// feed routes one inbound frame from carrier i. The carrier never learns its
// peer on its own; the Transport binds it with SetPeer only once a frame from
// src has authenticated, so an unauthenticated packet can neither redirect the
// carrier nor draw an answer.
func (tr *Transport) feed(i int, frame []byte, src any) {
	cs := tr.carriers[i]
	cs.ct.recv.Add(1)
	cs.ct.recvB.Add(int64(len(frame)))
	s := tr.sess.Load()
	if s == nil {
		if tr.role == roleServer {
			tr.tryInit(i, frame, src)
		} else {
			tr.tryResp(frame)
		}
		return
	}
	pt, err := s.unprotect(frame)
	if err != nil {
		if errors.Is(err, errReplay) {
			return
		}
		if tr.role == roleServer {
			tr.tryInit(i, frame, src) // maybe the client restarted with a new session
		}
		return
	}
	cs.c.SetPeer(src) // authenticated record: bind (or refresh, e.g. NAT rebind) the peer
	tr.handleInner(i, pt)
}

func (tr *Transport) handleInner(i int, pt []byte) {
	if len(pt) == 0 {
		return
	}
	switch pt[0] {
	case innerData:
		if len(pt) < 3 {
			return
		}
		n := int(binary.BigEndian.Uint16(pt[1:3]))
		if 3+n > len(pt) {
			return
		}
		b := make([]byte, n)
		copy(b, pt[3:3+n])
		select {
		case tr.inbound <- b:
		case <-tr.closed:
		default: // queue full: drop, kcp retransmits
		}
	case innerProbe:
		if len(pt) < 9 {
			return
		}
		pong := make([]byte, 9)
		pong[0] = innerPong
		copy(pong[1:], pt[1:9])
		tr.sendInner(i, pong)
	case innerPong:
		if len(pt) < 9 {
			return
		}
		id := binary.BigEndian.Uint64(pt[1:9])
		if v, ok := tr.probes.LoadAndDelete(id); ok {
			p := v.(probeSent)
			c := tr.carriers[p.carrier]
			c.ct.rttMs.Store(max(1, time.Since(p.at).Milliseconds()))
			c.misses.Store(0)
			c.down.Store(false)
			c.loss.add(true)
			c.ct.lossPct.Store(int64(c.loss.lossPct()))
		}
	}
}

// ---------------------------------------------------------------- handshake

func (tr *Transport) tryInit(i int, frame []byte, src any) {
	sid, key, eph, err := parseInit(tr.psk, tr.static, frame)
	if err != nil {
		return // silence: an unauthenticated packet never binds the peer or draws a Resp
	}
	now := time.Now()
	tr.hsMu.Lock()
	tr.gcSeen(now)
	e, seen := tr.seenEph[eph]
	if seen {
		// This ephemeral was already used. Never install a second session from
		// it: that would reset sendSeq and the replay window under the same key
		// (nonce reuse, and old frames replayed into a fresh window). And never
		// SetPeer here — a captured Init can be replayed from a spoofed source,
		// so a replay must not redirect the carrier. Only re-answer the genuine
		// client still re-sending its Init before it saw our Resp, and send that
		// Resp to the peer bound by the FIRST Init, not to this packet's source.
		live := tr.sess.Load()
		resend := e.sid == sid && e.carrier == i && now.Sub(e.first) <= respFresh && live != nil && live.sid == sid
		tr.hsMu.Unlock()
		if resend {
			if resp, rerr := makeResp(key, sid); rerr == nil {
				tr.carriers[i].c.Send(resp)
			}
		}
		return // a replay from elsewhere, or after the window, gets silence
	}
	tr.seenEph[eph] = seenInit{sid: sid, first: now, carrier: i}
	tr.hsMu.Unlock()

	// First time we see this ephemeral: a valid Init with a fresh ephemeral can
	// only come from someone holding the PSK, so bind the peer to it.
	tr.carriers[i].c.SetPeer(src)
	s, err := newSession(sid, key, false)
	if err != nil {
		return
	}
	prev := tr.sess.Swap(s)
	if resp, rerr := makeResp(key, sid); rerr == nil {
		tr.carriers[i].c.Send(resp)
	}
	if prev == nil || prev.sid != s.sid {
		if tr.onSession != nil {
			tr.onSession()
		}
	}
}

// gcSeen drops Inits older than the handshake freshness window keeps mattering
// for (2x the clock skew), so the cache cannot grow without bound.
func (tr *Transport) gcSeen(now time.Time) {
	for k, v := range tr.seenEph {
		if now.Sub(v.first) > 2*clkSkew {
			delete(tr.seenEph, k)
		}
	}
}

func (tr *Transport) tryResp(frame []byte) {
	if tr.pending == nil || tr.confirmOK.Load() {
		return
	}
	if err := readResp(tr.pending, frame); err != nil {
		return
	}
	tr.sess.Store(tr.pending)
	if tr.confirmOK.CompareAndSwap(false, true) {
		close(tr.confirmed)
	}
}

// clientHandshake sends Init across the carriers until a Resp arrives.
func (tr *Transport) clientHandshake(timeout time.Duration) error {
	initFrame, s, err := makeInit(tr.psk, tr.srvPub)
	if err != nil {
		return err
	}
	tr.pending = s
	deadline := time.Now().Add(timeout)
	t := time.NewTicker(700 * time.Millisecond)
	defer t.Stop()
	send := func() {
		for _, cs := range tr.carriers {
			if cs.c.Up() {
				cs.c.Send(initFrame)
			}
		}
	}
	send()
	for {
		select {
		case <-tr.confirmed:
			return nil
		case <-tr.closed:
			return errors.New("bridge: closed during handshake")
		case <-t.C:
			if time.Now().After(deadline) {
				return errors.New("bridge: no carrier answered the handshake")
			}
			send()
		}
	}
}

// ---------------------------------------------------------------- scheduler

func (tr *Transport) sendInner(carrier int, plaintext []byte) {
	s := tr.sess.Load()
	if s == nil {
		return
	}
	cs := tr.carriers[carrier]
	frame := s.protect(plaintext)
	if err := cs.c.Send(frame); err != nil {
		cs.ct.fail(err)
		return
	}
	cs.ct.sent.Add(1)
	cs.ct.sentB.Add(int64(len(frame)))
}

// pick chooses the carrier for one outgoing frame of n bytes: the healthiest
// one whose cap still has room. When every healthy carrier is at its cap, it
// paces on the best one (waiting briefly for tokens) rather than spilling over
// it, so a carrier's cap is a real ceiling even when it is the only path up —
// the ICMP cap, for example, keeps that carrier under the reported ~30 MB/s
// reroute trigger.
func (tr *Transport) pick(n int) int {
	order := tr.order()
	best := -1
	for _, i := range order {
		cs := tr.carriers[i]
		if !cs.healthy() {
			continue
		}
		if best < 0 {
			best = i
		}
		if cs.cap == nil || cs.cap.take(n) {
			return i
		}
	}
	if best >= 0 {
		if c := tr.carriers[best].cap; c != nil {
			c.takeWait(n, tr.closed)
		}
	}
	return best
}

// order lists carrier indexes best first (by health then priority).
func (tr *Transport) order() []int {
	idx := make([]int, len(tr.carriers))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ca, cb := tr.carriers[idx[a]], tr.carriers[idx[b]]
		if ca.healthy() != cb.healthy() {
			return ca.healthy()
		}
		if ca.priority != cb.priority {
			return ca.priority > cb.priority
		}
		return ca.ct.rttMs.Load() < cb.ct.rttMs.Load()
	})
	return idx
}

func (tr *Transport) probeLoop() {
	t := time.NewTicker(probeEvery)
	defer t.Stop()
	for {
		select {
		case <-tr.closed:
			return
		case <-t.C:
		}
		if tr.sess.Load() == nil {
			continue
		}
		now := time.Now()
		// time out probes that never got a pong
		tr.probes.Range(func(k, v any) bool {
			p := v.(probeSent)
			if now.Sub(p.at) > probeEvery {
				tr.probes.Delete(k)
				c := tr.carriers[p.carrier]
				c.loss.add(false)
				c.ct.lossPct.Store(int64(c.loss.lossPct()))
				if c.misses.Add(1) >= probeMiss {
					c.down.Store(true)
					c.ct.rttMs.Store(-1)
				}
			}
			return true
		})
		for i, cs := range tr.carriers {
			if !cs.c.Up() {
				cs.down.Store(true)
				continue
			}
			id := tr.nextID.Add(1)
			tr.probes.Store(id, probeSent{carrier: i, at: now})
			pr := make([]byte, 9)
			pr[0] = innerProbe
			binary.BigEndian.PutUint64(pr[1:], id)
			tr.sendInner(i, pr)
		}
	}
}

func (tr *Transport) rateLoop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-tr.closed:
			return
		case <-t.C:
		}
		for _, cs := range tr.carriers {
			up, dn := cs.ct.sentB.Load(), cs.ct.recvB.Load()
			cs.ct.rateUp.Store(up - cs.ct.lastUpB)
			cs.ct.rateDown.Store(dn - cs.ct.lastDnB)
			cs.ct.lastUpB, cs.ct.lastDnB = up, dn
		}
	}
}

// Stats is every carrier's live state.
func (tr *Transport) Stats() []CarrierStat {
	out := make([]CarrierStat, 0, len(tr.carriers))
	for _, cs := range tr.carriers {
		out = append(out, cs.ct.stat(cs.c.Name(), cs.healthy(), cs.capMBps))
	}
	return out
}

// ---------------------------------------------------------------- net.PacketConn

func (tr *Transport) WriteTo(p []byte, _ net.Addr) (int, error) {
	select {
	case <-tr.closed:
		return 0, net.ErrClosed
	default:
	}
	inner := make([]byte, 3, 3+len(p))
	inner[0] = innerData
	binary.BigEndian.PutUint16(inner[1:3], uint16(len(p)))
	inner = append(inner, p...)
	if pad := padLen(len(inner)); pad > len(inner) {
		grow := make([]byte, pad-len(inner))
		randFill(grow)
		inner = append(inner, grow...)
	}
	s := tr.sess.Load()
	if s == nil {
		return len(p), nil // not established yet: kcp will retransmit
	}
	frame := s.protect(inner)
	i := tr.pick(len(frame))
	if i < 0 {
		return len(p), nil // no carrier up right now; kcp retransmits
	}
	cs := tr.carriers[i]
	if err := cs.c.Send(frame); err != nil {
		cs.ct.fail(err)
		return len(p), nil
	}
	cs.ct.sent.Add(1)
	cs.ct.sentB.Add(int64(len(frame)))
	return len(p), nil
}

func (tr *Transport) ReadFrom(p []byte) (int, net.Addr, error) {
	var timer *time.Timer
	var tc <-chan time.Time
	if d := tr.rdl.Load(); d != 0 {
		wait := time.Until(time.Unix(0, d))
		if wait <= 0 {
			return 0, nil, timeoutErr{}
		}
		timer = time.NewTimer(wait)
		tc = timer.C
	}
	if timer != nil {
		defer timer.Stop()
	}
	select {
	case b := <-tr.inbound:
		n := copy(p, b)
		return n, bridgeAddr{}, nil
	case <-tc:
		return 0, nil, timeoutErr{}
	case <-tr.closed:
		return 0, nil, net.ErrClosed
	}
}

func (tr *Transport) Close() error {
	tr.closeMu.Do(func() {
		close(tr.closed)
		for _, cs := range tr.carriers {
			cs.c.Close()
		}
	})
	return nil
}

func (tr *Transport) LocalAddr() net.Addr                { return bridgeAddr{} }
func (tr *Transport) SetDeadline(t time.Time) error      { tr.SetReadDeadline(t); return nil }
func (tr *Transport) SetWriteDeadline(t time.Time) error { return nil }
func (tr *Transport) SetReadDeadline(t time.Time) error {
	if t.IsZero() {
		tr.rdl.Store(0)
	} else {
		tr.rdl.Store(t.UnixNano())
	}
	return nil
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "bridge: read deadline exceeded" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// ---------------------------------------------------------------- helpers

// bucket is a simple byte-per-second token bucket for pacing a carrier.
type bucket struct {
	mu    sync.Mutex
	rate  float64 // bytes per second
	burst float64
	toks  float64
	last  time.Time
}

func newBucket(bytesPerSec int) *bucket {
	if bytesPerSec <= 0 {
		return nil
	}
	return &bucket{rate: float64(bytesPerSec), burst: float64(bytesPerSec), toks: float64(bytesPerSec), last: time.Now()}
}

func (b *bucket) refill() {
	now := time.Now()
	b.toks += b.rate * now.Sub(b.last).Seconds()
	if b.toks > b.burst {
		b.toks = b.burst
	}
	b.last = now
}

func (b *bucket) take(n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	if b.toks >= float64(n) {
		b.toks -= float64(n)
		return true
	}
	return false
}

// takeWait paces to the cap: it waits in short steps until n tokens are
// available (up to ~1 s), then consumes them, so throughput on a sole capped
// carrier holds near its cap instead of spilling over it. A closed transport
// cuts the wait short.
func (b *bucket) takeWait(n int, closed <-chan struct{}) {
	for i := 0; i < 200; i++ {
		b.mu.Lock()
		b.refill()
		if b.toks >= float64(n) {
			b.toks -= float64(n)
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()
		select {
		case <-closed:
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
	b.mu.Lock()
	b.toks -= float64(n) // give up waiting: go into debt rather than stall forever
	b.mu.Unlock()
}

// lossRing tracks the last lossWindow probe outcomes.
type lossRing struct {
	mu   sync.Mutex
	bits [lossWindow]bool
	n    int
	i    int
}

func (r *lossRing) add(ok bool) {
	r.mu.Lock()
	r.bits[r.i] = ok
	r.i = (r.i + 1) % lossWindow
	if r.n < lossWindow {
		r.n++
	}
	r.mu.Unlock()
}

func (r *lossRing) lossPct() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.n == 0 {
		return 0
	}
	lost := 0
	for k := 0; k < r.n; k++ {
		if !r.bits[k] {
			lost++
		}
	}
	return lost * 100 / r.n
}
