// Package edge shares one TCP port (usually 443) between the panel and the
// Reality protocols.
//
// Every connection is peeked: a TLS ClientHello whose SNI belongs to a Reality
// inbound is piped, untouched, to that inbound on a private loopback port, with
// a PROXY protocol header so the core still sees the user's real address.
// Everything else (the panel, the subscription page, plain HTTP, probes by IP)
// goes to the panel's own http.Server through an in-memory listener.
//
// sing-box removed PROXY protocol support, so a route can go without the
// header; the router then remembers which loopback port belongs to which client
// (ClientOf), and the core's connection log is translated back to the real IP.
package edge

import (
	"bufio"
	"crypto/aes"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// Target is where one SNI is sent.
type Target struct {
	Addr        string // "127.0.0.1:port"
	ProxyHeader bool   // send a PROXY v1 line first (Xray accepts it, sing-box does not)

	// AuthKey, when set, lets through only ClientHellos whose random carries
	// this key (TrustTunnel's client_random_auth_key, see ValidAuthRandom).
	// Every other hello goes to Decoy, the real site of the name ("host:443"),
	// so a probe sees that site and its real certificate, as with Reality.
	AuthKey string // the key's bytes (a string keeps Target comparable)
	Decoy   string
}

// Fallback is the route key for TLS connections that name no known SNI.
const Fallback = "*"

// Router owns the shared port.
type Router struct {
	// PanelTLS: the panel speaks HTTPS, so TLS for unknown names goes to it;
	// otherwise such TLS goes to the Fallback route. Set it before Serve.
	PanelTLS bool

	ln     net.Listener
	panel  *chanListener
	mu     sync.RWMutex
	routes map[string]Target // lower-case SNI
	closed chan struct{}

	pmu   sync.Mutex
	peers map[string]peer // loopback address we dialed from -> the client behind it
}

type peer struct {
	ip     string
	closed time.Time
}

// Listen opens the shared port.
func Listen(addr string) (*Router, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	r := &Router{ln: ln, routes: map[string]Target{}, closed: make(chan struct{}), peers: map[string]peer{}}
	r.panel = &chanListener{ch: make(chan net.Conn, 64), addr: ln.Addr(), closed: r.closed, done: make(chan struct{})}
	return r, nil
}

// Addr is the shared port's address.
func (r *Router) Addr() net.Addr { return r.ln.Addr() }

// PanelListener hands the panel's http.Server every connection that is not for a protocol.
func (r *Router) PanelListener() net.Listener { return r.panel }

// SetRoutes replaces the SNI table (names are matched without case).
func (r *Router) SetRoutes(routes map[string]Target) {
	m := make(map[string]Target, len(routes))
	for k, v := range routes {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" && v.Addr != "" {
			m[k] = v
		}
	}
	r.mu.Lock()
	r.routes = m
	r.mu.Unlock()
}

// Routes returns a copy of the SNI table.
func (r *Router) Routes() map[string]Target {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Target, len(r.routes))
	for k, v := range r.routes {
		out[k] = v
	}
	return out
}

// ClientOf returns the real client IP behind a loopback address the router
// dialed a protocol from ("127.0.0.1:54321"), or "" when it is not one of ours.
// Closed connections are remembered for a minute, so a late log line still resolves.
func (r *Router) ClientOf(local string) string {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	return r.peers[local].ip
}

func (r *Router) addPeer(local, ip string) {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	if len(r.peers) > 2048 {
		cut := time.Now().Add(-time.Minute)
		for k, p := range r.peers {
			if !p.closed.IsZero() && p.closed.Before(cut) {
				delete(r.peers, k)
			}
		}
	}
	r.peers[local] = peer{ip: ip}
}

func (r *Router) closePeer(local string) {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	if p, ok := r.peers[local]; ok {
		p.closed = time.Now()
		r.peers[local] = p
	}
}

// Serve accepts connections until Close.
func (r *Router) Serve() error {
	for {
		c, err := r.ln.Accept()
		if err != nil {
			select {
			case <-r.closed:
				return nil
			default:
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		go r.handle(c)
	}
}

// Close stops the shared port and the panel listener.
func (r *Router) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return r.ln.Close()
}

func (r *Router) handle(c net.Conn) {
	br := bufio.NewReaderSize(c, 16<<10)
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	sni, isTLS := "", false
	var random []byte
	if hdr, err := br.Peek(5); err == nil && hdr[0] == 0x16 && hdr[1] == 0x03 {
		isTLS = true
		sni, random = peekHello(br)
	} else if err != nil {
		c.Close()
		return
	}
	c.SetReadDeadline(time.Time{})
	if isTLS {
		r.mu.RLock()
		target, ok := r.routes[strings.ToLower(sni)]
		if !ok && !r.PanelTLS {
			// a TLS hello for no known name: a plain-HTTP panel cannot answer it, so
			// the camouflage Reality inbound does, forwarding it to its real target
			// exactly as Reality alone would. Probes see the target site, not us.
			target, ok = r.routes[Fallback]
		}
		r.mu.RUnlock()
		if ok && target.AuthKey != "" && !ValidAuthRandom([]byte(target.AuthKey), random, sni) {
			if target.Decoy == "" {
				c.Close()
				return
			}
			// not one of the apps: the real site answers, untouched
			target = Target{Addr: target.Decoy}
		}
		if ok {
			r.pipe(c, br, target)
			return
		}
	}
	pc := &peekedConn{Conn: c, r: br}
	select {
	case r.panel.ch <- pc:
	case <-r.closed:
		c.Close()
	case <-r.panel.done:
		c.Close()
	case <-time.After(10 * time.Second):
		c.Close()
	}
}

// pipe connects the client to a protocol.
func (r *Router) pipe(c net.Conn, br *bufio.Reader, target Target) {
	defer c.Close()
	t, err := net.DialTimeout("tcp", target.Addr, 5*time.Second)
	if err != nil {
		return
	}
	defer t.Close()
	local := t.LocalAddr().String()
	if host, _, err := net.SplitHostPort(c.RemoteAddr().String()); err == nil {
		r.addPeer(local, host)
		defer r.closePeer(local)
	}
	if target.ProxyHeader {
		if _, err := io.WriteString(t, proxyHeader(c.RemoteAddr(), c.LocalAddr())); err != nil {
			return
		}
	}
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(t, br)
		if tc, ok := t.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		io.Copy(c, t)
		if tc, ok := c.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}

// proxyHeader is a PROXY protocol v1 line ("PROXY TCP4 src dst sport dport").
func proxyHeader(src, dst net.Addr) string {
	s, ok1 := src.(*net.TCPAddr)
	d, ok2 := dst.(*net.TCPAddr)
	if !ok1 || !ok2 {
		return "PROXY UNKNOWN\r\n"
	}
	fam := "TCP4"
	sip, dip := s.IP.To4(), d.IP.To4()
	if sip == nil || dip == nil {
		fam, sip, dip = "TCP6", s.IP.To16(), d.IP.To16()
	}
	return fmt.Sprintf("PROXY %s %s %s %d %d\r\n", fam, sip, dip, s.Port, d.Port)
}

// ValidAuthRandom checks a ClientHello's 32-byte random against TrustTunnel's
// client_random_auth_key: the first 16 bytes are random, the last 16 must be
// AES-128 (one block) of SHA-256(SNI)[:16] under the key HKDF-SHA256(secret =
// key, salt = random[:16], info = "tls13 encryption context"). The same check
// as the endpoint's rules (lib/src/rules.rs, TrustTunnel 1.3); the ClientHello
// still looks like any other.
func ValidAuthRandom(key, random []byte, sni string) bool {
	if len(key) == 0 || len(random) != 32 || sni == "" {
		return false
	}
	k, err := hkdf.Key(sha256.New, key, random[:16], "tls13 encryption context", 16)
	if err != nil {
		return false
	}
	b, err := aes.NewCipher(k)
	if err != nil {
		return false
	}
	h := sha256.Sum256([]byte(sni))
	var want [16]byte
	b.Encrypt(want[:], h[:16])
	return subtle.ConstantTimeCompare(want[:], random[16:]) == 1
}

// peekServerName reads the ClientHello's SNI without consuming anything.
func peekServerName(br *bufio.Reader) string {
	sni, _ := peekHello(br)
	return sni
}

// peekHello reads the ClientHello's SNI and random without consuming
// anything. A client with TLS fragment on (Xray's "tlshello") splits the
// ClientHello over many small handshake records; they are joined until the
// message is whole.
func peekHello(br *bufio.Reader) (string, []byte) {
	var hello []byte
	off := 0
	for records := 0; records < 512; records++ {
		hdr, err := br.Peek(off + 5)
		if err != nil {
			return "", nil
		}
		h := hdr[off:]
		if h[0] != 0x16 || h[1] != 0x03 {
			return "", nil
		}
		n := int(h[3])<<8 | int(h[4])
		if n == 0 || n > 16<<10 {
			return "", nil
		}
		rec, err := br.Peek(off + 5 + n)
		if err != nil {
			return "", nil // longer than the buffer: no name, the fallback answers
		}
		hello = append(hello, rec[off+5:]...)
		off += 5 + n
		if len(hello) >= 4 {
			if need := 4 + (int(hello[1])<<16 | int(hello[2])<<8 | int(hello[3])); len(hello) >= need {
				var random []byte
				if need >= 38 && hello[0] == 1 {
					random = append([]byte(nil), hello[6:38]...) // type, length, version, then the random
				}
				return ServerName(hello[:need]), random
			}
		}
	}
	return "", nil
}

// ServerName reads the SNI from a TLS handshake message (a ClientHello, without the record header).
func ServerName(b []byte) string {
	// handshake: type(1)=1, length(3), version(2), random(32)
	if len(b) < 38 || b[0] != 1 {
		return ""
	}
	p := 38
	skip := func(lenBytes int) bool {
		if p+lenBytes > len(b) {
			return false
		}
		n := 0
		for i := 0; i < lenBytes; i++ {
			n = n<<8 | int(b[p+i])
		}
		p += lenBytes + n
		return p <= len(b)
	}
	if !skip(1) || !skip(2) || !skip(1) { // session id, cipher suites, compression
		return ""
	}
	if p+2 > len(b) {
		return ""
	}
	end := p + 2 + (int(b[p])<<8 | int(b[p+1]))
	p += 2
	if end > len(b) {
		end = len(b)
	}
	for p+4 <= end {
		typ := int(b[p])<<8 | int(b[p+1])
		n := int(b[p+2])<<8 | int(b[p+3])
		p += 4
		if p+n > end {
			return ""
		}
		if typ == 0 { // server_name: list length(2), name type(1)=0, name length(2), name
			e := b[p : p+n]
			if len(e) >= 5 && e[2] == 0 {
				l := int(e[3])<<8 | int(e[4])
				if 5+l <= len(e) {
					return string(e[5 : 5+l])
				}
			}
			return ""
		}
		p += n
	}
	return ""
}

// ---------------------------------------------------------------- plumbing

// chanListener is the panel's side of the router. Closing it (http.Server.Close
// or Shutdown do) must wake a blocked Accept, or the server waits for ever.
type chanListener struct {
	ch     chan net.Conn
	addr   net.Addr
	closed chan struct{} // the router's
	done   chan struct{} // this listener's
	once   sync.Once
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}
func (l *chanListener) Addr() net.Addr { return l.addr }

// peekedConn replays the bytes the router already read.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
