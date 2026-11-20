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
	IP       string `json:"ip"`
	LastSeen int64  `json:"lastSeen"`
}

// devices remembers which addresses each user connects from. Xray reports
// them through its stats API; sing-box through its connection log.
type devices struct {
	mu    sync.Mutex
	seen  map[string]map[string]int64 // user → ip → last seen
	conn  map[string]string           // sing-box connection id → client ip
	order []string
}

func newDevices() *devices {
	return &devices{seen: map[string]map[string]int64{}, conn: map[string]string{}}
}

func (d *devices) see(user, ip string, at int64) {
	if user == "" || ip == "" || ip == "127.0.0.1" || ip == "::1" {
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
}

// list returns the user's addresses seen since `since`, newest first.
func (d *devices) list(user string, since int64) []Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []Device{}
	for ip, t := range d.seen[user] {
		if t >= since {
			out = append(out, Device{IP: ip, LastSeen: t})
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
			}
		}
		if len(m) == 0 {
			delete(d.seen, u)
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
