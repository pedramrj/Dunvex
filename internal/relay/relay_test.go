package relay

import (
	"bytes"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// freePort finds a port free for both TCP and UDP.
func freePort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 50; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := l.Addr().(*net.TCPAddr).Port
		l.Close()
		if u, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p}); err == nil {
			u.Close()
			return p
		}
	}
	t.Fatal("no free port")
	return 0
}

// echoServer answers TCP with "echo:"+data (until EOF) and UDP with "pong:"+data.
func echoServer(t *testing.T) string {
	t.Helper()
	p := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(p)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	u, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close(); u.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				b, _ := io.ReadAll(c) // the relay half-closes after the client's data
				c.Write(append([]byte("echo:"), b...))
			}()
		}
	}()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := u.ReadFromUDP(buf)
			if err != nil {
				return
			}
			u.WriteToUDP(append([]byte("pong:"), buf[:n]...), from)
		}
	}()
	return addr
}

func stat(r *Relay, port int) Stat {
	for _, s := range r.Stats() {
		if s.Listen == port {
			return s
		}
	}
	return Stat{}
}

func TestForwardTCPAndUDP(t *testing.T) {
	target := echoServer(t)
	r := New(nil)
	defer r.Close()
	port := freePort(t)
	r.Apply([]Forward{{Name: "Reality + Hysteria2", Listen: port, Network: "tcp+udp", Target: target}})
	if s := stat(r, port); !s.Listening || s.Error != "" {
		t.Fatalf("not listening: %+v", s)
	}
	at := "127.0.0.1:" + strconv.Itoa(port)

	// TCP, with the half-close passed through
	c, err := net.Dial("tcp", at)
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("x"), 100000)
	c.Write(payload)
	c.(*net.TCPConn).CloseWrite()
	got, _ := io.ReadAll(c)
	c.Close()
	if !bytes.Equal(got, append([]byte("echo:"), payload...)) {
		t.Fatalf("TCP answer: %d bytes", len(got))
	}

	// UDP, two clients kept apart
	for _, who := range []string{"a", "b"} {
		u, err := net.Dial("udp", at)
		if err != nil {
			t.Fatal(err)
		}
		u.SetDeadline(time.Now().Add(3 * time.Second))
		u.Write([]byte("ping-" + who))
		buf := make([]byte, 100)
		n, err := u.Read(buf)
		u.Close()
		if err != nil || string(buf[:n]) != "pong:ping-"+who {
			t.Fatalf("UDP %s: %q %v", who, buf[:n], err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	s := stat(r, port)
	if s.Up < 100000+12 || s.Down < 100005+20 {
		t.Errorf("counted up %d down %d", s.Up, s.Down)
	}
	if s.Conns != 2 { // the TCP one is done; two UDP flows are still open
		t.Errorf("open: %d", s.Conns)
	}
}

func TestApplyKeepsAndDrops(t *testing.T) {
	target := echoServer(t)
	r := New(map[int]string{2096: "the Dunvex agent"})
	defer r.Close()
	p1, p2 := freePort(t), freePort(t)
	a := Forward{Name: "a", Listen: p1, Network: "tcp", Target: target}
	r.Apply([]Forward{a, {Name: "agent", Listen: 2096, Network: "tcp", Target: target}})
	// an open connection on a forward that stays must survive the change
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(p1))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	r.Apply([]Forward{a, {Name: "b", Listen: p2, Network: "tcp", Target: target}})
	c.Write([]byte("still"))
	c.(*net.TCPConn).CloseWrite()
	got, _ := io.ReadAll(c)
	c.Close()
	if string(got) != "echo:still" {
		t.Errorf("the kept forward lost its connection: %q", got)
	}
	st := r.Stats()
	if len(st) != 2 || st[0].Listen != min(p1, p2) {
		t.Fatalf("stats: %+v", st)
	}
	if s := stat(r, 2096); s.Listen != 0 {
		t.Error("the reserved forward is still there")
	}
	r.Apply([]Forward{{Name: "agent", Listen: 2096, Network: "tcp", Target: target}})
	if s := stat(r, 2096); s.Listening || !strings.Contains(s.Error, "Dunvex agent") {
		t.Errorf("reserved port: %+v", s)
	}
	if _, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(p1), time.Second); err == nil {
		t.Error("a dropped forward still listens")
	}
}

func TestBusyPortAndDeadTarget(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	r := New(nil)
	defer r.Close()
	r.Apply([]Forward{{Name: "x", Listen: port, Network: "tcp", Target: "127.0.0.1:1"}})
	// on Windows a wildcard listener may share the port; Linux must refuse it
	if s := stat(r, port); !s.Listening && !strings.Contains(s.Error, "port") {
		t.Errorf("busy port: %+v", s)
	}

	dead := freePort(t)
	p := freePort(t)
	r.Apply([]Forward{{Name: "y", Listen: p, Network: "tcp", Target: "127.0.0.1:" + strconv.Itoa(dead)}})
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(p))
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Error("a connection to a dead server stayed open")
	}
	c.Close()
}
