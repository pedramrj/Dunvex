package sshproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// fakeSocks is a SOCKS5 server for the tests: names in its table go to local
// addresses (a real destination at 127.0.0.1 would be refused by the server).
type fakeSocks struct {
	ln    net.Listener
	table map[string]string // name:port → local address
	mu    sync.Mutex
	seen  []string // CONNECT and UDP destinations asked for
}

func newFakeSocks(t *testing.T, table map[string]string) *fakeSocks {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSocks{ln: ln, table: table}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeSocks) note(s string) { f.mu.Lock(); f.seen = append(f.seen, s); f.mu.Unlock() }

func (f *fakeSocks) handle(c net.Conn) {
	defer c.Close()
	var hello [2]byte
	if _, err := io.ReadFull(c, hello[:]); err != nil {
		return
	}
	methods := make([]byte, hello[1])
	io.ReadFull(c, methods)
	c.Write([]byte{5, 0})
	var head [3]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return
	}
	host, port, err := readSocksAddr(c)
	if err != nil {
		return
	}
	switch head[1] {
	case 1:
		dst := joinHostPort(host, port)
		f.note("tcp " + dst)
		to, ok := f.table[dst]
		if !ok {
			c.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0}) // host unreachable
			return
		}
		up, err := net.Dial("tcp", to)
		if err != nil {
			c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
			return
		}
		defer up.Close()
		c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		go io.Copy(up, c)
		io.Copy(c, up)
	case 3:
		pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			return
		}
		defer pc.Close()
		la := pc.LocalAddr().(*net.UDPAddr)
		rep, _ := socksAddr([]byte{5, 0, 0}, "127.0.0.1", la.Port)
		c.Write(rep)
		go func() { io.Copy(io.Discard, c); pc.Close() }()
		var client *net.UDPAddr
		back := map[string]string{} // local address → name:port
		buf := make([]byte, 70000)
		for {
			n, from, err := pc.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if from.Port == la.Port {
				continue
			}
			if name, ok := back[from.String()]; ok && client != nil { // an answer
				h, p, _ := net.SplitHostPort(name)
				var port int
				for _, ch := range p {
					port = port*10 + int(ch-'0')
				}
				out, _ := socksAddr([]byte{0, 0, 0}, h, port)
				pc.WriteToUDP(append(out, buf[:n]...), client)
				continue
			}
			client = from
			rd := &sliceReader{b: buf[3:n]}
			h, p, err := readSocksAddr(rd)
			if err != nil {
				continue
			}
			dst := joinHostPort(h, p)
			f.note("udp " + dst)
			to, ok := f.table[dst]
			if !ok {
				continue
			}
			ta, _ := net.ResolveUDPAddr("udp", to)
			back[ta.String()] = dst
			pc.WriteToUDP(rd.b, ta)
		}
	}
}

func echoTCP(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

func echoUDP(t *testing.T) string {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 70000)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pc.WriteTo(append([]byte("echo:"), buf[:n]...), from)
		}
	}()
	t.Cleanup(func() { pc.Close() })
	return pc.LocalAddr().String()
}

func startServer(t *testing.T, upstream string, users map[string]string) (*Server, string) {
	t.Helper()
	seed := NewHostKey()
	key, err := HostKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Listen(Config{Listen: "127.0.0.1:0", HostKey: key, Upstream: upstream, Users: users})
	if err != nil {
		t.Fatal(err)
	}
	s.Logf = t.Logf
	t.Cleanup(func() { s.Close() })
	return s, PublicKey(seed)
}

func dial(addr, user, pass, hostKey string) (*ssh.Client, error) {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(hostKey))
	if err != nil {
		return nil, err
	}
	return ssh.Dial("tcp", addr, &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.FixedHostKey(pk), Timeout: 10 * time.Second})
}

func roundTrip(t *testing.T, c net.Conn, msg string) {
	t.Helper()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != msg {
		t.Fatalf("echo: got %q, want %q", got, msg)
	}
}

func TestSignInForwardAndCount(t *testing.T) {
	echo := echoTCP(t)
	fs := newFakeSocks(t, map[string]string{"echo.test:7": echo})
	s, hk := startServer(t, fs.ln.Addr().String(), map[string]string{"alice": "secret-a", "bob": "secret-b"})
	addr := s.Addr().String()

	if _, err := dial(addr, "alice", "wrong", hk); err == nil {
		t.Fatal("a wrong password was let in")
	}
	if _, err := dial(addr, "mallory", "secret-a", hk); err == nil {
		t.Fatal("an unknown user was let in")
	}
	// a client that pins another key must refuse the server
	if _, err := dial(addr, "alice", "secret-a", PublicKey(NewHostKey())); err == nil {
		t.Fatal("the client accepted a host key it did not pin")
	}

	cl, err := dial(addr, "alice", "secret-a", hk)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	c, err := cl.Dial("tcp", "echo.test:7")
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, c, "hello through ssh")
	big := strings.Repeat("x", 200000)
	roundTrip(t, c, big)
	c.Close()
	time.Sleep(200 * time.Millisecond)

	tr := s.Take()["alice"]
	want := int64(len("hello through ssh") + len(big))
	if tr.Up != want || tr.Down != want {
		t.Fatalf("counted %+v, want %d each way", tr, want)
	}
	if again := s.Take()["alice"]; again.Up != 0 || again.Down != 0 {
		t.Fatalf("Take did not reset: %+v", again)
	}
	ss := s.Sessions()
	if len(ss) != 1 || ss[0].User != "alice" || ss[0].IP != "127.0.0.1" || ss[0].Last == 0 {
		t.Fatalf("sessions: %+v", ss)
	}
	// a destination the upstream cannot reach fails the forward, not the session
	if _, err := cl.Dial("tcp", "nowhere.test:9"); err == nil {
		t.Fatal("forward to an unknown destination succeeded")
	}
	c2, err := cl.Dial("tcp", "echo.test:7")
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, c2, "still here")
	c2.Close()
}

func TestNoShellNoLocalNoRemote(t *testing.T) {
	echo := echoTCP(t)
	fs := newFakeSocks(t, map[string]string{"echo.test:7": echo})
	s, hk := startServer(t, fs.ln.Addr().String(), map[string]string{"alice": "pw"})
	cl, err := dial(s.Addr().String(), "alice", "pw", hk)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	// the server itself and private networks are out of reach
	for _, d := range []string{"127.0.0.1:22", "localhost:62789", "[::1]:80", "10.0.0.1:80", "192.168.1.1:80", "169.254.169.254:80", "0.0.0.0:22", "100.64.0.1:80"} {
		if c, err := cl.Dial("tcp", d); err == nil {
			c.Close()
			t.Fatalf("forward to %s was allowed", d)
		} else if !strings.Contains(err.Error(), "prohibited") {
			t.Fatalf("forward to %s: %v (want prohibited)", d, err)
		}
	}
	for _, x := range fs.seen {
		t.Fatalf("a refused destination reached the upstream: %s", x)
	}

	// commands do not run; a shell ends at once
	sess, err := cl.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Run("id"); err == nil {
		t.Fatal("a command ran")
	}
	sess, err = cl.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.RequestSubsystem("sftp"); err == nil {
		t.Fatal("sftp was allowed")
	}
	sess, err = cl.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sess.Stdout = &out
	if err := sess.Shell(); err != nil {
		t.Fatalf("shell request: %v", err)
	}
	var exit *ssh.ExitError
	if err := sess.Wait(); !errors.As(err, &exit) || exit.ExitStatus() != 1 || out.Len() != 0 {
		t.Fatalf("shell: %v, output %q (want exit status 1 and nothing)", err, out.String())
	}

	// remote forwarding is refused
	if ln, err := cl.Listen("tcp", "0.0.0.0:0"); err == nil {
		ln.Close()
		t.Fatal("remote forwarding was allowed")
	}
	// other channel types are refused
	if _, _, err := cl.OpenChannel("auth-agent@openssh.com", nil); err == nil {
		t.Fatal("an agent channel was accepted")
	}
	// and the session still forwards
	c, err := cl.Dial("tcp", "echo.test:7")
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, c, "ok")
	c.Close()
}

func TestUpdateCutsOffRemovedUsers(t *testing.T) {
	echo := echoTCP(t)
	fs := newFakeSocks(t, map[string]string{"echo.test:7": echo})
	s, hk := startServer(t, fs.ln.Addr().String(), map[string]string{"alice": "pa", "bob": "pb", "carol": "pc"})
	addr := s.Addr().String()
	clients := map[string]*ssh.Client{}
	for name, pass := range map[string]string{"alice": "pa", "bob": "pb", "carol": "pc"} {
		cl, err := dial(addr, name, pass, hk)
		if err != nil {
			t.Fatal(err)
		}
		defer cl.Close()
		clients[name] = cl
	}
	conn, err := clients["alice"].Dial("tcp", "echo.test:7")
	if err != nil {
		t.Fatal(err)
	}
	// bob is removed, carol's password changes, alice stays
	s.Update(map[string]string{"alice": "pa", "carol": "new"})
	for _, name := range []string{"bob", "carol"} {
		done := make(chan error, 1)
		go func() { done <- clients[name].Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s was not disconnected", name)
		}
	}
	roundTrip(t, conn, "alice keeps her connection")
	if _, err := dial(addr, "bob", "pb", hk); err == nil {
		t.Fatal("a removed user signed in")
	}
	if _, err := dial(addr, "carol", "pc", hk); err == nil {
		t.Fatal("an old password still works")
	}
	cl, err := dial(addr, "carol", "new", hk)
	if err != nil {
		t.Fatal(err)
	}
	cl.Close()
	if n := len(s.Sessions()); n != 1 {
		time.Sleep(300 * time.Millisecond)
		if n = len(s.Sessions()); n != 1 {
			t.Fatalf("%d sessions left, want 1 (alice)", n)
		}
	}
}

func TestGuessingIsLimited(t *testing.T) {
	fs := newFakeSocks(t, nil)
	s, hk := startServer(t, fs.ln.Addr().String(), map[string]string{"alice": "pw"})
	s.mu.Lock()
	s.fails["127.0.0.1"] = &failures{n: failLimit, first: time.Now()}
	s.mu.Unlock()
	if _, err := dial(s.Addr().String(), "alice", "pw", hk); err == nil {
		t.Fatal("an address over the limit was let in, even with the right password")
	}
	s.mu.Lock()
	s.fails["127.0.0.1"].first = time.Now().Add(-failWindow - time.Second)
	s.mu.Unlock()
	cl, err := dial(s.Addr().String(), "alice", "pw", hk)
	if err != nil {
		t.Fatalf("the window ended but the address is still refused: %v", err)
	}
	cl.Close()
}

// uotFrame builds one app → server datagram (non-connected stream).
func uotFrame(host string, port int, p []byte) []byte {
	b := appendUoTAddr(nil, host, port)
	b = binary.BigEndian.AppendUint16(b, uint16(len(p)))
	return append(b, p...)
}

func TestUDPOverTCP(t *testing.T) {
	fs := newFakeSocks(t, map[string]string{"udp.test:53": echoUDP(t), "1.1.1.1:53": echoUDP(t)})
	s, hk := startServer(t, fs.ln.Addr().String(), map[string]string{"alice": "pw"})
	cl, err := dial(s.Addr().String(), "alice", "pw", hk)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	t.Run("v2", func(t *testing.T) {
		c, err := cl.Dial("tcp", uotMagic+":0")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		req, _ := socksAddr([]byte{0}, "udp.test", 53) // not connected: every datagram has an address
		c.Write(req)
		c.Write(uotFrame("udp.test", 53, []byte("q1")))
		c.Write(uotFrame("1.1.1.1", 53, []byte("q2")))
		c.Write(uotFrame("10.0.0.1", 53, []byte("never"))) // dropped: a private address
		br := bufio.NewReader(c)
		got := map[string]string{}
		for i := 0; i < 2; i++ {
			h, p, err := readUoTAddr(br)
			if err != nil {
				t.Fatal(err)
			}
			var l [2]byte
			io.ReadFull(br, l[:])
			body := make([]byte, binary.BigEndian.Uint16(l[:]))
			io.ReadFull(br, body)
			got[joinHostPort(h, p)] = string(body)
		}
		if got["udp.test:53"] != "echo:q1" || got["1.1.1.1:53"] != "echo:q2" {
			t.Fatalf("answers: %v", got)
		}
	})

	t.Run("v2 connected", func(t *testing.T) {
		c, err := cl.Dial("tcp", uotMagic+":0")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		req, _ := socksAddr([]byte{1}, "udp.test", 53)
		c.Write(req)
		c.Write(binary.BigEndian.AppendUint16(nil, 3))
		c.Write([]byte("abc"))
		var l [2]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			t.Fatal(err)
		}
		body := make([]byte, binary.BigEndian.Uint16(l[:]))
		io.ReadFull(c, body)
		if string(body) != "echo:abc" {
			t.Fatalf("answer %q", body)
		}
	})

	t.Run("v1", func(t *testing.T) {
		c, err := cl.Dial("tcp", uotLegacy+":0")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		c.Write(uotFrame("udp.test", 53, []byte("old")))
		br := bufio.NewReader(c)
		if _, _, err := readUoTAddr(br); err != nil {
			t.Fatal(err)
		}
		var l [2]byte
		io.ReadFull(br, l[:])
		body := make([]byte, binary.BigEndian.Uint16(l[:]))
		io.ReadFull(br, body)
		if string(body) != "echo:old" {
			t.Fatalf("answer %q", body)
		}
	})

	time.Sleep(100 * time.Millisecond)
	tr := s.Take()["alice"]
	if tr.Up != int64(len("q1q2abcold")) || tr.Down != int64(len("echo:q1echo:q2echo:abcecho:old")) {
		t.Fatalf("UDP counted %+v", tr)
	}
	for _, x := range fs.seen {
		if strings.Contains(x, "10.0.0.1") {
			t.Fatal("a private address reached the upstream")
		}
	}
}

func TestForbidden(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "127.8.8.8": true, "::1": true, "[::1]": true, "localhost": true, "LOCALHOST": true, "a.localhost": true,
		"10.1.2.3": true, "172.16.0.1": true, "192.168.0.1": true, "169.254.169.254": true, "fe80::1%eth0": true, "fd00::1": true,
		"0.0.0.0": true, "::": true, "100.64.1.1": true, "224.0.0.1": true, "::ffff:127.0.0.1": true,
		"1.1.1.1": false, "8.8.8.8": false, "2606:4700::1111": false, "example.com": false, "100.128.0.1": false,
	} {
		if got := Forbidden(host); got != want {
			t.Errorf("Forbidden(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestHostKeys(t *testing.T) {
	seed := NewHostKey()
	if seed == NewHostKey() {
		t.Fatal("two host keys are the same")
	}
	pub := PublicKey(seed)
	if !strings.HasPrefix(pub, "ssh-ed25519 AAAA") || pub != PublicKey(seed) {
		t.Fatalf("public key %q", pub)
	}
	if fp := Fingerprint(seed); !strings.HasPrefix(fp, "SHA256:") {
		t.Fatalf("fingerprint %q", fp)
	}
	for _, bad := range []string{"", "nope", "AAAA"} {
		if _, err := HostKey(bad); err == nil || PublicKey(bad) != "" {
			t.Fatalf("a bad seed %q was taken", bad)
		}
	}
	if _, err := Listen(Config{Listen: "127.0.0.1:0"}); err == nil {
		t.Fatal("a server without a host key started")
	}
	_ = context.Background
}
