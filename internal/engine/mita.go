package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/pedramrj/dunvex/internal/store"
)

// mita is the Mieru server. Dunvex writes its JSON config and talks to it over
// its control socket (gRPC, service mieru.appctl.ServerManagementService):
// Reload brings in user changes without dropping anyone, GetStatus tells
// whether it really serves, GetMetrics gives each user's byte counters.

const mitaService = "/mieru.appctl.ServerManagementService/"

// mita status values (mieru.appctl.AppStatus)
const (
	mitaIdle    = 1
	mitaRunning = 3
	mitaStopped = 5
)

func (m *Manager) mitaCall(method string) ([]byte, error) {
	c, err := stats.conn("unix://" + m.Mita.Socket())
	if err != nil {
		return nil, err
	}
	req, resp := []byte{}, []byte{}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := c.Invoke(ctx, mitaService+method, &req, &resp, grpc.ForceCodec(rawCodec{})); err != nil {
		return nil, err
	}
	return resp, nil
}

// mitaStatus is mita's own state (0 when it does not answer).
func (m *Manager) mitaStatus() int {
	b, err := m.mitaCall("GetStatus")
	if err != nil {
		return 0
	}
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return 0
		}
		b = b[n:]
		if num == 1 && typ == protowire.VarintType {
			v, k := protowire.ConsumeVarint(b)
			if k < 0 {
				return 0
			}
			return int(v)
		}
		k := protowire.ConsumeFieldValue(num, typ, b)
		if k < 0 {
			return 0
		}
		b = b[k:]
	}
	return mitaIdle // an empty answer is the default value
}

// mitaServing waits for mita to serve after a start; when it does not (a
// port it cannot open, say), it returns the reason from its log.
func (m *Manager) mitaServing() error {
	st := 0
	for i := 0; i < 30; i++ {
		if st = m.mitaStatus(); st == mitaRunning || !m.Mita.Running() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st == mitaRunning {
		return nil
	}
	if !m.Mita.Running() {
		return fmt.Errorf("mita stopped: %s", lastLines(strings.Join(m.Mita.Logs(), "\n"), 3))
	}
	logs := m.Mita.Logs()
	for i := len(logs) - 1; i >= 0 && i >= len(logs)-30; i-- {
		if l := logs[i]; strings.Contains(l, "ERROR") || strings.Contains(strings.ToLower(l), "failed") {
			return fmt.Errorf("mita does not serve: %s", strings.TrimSpace(l))
		}
	}
	return fmt.Errorf("mita does not serve (status %d): %s", st, lastLines(strings.Join(logs, "\n"), 3))
}

// mitaLive applies a change of users alone with a reload, so nobody is cut
// off; anything else needs a restart.
func (m *Manager) mitaLive(old, cfg []byte) bool {
	if !usersOnlyChange(old, cfg) || m.mitaStatus() != mitaRunning {
		return false // a reload only changes users of a mita that serves
	}
	if os.WriteFile(m.Mita.ConfigPath, cfg, 0o600) != nil {
		return false
	}
	if _, err := m.mitaCall("Reload"); err != nil {
		os.WriteFile(m.Mita.ConfigPath, old, 0o600)
		return false
	}
	// mita checks users only when a connection opens: whoever was removed keeps
	// the connections already open, so mitaCollect watches them
	had, have := mitaUserNames(old), mitaUserNames(cfg)
	now := time.Now().Unix()
	m.mu.Lock()
	if m.mitaGone == nil {
		m.mitaGone = map[string]int64{}
	}
	for name := range had {
		if !have[name] {
			m.mitaGone[name] = now
		}
	}
	for name := range have {
		delete(m.mitaGone, name)
	}
	m.mu.Unlock()
	return true
}

func mitaUserNames(cfg []byte) map[string]bool {
	var v struct {
		Users []struct {
			Name string `json:"name"`
		} `json:"users"`
	}
	json.Unmarshal(cfg, &v)
	out := map[string]bool{}
	for _, u := range v.Users {
		out[u.Name] = true
	}
	return out
}

// usersOnlyChange reports whether two mita configs differ in their users alone.
func usersOnlyChange(old, cfg []byte) bool {
	strip := func(b []byte) []byte {
		var v map[string]any
		if json.Unmarshal(b, &v) != nil {
			return nil
		}
		delete(v, "users")
		out, _ := json.Marshal(v)
		return out
	}
	a, b := strip(old), strip(cfg)
	return a != nil && bytes.Equal(a, b)
}

// mitaPlan is what the last apply gave mita.
type mitaPlan struct {
	inbound int64
}

// mitaCollect adds each user's traffic since the last look. mita counts from
// zero at every start (see Core.start), so a new start resets the baseline.
func (m *Manager) mitaCollect(byName map[string]*store.User, ud, id map[int64]store.Traffic) {
	m.mu.Lock()
	plan := m.mitaNow
	m.mu.Unlock()
	if plan == nil || !m.Mita.Running() {
		return
	}
	gen := m.Mita.StartedAt()
	b, err := m.mitaCall("GetMetrics")
	if err != nil {
		return
	}
	users, err := mitaUserBytes(b)
	if err != nil {
		return
	}
	now := time.Now().Unix()
	kick := ""
	defer func() {
		// a removed user still moving traffic: start mita again, which closes
		// every connection; the others reconnect at once, that user cannot
		if kick != "" {
			m.Store.Log("core", "%s was still connected to Mieru after losing access; mita restarted to disconnect it", kick)
			m.Mita.Restart()
			m.mitaServing()
		}
	}()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mitaPrev == nil || !m.mitaGen.Equal(gen) {
		m.mitaPrev, m.mitaGen = map[string]store.Traffic{}, gen
		m.mitaGone = map[string]int64{} // a new process has none of the old connections
	}
	for name, at := range m.mitaGone {
		if now-at > 900 {
			delete(m.mitaGone, name)
		}
	}
	for name, t := range users {
		prev := m.mitaPrev[name]
		m.mitaPrev[name] = t
		up, down := t.Up-prev.Up, t.Down-prev.Down
		if up < 0 || down < 0 {
			up, down = t.Up, t.Down
		}
		if _, gone := m.mitaGone[name]; gone && (up > 0 || down > 0) && now-m.mitaKick >= 30 {
			kick, m.mitaKick = name, now
		}
		u := byName[name]
		if u == nil || (up <= 0 && down <= 0) {
			continue
		}
		x := ud[u.ID]
		x.Up, x.Down = x.Up+up, x.Down+down
		ud[u.ID] = x
		it := id[plan.inbound]
		it.Up, it.Down = it.Up+up, it.Down+down
		id[plan.inbound] = it
	}
}

// mitaUserBytes reads the per-user counters out of a GetMetrics answer.
func mitaUserBytes(b []byte) (map[string]store.Traffic, error) {
	var js string
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, fmt.Errorf("bad metrics answer")
		}
		b = b[n:]
		if num == 1 && typ == protowire.BytesType {
			v, k := protowire.ConsumeString(b)
			if k < 0 {
				return nil, fmt.Errorf("bad metrics answer")
			}
			js = v
			break
		}
		k := protowire.ConsumeFieldValue(num, typ, b)
		if k < 0 {
			return nil, fmt.Errorf("bad metrics answer")
		}
		b = b[k:]
	}
	var doc struct {
		Users map[string]map[string]int64 `json:"users"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		return nil, err
	}
	out := map[string]store.Traffic{}
	for name, c := range doc.Users {
		out[name] = store.Traffic{Up: c["UploadBytes"], Down: c["DownloadBytes"]}
	}
	return out, nil
}

// MitaConfig is mita's current configuration, for the panel (it holds the
// users' passwords, like the Xray and sing-box configs do).
func (m *Manager) MitaConfig() []byte {
	b, _ := m.build()
	if b.mita == nil {
		return []byte("{}")
	}
	return b.mita
}
