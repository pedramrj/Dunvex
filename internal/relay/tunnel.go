package relay

// The reverse tunnel. The protocol server dials the relay (the direction
// filtering leaves alone), proves who it is with a shared secret inside TLS
// pinned to the relay's certificate, and keeps a few links open. The relay
// carries every user connection back over those links as a multiplexed
// stream; the server connects it to its own protocol on the same machine.
//
// Inside TLS: the server sends "DVXR1 <secret>\n", the relay answers "OK\n",
// then smux runs with the relay opening streams. A stream starts with one
// byte ('t' TCP, 'u' UDP) and the port (2 bytes, big endian); UDP streams then
// carry datagrams as 2-byte length + payload in both directions.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtaci/smux"
)

const tunnelMagic = "DVXR1"

// TunnelServer is a relay's tunnel port and secret.
type TunnelServer struct {
	Port   int    `json:"port"`
	Secret string `json:"secret"`
}

// TunnelClient is one relay a protocol server keeps links to.
type TunnelClient struct {
	Name        string `json:"name"`
	Addr        string `json:"addr"`        // relay host:tunnel port
	Fingerprint string `json:"fingerprint"` // SHA-256 of the relay's certificate (hex)
	Secret      string `json:"secret"`
	Ports       []int  `json:"ports"` // the only local ports the relay may reach
}

// TunnelStat is the relay's side of its tunnel.
type TunnelStat struct {
	Port      int    `json:"port"`
	Listening bool   `json:"listening"`
	Error     string `json:"error,omitempty"`
	Links     int    `json:"links"` // links from the server now
	Peer      string `json:"peer,omitempty"`
	Since     int64  `json:"since,omitempty"` // when the first current link came up
}

// ClientStat is a protocol server's side of one tunnel.
type ClientStat struct {
	Name    string `json:"name"`
	Addr    string `json:"addr"`
	Links   int    `json:"links"`
	Error   string `json:"error,omitempty"`
	Since   int64  `json:"since,omitempty"`
	Streams int64  `json:"streams"` // connections carried since start
}

func smuxConfig() *smux.Config {
	c := smux.DefaultConfig()
	c.Version = 2
	c.KeepAliveInterval = 10 * time.Second
	c.KeepAliveTimeout = 40 * time.Second
	c.MaxReceiveBuffer = 8 << 20
	c.MaxStreamBuffer = 2 << 20
	return c
}

// ---------------------------------------------------------------- relay side

type tunnelServer struct {
	cfg   TunnelServer
	ln    net.Listener
	err   string
	mu    sync.Mutex
	links []*smux.Session
	peer  string
	since int64
	next  atomic.Uint64
}

// SetCert gives the relay the certificate its tunnel port serves (the
// agent's own, which the panel already pins).
func (r *Relay) SetCert(c tls.Certificate) {
	r.mu.Lock()
	r.cert = &c
	r.mu.Unlock()
}

// ApplyTunnel opens, changes or (with nil) closes the tunnel port. Call it
// before Apply, so forwards know which port the tunnel takes.
func (r *Relay) ApplyTunnel(cfg *TunnelServer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.tun.Load(); t != nil && (cfg == nil || t.cfg != *cfg) {
		t.close()
		r.tun.Store(nil)
	}
	if cfg == nil || r.tun.Load() != nil {
		return
	}
	t := &tunnelServer{cfg: *cfg}
	defer r.tun.Store(t)
	switch {
	case r.cert == nil:
		t.err = "the relay has no certificate for its tunnel"
	case cfg.Secret == "":
		t.err = "the tunnel has no secret"
	default:
		if what, ok := r.reserved[cfg.Port]; ok {
			t.err = fmt.Sprintf("port %d is used by %s on the relay", cfg.Port, what)
			break
		}
		ln, err := tls.Listen("tcp", ":"+strconv.Itoa(cfg.Port), &tls.Config{Certificates: []tls.Certificate{*r.cert}, MinVersion: tls.VersionTLS13})
		if err != nil {
			t.err = portErr(cfg.Port, "TCP", err).Error()
			break
		}
		t.ln = ln
		go t.accept()
	}
}

func (t *tunnelServer) close() {
	if t.ln != nil {
		t.ln.Close()
	}
	t.mu.Lock()
	for _, s := range t.links {
		s.Close()
	}
	t.links = nil
	t.mu.Unlock()
}

func (t *tunnelServer) accept() {
	for {
		c, err := t.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		go t.greet(c)
	}
}

// greet checks the server's secret and turns the connection into a link.
func (t *tunnelServer) greet(c net.Conn) {
	c.SetDeadline(time.Now().Add(15 * time.Second))
	line, err := bufio.NewReaderSize(c, 256).ReadString('\n')
	got, ok := strings.CutPrefix(strings.TrimSpace(line), tunnelMagic+" ")
	if err != nil || !ok || subtle.ConstantTimeCompare([]byte(got), []byte(t.cfg.Secret)) != 1 {
		time.Sleep(time.Second) // a guess costs time
		c.Close()
		return
	}
	if _, err := io.WriteString(c, "OK\n"); err != nil {
		c.Close()
		return
	}
	c.SetDeadline(time.Time{})
	s, err := smux.Client(c, smuxConfig())
	if err != nil {
		c.Close()
		return
	}
	peer, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	t.mu.Lock()
	if len(t.links) == 0 {
		t.since = time.Now().Unix()
	}
	t.links, t.peer = append(t.links, s), peer
	t.mu.Unlock()
	<-s.CloseChan()
	t.mu.Lock()
	for i, x := range t.links {
		if x == s {
			t.links = append(t.links[:i], t.links[i+1:]...)
			break
		}
	}
	t.mu.Unlock()
}

// open starts a stream to the server's port, over the least busy link.
func (t *tunnelServer) open(kind byte, port int) (net.Conn, error) {
	t.mu.Lock()
	var best *smux.Session
	n := len(t.links)
	start := int(t.next.Add(1))
	for i := 0; i < n; i++ {
		s := t.links[(start+i)%n]
		if s.IsClosed() {
			continue
		}
		if best == nil || s.NumStreams() < best.NumStreams() {
			best = s
		}
	}
	t.mu.Unlock()
	if best == nil {
		return nil, errors.New("the server has not connected its tunnel")
	}
	st, err := best.OpenStream()
	if err != nil {
		return nil, err
	}
	hdr := []byte{kind, 0, 0}
	binary.BigEndian.PutUint16(hdr[1:], uint16(port))
	if _, err := st.Write(hdr); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

func (t *tunnelServer) stat() *TunnelStat {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := &TunnelStat{Port: t.cfg.Port, Listening: t.ln != nil, Error: t.err, Links: len(t.links)}
	if s.Links > 0 {
		s.Peer, s.Since = t.peer, t.since
	}
	return s
}

// TunnelStat is the state of the relay's tunnel port (nil when it has none).
func (r *Relay) TunnelStat() *TunnelStat {
	if t := r.tun.Load(); t != nil {
		return t.stat()
	}
	return nil
}

// tunnelOpen opens a stream through the tunnel (for reverse forwards).
func (r *Relay) tunnelOpen(kind byte, port int) (net.Conn, error) {
	t := r.tun.Load()
	if t == nil || t.ln == nil {
		return nil, errors.New("the relay has no tunnel port")
	}
	return t.open(kind, port)
}

// ---------------------------------------------------------------- server side

// Links is how many links a protocol server keeps to each relay.
var Links = 2

// Tunnels keeps a protocol server's links to its relays.
type Tunnels struct {
	local   string // where the server's own protocols listen
	mu      sync.Mutex
	clients map[string]*tclient
}

type tclient struct {
	TunnelClient
	cancel  context.CancelFunc
	allowed map[int]bool
	links   atomic.Int64
	streams atomic.Int64
	mu      sync.Mutex
	err     string
	since   int64
}

// NewTunnels makes the tunnel side of a protocol server; local is the host its
// protocols listen on ("" = 127.0.0.1).
func NewTunnels(local string) *Tunnels {
	if local == "" || local == "0.0.0.0" || local == "::" {
		local = "127.0.0.1"
	}
	return &Tunnels{local: local, clients: map[string]*tclient{}}
}

func (c TunnelClient) key() string {
	ports := make([]string, 0, len(c.Ports))
	for _, p := range c.Ports {
		ports = append(ports, strconv.Itoa(p))
	}
	return c.Addr + "|" + c.Fingerprint + "|" + c.Secret + "|" + strings.Join(ports, ",")
}

// Apply replaces the relays to keep links to.
func (t *Tunnels) Apply(list []TunnelClient) {
	t.mu.Lock()
	defer t.mu.Unlock()
	want := map[string]TunnelClient{}
	for _, c := range list {
		want[c.key()] = c
	}
	for k, c := range t.clients {
		if _, ok := want[k]; !ok {
			c.cancel()
			delete(t.clients, k)
		}
	}
	for k, c := range want {
		if _, ok := t.clients[k]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		x := &tclient{TunnelClient: c, cancel: cancel, allowed: map[int]bool{}}
		for _, p := range c.Ports {
			x.allowed[p] = true
		}
		t.clients[k] = x
		for i := 0; i < max(1, Links); i++ {
			go t.keep(ctx, x)
		}
	}
}

// Stats lists the relays and their links.
func (t *Tunnels) Stats() []ClientStat {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []ClientStat{}
	for _, c := range t.clients {
		c.mu.Lock()
		s := ClientStat{Name: c.Name, Addr: c.Addr, Links: int(c.links.Load()), Error: c.err, Streams: c.streams.Load()}
		if s.Links > 0 {
			s.Since, s.Error = c.since, ""
		}
		c.mu.Unlock()
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr < out[j].Addr })
	return out
}

// Close drops every link.
func (t *Tunnels) Close() { t.Apply(nil) }

// keep holds one link up, reconnecting with growing pauses.
func (t *Tunnels) keep(ctx context.Context, c *tclient) {
	wait := time.Second
	for ctx.Err() == nil {
		s, err := dialTunnel(ctx, c.TunnelClient)
		if err != nil {
			c.mu.Lock()
			c.err = err.Error()
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = min(wait*2, 30*time.Second)
			continue
		}
		wait = time.Second
		if c.links.Add(1) == 1 {
			c.mu.Lock()
			c.since, c.err = time.Now().Unix(), ""
			c.mu.Unlock()
		}
		go func() { <-ctx.Done(); s.Close() }()
		for {
			st, err := s.AcceptStream()
			if err != nil {
				break
			}
			c.streams.Add(1)
			go t.serve(st, c)
		}
		s.Close()
		c.links.Add(-1)
		if ctx.Err() == nil {
			time.Sleep(time.Second)
		}
	}
}

func dialTunnel(ctx context.Context, c TunnelClient) (*smux.Session, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the relay: %w", err)
	}
	host, _, _ := net.SplitHostPort(c.Addr)
	conn := tls.Client(raw, &tls.Config{
		ServerName: host, MinVersion: tls.VersionTLS13,
		InsecureSkipVerify: true, // the certificate is pinned below instead
		VerifyPeerCertificate: func(certs [][]byte, _ [][]*x509.Certificate) error {
			if len(certs) == 0 {
				return errors.New("the relay sent no certificate")
			}
			sum := sha256.Sum256(certs[0])
			if !strings.EqualFold(hex.EncodeToString(sum[:]), c.Fingerprint) {
				return errors.New("the relay's certificate does not match: not the paired relay")
			}
			return nil
		},
	})
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	if _, err := io.WriteString(conn, tunnelMagic+" "+c.Secret+"\n"); err != nil {
		conn.Close()
		return nil, err
	}
	line, err := bufio.NewReaderSize(conn, 16).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "OK" {
		conn.Close()
		return nil, errors.New("the relay refused the tunnel's secret")
	}
	conn.SetDeadline(time.Time{})
	return smux.Server(conn, smuxConfig())
}

// serve connects one carried connection to the server's own protocol.
func (t *Tunnels) serve(st *smux.Stream, c *tclient) {
	defer st.Close()
	hdr := make([]byte, 3)
	st.SetReadDeadline(time.Now().Add(15 * time.Second))
	if _, err := io.ReadFull(st, hdr); err != nil {
		return
	}
	st.SetReadDeadline(time.Time{})
	port := int(binary.BigEndian.Uint16(hdr[1:]))
	if !c.allowed[port] {
		log.Printf("tunnel %s: refused port %d", c.Name, port) // only the relayed protocols
		return
	}
	addr := net.JoinHostPort(t.local, strconv.Itoa(port))
	switch hdr[0] {
	case 't':
		out, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return
		}
		defer out.Close()
		var n atomic.Int64
		done := make(chan struct{})
		go func() { pipe(out, st, &n); close(done) }()
		pipe(st, out, &n)
		<-done
	case 'u':
		ua, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			return
		}
		out, err := net.DialUDP("udp", nil, ua)
		if err != nil {
			return
		}
		defer out.Close()
		go func() {
			buf := make([]byte, 64<<10)
			for {
				out.SetReadDeadline(time.Now().Add(UDPIdle))
				k, err := out.Read(buf)
				if err != nil {
					st.Close()
					return
				}
				if writeFrame(st, buf[:k]) != nil {
					return
				}
			}
		}()
		buf := make([]byte, 64<<10)
		for {
			st.SetReadDeadline(time.Now().Add(UDPIdle))
			k, err := readFrame(st, buf)
			if err != nil {
				return
			}
			out.Write(buf[:k])
		}
	}
}

func writeFrame(w io.Writer, b []byte) error {
	if len(b) > 65535 {
		return errors.New("datagram too large")
	}
	f := make([]byte, 2+len(b))
	binary.BigEndian.PutUint16(f, uint16(len(b)))
	copy(f[2:], b)
	_, err := w.Write(f)
	return err
}

func readFrame(r io.Reader, buf []byte) (int, error) {
	var l [2]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return 0, err
	}
	n := int(binary.BigEndian.Uint16(l[:]))
	if n > len(buf) {
		return 0, errors.New("datagram too large")
	}
	return io.ReadFull(r, buf[:n])
}
