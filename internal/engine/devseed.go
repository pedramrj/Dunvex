//go:build devseed

package engine

import (
	"os"
	"time"
)

// Sample connections for screenshots and demos: users at real public
// addresses (Iranian mobile and home networks, and a few abroad) reaching this
// server and its nodes. Built only with -tags devseed and run only with
// DUNVEX_DEV_SEED=1; never part of a release.
func init() {
	devSeed = func(m *Manager) {
		if os.Getenv("DUNVEX_DEV_SEED") != "1" {
			return
		}
		type c struct {
			user, ip string
			server   int
		}
		sample := []c{
			{"taraneh", "5.120.44.17", 0}, {"taraneh", "2.180.20.9", 1},
			{"kaveh-android", "5.112.81.3", 0}, {"parisa", "37.32.12.5", 0},
			{"sara-iphone", "5.200.70.11", 0}, {"sara-iphone", "91.98.30.2", 2},
			{"farhad", "87.123.40.8", 2}, {"behnam-mac", "79.127.30.1", 0},
			{"office-1", "31.56.10.4", 1}, {"office-1", "31.56.10.9", 1}, {"office-1", "2.185.100.20", 0},
			{"yasaman", "5.124.200.3", 0}, {"arash-tv", "94.200.10.7", 0}, {"negar", "81.2.69.160", 2},
			{"dariush", "88.230.10.4", 0}, {"family-pack", "5.208.30.30", 0}, {"family-pack", "151.238.10.2", 1},
			{"reza", "24.48.0.1", 2}, {"shirin", "2.144.10.9", 0}, {"omid", "185.112.33.10", 0},
		}
		nodes, _ := m.Store.Nodes()
		ids := []int64{0}
		for _, n := range nodes {
			ids = append(ids, n.ID)
		}
		for {
			now := time.Now().Unix()
			for _, s := range sample {
				m.dev.seeVia(s.user, s.ip, now-int64(len(s.ip)%7)*20, ids[s.server%len(ids)])
			}
			time.Sleep(30 * time.Second)
		}
	}
}
