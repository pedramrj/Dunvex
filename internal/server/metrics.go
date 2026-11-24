package server

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/tlsmgr"
)

// prom writes the Prometheus text format: one family at a time, each with
// its HELP and TYPE lines.
type prom struct{ b strings.Builder }

type sample struct {
	labels []string // name, value, name, value…
	v      float64
}

func (p *prom) family(name, typ, help string, samples ...sample) {
	if len(samples) == 0 {
		return
	}
	fmt.Fprintf(&p.b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
	for _, s := range samples {
		p.b.WriteString(name)
		if len(s.labels) > 0 {
			p.b.WriteByte('{')
			for i := 0; i+1 < len(s.labels); i += 2 {
				if i > 0 {
					p.b.WriteByte(',')
				}
				p.b.WriteString(s.labels[i])
				p.b.WriteString(`="`)
				p.b.WriteString(escapeLabel(s.labels[i+1]))
				p.b.WriteByte('"')
			}
			p.b.WriteByte('}')
		}
		p.b.WriteByte(' ')
		p.b.WriteString(formatValue(s.v))
		p.b.WriteByte('\n')
	}
}

func escapeLabel(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

func formatValue(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func gauge(v float64, labels ...string) sample { return sample{labels, v} }

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// metrics serves the panel's state for Prometheus. Any admin's API key (a
// read-only one is enough) authorizes it; ?users=false leaves out the
// per-user series on large panels.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Unix()
	var p prom
	title := s.St.GetSetting("sub_title", "Dunvex")
	p.family("dunvex_info", "gauge", "The panel's version and name.", gauge(1, "version", s.Version, "panel", title))

	// users
	users, _ := s.St.Users()
	online := s.Mgr.OnlineUsers(60)
	count := map[string]float64{}
	for _, st := range []string{store.StatusActive, store.StatusPending, store.StatusLimited, store.StatusExpired, store.StatusDisabled, store.StatusPaused} {
		count[st] = 0
	}
	var traffic []sample
	perUser := r.URL.Query().Get("users") != "false"
	for _, u := range users {
		count[u.Status(now)]++
		if perUser {
			traffic = append(traffic, gauge(float64(u.UsedUp), "user", u.Name, "direction", "up"), gauge(float64(u.UsedDown), "user", u.Name, "direction", "down"))
		}
	}
	var byStatus []sample
	for _, st := range sortedKeys(count) {
		byStatus = append(byStatus, gauge(count[st], "status", st))
	}
	p.family("dunvex_users", "gauge", "Users by status.", byStatus...)
	p.family("dunvex_users_online", "gauge", "Users seen in the last minute.", gauge(float64(len(online))))
	p.family("dunvex_user_traffic_bytes_total", "counter", "Traffic of each user in the current period (it restarts when the usage is reset).", traffic...)

	// protocols
	nodes := s.nodeMap()
	inbounds, _ := s.St.Inbounds()
	ierr, _ := s.Mgr.Errors()
	var inTraffic, inUp []sample
	for _, in := range inbounds {
		pr := protocol.Get(in.Preset)
		if pr == nil {
			continue
		}
		name := in.Remark
		if name == "" {
			name = pr.Name
		}
		server := "panel"
		if n := nodes[in.NodeID]; in.NodeID != 0 && n != nil {
			server = n.Name
		}
		l := []string{"id", strconv.FormatInt(in.ID, 10), "protocol", in.Preset, "name", name, "server", server}
		inTraffic = append(inTraffic, gauge(float64(in.Up), append(l, "direction", "up")...), gauge(float64(in.Down), append(l, "direction", "down")...))
		e := ierr[in.ID]
		if in.NodeID != 0 {
			e = s.nodeError(in, nodes)
		}
		inUp = append(inUp, gauge(b2f(in.Enabled && e == ""), l...))
	}
	p.family("dunvex_inbound_traffic_bytes_total", "counter", "Traffic through each protocol.", inTraffic...)
	p.family("dunvex_inbound_up", "gauge", "Whether each protocol is on and running.", inUp...)
	s.healthMetrics(&p)

	// servers: this one and the nodes
	snap := s.Sys.Get()
	srvUp := []sample{gauge(1, "server", "panel", "role", "panel")}
	cpu := []sample{gauge(snap.CPU, "server", "panel")}
	memUsed := []sample{gauge(float64(snap.MemUsed), "server", "panel")}
	memTotal := []sample{gauge(float64(snap.MemTotal), "server", "panel")}
	diskUsed := []sample{gauge(float64(snap.DiskUsed), "server", "panel")}
	diskTotal := []sample{gauge(float64(snap.DiskTotal), "server", "panel")}
	uptime := []sample{gauge(float64(snap.Uptime), "server", "panel")}
	netRate := []sample{gauge(float64(snap.NetUp), "server", "panel", "direction", "up"), gauge(float64(snap.NetDown), "server", "panel", "direction", "down")}
	var coreUp, coreRestarts, lastSeen, certs, relayConns, relayBytes []sample
	for _, c := range s.Mgr.CoreStatuses() {
		if c.Installed {
			coreUp = append(coreUp, gauge(b2f(c.Running), "server", "panel", "core", c.Name))
			coreRestarts = append(coreRestarts, gauge(float64(c.Restarts), "server", "panel", "core", c.Name))
		}
	}
	if env := s.Env(); !env.SelfSigned && env.CertFile != "" {
		if t, err := tlsmgr.Expiry(env.CertFile); err == nil {
			certs = append(certs, gauge(float64(t.Unix()), "server", "panel", "domain", env.Domain))
		}
	}
	list, _ := s.St.Nodes()
	for _, n := range list {
		v := s.viewNode(n, nil)
		role := n.Role
		if role == "" {
			role = store.RoleServer
		}
		srvUp = append(srvUp, gauge(b2f(v.Online), "server", n.Name, "role", role))
		if n.LastSeen > 0 {
			lastSeen = append(lastSeen, gauge(float64(n.LastSeen), "server", n.Name))
		}
		ns := v.Status
		if ns == nil {
			continue
		}
		cpu = append(cpu, gauge(ns.CPU, "server", n.Name))
		memUsed = append(memUsed, gauge(float64(ns.MemUsed), "server", n.Name))
		memTotal = append(memTotal, gauge(float64(ns.MemTotal), "server", n.Name))
		diskUsed = append(diskUsed, gauge(float64(ns.DiskUsed), "server", n.Name))
		diskTotal = append(diskTotal, gauge(float64(ns.DiskTotal), "server", n.Name))
		uptime = append(uptime, gauge(float64(ns.Uptime), "server", n.Name))
		netRate = append(netRate, gauge(float64(ns.NetUp), "server", n.Name, "direction", "up"), gauge(float64(ns.NetDown), "server", n.Name, "direction", "down"))
		for _, c := range ns.Cores {
			if c.Installed {
				coreUp = append(coreUp, gauge(b2f(c.Running), "server", n.Name, "core", c.Name))
				coreRestarts = append(coreRestarts, gauge(float64(c.Restarts), "server", n.Name, "core", c.Name))
			}
		}
		if ns.RealCert && ns.CertExpiry > 0 {
			certs = append(certs, gauge(float64(ns.CertExpiry), "server", n.Name, "domain", ns.Domain))
		}
		for _, f := range ns.Forwards {
			port := strconv.Itoa(f.Listen)
			relayConns = append(relayConns, gauge(float64(f.Conns), "relay", n.Name, "port", port))
			relayBytes = append(relayBytes, gauge(float64(f.Up), "relay", n.Name, "port", port, "direction", "up"), gauge(float64(f.Down), "relay", n.Name, "port", port, "direction", "down"))
		}
	}
	p.family("dunvex_server_up", "gauge", "Whether each server answers (the panel's own is always 1).", srvUp...)
	p.family("dunvex_server_last_seen_timestamp_seconds", "gauge", "When each node last answered.", lastSeen...)
	p.family("dunvex_server_cpu_percent", "gauge", "CPU use of each server.", cpu...)
	p.family("dunvex_server_memory_used_bytes", "gauge", "Memory in use on each server.", memUsed...)
	p.family("dunvex_server_memory_total_bytes", "gauge", "Memory of each server.", memTotal...)
	p.family("dunvex_server_disk_used_bytes", "gauge", "Disk in use on each server.", diskUsed...)
	p.family("dunvex_server_disk_total_bytes", "gauge", "Disk of each server.", diskTotal...)
	p.family("dunvex_server_uptime_seconds", "gauge", "Uptime of each server.", uptime...)
	p.family("dunvex_server_network_bytes_per_second", "gauge", "Network speed of each server now.", netRate...)
	p.family("dunvex_core_up", "gauge", "Whether each installed core runs.", coreUp...)
	p.family("dunvex_core_restarts_total", "counter", "Restarts of each core since the server started.", coreRestarts...)
	p.family("dunvex_cert_expiry_timestamp_seconds", "gauge", "When each real (Let's Encrypt) certificate expires.", certs...)
	p.family("dunvex_relay_connections", "gauge", "Open connections through each relay port.", relayConns...)
	p.family("dunvex_relay_traffic_bytes_total", "counter", "Traffic through each relay port.", relayBytes...)

	// events and webhooks
	var evs []sample
	ec := s.eventCounts()
	for _, k := range sortedKeys(ec) {
		evs = append(evs, gauge(float64(ec[k]), "kind", k))
	}
	p.family("dunvex_events_total", "counter", "Events logged since the panel started, by kind.", evs...)
	if s.Hooks != nil {
		ok, failed := s.Hooks.Counts()
		p.family("dunvex_webhook_deliveries_total", "counter", "Webhook deliveries since the panel started.", gauge(float64(ok), "result", "ok"), gauge(float64(failed), "result", "failed"))
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write([]byte(p.b.String()))
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
