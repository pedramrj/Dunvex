// Package dnsmux lets several DNS tunnels (dnstt, Slipstream and Xray's XDNS)
// share one UDP port, normally 53. Each query goes to the tunnel whose zone
// holds its question name, and the answer goes back to the client that asked.
// A name no tunnel serves gets REFUSED, as from any authoritative server.
//
// One socket per tunnel carries every client's queries: each query's ID is
// swapped for a free one on the way in and put back on the way out. None of
// the tunnels tell clients apart by address (they carry a client ID inside
// the query), so this is all they need.
package dnsmux

import (
	"encoding/binary"
	"errors"
	"math/rand/v2"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// pendingTTL is how long a query waits for its answer. Tunnels hold a
	// query for about a second before they answer it.
	pendingTTL = 10 * time.Second
	// maxPending leaves room in the 16-bit ID space of a tunnel's socket.
	maxPending = 60000
)

// Stat counts one zone's traffic.
type Stat struct {
	Zone    string `json:"zone"`
	Queries int64  `json:"queries"`
	Answers int64  `json:"answers"`
	Dropped int64  `json:"dropped"` // no free ID for it
}

// Mux is one shared UDP port.
type Mux struct {
	conn    net.PacketConn
	mu      sync.RWMutex
	zones   []*backend // longest zone first
	refused atomic.Int64
	closed  atomic.Bool
}

type backend struct {
	zone, target string
	conn         *net.UDPConn
	mux          *Mux
	closed       atomic.Bool

	mu      sync.Mutex
	next    uint16
	pending map[uint16]pending
	swept   time.Time

	queries, answers, dropped atomic.Int64
}

type pending struct {
	id     uint16
	client net.Addr
	at     time.Time
}

// Listen opens the shared port (e.g. "203.0.113.5:53") and serves it.
func Listen(addr string) (*Mux, error) {
	c, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	m := New(c)
	go m.Serve()
	return m, nil
}

// New wraps an open socket; call Serve to start.
func New(conn net.PacketConn) *Mux { return &Mux{conn: conn} }

// Addr is the shared port's address.
func (m *Mux) Addr() net.Addr { return m.conn.LocalAddr() }

// Serve reads queries until Close.
func (m *Mux) Serve() error {
	buf := make([]byte, 65535)
	for {
		n, from, err := m.conn.ReadFrom(buf)
		if err != nil {
			if m.closed.Load() || errors.Is(err, net.ErrClosed) {
				return nil
			}
			// e.g. Windows reports an earlier answer's ICMP "port unreachable" here
			time.Sleep(time.Millisecond)
			continue
		}
		m.handle(buf[:n], from)
	}
}

func (m *Mux) handle(q []byte, from net.Addr) {
	name, qend, ok := question(q)
	if !ok {
		return // not a DNS query (or an answer): nothing to say
	}
	if b := m.route(name); b != nil {
		b.forward(q, from)
		return
	}
	m.refused.Add(1)
	m.conn.WriteTo(refuse(q, qend), from)
}

func (m *Mux) route(name string) *backend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.zones {
		if name == b.zone || strings.HasSuffix(name, "."+b.zone) {
			return b
		}
	}
	return nil
}

// SetZones sets the tunnels: zone (a domain) → its UDP address on this
// server. Unchanged zones keep their socket. It reports the first zone it
// could not set up; the others still work.
func (m *Mux) SetZones(zones map[string]string) error {
	want := map[string]string{}
	for z, t := range zones {
		if z = Zone(z); z != "" {
			want[z] = t
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := map[string]*backend{}
	for _, b := range m.zones {
		if want[b.zone] == b.target && !m.closed.Load() {
			keep[b.zone] = b
		} else {
			b.close()
		}
	}
	var list []*backend
	var first error
	for z, t := range want {
		if b := keep[z]; b != nil {
			list = append(list, b)
			continue
		}
		b, err := m.dial(z, t)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		list = append(list, b)
	}
	sort.Slice(list, func(i, j int) bool {
		if len(list[i].zone) != len(list[j].zone) {
			return len(list[i].zone) > len(list[j].zone)
		}
		return list[i].zone < list[j].zone
	})
	m.zones = list
	return first
}

func (m *Mux) dial(zone, target string) (*backend, error) {
	addr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return nil, err
	}
	c, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, err
	}
	b := &backend{zone: zone, target: target, conn: c, mux: m, pending: map[uint16]pending{}, next: uint16(rand.N(65536))}
	go b.run()
	return b, nil
}

// Zones lists the zones served now.
func (m *Mux) Zones() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for _, b := range m.zones {
		out = append(out, b.zone)
	}
	return out
}

// Stats counts each zone's queries; Refused counts the other names.
func (m *Mux) Stats() (zones []Stat, refused int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.zones {
		zones = append(zones, Stat{Zone: b.zone, Queries: b.queries.Load(), Answers: b.answers.Load(), Dropped: b.dropped.Load()})
	}
	return zones, m.refused.Load()
}

// Close stops the port and every tunnel socket.
func (m *Mux) Close() error {
	m.closed.Store(true)
	err := m.conn.Close()
	m.mu.Lock()
	for _, b := range m.zones {
		b.close()
	}
	m.zones = nil
	m.mu.Unlock()
	return err
}

func (b *backend) close() {
	b.closed.Store(true)
	b.conn.Close()
}

func (b *backend) forward(q []byte, from net.Addr) {
	now := time.Now()
	b.mu.Lock()
	if now.Sub(b.swept) > time.Second {
		for id, p := range b.pending {
			if now.Sub(p.at) > pendingTTL {
				delete(b.pending, id)
			}
		}
		b.swept = now
	}
	if len(b.pending) >= maxPending {
		b.mu.Unlock()
		b.dropped.Add(1)
		return
	}
	for {
		b.next++
		if _, used := b.pending[b.next]; !used {
			break
		}
	}
	id := b.next
	b.pending[id] = pending{id: binary.BigEndian.Uint16(q), client: from, at: now}
	b.mu.Unlock()
	out := append([]byte(nil), q...)
	binary.BigEndian.PutUint16(out, id)
	b.queries.Add(1)
	b.conn.Write(out)
}

func (b *backend) run() {
	buf := make([]byte, 65535)
	for {
		n, err := b.conn.Read(buf)
		if err != nil {
			if b.closed.Load() || errors.Is(err, net.ErrClosed) {
				return
			}
			// "connection refused": the tunnel is not listening (yet)
			time.Sleep(time.Millisecond)
			continue
		}
		if n < 12 {
			continue
		}
		b.mu.Lock()
		p, ok := b.pending[binary.BigEndian.Uint16(buf)]
		if ok {
			delete(b.pending, binary.BigEndian.Uint16(buf))
		}
		b.mu.Unlock()
		if !ok {
			continue // late, or not an answer to us
		}
		binary.BigEndian.PutUint16(buf, p.id)
		b.mux.conn.WriteTo(buf[:n], p.client)
		b.answers.Add(1)
	}
}

// Zone normalises a domain: lower case, no surrounding dots or spaces.
func Zone(s string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(s)), ".")
}

// question reads a query's single question name (lower case, dotted) and the
// end of its question section. It refuses answers, several questions and
// names it cannot read.
func question(msg []byte) (name string, end int, ok bool) {
	if len(msg) < 12 || msg[2]&0x80 != 0 || binary.BigEndian.Uint16(msg[4:]) != 1 {
		return "", 0, false
	}
	var sb strings.Builder
	off, total := 12, 0
	for {
		if off >= len(msg) {
			return "", 0, false
		}
		l := int(msg[off])
		off++
		if l == 0 {
			break
		}
		if l > 63 || off+l > len(msg) {
			return "", 0, false // compression pointers have no place in a query's first name
		}
		if total += l + 1; total > 254 {
			return "", 0, false
		}
		if sb.Len() > 0 {
			sb.WriteByte('.')
		}
		for _, c := range msg[off : off+l] {
			switch {
			case c == '.':
				return "", 0, false
			case 'A' <= c && c <= 'Z':
				c += 'a' - 'A'
			}
			sb.WriteByte(c)
		}
		off += l
	}
	if off+4 > len(msg) {
		return "", 0, false
	}
	return sb.String(), off + 4, true
}

// refuse answers a query with REFUSED: its header and question, nothing else
// (never larger than the query).
func refuse(q []byte, qend int) []byte {
	r := append([]byte(nil), q[:qend]...)
	r[2] = 0x80 | q[2]&0x79 // QR, the same opcode and RD; not authoritative
	r[3] = 5                // REFUSED
	binary.BigEndian.PutUint16(r[6:], 0)
	binary.BigEndian.PutUint16(r[8:], 0)
	binary.BigEndian.PutUint16(r[10:], 0)
	return r
}
