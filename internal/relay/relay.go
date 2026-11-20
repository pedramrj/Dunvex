// Package relay forwards TCP and UDP ports to another server. A relay server
// (usually inside Iran) takes the users' connections on its own address and
// passes them, unchanged, to the server that runs the protocols, so the
// protocols' encryption stays end to end.
package relay

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Forward is one forwarded port.
type Forward struct {
	Name    string `json:"name"`    // what it carries, for the status
	Listen  int    `json:"listen"`  // port on the relay
	Network string `json:"network"` // tcp, udp or tcp+udp
	Target  string `json:"target"`  // host:port of the server
}

func (f Forward) key() string { return fmt.Sprintf("%d/%s/%s", f.Listen, f.Network, f.Target) }

func (f Forward) has(network string) bool { return f.Network == network || f.Network == "tcp+udp" }

// Stat is a forward's state.
type Stat struct {
	Forward
	Listening bool   `json:"listening"`
	Error     string `json:"error,omitempty"`
	Conns     int64  `json:"conns"` // open TCP connections and UDP flows
	Up        int64  `json:"up"`    // bytes from users to the server since the relay started
	Down      int64  `json:"down"`
	RTT       int64  `json:"rttMs"` // last TCP connect time to the server; -1 unreachable, 0 not checked
}

// Limits.
var (
	DialTimeout = 10 * time.Second
	UDPIdle     = 3 * time.Minute // a UDP flow with no packets either way is closed
	MaxUDPFlows = 20000
)

// Relay runs a set of forwards.
type Relay struct {
	mu       sync.Mutex
	fwds     map[string]*fwd
	reserved map[int]string // ports the relay must not take (its own agent)
	stop     chan struct{}
}

type fwd struct {
	Forward
	tcp   net.Listener
	udp   *net.UDPConn
	err   string
	conns atomic.Int64
	up    atomic.Int64
	down  atomic.Int64
	rtt   atomic.Int64
	done  chan struct{}
	wg    sync.WaitGroup
	mu    sync.Mutex
	open  map[net.Conn]bool // TCP connections, closed with the forward
	flows map[string]*flow  // UDP flows by client address
}

type flow struct {
	conn *net.UDPConn
	last atomic.Int64 // unix nano of the last packet
}

// New makes a relay; reserved ports (e.g. the agent's) are never forwarded.
func New(reserved map[int]string) *Relay {
	r := &Relay{fwds: map[string]*fwd{}, reserved: reserved, stop: make(chan struct{})}
	go r.health()
	return r
}

// Apply replaces the forwards: unchanged ones keep their connections.
func (r *Relay) Apply(list []Forward) {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := map[string]Forward{}
	for _, f := range list {
		want[f.key()] = f
	}
	for k, f := range r.fwds {
		if _, ok := want[k]; !ok {
			f.close()
			delete(r.fwds, k)
		}
	}
	for k, f := range want {
		if _, ok := r.fwds[k]; ok {
			continue
		}
		x := &fwd{Forward: f, done: make(chan struct{}), open: map[net.Conn]bool{}, flows: map[string]*flow{}}
		if what, ok := r.reserved[f.Listen]; ok {
			x.err = fmt.Sprintf("port %d is used by %s on the relay", f.Listen, what)
		} else if err := x.start(); err != nil {
			x.err = err.Error()
			log.Printf("relay: %s: %v", f.Name, err)
		}
		r.fwds[k] = x
	}
}

// Stats lists the forwards by port.
func (r *Relay) Stats() []Stat {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []Stat{}
	for _, f := range r.fwds {
		out = append(out, Stat{Forward: f.Forward, Listening: f.err == "" && (f.tcp != nil || f.udp != nil), Error: f.err,
			Conns: f.conns.Load(), Up: f.up.Load(), Down: f.down.Load(), RTT: f.rtt.Load()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Listen < out[j].Listen })
	return out
}

// Close stops every forward.
func (r *Relay) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
	for k, f := range r.fwds {
		f.close()
		delete(r.fwds, k)
	}
}

// health measures how long a TCP connection to each server takes.
func (r *Relay) health() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	first := time.After(2 * time.Second)
	for {
		select {
		case <-r.stop:
			return
		case <-first:
		case <-t.C:
		}
		r.mu.Lock()
		var list []*fwd
		for _, f := range r.fwds {
			if f.has("tcp") {
				list = append(list, f)
			}
		}
		r.mu.Unlock()
		for _, f := range list {
			start := time.Now()
			c, err := net.DialTimeout("tcp", f.Target, 5*time.Second)
			if err != nil {
				f.rtt.Store(-1)
				continue
			}
			c.Close()
			f.rtt.Store(max(1, time.Since(start).Milliseconds()))
		}
	}
}

func (f *fwd) start() error {
	if _, _, err := net.SplitHostPort(f.Target); err != nil {
		return fmt.Errorf("bad target %q", f.Target)
	}
	addr := ":" + strconv.Itoa(f.Listen)
	if f.has("tcp") {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return portErr(f.Listen, "TCP", err)
		}
		f.tcp = l
		f.wg.Add(1)
		go f.acceptTCP()
	}
	if f.has("udp") {
		ua, _ := net.ResolveUDPAddr("udp", addr)
		c, err := net.ListenUDP("udp", ua)
		if err != nil {
			if f.tcp != nil {
				f.tcp.Close()
				f.tcp = nil
			}
			return portErr(f.Listen, "UDP", err)
		}
		f.udp = c
		f.wg.Add(2)
		go f.readUDP()
		go f.sweepUDP()
	}
	return nil
}

func portErr(port int, network string, err error) error {
	if errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "address already in use") || strings.Contains(err.Error(), "Only one usage") {
		return fmt.Errorf("%s port %d is used by another program on the relay", network, port)
	}
	return fmt.Errorf("%s port %d: %v", network, port, err)
}

func (f *fwd) close() {
	select {
	case <-f.done:
		return
	default:
		close(f.done)
	}
	if f.tcp != nil {
		f.tcp.Close()
	}
	if f.udp != nil {
		f.udp.Close()
	}
	f.mu.Lock()
	for c := range f.open {
		c.Close()
	}
	for _, fl := range f.flows {
		fl.conn.Close()
	}
	f.mu.Unlock()
	f.wg.Wait()
}

// ---------------------------------------------------------------- TCP

func (f *fwd) acceptTCP() {
	defer f.wg.Done()
	for {
		c, err := f.tcp.Accept()
		if err != nil {
			select {
			case <-f.done:
				return
			default:
			}
			time.Sleep(50 * time.Millisecond) // e.g. too many open files: do not spin
			continue
		}
		go f.serveTCP(c)
	}
}

func (f *fwd) track(c net.Conn, on bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.done:
		return false
	default:
	}
	if on {
		f.open[c] = true
	} else {
		delete(f.open, c)
	}
	return true
}

func (f *fwd) serveTCP(in net.Conn) {
	if !f.track(in, true) {
		in.Close()
		return
	}
	f.conns.Add(1)
	defer func() { f.conns.Add(-1); f.track(in, false); in.Close() }()
	out, err := net.DialTimeout("tcp", f.Target, DialTimeout)
	if err != nil {
		return
	}
	if !f.track(out, true) {
		out.Close()
		return
	}
	defer func() { f.track(out, false); out.Close() }()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pipe(out, in, &f.up)
	}()
	pipe(in, out, &f.down)
	wg.Wait()
}

// pipe copies src to dst, counting bytes, and half-closes dst at the end.
func pipe(dst, src net.Conn, n *atomic.Int64) {
	buf := make([]byte, 32<<10)
	for {
		k, err := src.Read(buf)
		if k > 0 {
			if _, werr := dst.Write(buf[:k]); werr != nil {
				break
			}
			n.Add(int64(k))
		}
		if err != nil {
			break
		}
	}
	if c, ok := dst.(interface{ CloseWrite() error }); ok {
		c.CloseWrite()
	} else {
		dst.Close()
	}
}

// ---------------------------------------------------------------- UDP

func (f *fwd) readUDP() {
	defer f.wg.Done()
	buf := make([]byte, 64<<10)
	for {
		k, from, err := f.udp.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-f.done:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		fl := f.flowFor(from)
		if fl == nil {
			continue
		}
		fl.last.Store(time.Now().UnixNano())
		if _, err := fl.conn.Write(buf[:k]); err == nil {
			f.up.Add(int64(k))
		}
	}
}

// flowFor finds or opens the flow of one client address.
func (f *fwd) flowFor(from *net.UDPAddr) *flow {
	key := from.String()
	f.mu.Lock()
	fl := f.flows[key]
	if fl != nil || len(f.flows) >= MaxUDPFlows {
		f.mu.Unlock()
		return fl
	}
	f.mu.Unlock()
	ta, err := net.ResolveUDPAddr("udp", f.Target)
	if err != nil {
		return nil
	}
	c, err := net.DialUDP("udp", nil, ta)
	if err != nil {
		return nil
	}
	fl = &flow{conn: c}
	fl.last.Store(time.Now().UnixNano())
	f.mu.Lock()
	select {
	case <-f.done:
		f.mu.Unlock()
		c.Close()
		return nil
	default:
	}
	if other := f.flows[key]; other != nil {
		f.mu.Unlock()
		c.Close()
		return other
	}
	f.flows[key] = fl
	f.mu.Unlock()
	f.conns.Add(1)
	f.wg.Add(1)
	go f.backUDP(key, from, fl)
	return fl
}

// backUDP sends the server's answers back to the client.
func (f *fwd) backUDP(key string, to *net.UDPAddr, fl *flow) {
	defer f.wg.Done()
	defer func() {
		f.mu.Lock()
		if f.flows[key] == fl {
			delete(f.flows, key)
		}
		f.mu.Unlock()
		fl.conn.Close()
		f.conns.Add(-1)
	}()
	buf := make([]byte, 64<<10)
	for {
		k, err := fl.conn.Read(buf)
		if err != nil {
			return
		}
		fl.last.Store(time.Now().UnixNano())
		if _, err := f.udp.WriteToUDP(buf[:k], to); err == nil {
			f.down.Add(int64(k))
		}
	}
}

// sweepUDP closes flows that have been quiet for UDPIdle.
func (f *fwd) sweepUDP() {
	defer f.wg.Done()
	t := time.NewTicker(UDPIdle / 6)
	defer t.Stop()
	for {
		select {
		case <-f.done:
			return
		case <-t.C:
		}
		cut := time.Now().Add(-UDPIdle).UnixNano()
		f.mu.Lock()
		for _, fl := range f.flows {
			if fl.last.Load() < cut {
				fl.conn.Close() // backUDP removes it
			}
		}
		f.mu.Unlock()
	}
}
