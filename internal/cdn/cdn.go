// Package cdn helps put the CDN protocols (WebSocket, HTTPUpgrade, gRPC and
// XHTTP over TLS) behind a CDN such as Cloudflare: the settings for the links
// users get, a check of every leg (DNS, the CDN, the server itself), and the
// Cloudflare setup done with the owner's own API token.
package cdn

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/pedramrj/dunvex/internal/store"
)

// Setting is the key the CDN settings are kept under.
const Setting = "cdn"

// MaxAddresses caps the clean addresses (each adds a config per protocol).
const MaxAddresses = 30

// Settings say how users reach the CDN protocols.
type Settings struct {
	// Enabled: the domain is proxied by the CDN, so the CDN protocols' links
	// go through it (the CDN's certificate, not this server's, is checked).
	Enabled bool `json:"enabled"`
	// Domain is the proxied name; "" means the panel's domain.
	Domain string `json:"domain"`
	// Addresses are clean CDN addresses: each gives one more config per protocol.
	Addresses []Address `json:"addresses"`
	// Direct keeps the config that goes straight to the server too.
	Direct bool `json:"direct"`
}

// Address is a clean CDN address (an IP or a name) with an optional label.
type Address struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

// Label is how configs through the address are named.
func (a Address) Label() string {
	if a.Name != "" {
		return a.Name
	}
	return a.Address
}

// Load reads the settings (zero value when unset).
func Load(st *store.Store) Settings {
	var s Settings
	if v := st.GetSetting(Setting, ""); v != "" {
		json.Unmarshal([]byte(v), &s)
	}
	return s
}

// Save cleans and stores the settings.
func Save(st *store.Store, s Settings) (Settings, error) {
	if err := s.Clean(); err != nil {
		return s, err
	}
	b, _ := json.Marshal(s)
	return s, st.SetSetting(Setting, string(b))
}

// DomainFor is the proxied name, given the panel's domain.
func (s Settings) DomainFor(panel string) string {
	if s.Domain != "" {
		return s.Domain
	}
	return strings.ToLower(strings.TrimSpace(panel))
}

// Clean normalizes the settings and reports the first invalid value.
func (s *Settings) Clean() error {
	s.Domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s.Domain)), ".")
	if s.Domain != "" && !IsHostname(s.Domain) {
		return fmt.Errorf("%q is not a domain name", s.Domain)
	}
	seen := map[string]bool{}
	out := []Address{}
	for _, a := range s.Addresses {
		a.Address = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(a.Address)), ".")
		a.Address = strings.TrimSuffix(strings.TrimPrefix(a.Address, "["), "]")
		a.Name = strings.Join(strings.Fields(a.Name), " ")
		if a.Address == "" {
			continue
		}
		if _, err := netip.ParseAddr(a.Address); err != nil && !IsHostname(a.Address) {
			return fmt.Errorf("%q is not an IP address or a domain name", a.Address)
		}
		if len([]rune(a.Name)) > 32 {
			return fmt.Errorf("the name %q is too long (32 characters at most)", a.Name)
		}
		if seen[a.Address] {
			continue
		}
		seen[a.Address] = true
		out = append(out, a)
	}
	if len(out) > MaxAddresses {
		return fmt.Errorf("at most %d clean addresses", MaxAddresses)
	}
	s.Addresses = out
	return nil
}

// IsHostname reports whether v is a DNS name with at least two labels.
func IsHostname(v string) bool {
	if len(v) > 253 || !strings.Contains(v, ".") {
		return false
	}
	for _, l := range strings.Split(v, ".") {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	// a name is not all digits (that would be a mistyped IP)
	last := v[strings.LastIndexByte(v, '.')+1:]
	return strings.Trim(last, "0123456789") != ""
}

// ---------------------------------------------------------------- Cloudflare

// The ports Cloudflare proxies (developers.cloudflare.com/fundamentals/reference/network-ports).
var (
	HTTPSPorts = []int{443, 2053, 2083, 2087, 2096, 8443}
	HTTPPorts  = []int{80, 8080, 8880, 2052, 2082, 2086, 2095}
)

// ProxiedPort reports whether Cloudflare proxies port, over TLS or plain HTTP.
func ProxiedPort(port int, tls bool) bool {
	list := HTTPPorts
	if tls {
		list = HTTPSPorts
	}
	for _, p := range list {
		if p == port {
			return true
		}
	}
	return false
}

// Cloudflare's published address ranges (www.cloudflare.com/ips).
var cloudflareRanges = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
		"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
		"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
		"2a06:98c0::/29", "2c0f:f248::/32",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// IsCloudflare reports whether ip is one of Cloudflare's addresses.
func IsCloudflare(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	for _, p := range cloudflareRanges {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
