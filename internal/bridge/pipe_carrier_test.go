package bridge

import (
	"sync"
	"sync/atomic"
)

// pipeCarrier is an in-memory carrier for tests: two of them, crossed, move
// frames between the two Bridge sides over channels. It lets a test add a
// second path and kill the first mid-transfer to prove failover.
type pipeCarrier struct {
	out    chan []byte
	in     chan []byte
	recv   func([]byte)
	up     atomic.Bool
	closed chan struct{}
	once   sync.Once
}

func newPipeCarrierPair() (server, client *pipeCarrier) {
	c2s := make(chan []byte, 512)
	s2c := make(chan []byte, 512)
	server = &pipeCarrier{out: s2c, in: c2s, closed: make(chan struct{})}
	client = &pipeCarrier{out: c2s, in: s2c, closed: make(chan struct{})}
	return
}

func (p *pipeCarrier) Name() string { return "pipe" }
func (p *pipeCarrier) Up() bool     { return p.up.Load() }

func (p *pipeCarrier) Start(recv func([]byte)) error {
	p.recv = recv
	p.up.Store(true)
	go func() {
		for {
			select {
			case <-p.closed:
				return
			case b := <-p.in:
				if p.recv != nil {
					p.recv(b)
				}
			}
		}
	}()
	return nil
}

func (p *pipeCarrier) Send(frame []byte) error {
	if !p.up.Load() {
		return errNoPeer
	}
	b := make([]byte, len(frame))
	copy(b, frame)
	select {
	case p.out <- b:
	case <-p.closed:
	default: // full: drop, like a lossy carrier
	}
	return nil
}

func (p *pipeCarrier) Close() error {
	p.once.Do(func() { p.up.Store(false); close(p.closed) })
	return nil
}

// countingCarrier is a stub that only counts the frames Send is asked to move,
// for handshake/replay tests that feed frames in by hand.
type countingCarrier struct {
	recv func([]byte)
	sent atomic.Int64
}

func (c *countingCarrier) Name() string               { return "count" }
func (c *countingCarrier) Up() bool                   { return true }
func (c *countingCarrier) Start(r func([]byte)) error { c.recv = r; return nil }
func (c *countingCarrier) Send(frame []byte) error    { c.sent.Add(1); return nil }
func (c *countingCarrier) Close() error               { return nil }
