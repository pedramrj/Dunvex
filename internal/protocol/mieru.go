package protocol

import (
	"encoding/json"
	"math/rand/v2"
	"net"
	"net/url"
	"strconv"

	"github.com/pedramrj/dunvex/internal/store"
)

// Mieru: TCP or UDP with no fixed pattern (no handshake to fingerprint, random
// padding, a key derived from the user, password and time). Its server, mita,
// serves every user on every port it holds, so a server runs one Mieru inbound,
// either TCP ("mieru") or UDP ("mieru-udp").

// MieruMux is the multiplexing level apps use (client side only).
var MieruMux = []string{"MULTIPLEXING_LOW", "MULTIPLEXING_MIDDLE", "MULTIPLEXING_HIGH", "MULTIPLEXING_OFF"}

// IsMieru reports whether a preset is one of the two Mieru presets.
func IsMieru(p *Preset) bool { return p != nil && p.Engine == "mita" }

func mieruTransport(p *Preset) string {
	if p.Network == "udp" {
		return "UDP"
	}
	return "TCP"
}

func mieruDefaults(params map[string]any) {
	if n := num(params["mtu"]); n < 1280 || n > 1500 {
		params["mtu"] = 1400
	} else {
		params["mtu"] = n
	}
	ok := false
	for _, m := range MieruMux {
		ok = ok || str(params["multiplexing"]) == m
	}
	if !ok {
		params["multiplexing"] = "MULTIPLEXING_LOW"
	}
	// the server's own traffic pattern comes from this seed; apps keep theirs
	if num(params["tpSeed"]) == 0 {
		params["tpSeed"] = 1 + rand.IntN(1<<30)
	}
}

// MieruServer is mita's configuration for an inbound and its users; nil when
// nobody may use it (mita refuses to serve without users, so it stays off).
// egress, when set, is a local SOCKS5 address all traffic leaves through
// (Xray's routing inbound), so the server's routing rules apply to Mieru too.
func MieruServer(p *Preset, in *store.Inbound, users []*store.User, listen, egress string) ([]byte, error) {
	type user struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	us := []user{}
	for _, u := range users {
		if u.Name == "" || u.Password == "" || len(u.Name) > 64 {
			continue // mita refuses empty names and names over 64 bytes
		}
		us = append(us, user{u.Name, u.Password})
	}
	if len(us) == 0 {
		return nil, nil
	}
	cfg := map[string]any{
		"portBindings":   []map[string]any{{"port": in.Port, "protocol": mieruTransport(p)}},
		"users":          us,
		"loggingLevel":   "WARN", // errors and warnings; INFO notes every control call
		"mtu":            max(1280, min(1500, num(in.Params["mtu"]))),
		"dns":            map[string]any{"dualStack": "PREFER_IPv4"},
		"trafficPattern": map[string]any{"seed": num(in.Params["tpSeed"])},
		"advancedSettings": map[string]any{
			"metricsLoggingInterval": "24h", // Dunvex reads the counters itself
		},
	}
	if listen != "" {
		cfg["listenIPAddress"] = listen
	}
	if host, port, err := net.SplitHostPort(egress); err == nil {
		n, _ := strconv.Atoi(port)
		cfg["egress"] = map[string]any{
			"proxies": []map[string]any{{"name": "dunvex-router", "protocol": "SOCKS5_PROXY_PROTOCOL", "host": host, "port": n}},
			"rules":   []map[string]any{{"ipRanges": []string{"*"}, "domainNames": []string{"*"}, "action": "PROXY", "proxyNames": []string{"dunvex-router"}}},
		}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// mieruLink is the "simple" share link that Karing, Exclave, NyameBox and
// the mieru client import.
func mieruLink(p *Preset, in *store.Inbound, u *store.User, env Env, remark string) string {
	q := url.Values{}
	q.Set("profile", remark)
	q.Set("port", strconv.Itoa(in.Port))
	q.Set("protocol", mieruTransport(p))
	q.Set("mtu", strconv.Itoa(max(1280, min(1500, num(in.Params["mtu"])))))
	q.Set("multiplexing", or(in.Str("multiplexing"), "MULTIPLEXING_LOW"))
	lu := url.URL{Scheme: "mierus", User: url.UserPassword(u.Name, u.Password), Host: bracket(LinkHost(in, env)), RawQuery: q.Encode()}
	return lu.String() + "#" + url.PathEscape(remark)
}

// bracket writes an IPv6 address the way URLs need it.
func bracket(h string) string {
	if ip := net.ParseIP(h); ip != nil && ip.To4() == nil {
		return "[" + h + "]"
	}
	return h
}

func mieruClash(p *Preset, in *store.Inbound, u *store.User, c obj) {
	c["type"], c["transport"] = "mieru", mieruTransport(p)
	c["username"], c["password"] = u.Name, u.Password
	c["multiplexing"] = or(in.Str("multiplexing"), "MULTIPLEXING_LOW")
}
