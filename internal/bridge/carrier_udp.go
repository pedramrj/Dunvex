package bridge

// The UDP carrier: the plainest one, for when UDP gets out. The frames are
// already ciphertext padded to a few sizes, so on the wire this is just
// high-entropy datagrams of a handful of lengths to one port — no header a
// classifier can lock onto. It is the fastest carrier when UDP is open, and
// the first to die when the filter blocks UDP, which the scheduler handles.

import (
	"net"
	"sync"
	"sync/atomic"
)

// UDPCarrier moves frames as UDP datagrams. On the client it dials the relay;
// on the server it binds and answers whoever proved the key.
type UDPCarrier struct {
	client bool
	target *net.UDPAddr // client: the relay's udp address
	bind   string       // server: listen address

	conn atomic.Pointer[net.UDPConn]
	peer atomic.Pointer[net.UDPAddr] // server: the client's address, learned
	up   atomic.Bool
	recv func([]byte)

	mu     sync.Mutex
	closed bool
}

// NewUDPClient makes the client-side UDP carrier aimed at target ("host:port").
func NewUDPClient(target string) (*UDPCarrier, error) {
	ua, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, err
	}
	return &UDPCarrier{client: true, target: ua}, nil
}

// NewUDPServer makes the server-side UDP carrier bound to bind (":port").
func NewUDPServer(bind string) *UDPCarrier { return &UDPCarrier{bind: bind} }

func (u *UDPCarrier) Name() string { return "udp" }
func (u *UDPCarrier) Up() bool     { return u.up.Load() }

func (u *UDPCarrier) Start(recv func([]byte)) error {
	u.recv = recv
	var c *net.UDPConn
	var err error
	if u.client {
		c, err = net.DialUDP("udp", nil, u.target)
	} else {
		var la *net.UDPAddr
		if la, err = net.ResolveUDPAddr("udp", u.bind); err == nil {
			c, err = net.ListenUDP("udp", la)
		}
	}
	if err != nil {
		return err
	}
	u.conn.Store(c)
	u.up.Store(true)
	go u.read(c)
	return nil
}

func (u *UDPCarrier) read(c *net.UDPConn) {
	buf := make([]byte, 1<<16)
	for {
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			u.mu.Lock()
			closed := u.closed
			u.mu.Unlock()
			if closed {
				return
			}
			u.up.Store(false)
			return
		}
		if !u.client && from != nil {
			u.peer.Store(from)
		}
		if u.recv != nil && n > 0 {
			b := make([]byte, n)
			copy(b, buf[:n])
			u.recv(b)
		}
	}
}

func (u *UDPCarrier) Send(frame []byte) error {
	c := u.conn.Load()
	if c == nil {
		return net.ErrClosed
	}
	if u.client {
		_, err := c.Write(frame)
		return err
	}
	p := u.peer.Load()
	if p == nil {
		return errNoPeer
	}
	_, err := c.WriteToUDP(frame, p)
	return err
}

func (u *UDPCarrier) Close() error {
	u.mu.Lock()
	u.closed = true
	u.mu.Unlock()
	u.up.Store(false)
	if c := u.conn.Load(); c != nil {
		return c.Close()
	}
	return nil
}
