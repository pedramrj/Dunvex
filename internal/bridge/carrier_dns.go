package bridge

// The DNS carrier: the last resort, when a full shutdown leaves only DNS
// resolving. It reuses the dnstt code already in the repo (internal/dnstt),
// which gives a reliable stream through DNS queries; the carrier length-frames
// our frames over it. It is slow and for keeping a thin link alive, not for
// downloads, so the scheduler gives it the lowest priority and a small cap.

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pedramrj/dunvex/internal/dnstt"
	"github.com/pedramrj/dunvex/internal/dnstt/dnsttclient"
)

// DNSServer is the Iran-relay side: a dnstt server whose upstream is a local
// loopback service that speaks our frame protocol.
type DNSServer struct {
	bind    string // udp bind for the tunnel, e.g. ":5300"
	privHex string
	domain  string

	udp    net.PacketConn
	ln     net.Listener
	conn   atomic.Pointer[net.Conn] // the active stream, set only after a frame on it authenticates
	recv   func(frame []byte, src any)
	up     atomic.Bool
	closed chan struct{}
	once   sync.Once
}

// NewDNSServer makes the relay side. privHex is the dnstt private key; domain
// is the tunnel zone; bind is the UDP address to serve it on.
func NewDNSServer(bind, privHex, domain string) *DNSServer {
	return &DNSServer{bind: bind, privHex: privHex, domain: domain, closed: make(chan struct{})}
}

func (s *DNSServer) Name() string { return "dns" }
func (s *DNSServer) Up() bool     { return s.up.Load() }

// SetPeer makes a dnstt stream the active one once a frame on it authenticates.
func (s *DNSServer) SetPeer(src any) {
	if c, ok := src.(net.Conn); ok && c != nil {
		s.conn.Store(&c)
	}
}

func (s *DNSServer) Start(recv func(frame []byte, src any)) error {
	s.recv = recv
	key, err := dnstt.DecodeKey(s.privHex)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.ln = ln
	udp, err := net.ListenPacket("udp", s.bind)
	if err != nil {
		ln.Close()
		return err
	}
	s.udp = udp
	go func() { _ = dnstt.Run(key, s.domain, ln.Addr().String(), udp) }()
	go s.accept()
	s.up.Store(true)
	return nil
}

func (s *DNSServer) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
			}
			return
		}
		go s.read(c) // not the active stream until a frame on it authenticates (SetPeer)
	}
}

func (s *DNSServer) read(c net.Conn) {
	for {
		b, err := readLenFrame(c)
		if err != nil {
			return
		}
		if s.recv != nil && len(b) > 0 {
			s.recv(b, c)
		}
	}
}

func (s *DNSServer) Send(frame []byte) error {
	cp := s.conn.Load()
	if cp == nil {
		return errNoPeer
	}
	return writeLenFrame(*cp, frame)
}

func (s *DNSServer) Close() error {
	s.once.Do(func() { s.up.Store(false); close(s.closed) })
	if s.ln != nil {
		s.ln.Close()
	}
	if s.udp != nil {
		s.udp.Close()
	}
	return nil
}

// DNSClient is the target-abroad side: a dnstt client to the relay's zone.
type DNSClient struct {
	resolver string // the relay's udp tunnel address (or a resolver that reaches it)
	domain   string
	pubHex   string

	cl     atomic.Pointer[dnsttclient.Client]
	stream atomic.Pointer[io.ReadWriteCloser]
	recv   func(frame []byte, src any)
	up     atomic.Bool
	closed chan struct{}
	once   sync.Once
}

// NewDNSClient makes the target side; resolver is the relay's tunnel UDP
// address, domain the zone, pubHex the relay's dnstt public key.
func NewDNSClient(resolver, domain, pubHex string) *DNSClient {
	return &DNSClient{resolver: resolver, domain: domain, pubHex: pubHex, closed: make(chan struct{})}
}

func (c *DNSClient) Name() string { return "dns" }
func (c *DNSClient) Up() bool     { return c.up.Load() }

// SetPeer is a no-op: the client opened the dnstt stream itself.
func (c *DNSClient) SetPeer(any) {}

func (c *DNSClient) Start(recv func(frame []byte, src any)) error {
	c.recv = recv
	go c.keep()
	return nil
}

func (c *DNSClient) keep() {
	wait := 2 * time.Second
	for {
		select {
		case <-c.closed:
			return
		default:
		}
		cl, err := dnsttclient.Dial(c.resolver, c.domain, c.pubHex)
		if err == nil {
			var st io.ReadWriteCloser
			st, err = cl.Open()
			if err == nil {
				c.cl.Store(cl)
				c.stream.Store(&st)
				c.up.Store(true)
				wait = 2 * time.Second
				c.read(st)
				c.up.Store(false)
				c.stream.Store(nil)
			}
			cl.Close()
		}
		select {
		case <-c.closed:
			return
		case <-time.After(wait):
		}
		if wait < 20*time.Second {
			wait *= 2
		}
	}
}

func (c *DNSClient) read(st io.Reader) {
	for {
		b, err := readLenFrame(st)
		if err != nil {
			return
		}
		if c.recv != nil && len(b) > 0 {
			c.recv(b, st)
		}
	}
}

func (c *DNSClient) Send(frame []byte) error {
	sp := c.stream.Load()
	if sp == nil {
		return net.ErrClosed
	}
	return writeLenFrame(*sp, frame)
}

func (c *DNSClient) Close() error {
	c.once.Do(func() { c.up.Store(false); close(c.closed) })
	if sp := c.stream.Load(); sp != nil {
		(*sp).Close()
	}
	if cl := c.cl.Load(); cl != nil {
		cl.Close()
	}
	return nil
}

// ---------------------------------------------------------------- framing

func writeLenFrame(w io.Writer, b []byte) error {
	if len(b) > 65535 {
		return io.ErrShortWrite
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(b)))
	if _, err := w.Write(append(hdr[:], b...)); err != nil {
		return err
	}
	return nil
}

func readLenFrame(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return b, nil
}
