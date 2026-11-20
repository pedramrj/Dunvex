package server

import (
	"fmt"
	"strings"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// ttHostnames are the sites TrustTunnel can look like when it shares the
// panel's port (each name there must be different).
var ttHostnames = []string{protocol.DefaultReality, "www.apple.com", "www.microsoft.com", "dl.google.com", "www.samsung.com", "www.cloudflare.com"}

// panelTLSName is the name the panel answers HTTPS for on its port ("" when it
// speaks plain HTTP, so no TLS name is its own).
func (s *Server) panelTLSName() string {
	if s.St.GetSetting("panel_tls", "false") != "true" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(s.St.GetSetting("domain", "")))
}

// sharedNames are the TLS names already taken on the panel's port.
func (s *Server) sharedNames(except int64) map[string]bool {
	taken := map[string]bool{}
	if n := s.panelTLSName(); n != "" {
		taken[n] = true
	}
	port := s.panelPort()
	for _, in := range s.inboundsOn(0) {
		if in.ID != except && in.Port == port && protocol.SNIRouted(protocol.Get(in.Preset)) {
			taken[protocol.SharedSNI(in)] = true
		}
	}
	return taken
}

// ttHostFor picks a free hostname for a new TrustTunnel on the panel's port
// when the owner gave none and the default one is taken there.
func (s *Server) ttHostFor(ib *store.Inbound, given bool) {
	if given || ib.NodeID != 0 || ib.Port != s.panelPort() {
		return
	}
	taken := s.sharedNames(ib.ID)
	if !taken[protocol.SharedSNI(ib)] {
		return
	}
	for _, h := range ttHostnames {
		if !taken[h] {
			ib.Params["sni"] = h
			ib.Params = protocol.Defaults(protocol.Get(ib.Preset), ib.Params, s.domainOf(0)) // a certificate for the new name
			return
		}
	}
}

// sharedPortCheck explains what keeps an inbound from the panel's port, or "":
// TrustTunnel cannot take the name the panel answers HTTPS for, and uses
// HTTP/3 there only while no UDP protocol holds the port's UDP side.
func (s *Server) sharedPortCheck(ib *store.Inbound, p *protocol.Preset) string {
	if ib.NodeID != 0 || ib.Port != s.panelPort() || p == nil {
		return ""
	}
	list := s.inboundsOn(0)
	if protocol.IsTrustTunnel(p) {
		if n := s.panelTLSName(); n != "" && protocol.SharedSNI(ib) == n {
			return fmt.Sprintf("the panel answers HTTPS for %s on port %d; give TrustTunnel another hostname", n, ib.Port)
		}
		if ib.Str("protocol") == "http3" {
			if h := protocol.UDPHolder(list, 0, ib.Port, ib.ID); h != "" {
				return fmt.Sprintf("UDP %d is used by %s, so TrustTunnel on the panel's port can only use HTTP/2; choose HTTP/2", ib.Port, h)
			}
		}
		return ""
	}
	if p.Network == "udp" && ib.Enabled {
		for _, in := range list {
			if in.ID != ib.ID && in.Port == ib.Port && in.Enabled && protocol.IsTrustTunnel(protocol.Get(in.Preset)) && in.Str("protocol") == "http3" {
				return fmt.Sprintf("TrustTunnel on port %d uses HTTP/3 (QUIC on UDP %d); set it to HTTP/2 first", ib.Port, ib.Port)
			}
		}
	}
	return ""
}
