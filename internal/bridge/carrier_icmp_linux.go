//go:build linux

package bridge

// The ICMP carrier. On the worst days reported from Iran, echo was the only
// thing that crossed the border (net4people #676), so this is the carrier of
// last resort that still moves real traffic. One side sends echo requests, the
// other answers with echo replies, each carrying a frame in the payload. The
// request/answer pairing means the answering side can only push a frame when a
// request arrives, so the requesting side also polls while idle, giving the
// answering side slots to drain its queue — which is how a download keeps
// flowing in the answer direction.
//
// By default the target abroad sends the requests and the Iran relay answers,
// the direction #676 found works around one-way echo blocks. ServerRequests
// flips it for routes where the other way is better.
//
// Raw sockets need root (CAP_NET_RAW). On the answering side the kernel would
// also auto-reply to the requests; we set icmp_echo_ignore_all so only our own
// replies go out and the pps budget is not wasted.

import (
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// ICMPPollIdle is how often the requesting side sends an empty request while it
// has nothing to send, so the answering side can push queued frames back.
var ICMPPollIdle = 4 * time.Millisecond

type icmpCarrier struct {
	client         bool
	serverRequests bool   // the Iran relay sends the requests instead of the target
	id             int    // echo ID, derived from the PSK, so no two installs share it
	targetIP       net.IP // requester: where to send (fixed)

	conn   *icmp.PacketConn
	peer   atomic.Pointer[net.IPAddr] // answerer: the client, bound only after a frame authenticates
	recv   func(frame []byte, src any)
	up     atomic.Bool
	sendq  chan []byte
	seq    atomic.Uint32
	closed chan struct{}
	once   sync.Once
}

// NewICMPClient makes the target side; target is the relay's IP (no port). id
// is the PSK-derived echo ID both sides share.
func NewICMPClient(targetIP string, serverRequests bool, id uint16) (Carrier, error) {
	ip := net.ParseIP(targetIP)
	if ip == nil {
		return nil, &net.AddrError{Err: "bad relay IP for the ICMP carrier", Addr: targetIP}
	}
	c := &icmpCarrier{client: true, serverRequests: serverRequests, id: int(id), targetIP: ip.To4(), sendq: make(chan []byte, 1024), closed: make(chan struct{})}
	c.peer.Store(&net.IPAddr{IP: c.targetIP})
	return c, nil
}

// NewICMPServer makes the Iran relay side.
func NewICMPServer(serverRequests bool, id uint16) Carrier {
	return &icmpCarrier{serverRequests: serverRequests, id: int(id), sendq: make(chan []byte, 1024), closed: make(chan struct{})}
}

func (c *icmpCarrier) Name() string { return "icmp" }
func (c *icmpCarrier) Up() bool     { return c.up.Load() }

// requester reports whether this side sends echo requests.
func (c *icmpCarrier) requester() bool {
	if c.serverRequests {
		return !c.client // the server (relay) sends requests
	}
	return c.client // default: the target abroad sends requests
}

func (c *icmpCarrier) Start(recv func(frame []byte, src any)) error {
	c.recv = recv
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return err
	}
	c.conn = conn
	c.up.Store(true)
	if !c.requester() {
		// we answer: stop the kernel from auto-replying and doubling traffic
		_ = os.WriteFile("/proc/sys/net/ipv4/icmp_echo_ignore_all", []byte("1\n"), 0o644)
	}
	go c.read()
	if c.requester() {
		go c.pollLoop()
	}
	return nil
}

func (c *icmpCarrier) read() {
	buf := make([]byte, 1<<16)
	wantReply := c.requester() // the requester reads echo replies; the answerer reads echo requests
	for {
		n, peer, err := c.conn.ReadFrom(buf)
		if err != nil {
			select {
			case <-c.closed:
				return
			default:
			}
			c.up.Store(false)
			return
		}
		msg, err := icmp.ParseMessage(1, buf[:n]) // 1 = IPv4 ICMP
		if err != nil {
			continue
		}
		echo, ok := msg.Body.(*icmp.Echo)
		if !ok || echo.ID != c.id {
			continue
		}
		isReply := msg.Type == ipv4.ICMPTypeEchoReply
		if isReply != wantReply {
			continue // ignore our own direction echoed by anything in the middle
		}
		frame := echo.Data
		if c.requester() {
			// we read replies from the server; the peer is fixed (the target).
			if len(frame) > 1 && c.recv != nil {
				b := make([]byte, len(frame))
				copy(b, frame)
				c.recv(b, peer)
			}
			continue
		}
		// answerer: hand real frames up for authentication; the Transport binds
		// the peer (SetPeer) only if a frame opens. We answer with a queued
		// frame ONLY to the already-bound peer — a prober's request draws
		// nothing, so our ciphertext never goes to anyone without the key.
		if len(frame) > 1 && c.recv != nil {
			b := make([]byte, len(frame))
			copy(b, frame)
			c.recv(b, peer)
		}
		if c.isPeer(peer) { // bound = authenticated; polls from the bound peer pump downstream
			c.answer(peer)
		}
	}
}

// isPeer reports whether addr is the currently bound (authenticated) peer.
func (c *icmpCarrier) isPeer(addr net.Addr) bool {
	p := c.peer.Load()
	ip, ok := addr.(*net.IPAddr)
	return ok && p != nil && ip != nil && p.IP.Equal(ip.IP)
}

// answer pops one queued frame and sends it as an echo reply (empty if none).
func (c *icmpCarrier) answer(peer net.Addr) {
	var payload []byte
	select {
	case payload = <-c.sendq:
	default:
		payload = []byte{0} // empty reply keeps the pairing alive
	}
	c.write(ipv4.ICMPTypeEchoReply, payload, peer)
}

// SetPeer binds the answerer's peer to an authenticated source (no-op on the
// requester, whose target is fixed).
func (c *icmpCarrier) SetPeer(src any) {
	if c.requester() {
		return
	}
	if ip, ok := src.(*net.IPAddr); ok && ip != nil {
		c.peer.Store(ip)
	}
}

// pollLoop (requester only) sends queued frames at once and empty polls while
// idle, so the answering side always has a slot to push data into.
func (c *icmpCarrier) pollLoop() {
	t := time.NewTicker(ICMPPollIdle)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case f := <-c.sendq:
			c.request(f)
		case <-t.C:
			select {
			case f := <-c.sendq:
				c.request(f)
			default:
				c.request([]byte{0}) // idle poll
			}
		}
	}
}

func (c *icmpCarrier) request(payload []byte) {
	p := c.peer.Load()
	if p == nil {
		return
	}
	c.write(ipv4.ICMPTypeEcho, payload, p)
}

func (c *icmpCarrier) write(typ ipv4.ICMPType, payload []byte, peer net.Addr) {
	seq := int(c.seq.Add(1) & 0xffff)
	msg := icmp.Message{Type: typ, Code: 0, Body: &icmp.Echo{ID: c.id, Seq: seq, Data: payload}}
	b, err := msg.Marshal(nil)
	if err != nil {
		return
	}
	if _, err := c.conn.WriteTo(b, peer); err != nil {
		c.up.Store(false)
	}
}

func (c *icmpCarrier) Send(frame []byte) error {
	select {
	case c.sendq <- frame:
		return nil
	case <-c.closed:
		return net.ErrClosed
	default:
		return errCarrierBusy
	}
}

func (c *icmpCarrier) Close() error {
	c.once.Do(func() { c.up.Store(false); close(c.closed) })
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
