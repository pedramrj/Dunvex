package engine

import (
	"time"

	"github.com/pedramrj/dunvex/internal/store"
)

// AddRemote applies one traffic batch from a node: usage per user name and per
// inbound id, plus the addresses users connected from there. A batch is applied
// once (seq); one seen before only refreshes the devices. Users who start their
// plan, go online or cross a limit are handled as for local traffic.
func (m *Manager) AddRemote(nodeID, seq int64, users map[string]store.Traffic, inbounds map[int64]store.Traffic, devices []Seen) (bool, error) {
	now := time.Now()
	for _, d := range devices {
		m.dev.see(d.User, d.IP, d.LastSeen)
	}
	list, err := m.Store.Users()
	if err != nil {
		return false, err
	}
	byName := map[string]*store.User{}
	for _, u := range list {
		byName[u.Name] = u
	}
	ud := map[int64]store.Traffic{}
	for name, t := range users {
		if u := byName[name]; u != nil && (t.Up > 0 || t.Down > 0) {
			ud[u.ID] = t
		}
	}
	fresh, err := m.Store.AddNodeTraffic(nodeID, seq, now, ud, inbounds)
	if err != nil || !fresh {
		return false, err
	}
	m.mu.Lock()
	for uid := range ud {
		m.online[uid] = now.Unix()
	}
	active := m.activeNames
	m.mu.Unlock()

	for _, u := range list {
		t := ud[u.ID]
		if (t.Up > 0 || t.Down > 0) && u.ExpireAt == 0 && u.DurationDays > 0 && u.FirstUseAt == 0 {
			u.FirstUseAt = now.Unix()
			u.ExpireAt = now.Unix() + int64(u.DurationDays)*86400
			m.Store.UpdateUser(u)
			m.Store.Log("user", "%s connected for the first time; plan of %d days started", u.Name, u.DurationDays)
		}
	}
	// a user who crossed a limit leaves every server at the next apply
	after, _ := m.Store.Users()
	changed := m.enforceDevices(after, now.Unix())
	for _, u := range after {
		if u.CanConnect(now.Unix()) != active[u.Name] {
			changed = true
		}
	}
	if changed {
		m.RequestApply()
	}
	return true, nil
}
