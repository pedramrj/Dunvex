package protocol

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Fragment splits the start of a connection (by default the TLS ClientHello,
// which names the site) into small pieces with short pauses, so filters that
// read the server name from one packet miss it. It is the client's doing:
// Xray apps read it from the link ("fm", Xray's finalmask) and from the Xray
// JSON subscription; the server needs nothing. Apps whose core has no TCP
// finalmask (Xray before 2026-03) ignore it and connect as before.
type Fragment struct {
	Packets string // "tlshello", or which packets ("1-3")
	Length  string // bytes per piece ("100-200")
	Delay   string // milliseconds between pieces ("10-20")
}

// Fragment defaults: the ClientHello in pieces of 100-200 bytes, 10-20 ms apart.
const (
	FragmentPackets = "tlshello"
	FragmentLength  = "100-200"
	FragmentDelay   = "10-20"
)

var rangeRe = regexp.MustCompile(`^\d{1,5}(-\d{1,5})?$`)

// checkRange reads "a" or "a-b" with lo <= a <= b <= hi.
func checkRange(what, v string, lo, hi int) error {
	if !rangeRe.MatchString(v) {
		return fmt.Errorf("%s must be a number or a range like 10-20", what)
	}
	a, b, found := strings.Cut(v, "-")
	from, _ := strconv.Atoi(a)
	to := from
	if found {
		to, _ = strconv.Atoi(b)
	}
	if from < lo || to > hi || from > to {
		return fmt.Errorf("%s must be between %d and %d (low-high)", what, lo, hi)
	}
	return nil
}

// Check says what is wrong with the settings, if anything.
func (f Fragment) Check() error {
	if p := strings.ToLower(f.Packets); p != "tlshello" {
		if err := checkRange("packets", p, 1, 100); err != nil {
			return fmt.Errorf(`packets must be "tlshello" or a range like 1-3`)
		}
	}
	if err := checkRange("length", f.Length, 1, 4000); err != nil {
		return err
	}
	return checkRange("delay", f.Delay, 0, 1000)
}

// mask is the fragment entry of an Xray finalmask.
func (f *Fragment) mask() obj {
	return obj{"type": "fragment", "settings": obj{"packets": strings.ToLower(f.Packets), "length": f.Length, "delay": f.Delay}}
}

// fragmentMask is the link's "fm" with the fragment in it.
func fragmentMask(f *Fragment) string {
	b, _ := json.Marshal(obj{"tcp": []obj{f.mask()}})
	return string(b)
}

// Fragmentable: a TCP transport carrying TLS or Reality, where the
// ClientHello gives the server name away. UDP protocols, QUIC and plain
// connections are left alone.
func Fragmentable(p *Preset) bool {
	if p.Network != "tcp" || (p.Security != "tls" && p.Security != "reality") {
		return false
	}
	switch p.transport {
	case "", "raw", "xhttp", "grpc", "ws", "httpupgrade":
		return p.proto == "vless" || p.proto == "vmess" || p.proto == "trojan"
	}
	return false
}

// WithFragment adds f to an Xray outbound whose transport is TCP with TLS or
// Reality, unless it already has a fragment (from the link).
func WithFragment(o map[string]any, f *Fragment) {
	if f == nil {
		return
	}
	ss, _ := o["streamSettings"].(map[string]any)
	if ss == nil {
		return
	}
	switch ss["network"] {
	case "raw", "tcp", "xhttp", "grpc", "ws", "httpupgrade":
	default:
		return
	}
	if sec := ss["security"]; sec != "tls" && sec != "reality" {
		return
	}
	fm, _ := ss["finalmask"].(map[string]any)
	if fm == nil {
		fm = map[string]any{}
	}
	tcp, _ := fm["tcp"].([]any)
	for _, m := range tcp {
		if mm, ok := m.(map[string]any); ok && mm["type"] == "fragment" {
			return
		}
	}
	fm["tcp"] = append(tcp, any(map[string]any(f.mask())))
	ss["finalmask"] = fm
}
