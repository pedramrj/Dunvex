// Package sshproxy is Dunvex's own SSH server. It does one thing: port
// forwarding. Each user signs in with their name and password; every
// connection the app forwards (direct-tcpip) is handed to a SOCKS inbound of
// Xray on loopback, so the server's routing rules apply to it. There is no
// shell, no command, no subsystem (SFTP), no agent or X11 forwarding and no
// remote forwarding. UDP can ride inside a forwarded connection in sing-box's
// UDP-over-TCP (the Hiddify app turns it on for SSH).
//
// The server counts what each user moves and knows the address each session
// comes from, so usage, online state and device limits work as they do for
// the other protocols. A user who loses access is cut off at once.
package sshproxy

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// Version is what the server tells clients (and scanners) it is: the SSH
// server of an ordinary Ubuntu machine.
const Version = "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.14"

// Config is one SSH server.
type Config struct {
	Listen   string            // host:port to listen on
	HostKey  ssh.Signer        // the server's host key
	Upstream string            // SOCKS5 address (Xray on loopback) every forward goes through
	Users    map[string]string // name → password
}

// Traffic is what a user moved: Up from the app, Down to it.
type Traffic struct{ Up, Down int64 }

// Session is one signed-in connection.
type Session struct {
	User string
	IP   string
	Last int64 // unix second of its last activity (sign-in, a forward, data)
}

// Server is a running SSH server.
type Server struct {
	cfg Config
	ln  net.Listener
	sc  *ssh.ServerConfig

	// Logf receives the server's notable events (nil: the standard logger).
	Logf func(format string, v ...any)

	mu      sync.Mutex
	users   map[string]string
	conns   map[*conn]struct{}
	counts  map[string]*counter
	fails   map[string]*failures // per client IP
	closed  bool
	wg      sync.WaitGroup
	dialTTL time.Duration
	pending chan struct{} // one per sign-in under way
}

type counter struct{ up, down atomic.Int64 }

type failures struct {
	n     int
	first time.Time
}

// conn is one signed-in SSH connection.
type conn struct {
	sc   *ssh.ServerConn
	user string
	pass string
	ip   string
	last atomic.Int64
	cnt  *counter
	gone chan struct{} // closed when the connection ends: its forwards close too
}

func (c *conn) touch() { c.last.Store(time.Now().Unix()) }

// Limits on guessing: after failLimit wrong passwords from one address within
// failWindow, that address is refused until the window ends.
const (
	failLimit  = 10
	failWindow = 10 * time.Minute
	handshake  = 30 * time.Second // a client must sign in within this
	keepalive  = 30 * time.Second // the server checks a quiet client this often
	maxPending = 256              // sign-ins under way at once; more are turned away
)

// Listen starts an SSH server.
func Listen(cfg Config) (*Server, error) {
	if cfg.HostKey == nil {
		return nil, errors.New("ssh: no host key")
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, ln: ln, conns: map[*conn]struct{}{}, counts: map[string]*counter{}, fails: map[string]*failures{},
		dialTTL: 15 * time.Second, pending: make(chan struct{}, maxPending)}
	s.setUsers(cfg.Users)
	s.sc = &ssh.ServerConfig{
		ServerVersion:    Version,
		MaxAuthTries:     4,
		PasswordCallback: s.checkPassword,
	}
	s.sc.AddHostKey(cfg.HostKey)
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

// Addr is where the server listens.
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

func (s *Server) logf(format string, v ...any) {
	if s.Logf != nil {
		s.Logf(format, v...)
		return
	}
	log.Printf("ssh: "+format, v...)
}

func (s *Server) setUsers(users map[string]string) {
	m := make(map[string]string, len(users))
	for k, v := range users {
		if k != "" && v != "" {
			m[k] = v
		}
	}
	s.users = m
}

// Update replaces the users. Whoever is no longer among them, or whose
// password changed, is disconnected at once; nobody else notices.
func (s *Server) Update(users map[string]string) {
	s.mu.Lock()
	s.setUsers(users)
	var drop []*conn
	for c := range s.conns {
		if p, ok := s.users[c.user]; !ok || p != c.pass {
			drop = append(drop, c)
		}
	}
	s.mu.Unlock()
	for _, c := range drop {
		c.sc.Close()
	}
}

// Close stops the server and disconnects everyone.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	err := s.ln.Close()
	var all []*conn
	for c := range s.conns {
		all = append(all, c)
	}
	s.mu.Unlock()
	for _, c := range all {
		c.sc.Close()
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	return err
}

// Sessions lists the signed-in connections.
func (s *Server) Sessions() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, 0, len(s.conns))
	for c := range s.conns {
		out = append(out, Session{User: c.user, IP: c.ip, Last: c.last.Load()})
	}
	return out
}

// Take returns what each user moved since the last call.
func (s *Server) Take() map[string]Traffic {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Traffic{}
	for name, c := range s.counts {
		t := Traffic{Up: c.up.Swap(0), Down: c.down.Swap(0)}
		if t.Up > 0 || t.Down > 0 {
			out[name] = t
		}
	}
	return out
}

func clientIP(a net.Addr) string {
	if t, ok := a.(*net.TCPAddr); ok {
		return t.IP.String()
	}
	h, _, _ := net.SplitHostPort(a.String())
	return h
}

func (s *Server) checkPassword(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
	ip := clientIP(meta.RemoteAddr())
	now := time.Now()
	s.mu.Lock()
	f := s.fails[ip]
	if f != nil && now.Sub(f.first) > failWindow {
		delete(s.fails, ip)
		f = nil
	}
	if f != nil && f.n >= failLimit {
		s.mu.Unlock()
		return nil, errors.New("too many wrong passwords")
	}
	want, ok := s.users[meta.User()]
	s.mu.Unlock()
	if ok && len(pass) > 0 && subtle.ConstantTimeCompare([]byte(want), pass) == 1 {
		return &ssh.Permissions{Extensions: map[string]string{"pass": want}}, nil
	}
	s.mu.Lock()
	if f = s.fails[ip]; f == nil {
		f = &failures{first: now}
		s.fails[ip] = f
		if len(s.fails) > 4096 { // forget the oldest addresses rather than grow without end
			for k, v := range s.fails {
				if now.Sub(v.first) > failWindow || len(s.fails) > 4096 {
					delete(s.fails, k)
				}
			}
			s.fails[ip] = f
		}
	}
	f.n++
	blocked := f.n == failLimit
	s.mu.Unlock()
	if blocked {
		s.logf("%s gave %d wrong passwords; refused for %v", ip, failLimit, failWindow)
	}
	time.Sleep(time.Second) // slows guessing down
	return nil, errors.New("wrong name or password")
}

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(nc)
		}()
	}
}

func (s *Server) handle(nc net.Conn) {
	select {
	case s.pending <- struct{}{}:
	default:
		nc.Close() // a flood of half-open sign-ins: the real clients retry
		return
	}
	nc.SetDeadline(time.Now().Add(handshake))
	sc, chans, reqs, err := ssh.NewServerConn(nc, s.sc)
	<-s.pending
	if err != nil {
		nc.Close()
		return
	}
	nc.SetDeadline(time.Time{})
	c := &conn{sc: sc, user: sc.User(), pass: sc.Permissions.Extensions["pass"], ip: clientIP(nc.RemoteAddr()), gone: make(chan struct{})}
	c.touch()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		sc.Close()
		return
	}
	// the password may have changed while this client signed in
	if p, ok := s.users[c.user]; !ok || p != c.pass {
		s.mu.Unlock()
		sc.Close()
		return
	}
	cnt := s.counts[c.user]
	if cnt == nil {
		cnt = &counter{}
		s.counts[c.user] = cnt
	}
	c.cnt = cnt
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		sc.Close()
	}()
	go ssh.DiscardRequests(reqs) // keepalives get "no"; remote forwarding is refused
	done := make(chan struct{})
	defer close(done)
	go s.keepAlive(sc, done)
	var wg sync.WaitGroup
	for nch := range chans {
		switch nch.ChannelType() {
		case "direct-tcpip":
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.forward(c, nch)
			}()
		case "session":
			// like an account whose shell is /bin/false: apps that open a
			// session do not fail, and nothing ever runs
			wg.Add(1)
			go func() {
				defer wg.Done()
				emptySession(nch)
			}()
		default:
			nch.Reject(ssh.Prohibited, "only port forwarding is allowed")
		}
	}
	close(c.gone)
	wg.Wait()
}

// keepAlive asks a client for a sign of life now and then; one that does not
// answer (a phone that lost its network) is let go.
func (s *Server) keepAlive(sc *ssh.ServerConn, done <-chan struct{}) {
	t := time.NewTicker(keepalive)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
		}
		answered := make(chan struct{})
		go func() {
			sc.SendRequest("keepalive@openssh.com", true, nil)
			close(answered)
		}()
		select {
		case <-answered:
		case <-done:
			return
		case <-time.After(3 * keepalive):
			sc.Close()
			return
		}
	}
}

// emptySession accepts a session and refuses everything asked on it, except
// a shell, which "starts" and ends at once with exit status 1.
func emptySession(nch ssh.NewChannel) {
	ch, reqs, err := nch.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	timeout := time.NewTimer(time.Minute)
	defer timeout.Stop()
	for {
		select {
		case r, ok := <-reqs:
			if !ok {
				return
			}
			if r.Type == "shell" {
				r.Reply(true, nil)
				ch.SendRequest("exit-status", false, binary.BigEndian.AppendUint32(nil, 1))
				return
			}
			r.Reply(false, nil)
		case <-timeout.C:
			return
		}
	}
}

// forwardMsg is the payload of a direct-tcpip channel request (RFC 4254 7.2).
type forwardMsg struct {
	Host     string
	Port     uint32
	OrigHost string
	OrigPort uint32
}

func (s *Server) forward(c *conn, nch ssh.NewChannel) {
	var m forwardMsg
	if err := ssh.Unmarshal(nch.ExtraData(), &m); err != nil {
		nch.Reject(ssh.ConnectionFailed, "bad request")
		return
	}
	c.touch()
	host := strings.TrimSuffix(strings.TrimSpace(m.Host), ".")
	if v := uotVersion(host); v > 0 {
		s.uot(c, nch, v)
		return
	}
	if m.Port == 0 || m.Port > 65535 || host == "" {
		nch.Reject(ssh.ConnectionFailed, "bad address")
		return
	}
	if forbidden(host) {
		nch.Reject(ssh.Prohibited, "administratively prohibited")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.dialTTL)
	up, err := socksConnect(ctx, s.cfg.Upstream, host, int(m.Port))
	cancel()
	if err != nil {
		nch.Reject(ssh.ConnectionFailed, "connect failed")
		return
	}
	ch, reqs, err := nch.Accept()
	if err != nil {
		up.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	pipe(c, ch, up)
}

// pipe copies both ways until both sides are done, counting each way. When
// the SSH connection ends, the upstream connection is closed at once (a site
// that keeps its side open would hold it otherwise).
func pipe(c *conn, ch ssh.Channel, up net.Conn) {
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-c.gone:
			up.Close()
			ch.Close()
		case <-finished:
		}
	}()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // app → internet
		defer wg.Done()
		copyCount(up, ch, &c.cnt.up, c)
		if cw, ok := up.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			up.Close()
		}
	}()
	go func() { // internet → app
		defer wg.Done()
		copyCount(ch, up, &c.cnt.down, c)
		ch.CloseWrite()
	}()
	wg.Wait()
	ch.Close()
	up.Close()
}

func copyCount(dst io.Writer, src io.Reader, n *atomic.Int64, c *conn) {
	buf := make([]byte, 32<<10)
	for {
		k, err := src.Read(buf)
		if k > 0 {
			n.Add(int64(k))
			c.touch()
			if _, werr := dst.Write(buf[:k]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// forbidden is Forbidden (tests that reach a local server through a real Xray replace it).
var forbidden = Forbidden

// Forbidden reports a destination a user may not reach through the server:
// the server itself and private networks (its loopback services, the cloud
// provider's metadata address, the local network).
func Forbidden(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if i := strings.IndexByte(h, '%'); i >= 0 {
		h = h[:i] // a zone: fe80::1%eth0
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		if v4[0] == 0 || (v4[0] == 100 && v4[1]&0xc0 == 64) { // 0.0.0.0/8, 100.64.0.0/10 (shared address space)
			return true
		}
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast()
}
