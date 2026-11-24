package protocol

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/pedramrj/dunvex/internal/store"
)

// Anti-censorship options the owner turns on in Settings > Subscription, next
// to the TLS fragment (fragment.go) and ECH (ech.go). Each one goes only where
// the user's app reads it; docs/ANTI-CENSORSHIP.md lists which app reads what.

// ---------------------------------------------------------------- Mux

// Mux lets an app carry many connections over one connection to the server:
// fewer handshakes for a filter to watch and to slow down. Xray apps get
// Xray's own Mux.Cool in the Xray JSON subscription (an Xray server always
// takes it). sing-box apps, Clash and Hiddify get sing-box's multiplex on this
// server's sing-box protocols, whose inbounds turn it on (an Xray server does
// not speak it).
type Mux struct {
	Protocol string  // sing-box's multiplex: h2mux, smux or yamux
	Streams  int     // connections one connection to the server carries
	Padding  bool    // sing-box's multiplex pads its frames
	Brutal   *Brutal // TCP Brutal on sing-box's multiplex; nil = off, or the server cannot
	Singbox  bool    // the server's sing-box protocols take sing-box's multiplex (false for other servers)
}

// Brutal is TCP Brutal: the server sends at a fixed rate instead of slowing
// down on the losses a filtered network causes. It needs the tcp-brutal module
// in the server's Linux kernel. Speeds are in Mbps, from the user's side.
type Brutal struct {
	Up   int // from the device
	Down int // to the device
}

// Mux defaults.
const (
	MuxProtocol = "h2mux"
	MuxStreams  = 8
	BrutalUp    = 20
	BrutalDown  = 100
)

// MuxProtocols are the multiplex protocols of sing-box (and Clash).
var MuxProtocols = []string{"h2mux", "smux", "yamux"}

// Check says what is wrong with the settings, if anything.
func (m Mux) Check() error {
	if !slices.Contains(MuxProtocols, m.Protocol) {
		return fmt.Errorf("the protocol must be h2mux, smux or yamux")
	}
	if m.Streams < 1 || m.Streams > 128 {
		return fmt.Errorf("streams per connection must be between 1 and 128")
	}
	if b := m.Brutal; b != nil && (b.Up < 1 || b.Up > 10000 || b.Down < 1 || b.Down > 10000) {
		return fmt.Errorf("the Brutal speeds must be between 1 and 10000 Mbps")
	}
	return nil
}

// XrayMuxable: Mux.Cool on an Xray server, for VLESS without a flow, VMess,
// Trojan and Shadowsocks over raw TCP, WebSocket, HTTPUpgrade or mKCP. The
// server breaks it under Vision; XHTTP and gRPC multiplex on their own; the
// DNS and ping tunnels are too narrow for it.
func XrayMuxable(p *Preset) bool {
	if p.Engine != "xray" || p.flow != "" {
		return false
	}
	switch p.proto {
	case "vless", "vmess", "trojan", "shadowsocks":
	default:
		return false
	}
	switch p.transport {
	case "", "raw", "ws", "httpupgrade", "kcp":
		return true
	}
	return false
}

// SingMuxable: sing-box's multiplex, on this server's sing-box VLESS (Reality
// with Vision) and the Shadowsocks behind ShadowTLS.
func SingMuxable(p *Preset) bool {
	return p.Engine == "singbox" && (p.proto == "vless" || p.proto == "shadowtls")
}

// singMux reports whether p's configs (and inbound) carry sing-box's multiplex.
func (m *Mux) singMux(p *Preset) bool { return m != nil && m.Singbox && SingMuxable(p) }

// WithMux adds Mux.Cool to an Xray outbound of the Xray JSON subscription.
// QUIC to port 443 is refused inside it: browsers fall back to TCP, which the
// mux carries well.
func WithMux(o map[string]any, p *Preset, m *Mux) {
	if m == nil || !XrayMuxable(p) {
		return
	}
	o["mux"] = map[string]any{"enabled": true, "concurrency": m.Streams, "xudpConcurrency": m.Streams, "xudpProxyUDP443": "reject"}
}

// singbox is a sing-box client's multiplex.
func (m *Mux) singbox() obj {
	o := obj{"enabled": true, "protocol": m.Protocol, "max_streams": m.Streams, "padding": m.Padding}
	if b := m.Brutal; b != nil {
		o["brutal"] = obj{"enabled": true, "up_mbps": b.Up, "down_mbps": b.Down}
	}
	return o
}

// singboxServer is the inbound's multiplex: the server sends at most what the
// device downloads, and takes at most what it uploads.
func (m *Mux) singboxServer() obj {
	o := obj{"enabled": true, "padding": m.Padding}
	if b := m.Brutal; b != nil {
		o["brutal"] = obj{"enabled": true, "up_mbps": b.Down, "down_mbps": b.Up}
	}
	return o
}

// clash is Clash Meta's smux (sing-box's multiplex).
func (m *Mux) clash() obj {
	o := obj{"enabled": true, "protocol": m.Protocol, "max-streams": m.Streams, "padding": m.Padding}
	if b := m.Brutal; b != nil {
		o["brutal-opts"] = obj{"enabled": true, "up": b.Up, "down": b.Down}
	}
	return o
}

// linkParams: Hiddify reads sing-box's multiplex from the link (ray2sing's
// muxtype, muxsmax, muxpad, muxup and muxdown); other apps skip them.
func (m *Mux) linkParams(q url.Values) {
	q.Set("muxtype", m.Protocol)
	q.Set("muxsmax", strconv.Itoa(m.Streams))
	q.Set("muxpad", strconv.FormatBool(m.Padding))
	if b := m.Brutal; b != nil {
		q.Set("muxup", strconv.Itoa(b.Up))
		q.Set("muxdown", strconv.Itoa(b.Down))
	}
}

// ---------------------------------------------------------------- WireGuard noise

// Noise is a few random UDP packets the app sends before WireGuard's first
// packet, so a filter that matches the start of a WireGuard tunnel sees junk
// first. The server drops them unread (WireGuard ignores what it cannot
// decrypt): it needs nothing.
type Noise struct {
	Count int    // packets
	Size  string // bytes in each ("50-100")
	Delay string // milliseconds after each ("10-20")
}

// Noise defaults.
const (
	NoiseCount = 5
	NoiseSize  = "50-100"
	NoiseDelay = "10-20"
)

// Check says what is wrong with the settings, if anything.
func (n Noise) Check() error {
	if n.Count < 1 || n.Count > 50 {
		return fmt.Errorf("packets must be between 1 and 50")
	}
	if err := checkRange("size", n.Size, 1, 1200); err != nil {
		return err
	}
	return checkRange("delay", n.Delay, 0, 1000)
}

// mask is Xray's UDP finalmask "noise": Count random packets before the first.
func (n *Noise) mask() obj {
	items := make([]obj, n.Count)
	for i := range items {
		items[i] = obj{"rand": n.Size, "delay": n.Delay}
	}
	return obj{"type": "noise", "settings": obj{"noise": items}}
}

// noiseMask is the link's "fm" with the noise in it.
func noiseMask(n *Noise) string {
	b, _ := json.Marshal(obj{"udp": []obj{n.mask()}})
	return string(b)
}

// noiseLinkParams: Hiddify reads WireGuard noise from ifp (packets), ifps
// (their size) and ifpd (the pause after each).
func noiseLinkParams(n *Noise, q url.Values) {
	q.Set("ifp", strconv.Itoa(n.Count))
	q.Set("ifps", n.Size)
	q.Set("ifpd", n.Delay)
}

// noiseClash is Clash Meta's AmneziaWG junk packets. With only these set (no
// S1, S2 or H1 to H4) the tunnel is plain WireGuard: any WireGuard server
// takes it.
func noiseClash(n *Noise) obj {
	lo, hi, _ := strings.Cut(n.Size, "-")
	if hi == "" {
		hi = lo
	}
	jmin, _ := strconv.Atoi(lo)
	jmax, _ := strconv.Atoi(hi)
	return obj{"jc": n.Count, "jmin": jmin, "jmax": jmax}
}

// ---------------------------------------------------------------- XHTTP downloads another way

// Route is the way an XHTTP config fetches its downloads when they do not take
// the way of its uploads: the same inbound (so the same session) reached at
// another address, such as straight to the server while uploads go through
// the CDN. Xray apps read it from the link's "extra" (downloadSettings).
type Route struct {
	In  *store.Inbound
	Env Env
}

// Split modes of XHTTP through the CDN (Settings > Subscription).
const (
	SplitOff  = ""
	SplitDown = "down" // uploads through the CDN, downloads straight to the server
	SplitUp   = "up"   // uploads straight to the server, downloads through the CDN
)

// Splittable: XHTTP over TLS, unless it is set to stream-one, which sends
// both ways in one request.
func Splittable(p *Preset, in *store.Inbound) bool {
	return p.transport == "xhttp" && p.Security == "tls" && in.Str("mode") != "stream-one"
}

// downloadSettings is Xray's downloadSettings for a route.
func downloadSettings(p *Preset, r *Route) obj {
	in, env := r.In, r.Env
	x := obj{"path": in.Str("path")}
	if h := in.Str("host"); h != "" {
		x["host"] = h
	}
	d := obj{"address": LinkHost(in, env), "port": in.Port, "network": "xhttp", "xhttpSettings": x}
	if p.Security == "tls" {
		t := obj{"serverName": sni(in, env), "fingerprint": or(in.Str("fp"), "chrome"), "alpn": []string{"h2", "http/1.1"}}
		if env.SelfSigned && env.CertSHA256 != "" {
			t["pinnedPeerCertSha256"] = env.CertSHA256
		}
		if ech := echFor(p, env); ech != "" {
			t["echConfigList"] = ech
		}
		d["security"], d["tlsSettings"] = "tls", t
	} else {
		d["security"] = "none"
	}
	if env.Fragment != nil {
		d["finalmask"] = obj{"tcp": []obj{env.Fragment.mask()}}
	}
	return d
}

// xhttpExtra is the link's "extra" for a route.
func xhttpExtra(p *Preset, r *Route) string {
	b, _ := json.Marshal(obj{"downloadSettings": downloadSettings(p, r)})
	return string(b)
}

// ---------------------------------------------------------------- the Xray JSON subscription

// XrayExtras adds to an Xray outbound read from a link what only the Xray JSON
// subscription carries: the fragment for every TLS config over TCP (VMess
// included) and Mux.Cool. The config check tests the same outbound.
func XrayExtras(o map[string]any, p *Preset, env Env) {
	WithFragment(o, env.Fragment)
	WithMux(o, p, env.Mux)
}
