package protocol

import (
	"fmt"
	"strings"

	"github.com/pedramrj/dunvex/internal/store"
)

// The panel's TCP port (usually 443) is shared through the edge router: a TLS
// handshake that names a Reality inbound's SNI goes to that inbound, everything
// else reaches the panel. UDP never clashes with the panel, so UDP protocols
// (Hysteria2, TUIC, WireGuard, mKCP) may use the same port number as well.

// CanSharePanelPort reports whether a preset may use the panel's port.
func CanSharePanelPort(p *Preset) bool {
	return p != nil && (p.Network == "udp" || SNIRouted(p))
}

// SNIRouted reports whether the edge router can tell this protocol apart by
// the server name in its TLS hello: Reality, and the Telegram proxy's fake TLS.
func SNIRouted(p *Preset) bool {
	return p != nil && p.Network == "tcp" && (p.Security == "reality" || p.Security == "faketls")
}

// SharedSNI is the server name that routes a shared Reality inbound.
func SharedSNI(in *store.Inbound) string {
	return strings.ToLower(strings.TrimSpace(in.Str("sni")))
}

// PortConflict explains why an inbound of preset p (with Reality server name sni)
// cannot listen on port, or returns "". except skips the inbound being edited.
func PortConflict(list []*store.Inbound, panelPort, port int, except int64, p *Preset, sni string) string {
	if port < 1 || port > 65535 {
		return "port must be between 1 and 65535"
	}
	onPanel := port == panelPort
	if onPanel && !CanSharePanelPort(p) {
		return fmt.Sprintf("port %d is the panel's port: only Reality, the Telegram proxy and UDP protocols can share it", port)
	}
	sni = strings.ToLower(strings.TrimSpace(sni))
	if onPanel && p.Network == "tcp" && sni == "" {
		return "a protocol on the panel's port needs an SNI (server name)"
	}
	for _, in := range list {
		if in.ID == except || in.Port != port {
			continue
		}
		q := Get(in.Preset)
		if q == nil {
			return fmt.Sprintf("port %d is already used", port)
		}
		if p != nil && q.Network != p.Network {
			continue // TCP and UDP on one port number do not clash
		}
		if onPanel && SNIRouted(p) && SNIRouted(q) {
			if SharedSNI(in) == sni {
				return fmt.Sprintf("%s on port %d already uses the SNI %s; pick another SNI", q.Name, port, sni)
			}
			continue // the router tells them apart by SNI
		}
		return fmt.Sprintf("port %d is already used by %s", port, q.Name)
	}
	return ""
}

// PanelPortConflict explains why the panel cannot move to port, or returns "".
func PanelPortConflict(list []*store.Inbound, port int) string {
	if port < 1 || port > 65535 {
		return "port must be between 1 and 65535"
	}
	seen := map[string]string{}
	for _, in := range list {
		if in.Port != port {
			continue
		}
		q := Get(in.Preset)
		if !CanSharePanelPort(q) {
			name := in.Preset
			if q != nil {
				name = q.Name
			}
			return fmt.Sprintf("port %d is used by %s, which cannot share the panel's port; move it first", port, name)
		}
		if q.Network == "tcp" {
			sni := SharedSNI(in)
			if sni == "" {
				return fmt.Sprintf("%s on port %d has no SNI; set one first", q.Name, port)
			}
			if other, dup := seen[sni]; dup {
				return fmt.Sprintf("%s and %s on port %d use the same SNI %s; change one first", other, q.Name, port, sni)
			}
			seen[sni] = q.Name
		}
	}
	return ""
}

// UsesPort reports whether one of the inbounds listens on port.
func UsesPort(list []*store.Inbound, port int) bool {
	for _, in := range list {
		if in.Port == port {
			return true
		}
	}
	return false
}
