package protocol

import (
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/store"
)

func TestPortConflict(t *testing.T) {
	reality, xhttp, ws, hy := Get("vless-reality-universal"), Get("vless-reality-xhttp"), Get("vless-ws-tls"), Get("hysteria2")
	list := []*store.Inbound{
		{ID: 1, Preset: reality.ID, Port: 443, Params: map[string]any{"sni": "www.microsoft.com"}},
		{ID: 2, Preset: hy.ID, Port: 4444},
	}
	cases := []struct {
		name string
		port int
		p    *Preset
		sni  string
		want string // "" or a fragment of the reason
	}{
		{"second Reality, other SNI, on the panel port", 443, xhttp, "dl.google.com", ""},
		{"second Reality, same SNI", 443, xhttp, "WWW.Microsoft.com", "already uses the SNI"},
		{"TLS protocol on the panel port", 443, ws, "", "only Reality, TrustTunnel, the Telegram proxy and UDP"},
		{"UDP on the panel port", 443, hy, "", ""},
		{"Reality without SNI on the panel port", 443, xhttp, "", "needs an SNI"},
		{"UDP next to UDP", 4444, Get("tuic"), "", "already used by Hysteria2"},
		{"TCP next to UDP", 4444, ws, "", ""},
		{"out of range", 70000, ws, "", "between 1 and 65535"},
	}
	for _, c := range cases {
		got := PortConflict(list, 443, c.port, 0, c.p, c.sni)
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// editing the inbound itself is not a clash
	if got := PortConflict(list, 443, 443, 1, reality, "www.microsoft.com"); got != "" {
		t.Errorf("self: %q", got)
	}
}

// TrustTunnel shares the panel's port by its hostname (TCP) and leaves the
// port's UDP to a UDP protocol there; elsewhere it needs its port's UDP too.
func TestTrustTunnelOnThePanelPort(t *testing.T) {
	tt, hy, reality := Get("trusttunnel"), Get("hysteria2"), Get("vless-reality-universal")
	list := []*store.Inbound{
		{ID: 1, Preset: reality.ID, Port: 443, Enabled: true, Params: map[string]any{"sni": "www.speedtest.net"}},
		{ID: 2, Preset: hy.ID, Port: 443, Enabled: true},
		{ID: 3, Preset: hy.ID, Port: 8443, Enabled: true},
		{ID: 4, Preset: hy.ID, Port: 443, Enabled: true, NodeID: 7},
	}
	for _, c := range []struct {
		name, sni, want string
		port            int
	}{
		{"next to Reality and Hysteria2", "www.apple.com", "", 443},
		{"the Reality hostname", "WWW.SPEEDTEST.NET", "already uses the SNI", 443},
		{"another port held by UDP", "www.apple.com", "already used by Hysteria2", 8443},
	} {
		if got := PortConflict(list, 443, c.port, 0, tt, c.sni); c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: %q", c.name, got)
		}
	}
	withTT := append(list, &store.Inbound{ID: 5, Preset: tt.ID, Port: 443, Enabled: true, Params: map[string]any{"sni": "www.apple.com"}})
	if got := PortConflict([]*store.Inbound{withTT[0], withTT[4]}, 443, 443, 0, Get("tuic"), ""); got != "" {
		t.Errorf("UDP next to TrustTunnel on the panel's port: %q", got)
	}
	if got := PanelPortConflict(withTT, 443); got != "" {
		t.Errorf("panel port with TrustTunnel: %q", got)
	}
	if UDPHolder(list, 0, 443, 0) != hy.Name || UDPHolder(list, 0, 443, 2) != "" || UDPHolder(list, 7, 443, 0) != hy.Name || UDPHolder(list, 0, 2096, 0) != "" {
		t.Error("UDPHolder")
	}
}

func TestPanelPortConflict(t *testing.T) {
	list := []*store.Inbound{
		{ID: 1, Preset: "vless-reality-universal", Port: 443, Params: map[string]any{"sni": "a.com"}},
		{ID: 2, Preset: "hysteria2", Port: 443},
		{ID: 3, Preset: "vless-ws-tls", Port: 2087},
		{ID: 4, Preset: "vless-reality-xhttp", Port: 8443, Params: map[string]any{"sni": "a.com"}},
		{ID: 5, Preset: "trojan-reality", Port: 8443, Params: map[string]any{"sni": "A.com"}},
	}
	if got := PanelPortConflict(list, 443); got != "" {
		t.Errorf("443: %q", got)
	}
	if got := PanelPortConflict(list, 2087); !strings.Contains(got, "cannot share") {
		t.Errorf("2087: %q", got)
	}
	if got := PanelPortConflict(list, 8443); !strings.Contains(got, "same SNI") {
		t.Errorf("8443: %q", got)
	}
	if got := PanelPortConflict(list, 9999); got != "" {
		t.Errorf("free port: %q", got)
	}
}
