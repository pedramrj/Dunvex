package engine

import (
	"net"
	"time"

	"github.com/pedramrj/dunvex/internal/awg"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// awgPlan is what one apply wants from AmneziaWG.
type awgPlan struct {
	cfg     *awg.Config
	inbound int64
	users   map[string]int64 // public key → user id
}

func (m *Manager) awgBuild(in *store.Inbound, us []*store.User) *awgPlan {
	p := &awgPlan{inbound: in.ID, users: map[string]int64{}, cfg: &awg.Config{
		PrivateKey: in.Str("privateKey"), Port: in.Port, Address: protocol.AWGServerAddress(),
		MTU: 1420, Device: protocol.AWGDevice(in),
	}}
	for _, u := range us {
		if u.WGPublic == "" {
			continue
		}
		p.cfg.Peers = append(p.cfg.Peers, awg.Peer{User: u.Name, PublicKey: u.WGPublic, Address: protocol.AWGAddress(u) + "/32"})
		p.users[u.WGPublic] = u.ID
	}
	return p
}

// awgApply brings the interface in line with the plan (nil stops it).
func (m *Manager) awgApply(p *awgPlan) error {
	m.mu.Lock()
	m.awgNow = p
	m.mu.Unlock()
	if p == nil {
		m.AWG.Stop()
		return nil
	}
	return m.AWG.Apply(p.cfg)
}

// awgCollect turns the peers' counters into usage, online state and devices.
func (m *Manager) awgCollect(ud, id map[int64]store.Traffic) {
	now := time.Now().Unix()
	m.mu.Lock()
	plan := m.awgNow
	m.mu.Unlock()
	if plan == nil {
		return
	}
	if !m.AWG.Running() {
		// it stopped on its own: start it again, at most twice a minute
		m.mu.Lock()
		retry := now-m.awgRetry >= 30
		if retry {
			m.awgRetry = now
		}
		m.mu.Unlock()
		if retry {
			go func() {
				if err := m.AWG.Apply(plan.cfg); err == nil {
					m.Store.Log("core", "AmneziaWG had stopped and was started again")
				}
			}()
		}
		return
	}
	gen := m.AWG.Starts()
	stats, err := m.AWG.Stats()
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.awgPrev == nil || m.awgGen != gen {
		m.awgPrev, m.awgGen = map[string]awg.PeerStat{}, gen // a new process counts from zero
	}
	for key, st := range stats {
		uid, ok := plan.users[key]
		prev := m.awgPrev[key]
		m.awgPrev[key] = st
		if !ok {
			continue
		}
		up, down := st.RX-prev.RX, st.TX-prev.TX
		if up < 0 || down < 0 { // the peer was added again: its counters started over
			up, down = st.RX, st.TX
		}
		if up > 0 || down > 0 {
			t := ud[uid]
			t.Up, t.Down = t.Up+up, t.Down+down
			ud[uid] = t
			it := id[plan.inbound]
			it.Up, it.Down = it.Up+up, it.Down+down
			id[plan.inbound] = it
		}
		// a fresh handshake or traffic means the device is there now
		if host, _, err := net.SplitHostPort(st.Endpoint); err == nil && (up > 0 || down > 0 || now-st.LastHandshake < 180) {
			for _, u := range plan.cfg.Peers {
				if u.PublicKey == key {
					m.dev.see(u.User, host, now)
					break
				}
			}
		}
	}
}

// awgStatus is AmneziaWG as a core in the panel.
func (m *Manager) awgStatus() Status {
	s := m.AWG
	m.mu.Lock()
	idle := m.awgNow == nil
	m.mu.Unlock()
	st := Status{Name: s.Name, Installed: s.Installed(), Running: s.Running(), Version: s.Version(), Uptime: s.Uptime(), Error: s.LastError()}
	st.Idle = idle && !st.Running && st.Error == ""
	return st
}

// AWGConfig is what AmneziaWG serves, as text (no private key).
func (m *Manager) AWGConfig() []byte {
	m.mu.Lock()
	p := m.awgNow
	m.mu.Unlock()
	if p == nil {
		return []byte((*awg.Config)(nil).Describe(m.AWG.Iface))
	}
	return []byte(p.cfg.Describe(m.AWG.Iface))
}
