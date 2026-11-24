package main

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/pedramrj/dunvex/internal/awg"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// A fresh install turns on every protocol that needs no domain, with as few
// open ports as possible: the TCP ones share the panel's port 443 by server
// name (Reality, Reality XHTTP, TrustTunnel, the Telegram proxy), Hysteria2
// takes UDP 443, AmneziaWG and Mieru get a port each, and xICMP needs none.
// The CDN protocols and the DNS tunnels need a domain and DNS records, so the
// owner adds them later.

// starterEnv is what the starter set depends on, so tests can stand in for it.
type starterEnv struct {
	binDir   string
	free     func(port int, udp bool) bool // can the port be bound here
	goodSNI  func(host string) bool        // a good Reality camouflage site (TLS 1.3 + HTTP/2)
	icmpOK   func() error                  // raw ICMP sockets for xICMP
	randPort func() int
}

// xhttpSites are Reality XHTTP's camouflage candidates, tested at install time.
var xhttpSites = []string{"www.microsoft.com", "www.apple.com", "dl.google.com", "www.samsung.com"}

// ttSites are the hostnames TrustTunnel may look like on the shared port.
var ttSites = []string{"www.apple.com", "www.microsoft.com", "dl.google.com", "www.samsung.com"}

type starterItem struct {
	name string
	port int
	udp  bool
	none bool // listens on no port (xICMP)
}

// starterInbounds creates the starter set on an empty server and describes it.
func starterInbounds(st *store.Store, env starterEnv) []starterItem {
	pp, _ := strconv.Atoi(st.GetSetting("panel_port", "0"))
	shared := pp == 443 // the panel's router hands TCP 443 out by server name
	var made []starterItem
	taken := map[string]bool{protocol.DefaultReality: true, protocol.DefaultFakeTLS: true}
	installed := func(name string) bool {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		st, err := os.Stat(filepath.Join(env.binDir, name))
		return err == nil && !st.IsDir()
	}
	// ownPort finds a port for preset by the panel's own rules (TCP and UDP on
	// one number do not clash) that nothing else on the server holds
	ownPort := func(preset string, prefer ...int) int {
		p := protocol.Get(preset)
		udp := p.Network == "udp"
		ok := func(port int) bool {
			list, _ := st.Inbounds()
			if port <= 0 || (!udp && port == pp) || protocol.PortConflict(list, pp, port, 0, p, "") != "" {
				return false
			}
			return (udp && port == pp) || env.free(port, udp) // UDP of the panel's port is free for UDP protocols
		}
		for _, port := range prefer {
			if ok(port) {
				return port
			}
		}
		for i := 0; i < 200; i++ {
			if port := env.randPort(); ok(port) {
				return port
			}
		}
		return 0
	}
	add := func(preset, remark string, port int, params map[string]any, item starterItem) *store.Inbound {
		p := protocol.Get(preset)
		if p == nil || port == 0 {
			return nil
		}
		in := &store.Inbound{Preset: p.ID, Port: port, Enabled: true, Remark: remark, Params: protocol.Defaults(p, params, "")}
		list, _ := st.Inbounds()
		if msg := protocol.PortConflict(list, pp, port, 0, p, protocol.RouteName(in)); msg != "" {
			fmt.Printf("  ! %s: %s\n", remark, msg)
			return nil
		}
		if err := st.CreateInbound(in); err != nil {
			fmt.Printf("  ! %s: %v\n", remark, err)
			return nil
		}
		item.port = port
		made = append(made, item)
		return in
	}
	tcp443 := func(preset string, fallback int) int { // 443 behind the router, else a port of its own
		if shared {
			return 443
		}
		return ownPort(preset, fallback)
	}

	// Reality for every app, on the panel's port
	port := 443
	if !shared && !env.free(443, false) {
		port = ownPort("vless-reality-universal", 8443, 2443)
	}
	add("vless-reality-universal", "Reality", port, nil, starterItem{name: "Reality"})

	// Hysteria2 on UDP 443: fast on lossy networks, looks like HTTP/3
	add("hysteria2", "Hysteria2", ownPort("hysteria2", 443, protocol.Get("hysteria2").DefaultPort), map[string]any{"obfs": true}, starterItem{name: "Hysteria2", udp: true})

	// Reality XHTTP: ordinary-looking HTTP requests, against connection-length detection
	xsni := ""
	for _, s := range xhttpSites {
		if !taken[s] && env.goodSNI(s) {
			xsni = s
			break
		}
	}
	if shared && xsni != "" {
		taken[xsni] = true
		add("vless-reality-xhttp", "Reality XHTTP", 443, map[string]any{"sni": xsni}, starterItem{name: "Reality XHTTP"})
	} else {
		// on a port of its own the usual camouflage site serves it too
		add("vless-reality-xhttp", "Reality XHTTP", ownPort("vless-reality-xhttp", 8443, 2053), map[string]any{"sni": firstNonEmpty(xsni, protocol.DefaultReality)}, starterItem{name: "Reality XHTTP"})
	}

	// the Telegram proxy: Telegram without a VPN app
	if installed("mtg") {
		add("mtproto", "Telegram", tcp443("mtproto", 2083), nil, starterItem{name: "Telegram proxy"})
	}

	// TrustTunnel for its official apps and Shadowrocket; HTTP/2, as Hysteria2 holds UDP 443
	if installed("trusttunnel") {
		host := ""
		for _, s := range ttSites {
			if !taken[s] {
				host = s
				break
			}
		}
		if host != "" {
			taken[host] = true
			params := map[string]any{"sni": host, "protocol": "http2"}
			add("trusttunnel", "TrustTunnel", tcp443("trusttunnel", 2087), params, starterItem{name: "TrustTunnel"})
		}
	}

	// AmneziaWG: a full VPN that DPI cannot recognise (AmneziaVPN apps)
	if awg.New(env.binDir, "").Installed() {
		add("amneziawg", "AmneziaWG", ownPort("amneziawg", protocol.Get("amneziawg").DefaultPort), nil, starterItem{name: "AmneziaWG", udp: true})
	}

	// Mieru: TCP with no fixed pattern (Clash.Meta apps, Karing)
	if installed("mita") {
		add("mieru", "Mieru", ownPort("mieru"), nil, starterItem{name: "Mieru"})
	}

	// xICMP: for networks where only ping works; it listens on no port
	if env.icmpOK() == nil {
		// its number only appears in links; one nothing else shows keeps them clear
		p := 0
		for i := 0; i < 200 && p == 0; i++ {
			list, _ := st.Inbounds()
			if c := env.randPort(); !protocol.UsesPort(list, c) && c != pp {
				p = c
			}
		}
		add("vless-xicmp", "xICMP", p, nil, starterItem{name: "xICMP", none: true})
	}
	return made
}

// starterLine sums the set up for install-info.txt. The installer reads the
// ports from its "(port N)" parts (to open them in the firewall) and shows the
// rest after "ready".
func starterLine(items []starterItem) string {
	var order []int
	names := map[int][]string{}
	var none []string
	for _, it := range items {
		if it.none {
			none = append(none, it.name)
			continue
		}
		if _, ok := names[it.port]; !ok {
			order = append(order, it.port)
		}
		names[it.port] = append(names[it.port], it.name)
	}
	var parts []string
	for _, p := range order {
		n := names[p]
		if len(n) == 1 {
			parts = append(parts, fmt.Sprintf("%s (port %d)", n[0], p))
		} else {
			parts = append(parts, fmt.Sprintf("%d protocols (port %d)", len(n), p))
		}
	}
	parts = append(parts, none...)
	return strings.Join(parts, " + ")
}

// starterDetails lists each protocol with its port, for the record.
func starterDetails(items []starterItem) string {
	var parts []string
	for _, it := range items {
		switch {
		case it.none:
			parts = append(parts, it.name+": pings")
		case it.udp:
			parts = append(parts, fmt.Sprintf("%s: UDP %d", it.name, it.port))
		default:
			parts = append(parts, fmt.Sprintf("%s: TCP %d", it.name, it.port))
		}
	}
	return strings.Join(parts, " · ")
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func starterPort() int { return 20000 + rand.IntN(30000) }
