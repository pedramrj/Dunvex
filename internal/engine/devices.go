package engine

import (
	"net"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Device is one address a user connected from.
type Device struct {
	IP       string  `json:"ip"`
	LastSeen int64   `json:"lastSeen"`
	Via      []int64 `json:"via"` // servers it reached in the window: 0 is this server, others are node ids
}

// devices remembers which addresses each user connects from. Xray reports
// them through its stats API; sing-box through its connection log.
type devices struct {
	mu    sync.Mutex
	seen  map[string]map[string]int64           // user → ip → last seen
	via   map[string]map[string]map[int64]int64 // user → ip → server (0 = this one) → last seen
	conn  map[string]string                     // sing-box connection id → client ip
	order []string

	// resolve turns a loopback address the edge router dialed from into the
	// real client IP (sing-box sees only the router)
	resolve func(addr string) string
}

func newDevices() *devices {
	return &devices{seen: map[string]map[string]int64{}, via: map[string]map[string]map[int64]int64{}, conn: map[string]string{}}
}

func (d *devices) see(user, ip string, at int64) { d.seeVia(user, ip, at, 0) }

// seeVia records an address with the server that saw it (0 is this server).
func (d *devices) seeVia(user, ip string, at, server int64) {
	if user == "" || user == HealthName || ip == "" || ip == "127.0.0.1" || ip == "::1" || tunnelID(ip) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.seen[user]
	if m == nil {
		m = map[string]int64{}
		d.seen[user] = m
	}
	if at > m[ip] {
		m[ip] = at
	}
	vu := d.via[user]
	if vu == nil {
		vu = map[string]map[int64]int64{}
		d.via[user] = vu
	}
	vs := vu[ip]
	if vs == nil {
		vs = map[int64]int64{}
		vu[ip] = vs
	}
	if at > vs[server] {
		vs[server] = at
	}
}

// servers lists the servers that saw user at ip since `since`, this one first.
// The caller holds d.mu.
func (d *devices) servers(user, ip string, since int64) []int64 {
	out := []int64{}
	for s, t := range d.via[user][ip] {
		if t >= since {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// tunnelID reports a stand-in address of a DNS or ping tunnel client (XDNS,
// xICMP): Xray names each tunnel session fd00::/64 plus its random client ID,
// a new one per connection, so it is no device and must not count as one.
func tunnelID(ip string) bool {
	a := net.ParseIP(ip)
	if a == nil || a.To4() != nil {
		return false
	}
	if a[0] != 0xfd {
		return false
	}
	for _, b := range a[1:8] {
		if b != 0 {
			return false
		}
	}
	return true
}

// list returns the user's addresses seen since `since`, newest first.
func (d *devices) list(user string, since int64) []Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []Device{}
	for ip, t := range d.seen[user] {
		if t >= since {
			out = append(out, Device{IP: ip, LastSeen: t, Via: d.servers(user, ip, since)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })
	return out
}

func (d *devices) counts(since int64) map[string]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]int{}
	for u, m := range d.seen {
		for _, t := range m {
			if t >= since {
				out[u]++
			}
		}
	}
	return out
}

func (d *devices) prune(before int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for u, m := range d.seen {
		for ip, t := range m {
			if t < before {
				delete(m, ip)
				delete(d.via[u], ip)
			}
		}
		for ip, vs := range d.via[u] {
			for s, t := range vs {
				if t < before {
					delete(vs, s)
				}
			}
			if len(vs) == 0 {
				delete(d.via[u], ip)
			}
		}
		if len(m) == 0 {
			delete(d.seen, u)
			delete(d.via, u)
		}
	}
}

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	// [3271034497 0ms] inbound/vless[in-3]: [alice] inbound connection to example.com:443
	sbConnRe = regexp.MustCompile(`\[(\d+) [^\]]*\] (inbound|outbound)/[\w-]+\[[^\]]*\]: (?:\[([^\]]+)\] )?(?:inbound|outbound) (?:packet )?connection (from|to) (\S+)`)
)

// singboxLine reads one sing-box log line. It returns true for per-connection
// lines, which are kept out of the visible log.
func (d *devices) singboxLine(line string) bool {
	m := sbConnRe.FindStringSubmatch(line)
	if m == nil {
		return false
	}
	if m[2] != "inbound" {
		return true
	}
	id, user, dir, addr := m[1], m[3], m[4], m[5]
	d.mu.Lock()
	if dir == "from" {
		if host, _, err := net.SplitHostPort(addr); err == nil {
			if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() && d.resolve != nil {
				if real := d.resolve(addr); real != "" {
					host = real
				}
			}
			if _, ok := d.conn[id]; !ok {
				d.order = append(d.order, id)
			}
			d.conn[id] = host
			if len(d.order) > 4096 {
				delete(d.conn, d.order[0])
				d.order = d.order[1:]
			}
		}
		d.mu.Unlock()
		return true
	}
	ip := d.conn[id]
	d.mu.Unlock()
	if user != "" && ip != "" {
		d.see(user, ip, time.Now().Unix())
	}
	return true
}

// Seen is one address a user connected from.
type Seen struct {
	User     string
	IP       string
	LastSeen int64
	Via      []int64 `json:",omitempty"` // filled by the main server; nodes send their own addresses without it
}

// all returns every user's addresses seen since `since`.
func (d *devices) all(since int64) []Seen {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Seen
	for u, m := range d.seen {
		for ip, t := range m {
			if t >= since {
				out = append(out, Seen{User: u, IP: ip, LastSeen: t, Via: d.servers(u, ip, since)})
			}
		}
	}
	return out
}
