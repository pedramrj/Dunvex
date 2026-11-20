package dnsmux_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/dnsmux"
	"github.com/pedramrj/dunvex/internal/dnstt"
	"github.com/pedramrj/dunvex/internal/dnstt/dnsttclient"
)

// query builds a DNS query for name with the given ID.
func query(id uint16, name string) []byte {
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q, id)
	q[2] = 0x01                          // RD
	binary.BigEndian.PutUint16(q[4:], 1) // one question
	for _, l := range strings.Split(name, ".") {
		q = append(q, byte(len(l)))
		q = append(q, l...)
	}
	return append(q, 0, 0, 16, 0, 1) // TXT IN
}

// tunnel is a fake DNS tunnel: it answers every query with its own name.
func tunnel(t *testing.T, tag string) string {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := c.ReadFrom(buf)
			if err != nil {
				return
			}
			r := append(append([]byte(nil), buf[:n]...), tag...)
			r[2] |= 0x80
			c.WriteTo(r, from)
		}
	}()
	return c.LocalAddr().String()
}

func ask(t *testing.T, mux string, q []byte) []byte {
	t.Helper()
	c, err := net.Dial("udp", mux)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write(q)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, err := c.Read(buf)
	if err != nil {
		return nil
	}
	return buf[:n]
}

func TestRoutesByZone(t *testing.T) {
	m, err := dnsmux.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	addr := m.Addr().String()
	if err := m.SetZones(map[string]string{"t.example.com": tunnel(t, "A"), "S.Example.com.": tunnel(t, "B"), "x.t.example.com": tunnel(t, "C")}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"abc.t.example.com":    "A",
		"T.EXAMPLE.COM":        "A",
		"zz.s.example.com":     "B",
		"deep.x.t.example.com": "C", // the longest zone wins
		"x.t.example.com":      "C",
	} {
		r := ask(t, addr, query(0x1234, name))
		if r == nil || !strings.HasSuffix(string(r), want) || binary.BigEndian.Uint16(r) != 0x1234 || r[2]&0x80 == 0 {
			t.Errorf("%s: got %q, want an answer from %s with the query's ID", name, r, want)
		}
	}

	// other names: REFUSED, no bigger than the query
	q := query(7, "www.google.com")
	r := ask(t, addr, q)
	if r == nil || r[3]&0x0f != 5 || r[2]&0x80 == 0 || binary.BigEndian.Uint16(r) != 7 || len(r) > len(q) {
		t.Fatalf("an unknown name: %x", r)
	}
	if r := ask(t, addr, query(8, "evilt.example.com")); r == nil || r[3]&0x0f != 5 {
		t.Fatalf("a name that only ends like a zone must be refused: %x", r)
	}
	// answers and junk get no reply at all (nothing to reflect)
	a := query(9, "a.t.example.com")
	a[2] |= 0x80
	if r := ask(t, addr, a); r != nil {
		t.Fatalf("an answer was forwarded: %x", r)
	}
	if r := ask(t, addr, []byte("hello")); r != nil {
		t.Fatalf("junk got a reply: %x", r)
	}
	zones, refused := m.Stats()
	if len(zones) != 3 || refused != 2 {
		t.Errorf("stats: %+v refused %d", zones, refused)
	}

	// a zone removed is refused; one kept keeps working
	m.SetZones(map[string]string{"t.example.com": tunnel(t, "D")}) // a new address for it
	if got := m.Zones(); len(got) != 1 || got[0] != "t.example.com" {
		t.Fatalf("zones: %v", got)
	}
	if r := ask(t, addr, query(4, "a.t.example.com")); r == nil || !strings.HasSuffix(string(r), "D") {
		t.Fatalf("the zone at its new address: %q", r)
	}
	if r := ask(t, addr, query(3, "a.s.example.com")); r == nil || r[3]&0x0f != 5 {
		t.Fatalf("a removed zone: %x", r)
	}
}

// Two clients that use the same query ID each get their own answer.
func TestSameIDFromTwoClients(t *testing.T) {
	m, err := dnsmux.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.SetZones(map[string]string{"t.example.com": tunnel(t, "A")})
	var conns []net.Conn
	for i := 0; i < 2; i++ {
		c, err := net.Dial("udp", m.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns = append(conns, c)
	}
	conns[0].Write(query(42, "one.t.example.com"))
	conns[1].Write(query(42, "two.t.example.com"))
	for i, want := range []string{"one", "two"} {
		conns[i].SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 512)
		n, err := conns[i].Read(buf)
		if err != nil || !bytes.Contains(buf[:n], []byte(want)) || binary.BigEndian.Uint16(buf) != 42 {
			t.Fatalf("client %d: %q %v", i, buf[:n], err)
		}
	}
}

// A real dnstt session through the shared port, next to another zone.
func TestDnsttBehindTheMux(t *testing.T) {
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	priv, pub, _ := dnstt.NewKey()
	key, _ := dnstt.DecodeKey(priv)
	backend, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go dnstt.Run(key, "d.example.com", up.Addr().String(), backend)
	defer backend.Close()

	m, err := dnsmux.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.SetZones(map[string]string{"d.example.com": backend.LocalAddr().String(), "x.example.com": tunnel(t, "X")})

	c, err := dnsttclient.Dial(m.Addr().String(), "d.example.com", pub)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	msg := bytes.Repeat([]byte("dunvex "), 1500)
	go s.Write(msg)
	s.SetReadDeadline(time.Now().Add(30 * time.Second))
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(s, got); err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("through the mux: %v", err)
	}
	if zones, _ := m.Stats(); zones[0].Zone != "d.example.com" || zones[0].Answers == 0 {
		t.Errorf("stats: %+v", zones)
	}
}
