package protocol

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/store"
)

// xICMP hides mKCP inside ping packets (Xray finalmask "xicmp"): the app sends
// ICMP echo requests to the server, and Xray answers with echo replies that
// carry the data. It needs no port and no domain, only that pings reach the
// server. The server reads every ping with a raw socket (root), so one xICMP
// serves a whole server.

// IsXICMP reports whether p is the ping tunnel.
func IsXICMP(p *Preset) bool { return p != nil && p.transport == "xicmp" }

// HoldsNoPort reports whether a protocol listens on no port at all (xICMP):
// its port only appears in links, so it never clashes with another one.
func HoldsNoPort(p *Preset) bool { return IsXICMP(p) }

// lookupIPs resolves a host name for the links (the apps need an address).
var lookupIPs = func(host string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}
	var v4, v6 []string
	for _, a := range addrs {
		if a.IP.To4() != nil {
			v4 = append(v4, a.IP.String())
		} else {
			v6 = append(v6, a.IP.String())
		}
	}
	if len(v4) > 0 {
		return v4 // most networks that pass pings pass IPv4
	}
	return v6
}

// xicmpMask is the app's finalmask, URL-ready JSON for the link's "fm".
// "dgram" uses the system's unprivileged ping sockets (phones, macOS, Linux);
// without it the app opens a raw ICMP socket, which Windows apps need (Windows
// has no ping sockets, and lets ordinary programs send raw pings).
// A server known by name is pinned to its addresses: pings go to an address.
func xicmpMask(in *store.Inbound, env Env) string {
	s := obj{}
	if in.Bool("icmpDgram") {
		s["dgram"] = true
	}
	if h := LinkHost(in, env); net.ParseIP(strings.Trim(h, "[]")) == nil {
		if ips := lookupIPs(h); len(ips) > 0 {
			s["ips"] = ips
		}
	}
	b, _ := json.Marshal(obj{"udp": []obj{{"type": "xicmp", "settings": s}}})
	return string(b)
}
