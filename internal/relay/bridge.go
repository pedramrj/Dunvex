package relay

// The Bridge mode wires the internal/bridge multi-carrier tunnel in as a third
// relay mode beside forward and reverse. It keeps the reverse tunnel's exact
// shape — the relay inside Iran opens smux streams for the forwarded ports, the
// target abroad accepts them and connects them to its protocols — but the one
// pinned-TLS link is replaced by a Bridge session that spreads the bytes across
// ICMP, fake-TCP, UDP, a CDN and DNS, probing and failing over on its own.
//
// Everything above the transport is unchanged: a bridge relay's forwards are
// the same Reverse forwards, and tunnelOpen routes them to the Bridge's smux
// links instead of the TLS tunnel's.

import (
	"crypto/tls"
	"encoding/base64"
	"log"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/bridge"
	"github.com/xtaci/smux"
)

// BridgeCarrierSpec configures one carrier, the same on both sides; a side
// builds its server (relay) or client (target) carrier from it.
type BridgeCarrierSpec struct {
	Kind     string `json:"kind"` // udp, icmp, faketcp, tls-cdn, dns
	Priority int    `json:"priority"`
	CapMBps  int    `json:"capMBps,omitempty"`
	Port     int    `json:"port,omitempty"`

	ServerRequests bool `json:"serverRequests,omitempty"` // icmp: the relay sends the requests

	Path       string `json:"path,omitempty"`       // tls-cdn
	ServerName string `json:"serverName,omitempty"` // tls-cdn: inner name / relay domain
	ECH        string `json:"ech,omitempty"`        // tls-cdn: CDN ECH config list (base64)
	PinSHA     string `json:"pinSha,omitempty"`     // tls-cdn: pin the relay cert when no real CDN

	Zone string `json:"zone,omitempty"` // dns: tunnel zone
}

// BridgeServerConfig is what the relay inside Iran listens with.
type BridgeServerConfig struct {
	PSK        string              `json:"psk"`
	StaticPriv string              `json:"staticPriv"` // base64 32-byte X25519 private key
	DNSPriv    string              `json:"dnsPriv"`    // dnstt private key (hex), for the DNS carrier
	Carriers   []BridgeCarrierSpec `json:"carriers"`
}

// BridgeClientConfig is what the target abroad dials a relay with.
type BridgeClientConfig struct {
	Name      string              `json:"name"`
	RelayHost string              `json:"relayHost"` // the relay's address (host or IP)
	PSK       string              `json:"psk"`
	StaticPub string              `json:"staticPub"` // base64 32-byte X25519 public key
	DNSPub    string              `json:"dnsPub"`    // dnstt public key (hex)
	Carriers  []BridgeCarrierSpec `json:"carriers"`
	Ports     []int               `json:"ports"` // the only local ports the relay may reach
}

func (c BridgeClientConfig) key() string {
	b, _ := marshalInts(c.Ports)
	return c.RelayHost + "|" + c.StaticPub + "|" + c.PSK + "|" + b
}

func decodeKey32(b64 string) ([32]byte, bool) {
	var out [32]byte
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != 32 {
		return out, false
	}
	copy(out[:], raw)
	return out, true
}

// ---------------------------------------------------------------- relay side

// ApplyBridgeServer opens, changes or (with nil) closes the Bridge listener.
// Call it before Apply, so forwards know to route through the Bridge.
func (r *Relay) ApplyBridgeServer(cfg *BridgeServerConfig) {
	r.mu.Lock()
	cur := r.brCfgHash
	next := hashBridgeServer(cfg)
	r.mu.Unlock()
	if next == cur {
		return
	}
	if b := r.br.Swap(nil); b != nil {
		b.close()
	}
	r.mu.Lock()
	r.brCfgHash = next
	cert := r.cert
	r.mu.Unlock()
	if cfg == nil {
		return
	}
	priv, ok := decodeKey32(cfg.StaticPriv)
	if !ok {
		log.Printf("bridge relay: bad static key")
		return
	}
	pub, err := bridge.PubFromPriv(priv)
	if err != nil {
		log.Printf("bridge relay: %v", err)
		return
	}
	opts, err := serverCarriers(cfg, cert)
	if err != nil {
		log.Printf("bridge relay: %v", err)
		return
	}
	lis, err := bridge.Listen(bridge.Config{PSK: []byte(cfg.PSK), Static: bridge.KeyPair{Priv: priv, Pub: pub}, Carriers: opts})
	if err != nil {
		log.Printf("bridge relay: listen: %v", err)
		return
	}
	bt := &bridgeTunnel{lis: lis}
	r.br.Store(bt)
	go bt.accept()
}

// bridgeTunnel is the relay's side of a Bridge: it accepts sessions and keeps
// a smux link over each, opening streams for the forwards (like tunnelServer).
type bridgeTunnel struct {
	lis   *bridge.Listener
	mu    sync.Mutex
	links []*smux.Session
	since int64
	next  uint64
	done  bool
}

func (bt *bridgeTunnel) accept() {
	for {
		c, err := bt.lis.Accept()
		if err != nil {
			bt.mu.Lock()
			done := bt.done
			bt.mu.Unlock()
			if done {
				return
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		s, err := smux.Client(c, smuxConfig())
		if err != nil {
			c.Close()
			continue
		}
		bt.mu.Lock()
		if len(bt.links) == 0 {
			bt.since = time.Now().Unix()
		}
		bt.links = append(bt.links, s)
		bt.mu.Unlock()
		go func() {
			<-s.CloseChan()
			bt.mu.Lock()
			for i, x := range bt.links {
				if x == s {
					bt.links = append(bt.links[:i], bt.links[i+1:]...)
					break
				}
			}
			bt.mu.Unlock()
		}()
	}
}

func (bt *bridgeTunnel) hasLinks() bool {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	for _, s := range bt.links {
		if !s.IsClosed() {
			return true
		}
	}
	return false
}

func (bt *bridgeTunnel) open(kind byte, port int) (net.Conn, error) {
	bt.mu.Lock()
	var best *smux.Session
	n := len(bt.links)
	bt.next++
	start := int(bt.next)
	for i := 0; i < n; i++ {
		s := bt.links[(start+i)%n]
		if s.IsClosed() {
			continue
		}
		if best == nil || s.NumStreams() < best.NumStreams() {
			best = s
		}
	}
	bt.mu.Unlock()
	if best == nil {
		return nil, errNoBridge
	}
	st, err := best.OpenStream()
	if err != nil {
		return nil, err
	}
	hdr := []byte{kind, byte(port >> 8), byte(port)}
	if _, err := st.Write(hdr); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

func (bt *bridgeTunnel) close() {
	bt.mu.Lock()
	bt.done = true
	for _, s := range bt.links {
		s.Close()
	}
	bt.links = nil
	bt.mu.Unlock()
	bt.lis.Close()
}

// ---------------------------------------------------------------- target side

// ApplyBridges replaces the Bridge relays this target keeps links to.
func (t *Tunnels) ApplyBridges(list []BridgeClientConfig) {
	t.bmu.Lock()
	defer t.bmu.Unlock()
	if t.bridges == nil {
		t.bridges = map[string]*bclient{}
	}
	want := map[string]BridgeClientConfig{}
	for _, c := range list {
		want[c.key()] = c
	}
	for k, c := range t.bridges {
		if _, ok := want[k]; !ok {
			c.stop()
			delete(t.bridges, k)
		}
	}
	for k, c := range want {
		if _, ok := t.bridges[k]; ok {
			continue
		}
		bc := &bclient{cfg: c, local: t.local, stopCh: make(chan struct{}), allowed: map[int]bool{}}
		for _, p := range c.Ports {
			bc.allowed[p] = true
		}
		t.bridges[k] = bc
		go bc.run(t)
	}
}

// bclient keeps one Bridge session up to one relay, reconnecting.
type bclient struct {
	cfg     BridgeClientConfig
	local   string
	allowed map[int]bool
	stopCh  chan struct{}
	once    sync.Once

	mu      sync.Mutex
	conn    *bridge.Conn
	links   int
	since   int64
	err     string
	streams int64
}

func (bc *bclient) stop() { bc.once.Do(func() { close(bc.stopCh) }) }

func (bc *bclient) run(t *Tunnels) {
	wait := 2 * time.Second
	for {
		select {
		case <-bc.stopCh:
			return
		default:
		}
		pub, ok := decodeKey32(bc.cfg.StaticPub)
		if !ok {
			bc.setErr("the relay's bridge key is bad")
			if !bc.sleep(wait) {
				return
			}
			continue
		}
		opts, err := clientCarriers(bc.cfg)
		if err != nil {
			bc.setErr(err.Error())
			if !bc.sleep(wait) {
				return
			}
			wait = min(wait*2, 30*time.Second)
			continue
		}
		conn, err := bridge.Dial(bridge.Config{PSK: []byte(bc.cfg.PSK), ServerPub: pub, Carriers: opts}, 25*time.Second)
		if err != nil {
			bc.setErr(err.Error())
			if !bc.sleep(wait) {
				return
			}
			wait = min(wait*2, 30*time.Second)
			continue
		}
		wait = 2 * time.Second
		sess, err := smux.Server(conn, smuxConfig())
		if err != nil {
			conn.Close()
			continue
		}
		bc.mu.Lock()
		bc.conn, bc.links, bc.since, bc.err = conn, 1, time.Now().Unix(), ""
		bc.mu.Unlock()
		go func() { <-bc.stopCh; sess.Close(); conn.Close() }()
		for {
			st, err := sess.AcceptStream()
			if err != nil {
				break
			}
			bc.mu.Lock()
			bc.streams++
			bc.mu.Unlock()
			go t.serveStream(st, bc.allowed, bc.cfg.Name)
		}
		sess.Close()
		conn.Close()
		bc.mu.Lock()
		bc.conn, bc.links = nil, 0
		bc.mu.Unlock()
		if !bc.sleep(time.Second) {
			return
		}
	}
}

func (bc *bclient) sleep(d time.Duration) bool {
	select {
	case <-bc.stopCh:
		return false
	case <-time.After(d):
		return true
	}
}

func (bc *bclient) setErr(s string) {
	bc.mu.Lock()
	bc.err = s
	bc.mu.Unlock()
}

// ---------------------------------------------------------------- carrier builders

func serverCarriers(cfg *BridgeServerConfig, cert *tls.Certificate) ([]bridge.CarrierOpt, error) {
	var opts []bridge.CarrierOpt
	for _, s := range cfg.Carriers {
		var c bridge.Carrier
		switch s.Kind {
		case "udp":
			c = bridge.NewUDPServer(":" + strconv.Itoa(s.Port))
		case "icmp":
			c = bridge.NewICMPServer(s.ServerRequests)
		case "faketcp":
			c = bridge.NewFakeTCPServer(s.Port)
		case "tls-cdn":
			if cert == nil {
				continue // no certificate for the wss listener yet
			}
			c = bridge.NewTLSCDNServer(":"+strconv.Itoa(s.Port), s.Path, *cert)
		case "dns":
			c = bridge.NewDNSServer(":"+strconv.Itoa(s.Port), cfg.DNSPriv, s.Zone)
		default:
			continue
		}
		opts = append(opts, bridge.CarrierOpt{Carrier: c, Priority: s.Priority, CapMBps: s.CapMBps})
	}
	if len(opts) == 0 {
		return nil, errNoCarriers
	}
	return opts, nil
}

func clientCarriers(cfg BridgeClientConfig) ([]bridge.CarrierOpt, error) {
	host := cfg.RelayHost
	ip := ""
	if addrs, err := net.LookupIP(host); err == nil {
		for _, a := range addrs {
			if v4 := a.To4(); v4 != nil {
				ip = v4.String()
				break
			}
		}
	}
	if ip == "" {
		if v := net.ParseIP(host); v != nil {
			ip = v.String()
		}
	}
	var opts []bridge.CarrierOpt
	for _, s := range cfg.Carriers {
		var c bridge.Carrier
		var err error
		switch s.Kind {
		case "udp":
			c, err = bridge.NewUDPClient(net.JoinHostPort(host, strconv.Itoa(s.Port)))
		case "icmp":
			if ip == "" {
				continue
			}
			c, err = bridge.NewICMPClient(ip, s.ServerRequests)
		case "faketcp":
			if ip == "" {
				continue
			}
			c, err = bridge.NewFakeTCPClient(ip, s.Port)
		case "tls-cdn":
			name := s.ServerName
			if name == "" {
				name = host
			}
			url := "wss://" + net.JoinHostPort(name, strconv.Itoa(s.Port)) + firstNonEmpty(s.Path, "/dvxb")
			c, err = bridge.NewTLSCDNClient(url, name, s.ECH, s.PinSHA)
		case "dns":
			c = bridge.NewDNSClient(net.JoinHostPort(host, strconv.Itoa(s.Port)), s.Zone, cfg.DNSPub)
		default:
			continue
		}
		if err != nil {
			log.Printf("bridge target %s: %s carrier: %v", cfg.Name, s.Kind, err)
			continue
		}
		opts = append(opts, bridge.CarrierOpt{Carrier: c, Priority: s.Priority, CapMBps: s.CapMBps})
	}
	if len(opts) == 0 {
		return nil, errNoCarriers
	}
	return opts, nil
}

// ---------------------------------------------------------------- stats

// BridgeStat is the relay's side of its Bridge, for the panel.
type BridgeStat struct {
	Links    int                  `json:"links"`
	Since    int64                `json:"since,omitempty"`
	Carriers []bridge.CarrierStat `json:"carriers"`
}

// BridgeStat returns the relay's Bridge state (nil when it has none).
func (r *Relay) BridgeStat() *BridgeStat {
	b := r.br.Load()
	if b == nil {
		return nil
	}
	b.mu.Lock()
	st := &BridgeStat{Links: len(b.links), Since: b.since}
	b.mu.Unlock()
	st.Carriers = b.lis.Stats()
	return st
}

// BridgeClientStat is a target's side of one Bridge relay.
type BridgeClientStat struct {
	Name      string               `json:"name"`
	RelayHost string               `json:"relayHost"`
	Links     int                  `json:"links"`
	Since     int64                `json:"since,omitempty"`
	Error     string               `json:"error,omitempty"`
	Streams   int64                `json:"streams"`
	Carriers  []bridge.CarrierStat `json:"carriers"`
}

// BridgeStats lists this target's Bridge relays and their carriers.
func (t *Tunnels) BridgeStats() []BridgeClientStat {
	t.bmu.Lock()
	defer t.bmu.Unlock()
	out := []BridgeClientStat{}
	for _, bc := range t.bridges {
		bc.mu.Lock()
		s := BridgeClientStat{Name: bc.cfg.Name, RelayHost: bc.cfg.RelayHost, Links: bc.links, Since: bc.since, Error: bc.err, Streams: bc.streams}
		if bc.conn != nil {
			s.Carriers = bc.conn.Stats()
		}
		bc.mu.Unlock()
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
