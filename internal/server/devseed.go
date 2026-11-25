//go:build devseed

package server

import (
	"os"
	"time"

	"github.com/pedramrj/dunvex/internal/bridge"
	"github.com/pedramrj/dunvex/internal/node"
	"github.com/pedramrj/dunvex/internal/relay"
	"github.com/pedramrj/dunvex/internal/store"
)

// seededBridgeID is the id of the sample Bridge relay, so the sync loop skips it.
var seededBridgeID int64

func init() {
	seedNodes = func(s *Server) {
		if os.Getenv("DUNVEX_DEV_SEED") != "1" {
			return
		}
		go seedBridgeRelay(s)
	}
	skipSync = func(n *store.Node) bool { return n.ID != 0 && n.ID == seededBridgeID }
}

// seedBridgeRelay inserts one bridge relay named "Tehran" and keeps a lifelike
// live status on it so the Servers page renders the carrier console.
func seedBridgeRelay(s *Server) {
	name := "Tehran"
	var id int64
	for _, n := range mustNodes(s) {
		if n.Name == name {
			id = n.ID
		}
	}
	if id == 0 {
		n := &store.Node{Name: name, Address: "10.10.0.2", AgentPort: 62050, Fingerprint: "seedseedseedseed",
			Enabled: true, Role: store.RoleRelay, RelayMode: store.RelayBridge, CertSHA256: "aa11bb22cc33"}
		_ = ensureBridgeKeys(n)
		n.SetBridge(store.DefaultBridgeSettings())
		if err := s.St.CreateNode(n); err != nil {
			return
		}
		id = n.ID
	}
	seededBridgeID = id
	for {
		s.hub().setSeedStatus(id)
		time.Sleep(3 * time.Second)
	}
}

func mustNodes(s *Server) []*store.Node { l, _ := s.St.Nodes(); return l }

// setSeedStatus writes a fresh, lifelike status onto a seeded node.
func (h *nodeHub) setSeedStatus(id int64) {
	now := time.Now().Unix()
	jitter := func(base int64) int64 { return base + (now%7)*base/40 }
	st := &node.Status{
		Version: "1.36.0", Hostname: "ir-dc-01", PublicIP: "10.10.0.2", Uptime: 86400 * 3,
		CPU: 9, MemUsed: 300 << 20, MemTotal: 2 << 30, DiskUsed: 6 << 30, DiskTotal: 40 << 30,
		NetUp: uint64(jitter(2_000_000)), NetDown: uint64(jitter(21_000_000)), OS: "linux", Arch: "amd64", Time: now,
		Forwards: []relay.Stat{
			{Forward: relay.Forward{Name: "Reality", Listen: 443, Network: "tcp", Reverse: true}, Listening: true, Conns: 37, Up: 4 << 30, Down: 51 << 30},
			{Forward: relay.Forward{Name: "Hysteria2", Listen: 443, Network: "udp", Reverse: true}, Listening: true, Conns: 12, Up: 1 << 30, Down: 18 << 30},
		},
		Bridge: &relay.BridgeStat{Links: 2, Since: now - 5400, Carriers: []bridge.CarrierStat{
			{Name: "faketcp", Up: true, RTTMs: 48, LossPct: 1, RateDown: jitter(17_800_000), RateUp: jitter(2_100_000)},
			{Name: "udp", Up: true, RTTMs: 44, LossPct: 0, RateDown: jitter(9_200_000), RateUp: jitter(1_000_000)},
			{Name: "icmp", Up: true, RTTMs: 53, LossPct: 3, RateDown: jitter(6_300_000), RateUp: 520_000, CapMBps: 22},
			{Name: "tls-cdn", Up: false, RTTMs: -1, LossPct: 100, LastErr: "the relay refused the connection (throttled by the ISP)", LastFail: now - 120},
		}},
		CoreErrors: map[string]string{}, InboundErrors: map[int64]string{},
	}
	l := h.state(id)
	h.mu.Lock()
	l.Status, l.Error, l.Fails, l.LastOK, l.LastTry = st, "", 0, now, now
	l.RateUp, l.RateDown = 2_100_000, 23_800_000
	h.mu.Unlock()
}
