package engine

import (
	"fmt"
	"sync"

	"golang.org/x/net/icmp"
)

// xICMP reads pings with raw ICMP sockets, IPv4 and IPv6 both. When Xray
// cannot open them (Dunvex not running as root, or IPv6 switched off in the
// kernel) it exits as a whole, so the inbound is held back with the reason
// instead of taking every Xray protocol down.

var xicmpCheck struct {
	mu sync.Mutex
	ok bool
}

// XICMPReady reports why xICMP cannot run on this server, or nil.
func XICMPReady() error { return xicmpReady() }

// xicmpReady reports why raw ICMP sockets cannot be opened here, or nil.
// Success is remembered; a failure is tried again at the next apply.
var xicmpReady = func() error {
	xicmpCheck.mu.Lock()
	defer xicmpCheck.mu.Unlock()
	if xicmpCheck.ok {
		return nil
	}
	c4, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return fmt.Errorf("xICMP needs raw ICMP sockets, which need root (or CAP_NET_RAW): %v", err)
	}
	c4.Close()
	c6, err := icmp.ListenPacket("ip6:ipv6-icmp", "::")
	if err != nil {
		return fmt.Errorf("xICMP needs IPv4 and IPv6 raw ICMP sockets; IPv6 cannot be used here: %v", err)
	}
	c6.Close()
	xicmpCheck.ok = true
	return nil
}
