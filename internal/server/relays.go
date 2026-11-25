package server

import (
	"encoding/base64"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/pedramrj/dunvex/internal/bridge"
	"github.com/pedramrj/dunvex/internal/dnstt"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/relay"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// relayable reports whether users can reach an inbound through a relay: the
// app must dial an address and port, which the relay then stands in for. A
// CDN address, NaiveProxy (dialled by its domain), the DNS tunnels and the ping
// tunnel (no port to forward) cannot.
func relayable(p *protocol.Preset, in *store.Inbound) bool {
	if p == nil || !in.Enabled {
		return false
	}
	switch p.ID {
	case "naive", "vless-xdns", "dnstt", "slipstream", "vless-xicmp":
		return false
	}
	return strings.TrimSpace(in.Str("address")) == ""
}

// relayTargetHost is the address relay traffic goes to: the server itself,
// never a CDN name in front of it.
func (s *Server) relayTargetHost(target int64, nodes map[int64]*store.Node) string {
	if target == 0 {
		// an IP set in Settings is this server's address; a name there may be a CDN
		if ip := net.ParseIP(strings.TrimSpace(s.St.GetSetting("host", ""))); ip != nil {
			return ip.String()
		}
		if s.Sys != nil {
			if ip := s.Sys.PublicIP(); ip != "" {
				return ip
			}
		}
		return s.Env().Host
	}
	if n := nodes[target]; n != nil && !n.IsRelay() {
		return n.Address
	}
	return ""
}

// relayForwards are the ports relay n forwards: every protocol of its target,
// each on its own port.
func (s *Server) relayForwards(n *store.Node) []relay.Forward {
	nodes := s.nodeMap()
	host := s.relayTargetHost(n.RelayTarget, nodes)
	if host == "" {
		return nil
	}
	inbounds, _ := s.St.Inbounds()
	byPort := map[int]*relay.Forward{}
	names := map[int][]string{}
	for _, in := range inbounds {
		p := protocol.Get(in.Preset)
		if in.NodeID != n.RelayTarget || !relayable(p, in) {
			continue
		}
		listen := in.Port + n.RelayShift
		if listen < 1 || listen > 65535 {
			continue
		}
		network := protocol.Networks(p)
		f := byPort[in.Port]
		if f == nil {
			f = &relay.Forward{Listen: listen, Network: network, Target: net.JoinHostPort(host, strconv.Itoa(in.Port)), Reverse: n.Tunneled()}
			byPort[in.Port] = f
		} else if f.Network != network {
			f.Network = "tcp+udp" // e.g. Reality on TCP 443 and Hysteria2 on UDP 443
		}
		name := in.Remark
		if name == "" {
			name = p.Name
		}
		names[in.Port] = append(names[in.Port], name)
	}
	out := make([]relay.Forward, 0, len(byPort))
	for port, f := range byPort {
		f.Name = strings.Join(names[port], " + ")
		if r := []rune(f.Name); len(r) > 80 {
			f.Name = string(r[:79]) + "…"
		}
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Listen < out[j].Listen })
	return out
}

// tunnelLinks are the reverse relays server target (0 = this one) keeps
// links to, each allowed to reach only the ports it relays.
func (s *Server) tunnelLinks(target int64) []relay.TunnelClient {
	list, _ := s.St.Nodes()
	var out []relay.TunnelClient
	for _, n := range list {
		if !n.Reverse() || !n.Enabled || !n.Paired() || n.RelayTarget != target || n.RelayPort == 0 || n.RelaySecret == "" {
			continue
		}
		c := relay.TunnelClient{Name: n.Name, Addr: net.JoinHostPort(n.Address, strconv.Itoa(n.RelayPort)), Fingerprint: n.Fingerprint, Secret: n.RelaySecret}
		for _, f := range s.relayForwards(n) {
			_, p, _ := net.SplitHostPort(f.Target)
			port, _ := strconv.Atoi(p)
			c.Ports = append(c.Ports, port)
		}
		out = append(out, c)
	}
	return out
}

// applyTunnels brings this server's own links to its reverse relays and its
// Bridge sessions to its bridge relays in line.
func (s *Server) applyTunnels() {
	if s.Tunnels != nil {
		s.Tunnels.Apply(s.tunnelLinks(0))
		s.Tunnels.ApplyBridges(s.bridgeLinks(0))
	}
}

// ---------------------------------------------------------------- Bridge

// bridgeServer is the listener config a bridge relay runs: its carriers, its
// pre-shared key and its own keys.
func (s *Server) bridgeServer(n *store.Node) *relay.BridgeServerConfig {
	if !n.IsBridge() {
		return nil
	}
	cfg := &relay.BridgeServerConfig{PSK: n.RelaySecret, StaticPriv: n.BridgePriv, DNSPriv: n.BridgeDNSPriv,
		Carriers: bridgeSpecs(n, false)}
	return cfg
}

// bridgeLinks are the Bridge sessions server `target` (0 = this one) keeps to
// the bridge relays in front of it, each allowed to reach only its ports.
func (s *Server) bridgeLinks(target int64) []relay.BridgeClientConfig {
	list, _ := s.St.Nodes()
	var out []relay.BridgeClientConfig
	for _, n := range list {
		if !n.IsBridge() || !n.Enabled || !n.Paired() || n.RelayTarget != target || n.BridgePriv == "" {
			continue
		}
		pub, dnsPub, ok := bridgePub(n)
		if !ok {
			continue
		}
		c := relay.BridgeClientConfig{Name: n.Name, RelayHost: n.Address, PSK: n.RelaySecret, StaticPub: pub, DNSPub: dnsPub,
			Carriers: bridgeSpecs(n, true)}
		for _, f := range s.relayForwards(n) {
			_, p, _ := net.SplitHostPort(f.Target)
			port, _ := strconv.Atoi(p)
			c.Ports = append(c.Ports, port)
		}
		out = append(out, c)
	}
	return out
}

// bridgeSpecs turns a relay's carrier preferences into the wire specs. For the
// client side (target) the tls-cdn carrier is pinned to the relay's agent
// certificate when it has no CDN-fronted domain of its own.
func bridgeSpecs(n *store.Node, client bool) []relay.BridgeCarrierSpec {
	bs := n.Bridge()
	var specs []relay.BridgeCarrierSpec
	for _, c := range bs.Carriers {
		if !c.On {
			continue
		}
		spec := relay.BridgeCarrierSpec{Kind: c.Kind, Priority: c.Priority, CapMBps: c.CapMBps, Port: c.Port, ServerRequests: c.ServerRequests}
		switch c.Kind {
		case "tls-cdn":
			spec.Path = "/dvxb"
			if bs.TLSDomain != "" {
				spec.ServerName = bs.TLSDomain // a real CDN in front: verify its certificate
			} else if client {
				spec.PinSHA = n.CertSHA256 // no CDN: dial the relay and pin its agent certificate
			}
		case "dns":
			spec.Zone = bs.DNSZone
		}
		specs = append(specs, spec)
	}
	return specs
}

// bridgePub derives a bridge relay's public keys from its stored private ones.
func bridgePub(n *store.Node) (staticPub, dnsPub string, ok bool) {
	raw, err := base64.StdEncoding.DecodeString(n.BridgePriv)
	if err != nil || len(raw) != 32 {
		return "", "", false
	}
	var priv [32]byte
	copy(priv[:], raw)
	pub, err := bridge.PubFromPriv(priv)
	if err != nil {
		return "", "", false
	}
	return base64.StdEncoding.EncodeToString(pub[:]), dnstt.PublicKey(n.BridgeDNSPriv), true
}

// checkBridge validates a bridge relay's carrier ports: each in range, none on
// the agent's own port, and no two carriers on the same port.
func checkBridge(n *store.Node) string {
	if !n.IsBridge() {
		return ""
	}
	bs := n.Bridge()
	any := false
	seen := map[int]string{}
	for _, c := range bs.Carriers {
		if !c.On {
			continue
		}
		any = true
		if c.Kind == "icmp" { // raw sockets, no port
			continue
		}
		if c.Port < 1 || c.Port > 65535 {
			return fmt.Sprintf("the %s carrier needs a port between 1 and 65535", c.Kind)
		}
		if c.Port == n.AgentPort {
			return fmt.Sprintf("port %d is the relay's Dunvex agent; give the %s carrier another port", c.Port, c.Kind)
		}
		if other, ok := seen[c.Port]; ok {
			return fmt.Sprintf("the %s and %s carriers both use port %d", other, c.Kind, c.Port)
		}
		seen[c.Port] = c.Kind
	}
	if !any {
		return "turn on at least one Bridge carrier"
	}
	if bs.DNSZone == "" {
		for _, c := range bs.Carriers {
			if c.Kind == "dns" && c.On {
				return "the DNS carrier needs a tunnel zone (a domain whose NS points at the relay)"
			}
		}
	}
	return ""
}

// ensureBridgeKeys gives a bridge relay its keys the first time it needs them.
func ensureBridgeKeys(n *store.Node) error {
	if !n.IsBridge() {
		return nil
	}
	if n.RelaySecret == "" {
		n.RelaySecret = util.RandomString(32)
	}
	if n.BridgePriv == "" {
		kp, err := bridge.GenerateKey()
		if err != nil {
			return err
		}
		n.BridgePriv = base64.StdEncoding.EncodeToString(kp.Priv[:])
	}
	if n.BridgeDNSPriv == "" {
		priv, _, err := dnstt.NewKey()
		if err != nil {
			return err
		}
		n.BridgeDNSPriv = priv
	}
	return nil
}

// relaysFor lists the relays users can reach an inbound through.
func relaysFor(in *store.Inbound, p *protocol.Preset, nodes map[int64]*store.Node) []*store.Node {
	if !relayable(p, in) {
		return nil
	}
	var out []*store.Node
	for _, n := range nodes {
		if p := in.Port + n.RelayShift; n.IsRelay() && n.Enabled && n.RelayTarget == in.NodeID && p >= 1 && p <= 65535 {
			if in.NodeID != 0 && nodes[in.NodeID] == nil {
				continue
			}
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// onlyViaRelay reports whether users should get an inbound only through relays.
func onlyViaRelay(relays []*store.Node) bool {
	for _, r := range relays {
		if r.RelayOnly {
			return true
		}
	}
	return false
}

// viaRelay is a copy of an inbound as its users reach it through relay r: the
// same protocol and keys, at the relay's address and port.
func viaRelay(in *store.Inbound, r *store.Node) *store.Inbound {
	c := *in
	c.Port = in.Port + r.RelayShift
	c.Params = make(map[string]any, len(in.Params)+1)
	for k, v := range in.Params {
		c.Params[k] = v
	}
	c.Params["address"] = r.Address
	return &c
}

// relayRemark names a config reached through a relay.
func relayRemark(in *store.Inbound, p *protocol.Preset, title string, r *store.Node) string {
	name := in.Remark
	if name == "" {
		name = p.Name
	}
	if title != "" {
		return title + " · " + r.Name + " · " + name
	}
	return r.Name + " · " + name
}

// checkRelayTarget explains why a node cannot relay to target, or "".
func (s *Server) checkRelayTarget(self, target int64) string {
	if target == 0 {
		return ""
	}
	if target == self {
		return "a relay cannot forward to itself"
	}
	t, err := s.St.NodeByID(target)
	if err != nil {
		return "the relay's target server does not exist"
	}
	if t.IsRelay() {
		return fmt.Sprintf("%s is a relay itself; choose a server that runs protocols", t.Name)
	}
	return ""
}
