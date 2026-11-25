//go:build !linux

package bridge

// On any OS but Linux the raw-socket ICMP carrier is unavailable; the panel
// shows it as down with the reason. The Iran relay and the abroad target both
// run Linux in production.

// NewICMPClient is unsupported off Linux.
func NewICMPClient(_ string, _ bool) (Carrier, error) { return unsupportedCarrier{name: "icmp"}, nil }

// NewICMPServer is unsupported off Linux.
func NewICMPServer(_ bool) Carrier { return unsupportedCarrier{name: "icmp"} }
