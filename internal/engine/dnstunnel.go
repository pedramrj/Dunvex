package engine

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/dnsmux"
	"github.com/pedramrj/dunvex/internal/dnstt"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/util"
)

// DNS tunnels. dnstt runs inside Dunvex and Slipstream as its own server;
// both listen on loopback UDP and hand every stream to a SOCKS inbound of
// Xray (protocol.TunnelSocks). A shared DNS port (dnsmux) sends each query to
// the tunnel of its domain: it runs on every port that has dnstt or
// Slipstream, or more than one XDNS, and XDNS there moves behind it.

// dnsBackendKey sets a DNS tunnel's UDP port apart from its SOCKS port in
// internalPort, which keeps one port per key.
const dnsBackendKey = int64(1) << 40

// DnsttVersion is the dnstt release Dunvex carries.
const DnsttVersion = "1.20260501.0"

// dnsPort is one shared DNS port.
type dnsPort struct {
	listen   string            // its public address
	zones    map[string]string // tunnel domain → loopback UDP address
	inbounds []int64
}

// dnsttPlan is one dnstt server.
type dnsttPlan struct {
	key      string // private key, hex
	domain   string
	upstream string // the SOCKS inbound
	listen   string // loopback UDP, behind the shared port
}

type dnsttRun struct {
	plan dnsttPlan
	conn net.PacketConn
	done chan struct{}
}

// dnsState is what serves DNS tunnels now (guarded by Manager.applyMu).
type dnsState struct {
	mux   map[int]*dnsmux.Mux
	addr  map[int]string
	dnstt map[int64]*dnsttRun
}

var dnsttLog struct {
	once sync.Once
	mu   sync.Mutex
	logs []string
}

// DnsttLogs returns the last log lines of the dnstt servers.
func DnsttLogs() []string {
	dnsttLog.mu.Lock()
	defer dnsttLog.mu.Unlock()
	return append([]string(nil), dnsttLog.logs...)
}

func keepDnsttLogs() {
	dnsttLog.once.Do(func() {
		dnstt.Logf = func(format string, v ...any) {
			line := fmt.Sprintf(format, v...)
			if strings.HasPrefix(line, "begin stream") || strings.HasPrefix(line, "end stream") {
				return // one per connection: too many to keep
			}
			dnsttLog.mu.Lock()
			dnsttLog.logs = append(dnsttLog.logs, time.Now().Format("2006/01/02 15:04:05 ")+line)
			if len(dnsttLog.logs) > 200 {
				dnsttLog.logs = dnsttLog.logs[len(dnsttLog.logs)-200:]
			}
			dnsttLog.mu.Unlock()
		}
	})
}

// dnsListen is where a shared DNS port listens: the panel's bind address or,
// for port 53, the address facing the internet (systemd-resolved holds
// 127.0.0.53:53, so "every address" cannot be bound there).
func dnsListen(port int, env protocol.Env) string {
	host := env.Listen
	if host == "" && port == 53 {
		host = util.OutboundIP()
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// dnsMuxed lists the UDP ports that need the shared DNS port.
func dnsMuxed(count map[int]int, tunnel map[int]bool) map[int]bool {
	out := map[int]bool{}
	for port, n := range count {
		if tunnel[port] || n > 1 {
			out[port] = true
		}
	}
	return out
}

// addZone puts a tunnel domain on a shared port, or explains why it cannot.
func (b *built) addZone(port int, listen, domain, addr string, id int64) string {
	if b.dns == nil {
		b.dns = map[int]*dnsPort{}
	}
	p := b.dns[port]
	if p == nil {
		p = &dnsPort{listen: listen, zones: map[string]string{}}
		b.dns[port] = p
	}
	if _, dup := p.zones[domain]; dup {
		return fmt.Sprintf("another DNS tunnel on port %d already uses the tunnel domain %s", port, domain)
	}
	p.zones[domain], p.inbounds = addr, append(p.inbounds, id)
	return ""
}

// dnsApply opens and closes the shared DNS ports and sets their zones. It runs
// before the cores start (closing ports a core needs back) and after (opening
// ports a core just gave up); only then do failures count.
func (m *Manager) dnsApply(ports map[int]*dnsPort, final bool) map[int64]string {
	errs := map[int64]string{}
	s := &m.dns
	if s.mux == nil {
		s.mux, s.addr = map[int]*dnsmux.Mux{}, map[int]string{}
	}
	for port, mx := range s.mux {
		if p := ports[port]; p == nil || s.addr[port] != p.listen {
			mx.Close()
			delete(s.mux, port)
			delete(s.addr, port)
		}
	}
	for port, p := range ports {
		mx := s.mux[port]
		if mx == nil {
			var err error
			if mx, err = dnsmux.Listen(p.listen); err != nil {
				if final {
					for _, id := range p.inbounds {
						errs[id] = fmt.Sprintf("UDP port %d is used by another program on this server; choose another port", port)
					}
				}
				continue
			}
			s.mux[port], s.addr[port] = mx, p.listen
		}
		if err := mx.SetZones(p.zones); err != nil && final {
			log.Printf("dns port %d: %v", port, err)
		}
	}
	return errs
}

// dnsttApply starts, restarts and stops the dnstt servers.
func (m *Manager) dnsttApply(plans map[int64]dnsttPlan) map[int64]string {
	keepDnsttLogs()
	errs := map[int64]string{}
	s := &m.dns
	if s.dnstt == nil {
		s.dnstt = map[int64]*dnsttRun{}
	}
	for id, r := range s.dnstt {
		if p, ok := plans[id]; !ok || p != r.plan {
			r.stop()
			delete(s.dnstt, id)
		}
	}
	for id, p := range plans {
		if s.dnstt[id] != nil {
			continue
		}
		key, err := dnstt.DecodeKey(p.key)
		if err != nil {
			errs[id] = "the dnstt private key is not valid; clear it to make a new one"
			continue
		}
		conn, err := net.ListenPacket("udp", p.listen)
		if err != nil {
			errs[id] = fmt.Sprintf("dnstt could not listen on %s: %v", p.listen, err)
			continue
		}
		r := &dnsttRun{plan: p, conn: conn, done: make(chan struct{})}
		go func() {
			defer close(r.done)
			if err := dnstt.Run(key, p.domain, p.upstream, conn); err != nil {
				log.Printf("dnstt %s: %v", p.domain, err)
			}
		}()
		s.dnstt[id] = r
	}
	return errs
}

func (r *dnsttRun) stop() {
	r.conn.Close()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
	}
}

// stopDNS stops every DNS tunnel that runs inside Dunvex.
func (m *Manager) stopDNS() {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.dnsApply(nil, false)
	m.dnsttApply(nil)
	m.dnsSnapshot()
}

// dnsSnapshot publishes what serves DNS tunnels now, for status calls that
// must not wait for an apply.
func (m *Manager) dnsSnapshot() {
	mux := map[int]*dnsmux.Mux{}
	for port, mx := range m.dns.mux {
		mux[port] = mx
	}
	m.mu.Lock()
	m.dnsNow, m.dnsttOn = mux, len(m.dns.dnstt)
	m.mu.Unlock()
}

// DNSPorts reports the shared DNS ports: the zones each sends queries to.
func (m *Manager) DNSPorts() map[int][]dnsmux.Stat {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[int][]dnsmux.Stat{}
	for port, mx := range m.dnsNow {
		out[port], _ = mx.Stats()
	}
	return out
}

// slipCert is Slipstream's QUIC certificate (self-signed: its apps do not
// check it), kept in the run directory.
func (m *Manager) slipCert(domain string) (cert, key string, err error) {
	dir := filepath.Join(filepath.Dir(m.Slip.ConfigPath), "slipstream-"+domain)
	return util.SelfSignedCert(dir, domain)
}

// slipArgs reads the Slipstream command line Apply wrote.
func slipArgs(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// dnsttStatus is the in-process dnstt, for the panel's list of cores.
func (m *Manager) dnsttStatus() (Status, bool) {
	m.mu.Lock()
	n := m.dnsttOn
	m.mu.Unlock()
	if n == 0 {
		return Status{}, false
	}
	return Status{Name: "dnstt", Installed: true, Running: true, Version: DnsttVersion, BuiltIn: true}, true
}

// SlipConfig is the Slipstream command line, for the panel.
func (m *Manager) SlipConfig() []byte {
	b, _ := m.build()
	return b.slip
}

// dnsttLogs gives the dnstt log lines to the panel.
type dnsttLogs struct{}

func (dnsttLogs) Logs() []string { return DnsttLogs() }

// DnsttLogger is the panel's handle on the dnstt log lines.
var DnsttLogger interface{ Logs() []string } = dnsttLogs{}
