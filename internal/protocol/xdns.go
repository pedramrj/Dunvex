package protocol

import (
	"encoding/json"
	"net"
	"strings"

	"github.com/pedramrj/dunvex/internal/store"
)

// XDNS hides mKCP inside DNS queries (Xray finalmask "xdns", like DNSTT).
// The client sends ordinary DNS queries for names under the tunnel domain to a
// public resolver, which forwards them to this server because the domain's NS
// record points here. In "direct" mode the client asks this server itself.

// XDNSDomain is the inbound's tunnel domain, without a trailing dot.
func XDNSDomain(in *store.Inbound) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Str("xdnsDomain")), "."))
}

// xdnsTarget is where the client sends its DNS queries.
func xdnsTarget(in *store.Inbound, env Env) (string, int) {
	r := strings.TrimSpace(in.Str("resolver"))
	if r == "" || r == "direct" {
		return LinkHost(in, env), in.Port
	}
	if h, p, err := net.SplitHostPort(r); err == nil {
		port := 53
		if n, err := net.LookupPort("udp", p); err == nil {
			port = n
		}
		return h, port
	}
	return r, 53
}

// xdnsMask is the client's finalmask, URL-ready JSON for the link's "fm".
func xdnsMask(in *store.Inbound, env Env) string {
	h, port := xdnsTarget(in, env)
	m := obj{"udp": []obj{{"type": "xdns", "settings": obj{
		"domains":   []obj{{"names": []string{XDNSDomain(in)}, "edns0": 1232}},
		"resolvers": []obj{{"addrs": []string{"udp://" + hostPort(h, port)}}},
	}}}}
	b, _ := json.Marshal(m)
	return string(b)
}
