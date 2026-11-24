package engine

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/awg"
	"github.com/pedramrj/dunvex/internal/dnsmux"
	"github.com/pedramrj/dunvex/internal/edge"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/routing"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
	"github.com/pedramrj/dunvex/internal/warp"
)

type obj = map[string]any

// Sample is one point of live proxy throughput (bytes per second).
type Sample struct {
	T    int64 `json:"t"`
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// Manager owns both cores and keeps them in sync with the database.
type Manager struct {
	Store   *store.Store
	Xray    *Core
	SingBox *Core
	MTG     *Core       // the Telegram (MTProto) proxy
	AWG     *awg.Server // AmneziaWG (its own interface)
	Mita    *Core       // the Mieru server
	TT      *Core       // the TrustTunnel endpoint
	Usque   *Core       // Cloudflare WARP over MASQUE, a local SOCKS proxy (APIPort)
	Slip    *Core       // the Slipstream server (a DNS tunnel)
	Env     func() protocol.Env

	// SharedPort is the panel's TCP port when the edge router owns it (0: none).
	// Reality inbounds on it listen on loopback and get their clients by SNI.
	SharedPort func() int
	// OnRoutes receives the router's SNI table after every apply.
	OnRoutes  func(map[string]edge.Target)
	internals map[int64]int // inbound → its loopback port behind the router (guarded by mu)

	// OnUDP receives the UDP ports to pass on to a loopback address after
	// every apply: TrustTunnel's QUIC when it shares the panel's port.
	OnUDP func(map[int]string)
	// ClientOf maps a loopback address the router dialed from to the real client IP.
	ClientOf func(string) string
	// OnApplied runs after every apply, so other servers can be brought in line.
	OnApplied func()
	// RouterPort and WarpTestPort are the loopback SOCKS inbounds of the routing.
	RouterPort, WarpTestPort int
	// WarpClient registers WARP devices (nil: Cloudflare's API).
	WarpClient *warp.Client
	// InstallUsque downloads usque when a MASQUE device needs it (nil: never).
	InstallUsque func() error

	applyCh chan struct{}
	applyMu sync.Mutex

	mu          sync.Mutex
	inboundErrs map[int64]string
	coreErrs    map[string]string
	activeNames map[string]bool
	live        []Sample
	online      map[int64]int64 // user id → last unix second with traffic
	userRate    map[int64]Sample
	inRate      map[int64]Sample
	badPorts    map[int]string // ports another program holds: those inbounds are skipped

	dev       *devices
	liveGone  map[string]int64 // users removed live from Xray → when
	lastKick  int64
	overSince map[int64]int64 // user id → first moment over the device limit

	awgNow   *awgPlan                // what AmneziaWG serves now
	awgPrev  map[string]awg.PeerStat // last counters per peer
	awgGen   int                     // amneziawg-go start those counters belong to
	awgRetry int64                   // last time a stopped AmneziaWG was started again

	mitaNow  *mitaPlan                // what mita serves now
	mitaPrev map[string]store.Traffic // last counters per Mieru user
	mitaGen  time.Time                // start of the mita process they came from
	mitaGone map[string]int64         // users removed by a reload, and when
	mitaKick int64                    // last restart to cut a removed user off

	ttNow  *mitaPlan                // what TrustTunnel serves now (its inbound)
	ttPrev map[string]store.Traffic // last counters per TrustTunnel user
	ttGen  time.Time                // start of the endpoint they came from

	dns     dnsState            // shared DNS ports and dnstt servers (guarded by applyMu)
	dnsNow  map[int]*dnsmux.Mux // a copy of dns.mux for status calls (guarded by mu)
	dnsttOn int                 // dnstt servers running (guarded by mu)
}

// NewManager wires a manager for the given home directory.
func NewManager(st *store.Store, binDir, runDir string, env func() protocol.Env) *Manager {
	os.MkdirAll(runDir, 0o700)
	m := &Manager{
		Store:        st,
		Xray:         &Core{Name: "xray", BinDir: binDir, ConfigPath: filepath.Join(runDir, "xray.json"), APIPort: 62789},
		SingBox:      &Core{Name: "sing-box", BinDir: binDir, ConfigPath: filepath.Join(runDir, "sing-box.json"), APIPort: 62790},
		MTG:          &Core{Name: "mtg", BinDir: binDir, ConfigPath: filepath.Join(runDir, "mtg.toml"), APIPort: 62791},
		AWG:          awg.New(binDir, "dvxawg0"),
		Mita:         &Core{Name: "mita", BinDir: binDir, ConfigPath: filepath.Join(runDir, "mita.json")},
		TT:           &Core{Name: "trusttunnel", BinDir: binDir, ConfigPath: filepath.Join(runDir, "tt-vpn.toml"), APIPort: 62792},
		Usque:        &Core{Name: "usque", BinDir: binDir, ConfigPath: filepath.Join(runDir, "usque.json"), APIPort: 62797},
		Slip:         &Core{Name: "slipstream-server", BinDir: binDir, ConfigPath: filepath.Join(runDir, "slipstream.args")},
		Env:          env,
		RouterPort:   RouterPort,
		WarpTestPort: WarpTestPort,
		applyCh:      make(chan struct{}, 1),
		inboundErrs:  map[int64]string{},
		coreErrs:     map[string]string{},
		online:       map[int64]int64{},
		userRate:     map[int64]Sample{},
		inRate:       map[int64]Sample{},
		badPorts:     map[int]string{},
		dev:          newDevices(),
		liveGone:     map[string]int64{},
		overSince:    map[int64]int64{},
	}
	m.Xray.Live = m.xrayLive
	m.Mita.Live = m.mitaLive
	m.SingBox.OnLine = m.dev.singboxLine
	m.dev.resolve = func(addr string) string {
		if m.ClientOf != nil {
			return m.ClientOf(addr)
		}
		return ""
	}
	return m
}

func (m *Manager) xrayAPI() string { return "127.0.0.1:" + strconv.Itoa(m.Xray.APIPort) }

// xrayLive applies user-only changes to the running Xray through its API.
func (m *Manager) xrayLive(old, cfg []byte) bool {
	ops, gone, ok := xrayUserDiff(old, cfg)
	if !ok {
		return false
	}
	if len(ops) == 0 {
		return true
	}
	if err := applyUserOps(m.xrayAPI(), ops); err != nil {
		log.Printf("live user change failed, restarting xray: %v", err)
		return false
	}
	now := time.Now().Unix()
	m.mu.Lock()
	for _, op := range ops {
		if op.add != nil {
			delete(m.liveGone, op.email)
		}
	}
	for _, g := range gone {
		m.liveGone[g] = now
	}
	m.mu.Unlock()
	return true
}

// RequestApply schedules a config rebuild (debounced).
func (m *Manager) RequestApply() {
	select {
	case m.applyCh <- struct{}{}:
	default:
	}
}

// Run starts the apply and collector loops. It blocks.
func (m *Manager) Run(stop <-chan struct{}) {
	if err := m.Apply(); err != nil {
		log.Printf("initial apply: %v", err)
	}
	collect := time.NewTicker(5 * time.Second)
	maint := time.NewTicker(time.Minute)
	defer collect.Stop()
	defer maint.Stop()
	for {
		select {
		case <-stop:
			m.collect()
			m.Xray.Stop()
			m.SingBox.Stop()
			m.MTG.Stop()
			m.Mita.Stop()
			m.TT.Stop()
			m.Usque.Stop()
			m.Slip.Stop()
			m.AWG.Stop()
			m.stopDNS()
			return
		case <-m.applyCh:
			time.Sleep(1500 * time.Millisecond) // coalesce bursts of edits
			select {
			case <-m.applyCh:
			default:
			}
			if err := m.Apply(); err != nil {
				log.Printf("apply: %v", err)
			}
		case <-collect.C:
			if m.collect() {
				m.Apply()
			}
		case <-maint.C:
			if m.maintenance() {
				m.Apply()
			}
		}
	}
}

// Configs builds both core configs from the database.
// A nil config means the core has nothing to serve.
func (m *Manager) Configs() (xray, singbox []byte, inboundErrs map[int64]string, active map[string]bool, err error) {
	b, err := m.build()
	if err != nil {
		return
	}
	return b.xray, b.singbox, b.errs, b.active, nil
}

// SNIRoutes returns the edge router's table for the current database.
func (m *Manager) SNIRoutes() (map[string]edge.Target, error) {
	b, err := m.build()
	return b.routes, err
}

// built is one generation of core configs plus the edge router's SNI table.
type built struct {
	xray, singbox []byte
	mtg           []byte             // the Telegram proxy (one per server)
	mita          []byte             // Mieru (one per server)
	mitaIn        int64              // its inbound
	tt            *protocol.TTServer // TrustTunnel (one per server)
	ttIn          int64              // its inbound
	awg           *awgPlan           // AmneziaWG (one per server)
	usque         []byte             // WARP over MASQUE (nil: not running)
	udp           map[int]string     // public UDP port → loopback address (TrustTunnel's QUIC behind the router)
	dns           map[int]*dnsPort   // shared DNS ports
	dnstt         map[int64]dnsttPlan
	slip          []byte // Slipstream's command line, one argument per line (one per server)
	slipIn        int64  // its inbound
	errs          map[int64]string
	active        map[string]bool
	routes        map[string]edge.Target
}

// internalPort is the loopback port an inbound behind the router listens on.
// It keeps the port it got (so its address never changes under it); a new one
// skips ports another program on the server already holds (TCP and UDP:
// TrustTunnel listens on both).
func (m *Manager) internalPort(id int64, used map[int]bool) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.internals == nil {
		m.internals = map[int64]int{}
	}
	if p, ok := m.internals[id]; ok && !used[p] {
		used[p] = true
		return p
	}
	p := 40000 + int(id%20000)
	for tries := 0; tries < 25000; tries++ {
		if !used[p] && util.FreePort(p) {
			break
		}
		if p++; p > 65000 {
			p = 40000
		}
	}
	used[p] = true
	m.internals[id] = p
	return p
}

func (m *Manager) build() (b built, err error) {
	users, err := m.Store.Users()
	if err != nil {
		return
	}
	inbounds, err := m.Store.Inbounds()
	if err != nil {
		return
	}
	env := m.Env()
	now := time.Now().Unix()
	rp := m.routePlan()
	if rp.acct != nil && rp.acct.Masque() && rp.serves {
		b.usque = rp.acct.UsqueConfig()
	}
	router := "" // Mieru and TrustTunnel leave through Xray when routing rules are on
	if rp.useRouter() {
		router = rp.routerAddr()
	}
	shared := 0
	if m.SharedPort != nil {
		shared = m.SharedPort()
	}
	b.errs, b.active, b.routes = map[int64]string{}, map[string]bool{}, map[string]edge.Target{}
	used := map[int]bool{m.Xray.APIPort: true, m.SingBox.APIPort: true, shared: true, m.RouterPort: true, m.WarpTestPort: true, m.Usque.APIPort: true}
	m.mu.Lock()
	for _, in := range inbounds {
		used[in.Port] = true
	}
	for p := range m.badPorts {
		used[p] = true
	}
	m.mu.Unlock()
	// UDP ports with dnstt, Slipstream or several XDNS get the shared DNS port
	dnsCount, dnsTunnel := map[int]int{}, map[int]bool{}
	for _, in := range inbounds {
		if p := protocol.Get(in.Preset); in.Enabled && in.NodeID == 0 && protocol.DNSRouted(p) {
			dnsCount[in.Port]++
			dnsTunnel[in.Port] = dnsTunnel[in.Port] || protocol.IsDNSTunnel(p)
		}
	}
	muxed := dnsMuxed(dnsCount, dnsTunnel)
	xicmpIn := int64(0)           // the one ping tunnel of this server
	health := HealthUser(m.Store) // the config check's own user (see health.go)
	for _, u := range users {
		if u.Name == HealthName {
			health = nil // a restored database with a user of that name: leave it alone
		}
	}
	var xin, sin []obj
	var sTags, sUsers []string
	seenUser := map[string]bool{}
	for _, in := range inbounds {
		if !in.Enabled || in.NodeID != 0 {
			continue // inbounds of other servers run there
		}
		p := protocol.Get(in.Preset)
		if p == nil {
			b.errs[in.ID] = "unknown preset " + in.Preset
			continue
		}
		// a TCP inbound on the panel's port is reached through the edge router
		listen, sni := 0, ""
		if shared > 0 && in.Port == shared && p.Network == "tcp" {
			if !protocol.SNIRouted(p) {
				b.errs[in.ID] = fmt.Sprintf("port %d is the panel's port: only Reality, TrustTunnel and the Telegram proxy can share it; choose another port", in.Port)
				continue
			}
			if sni = protocol.SharedSNI(in); sni == "" {
				b.errs[in.ID] = fmt.Sprintf("a protocol on the panel's port %d needs an SNI (server name)", in.Port)
				continue
			}
			if _, dup := b.routes[sni]; dup {
				b.errs[in.ID] = fmt.Sprintf("another protocol on port %d already uses the SNI %s", in.Port, sni)
				continue
			}
			listen = m.internalPort(in.ID, used)
		} else {
			m.mu.Lock()
			bad, isBad := m.badPorts[in.Port]
			m.mu.Unlock()
			if isBad {
				b.errs[in.ID] = bad
				continue
			}
		}
		var us []*store.User
		for _, u := range users {
			if u.CanConnect(now) && u.AllowsInbound(in.ID) {
				us = append(us, u)
				b.active[u.Name] = true
			}
		}
		if health != nil && HealthServed(p) {
			us = append(us, health)
		}
		switch p.Engine {
		case "trusttunnel":
			if b.ttIn != 0 {
				b.errs[in.ID] = "only one TrustTunnel can run on a server: it serves every user on its port"
				continue
			}
			// nil while nobody may use it: without users the endpoint lets anyone in
			bind := ""
			if listen > 0 {
				// on the panel's port: TCP comes through the router by its
				// hostname; QUIC is passed on from the public UDP port unless
				// a UDP protocol holds it
				bind = "127.0.0.1:" + strconv.Itoa(listen)
			}
			b.tt, b.ttIn = protocol.TrustTunnelServer(in, us, env, filepath.Dir(m.TT.ConfigPath), m.TT.APIPort, router, bind), in.ID
			if bind != "" && b.tt != nil {
				b.routes[sni] = edge.Target{Addr: bind}
				if protocol.TTHide(in, env) {
					// hellos without the apps' key go to the hostname's real site
					b.routes[sni] = edge.Target{Addr: bind, AuthKey: string(protocol.TTAuthKey(in)), Decoy: net.JoinHostPort(in.Str("sni"), "443")}
				}
				if protocol.UDPHolder(inbounds, in.NodeID, in.Port, in.ID) == "" {
					if b.udp == nil {
						b.udp = map[int]string{}
					}
					b.udp[in.Port] = bind
				}
			}
		case "mita":
			if b.mitaIn != 0 {
				b.errs[in.ID] = "only one Mieru (TCP or UDP) can run on a server: it serves every user on its port"
				continue
			}
			cfg, e := protocol.MieruServer(p, in, us, env.Listen, router)
			if e != nil {
				b.errs[in.ID] = e.Error()
				continue
			}
			b.mita, b.mitaIn = cfg, in.ID // cfg is nil while nobody may use it: mita stays off
		case "awg":
			if b.awg != nil {
				b.errs[in.ID] = "only one AmneziaWG can run on a server"
				continue
			}
			b.awg = m.awgBuild(in, us)
		case "mtg":
			if b.mtg != nil {
				b.errs[in.ID] = "only one Telegram proxy can run on a server"
				continue
			}
			bind, pp := net.JoinHostPort(or(env.Listen, "0.0.0.0"), strconv.Itoa(in.Port)), false
			if listen > 0 {
				bind, pp = "127.0.0.1:"+strconv.Itoa(listen), true
				b.routes[sni] = edge.Target{Addr: bind, ProxyHeader: true}
			}
			b.mtg = mtgConfig(protocol.MTProtoSecret(in.Str("mtKey"), in.Str("sni")), bind, pp, m.MTG.APIPort)
		case "dnstt", "slipstream":
			dom := protocol.XDNSDomain(in)
			if dom == "" {
				b.errs[in.ID] = fmt.Sprintf("%s needs its tunnel domain (an NS record that points to this server)", p.Name)
				continue
			}
			if len(us) == 0 && !protocol.OpenSignIn(in) {
				continue // nobody may use it: it stays off
			}
			if p.Engine == "slipstream" && b.slipIn != 0 {
				b.errs[in.ID] = "only one Slipstream can run on a server; use dnstt for another tunnel domain"
				continue
			}
			back := "127.0.0.1:" + strconv.Itoa(m.internalPort(in.ID+dnsBackendKey, used))
			if e := b.addZone(in.Port, dnsListen(in.Port, env), dom, back, in.ID); e != "" {
				b.errs[in.ID] = e
				continue
			}
			socks := m.internalPort(in.ID, used)
			if p.Engine == "dnstt" {
				if b.dnstt == nil {
					b.dnstt = map[int64]dnsttPlan{}
				}
				b.dnstt[in.ID] = dnsttPlan{key: in.Str("privateKey"), domain: dom, upstream: "127.0.0.1:" + strconv.Itoa(socks), listen: back}
			} else {
				cert, key, e := m.slipCert(dom)
				if e != nil {
					b.errs[in.ID] = "Slipstream's certificate: " + e.Error()
					continue
				}
				_, port, _ := net.SplitHostPort(back)
				n, _ := strconv.Atoi(port)
				b.slip = []byte(strings.Join(protocol.SlipstreamArgs(in, n, "127.0.0.1:"+strconv.Itoa(socks), cert, key), "\n") + "\n")
				b.slipIn = in.ID
			}
			xin = append(xin, protocol.TunnelSocks(in, us, socks))
		case "xray":
			if protocol.IsXICMP(p) {
				if xicmpIn != 0 {
					b.errs[in.ID] = "only one xICMP can run on a server: it reads every ping the server gets"
					continue
				}
				if e := xicmpReady(); e != nil {
					b.errs[in.ID] = e.Error()
					continue
				}
				xicmpIn = in.ID
			}
			ib, e := protocol.XrayInbound(p, in, us, env)
			if e != nil {
				b.errs[in.ID] = e.Error()
				continue
			}
			if protocol.DNSRouted(p) && muxed[in.Port] {
				// XDNS next to other DNS tunnels: behind the shared port, by its domain
				back := m.internalPort(in.ID+dnsBackendKey, used)
				if e := b.addZone(in.Port, dnsListen(in.Port, env), protocol.XDNSDomain(in), "127.0.0.1:"+strconv.Itoa(back), in.ID); e != "" {
					b.errs[in.ID] = e
					continue
				}
				ib["listen"], ib["port"] = "127.0.0.1", back
			} else if p.Network == "udp" && in.Port == 53 && env.Listen == "" {
				// systemd-resolved holds 127.0.0.53:53, so "every address" cannot
				// be bound; the address facing the internet can
				if ip := util.OutboundIP(); ip != "" {
					ib["listen"] = ip
				}
			}
			if listen > 0 {
				ib["listen"], ib["port"] = "127.0.0.1", listen
				if ss, ok := ib["streamSettings"].(obj); ok {
					ss["sockopt"] = obj{"acceptProxyProtocol": true} // the router sends the client's address first
				}
				b.routes[sni] = edge.Target{Addr: "127.0.0.1:" + strconv.Itoa(listen), ProxyHeader: true}
			}
			xin = append(xin, ib)
		case "singbox":
			if len(us) == 0 {
				continue // sing-box refuses user-based inbounds without users
			}
			ibs, e := protocol.SingboxInbounds(p, in, us, env)
			if e != nil {
				b.errs[in.ID] = e.Error()
				continue
			}
			if listen > 0 {
				// sing-box dropped PROXY protocol: the router maps the loopback port back to the client
				ibs[0]["listen"], ibs[0]["listen_port"] = "127.0.0.1", listen
				b.routes[sni] = edge.Target{Addr: "127.0.0.1:" + strconv.Itoa(listen)}
			}
			sin = append(sin, ibs...)
			sTags = append(sTags, in.Tag())
			for _, u := range us {
				if u.Name != HealthName && !seenUser[u.Name] {
					seenUser[u.Name] = true
					sUsers = append(sUsers, u.Name)
				}
			}
		}
	}
	if mitaOrTT := b.mita != nil || b.tt != nil; len(xin) > 0 || (mitaOrTT && rp.useRouter()) {
		b.xray, _ = json.MarshalIndent(m.xrayConfig(xin, rp, mitaOrTT), "", "  ")
	}
	// TLS for an unknown name goes to the first shared Reality inbound (lowest id),
	// which forwards it to its real target: the port behaves like plain Reality
	for _, in := range inbounds {
		if p := protocol.Get(in.Preset); p == nil || p.Security != "reality" {
			continue // the Telegram proxy only answers its own domain
		}
		if sni := protocol.SharedSNI(in); in.Port == shared && sni != "" {
			if t, ok := b.routes[sni]; ok {
				b.routes[edge.Fallback] = t
				break
			}
		}
	}
	if len(sin) > 0 {
		sort.Strings(sUsers)
		b.singbox, _ = json.MarshalIndent(m.singboxConfig(sin, sTags, sUsers, rp), "", "  ")
	}
	return
}

func (m *Manager) xrayConfig(inbounds []obj, rp *routePlan, mitaOrTT bool) obj {
	rules, outbounds, extra, strategy := rp.xrayRouting(m.Store.GetSetting("block_torrent", "true") == "true", mitaOrTT)
	inbounds = append(inbounds, extra...)
	return obj{
		"log":   obj{"loglevel": "warning", "access": "none"},
		"api":   obj{"tag": "api", "listen": "127.0.0.1:" + strconv.Itoa(m.Xray.APIPort), "services": []string{"StatsService", "HandlerService"}},
		"stats": obj{},
		"policy": obj{
			"levels": obj{"0": obj{"statsUserUplink": true, "statsUserDownlink": true, "statsUserOnline": true, "handshake": 4, "connIdle": 300}},
			"system": obj{"statsInboundUplink": true, "statsInboundDownlink": true},
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   obj{"domainStrategy": strategy, "rules": rules},
	}
}

func (m *Manager) singboxConfig(inbounds []obj, tags, users []string, rp *routePlan) obj {
	torrent := m.Store.GetSetting("block_torrent", "true") == "true"
	rules, ruleSets, endpoints, outs, final, resolver, err := m.singboxRouting(rp, torrent)
	if err != nil {
		// the site lists could not be read: keep the basic rules rather than stop the core
		log.Printf("sing-box routing: %v", err)
		rules, ruleSets, endpoints, outs, final, resolver, _ = m.singboxRouting(&routePlan{set: routing.Defaults()}, torrent)
	}
	route := obj{"rules": rules, "final": final}
	if len(ruleSets) > 0 {
		route["rule_set"] = ruleSets
	}
	cfg := obj{
		"log":       obj{"level": "info", "timestamp": true}, // info: connection lines feed the device list
		"inbounds":  inbounds,
		"outbounds": append([]obj{{"type": "direct", "tag": "direct"}}, outs...),
		"route":     route,
	}
	if len(endpoints) > 0 {
		cfg["endpoints"] = endpoints
	}
	if resolver {
		cfg["dns"] = obj{"servers": []obj{{"type": "local", "tag": "local"}}}
		route["default_domain_resolver"] = "local"
	}
	if m.SingBox.HasTag("with_v2ray_api") {
		cfg["experimental"] = obj{"v2ray_api": obj{
			"listen": "127.0.0.1:" + strconv.Itoa(m.SingBox.APIPort),
			"stats":  obj{"enabled": true, "inbounds": tags, "users": users},
		}}
	}
	return cfg
}

// Apply rebuilds configs and restarts cores whose config changed.
func (m *Manager) Apply() error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	b, err := m.build()
	if err != nil {
		return err
	}
	xcfg, scfg, tcfg := b.xray, b.singbox, b.mtg
	ierrs, active := b.errs, b.active
	m.collect() // save counters before a restart wipes them
	if m.OnUDP != nil {
		m.OnUDP(b.udp) // first: a UDP port given back to a core must be free when it starts
	}
	m.dnsApply(b.dns, false) // likewise for a DNS port that XDNS takes back
	cerrs := map[string]string{}
	for attempt := 0; attempt < 6; attempt++ {
		cerrs = map[string]string{}
		retry := false
		var ttMain []byte
		if b.tt != nil {
			ttMain = b.tt.Main
			if e := m.writeTTFiles(b.tt); e != nil {
				cerrs[m.TT.Name] = e.Error()
				ttMain = nil
			}
		}
		for _, c := range []struct {
			core *Core
			cfg  []byte
		}{{m.Usque, b.usque}, {m.Xray, xcfg}, {m.SingBox, scfg}, {m.MTG, tcfg}, {m.Mita, b.mita}, {m.TT, ttMain}, {m.Slip, b.slip}} {
			if c.core == m.TT && cerrs[m.TT.Name] != "" {
				continue
			}
			if e := c.core.Apply(c.cfg); e != nil {
				cerrs[c.core.Name] = e.Error()
				continue
			}
			// a port held by another program makes the whole core exit: find it,
			// park only that inbound and start again without it
			if port := c.core.BindFailure(c.cfg != nil); port > 0 {
				m.mu.Lock()
				m.badPorts[port] = fmt.Sprintf("port %d is used by another program on this server; choose another port", port)
				m.mu.Unlock()
				m.Store.Log("core", "%s: port %d is busy, that protocol was paused", c.core.Name, port)
				retry = true
			}
		}
		if !retry {
			break
		}
		if b, err = m.build(); err != nil {
			return err
		}
		xcfg, scfg, tcfg = b.xray, b.singbox, b.mtg
		ierrs, active = b.errs, b.active
	}
	m.mu.Lock()
	m.mitaNow = nil
	if b.mita != nil {
		m.mitaNow = &mitaPlan{inbound: b.mitaIn}
	}
	m.mu.Unlock()
	m.mu.Lock()
	m.ttNow = nil
	if b.tt != nil {
		m.ttNow = &mitaPlan{inbound: b.ttIn}
	}
	m.mu.Unlock()
	if b.tt != nil && cerrs[m.TT.Name] == "" {
		if err := m.ttServing(); err != nil {
			cerrs[m.TT.Name] = err.Error()
			ierrs[b.ttIn] = err.Error()
		}
	}
	if b.mita != nil && cerrs["mita"] == "" {
		if err := m.mitaServing(); err != nil {
			cerrs["mita"] = err.Error()
			ierrs[b.mitaIn] = err.Error()
		}
	}
	if err := m.awgApply(b.awg); err != nil {
		cerrs["amneziawg"] = err.Error()
		ierrs[b.awg.inbound] = err.Error()
	}
	if m.OnUDP != nil {
		m.OnUDP(b.udp) // again: a port a core just gave up can be taken now
	}
	for id, e := range m.dnsttApply(b.dnstt) {
		ierrs[id] = e
	}
	for id, e := range m.dnsApply(b.dns, true) {
		ierrs[id] = e
	}
	if b.slip != nil && cerrs[m.Slip.Name] != "" {
		ierrs[b.slipIn] = "Slipstream: " + cerrs[m.Slip.Name]
	}
	m.dnsSnapshot()
	if m.OnRoutes != nil {
		m.OnRoutes(b.routes)
	}
	if m.OnApplied != nil {
		m.OnApplied()
	}
	m.mu.Lock()
	prev := m.activeNames
	m.inboundErrs, m.coreErrs, m.activeNames = ierrs, cerrs, active
	m.mu.Unlock()
	if prev != nil {
		now := time.Now().Unix()
		if users, err := m.Store.Users(); err == nil {
			for _, u := range users {
				if prev[u.Name] && !active[u.Name] && !u.CanConnect(now) {
					m.Store.Log("user", "%s is now %s and was disconnected", u.Name, u.Status(now))
				}
			}
		}
	}
	if len(cerrs) > 0 {
		var parts []string
		for k, v := range cerrs {
			parts = append(parts, k+": "+v)
		}
		sort.Strings(parts)
		return fmt.Errorf("%s", strings.Join(parts, "; "))
	}
	return nil
}

// ClearPortErrors forgets busy ports so the next apply tries them again.
func (m *Manager) ClearPortErrors() {
	m.mu.Lock()
	m.badPorts = map[int]string{}
	m.mu.Unlock()
}

// Errors returns the latest per-inbound and per-core problems.
func (m *Manager) Errors() (map[int64]string, map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ie := map[int64]string{}
	for k, v := range m.inboundErrs {
		ie[k] = v
	}
	ce := map[string]string{}
	for k, v := range m.coreErrs {
		ce[k] = v
	}
	return ie, ce
}

// collect reads and resets the cores' counters and stores them.
// It returns true when a user's status changed and configs must be rebuilt.
func (m *Manager) collect() bool {
	raw := map[string]int64{}
	xr := map[string]int64{}
	if m.Xray.Running() {
		if s, err := QueryStats(m.xrayAPI(), "xray.app.stats.command.StatsService", true); err == nil {
			for k, v := range s {
				raw[k] += v
				xr[k] = v
			}
		}
		if ips, err := xrayOnlineIPs(m.xrayAPI()); err == nil {
			for name, list := range ips {
				for _, e := range list {
					m.dev.see(name, e.IP, e.LastSeen)
				}
			}
		}
	}
	if m.SingBox.Running() && m.SingBox.HasTag("with_v2ray_api") {
		if s, err := QueryStats("127.0.0.1:"+strconv.Itoa(m.SingBox.APIPort), "v2ray.core.app.stats.command.StatsService", true); err == nil {
			for k, v := range s {
				raw[k] += v
			}
		}
	}
	now := time.Now()
	users, err := m.Store.Users()
	if err != nil {
		return false
	}
	byName := map[string]*store.User{}
	for _, u := range users {
		byName[u.Name] = u
	}
	ud := map[int64]store.Traffic{}
	id := map[int64]store.Traffic{}
	var totUp, totDown int64
	for k, v := range raw {
		if v <= 0 {
			continue
		}
		c, ok := ParseCounter(k)
		if !ok {
			continue
		}
		switch c.Kind {
		case "user":
			u := byName[c.Name]
			if u == nil {
				continue
			}
			t := ud[u.ID]
			if c.Dir == "uplink" {
				t.Up += v
			} else {
				t.Down += v
			}
			ud[u.ID] = t
		case "inbound":
			if !strings.HasPrefix(c.Name, "in-") {
				continue
			}
			n, err := strconv.ParseInt(strings.TrimPrefix(c.Name, "in-"), 10, 64)
			if err != nil {
				continue // e.g. the internal "in-5-ss" ShadowTLS hop
			}
			t := id[n]
			if c.Dir == "uplink" {
				t.Up += v
			} else {
				t.Down += v
			}
			id[n] = t
		}
	}
	m.awgCollect(ud, id)
	m.mitaCollect(byName, ud, id)
	m.ttCollect(byName, ud, id)
	for _, t := range id {
		totUp, totDown = totUp+t.Up, totDown+t.Down
	}
	if err := m.Store.AddTraffic(now, ud, id); err != nil {
		log.Printf("store traffic: %v", err)
	}

	m.mu.Lock()
	interval := int64(5)
	if n := len(m.live); n > 0 {
		if d := now.Unix() - m.live[n-1].T; d > 0 {
			interval = d
		}
	}
	m.live = append(m.live, Sample{T: now.Unix(), Up: totUp / interval, Down: totDown / interval})
	if len(m.live) > 360 {
		m.live = m.live[len(m.live)-360:]
	}
	m.userRate = map[int64]Sample{}
	m.inRate = map[int64]Sample{}
	for iid, t := range id {
		m.inRate[iid] = Sample{T: now.Unix(), Up: t.Up / interval, Down: t.Down / interval}
	}
	for uid, t := range ud {
		m.online[uid] = now.Unix()
		m.userRate[uid] = Sample{T: now.Unix(), Up: t.Up / interval, Down: t.Down / interval}
	}
	active := m.activeNames
	// a user removed live keeps open connections until they close; if one is
	// still moving traffic, restart Xray once to cut it off
	kick := ""
	for name, at := range m.liveGone {
		switch {
		case now.Unix()-at > 900 || active[name]:
			delete(m.liveGone, name)
		case now.Unix()-at >= 20 && xr["user>>>"+name+">>>traffic>>>uplink"]+xr["user>>>"+name+">>>traffic>>>downlink"] > 0:
			kick = name
		}
	}
	if kick != "" && now.Unix()-m.lastKick >= 60 {
		m.lastKick = now.Unix()
		m.liveGone = map[string]int64{}
	} else {
		kick = ""
	}
	m.mu.Unlock()
	if kick != "" {
		m.Store.Log("core", "%s was still connected after losing access; Xray restarted to disconnect it", kick)
		m.Xray.Restart()
	}

	// first connection starts the timer of "pending" users
	changed := false
	for _, u := range users {
		t := ud[u.ID]
		if (t.Up > 0 || t.Down > 0) && u.ExpireAt == 0 && u.DurationDays > 0 && u.FirstUseAt == 0 {
			u.FirstUseAt = now.Unix()
			u.ExpireAt = now.Unix() + int64(u.DurationDays)*86400
			m.Store.UpdateUser(u)
			m.Store.Log("user", "%s connected for the first time; plan of %d days started", u.Name, u.DurationDays)
		}
	}
	// detect users that crossed their limits
	fresh, _ := m.Store.Users()
	if m.enforceDevices(fresh, now.Unix()) {
		changed = true
	}
	for _, u := range fresh {
		if u.CanConnect(now.Unix()) != active[u.Name] {
			changed = true
		}
	}
	return changed
}

// Device limit: an address counts while it opened a connection in the last
// minute. A user above the limit for two minutes straight is paused for a few
// minutes. The window ignores short overlaps such as a phone moving from
// Wi-Fi to mobile data.
const (
	deviceWindow = 60
	deviceGrace  = 120
)

func (m *Manager) enforceDevices(users []*store.User, now int64) bool {
	counts := m.dev.counts(now - deviceWindow)
	pause := int64(10)
	if v, err := strconv.Atoi(m.Store.GetSetting("ip_pause_minutes", "10")); err == nil && v > 0 {
		pause = int64(v)
	}
	changed := false
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range users {
		n := counts[u.Name]
		if u.IPLimit <= 0 || !u.CanConnect(now) || n <= u.IPLimit {
			delete(m.overSince, u.ID)
			continue
		}
		first := m.overSince[u.ID]
		if first == 0 {
			m.overSince[u.ID] = now
			continue
		}
		if now-first < deviceGrace {
			continue
		}
		delete(m.overSince, u.ID)
		u.PausedUntil = now + pause*60
		if err := m.Store.UpdateUser(u); err == nil {
			m.Store.Log("limit", "%s used %d devices at once (limit %d) and is paused for %d minutes", u.Name, n, u.IPLimit, pause)
			changed = true
		}
	}
	return changed
}

// Devices returns the addresses a user connected from in the last 5 minutes.
func (m *Manager) Devices(name string) []Device {
	return m.dev.list(name, time.Now().Unix()-300)
}

// AllDevices returns every address any user connected from in the last 5 minutes.
func (m *Manager) AllDevices() []Seen {
	return m.dev.all(time.Now().Unix() - 300)
}

// DeviceCounts returns, per user name, addresses seen in the last 5 minutes.
func (m *Manager) DeviceCounts() map[string]int {
	return m.dev.counts(time.Now().Unix() - 300)
}

// maintenance performs periodic resets and pruning; returns true if configs must change.
func (m *Manager) maintenance() bool {
	users, err := m.Store.Users()
	if err != nil {
		return false
	}
	now := time.Now()
	changed := false
	for _, u := range users {
		if u.ResetPeriod == "" || u.ResetPeriod == "none" {
			continue
		}
		last := u.LastResetAt
		if last == 0 {
			last = u.CreatedAt
		}
		lt := time.Unix(last, 0)
		var next time.Time
		switch u.ResetPeriod {
		case "daily":
			next = lt.AddDate(0, 0, 1)
		case "weekly":
			next = lt.AddDate(0, 0, 7)
		case "monthly":
			next = lt.AddDate(0, 1, 0)
		default:
			continue
		}
		if now.After(next) {
			wasLimited := u.Status(now.Unix()) == store.StatusLimited
			u.UsedUp, u.UsedDown, u.LastResetAt, u.ResetUsage, u.Notified = 0, 0, now.Unix(), true, ""
			m.Store.UpdateUser(u)
			m.Store.Log("user", "%s usage reset (%s)", u.Name, u.ResetPeriod)
			if wasLimited {
				changed = true
			}
		}
	}
	// a paused user whose pause ended can connect again
	for _, u := range users {
		if u.PausedUntil > 0 && u.PausedUntil <= now.Unix() {
			u.PausedUntil = 0
			m.Store.UpdateUser(u)
			changed = true
		}
	}
	m.dev.prune(now.Unix() - 3600)
	if now.Minute() == 0 {
		m.Store.PruneUsage(now.AddDate(0, 0, -400).Unix())
	}
	return changed
}

// Live returns recent throughput samples.
func (m *Manager) Live() []Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Sample(nil), m.live...)
}

// OnlineUsers returns user ids that moved traffic within the last `within` seconds.
func (m *Manager) OnlineUsers(within int64) map[int64]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().Unix()
	out := map[int64]bool{}
	for id, t := range m.online {
		if now-t <= within {
			out[id] = true
		}
	}
	return out
}

// InboundRate returns the latest per-inbound speed (bytes/s).
func (m *Manager) InboundRate(id int64) Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inRate[id]
}

// UserRate returns the latest per-user speed (bytes/s).
func (m *Manager) UserRate(id int64) Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userRate[id]
}

// CertsRenewed restarts the cores that read certificate files, so a renewed
// certificate written over the old one is served at once.
func (m *Manager) CertsRenewed() {
	m.applyMu.Lock()
	for _, c := range []*Core{m.Xray, m.SingBox} {
		if c.Running() {
			c.Restart()
		}
	}
	m.applyMu.Unlock()
	m.RequestApply()
}
