package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// TrustTunnel's endpoint reads its users once at start, so any change of users
// starts it again (which also cuts off whoever was removed). Its local metrics
// listener answers /health-check and /clients: per user the bytes of this
// process, the open sessions and an address. Its "inbound" is what reached the
// user (download) and "outbound" what the user sent: measured against the
// same request through Mieru, despite the endpoint's documentation.

var ttHTTP = &http.Client{Timeout: 3 * time.Second}

// writeTTFiles writes the endpoint's credentials, hosts and certificate files.
func (m *Manager) writeTTFiles(s *protocol.TTServer) error {
	dir := filepath.Dir(m.TT.ConfigPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for name, b := range s.Files {
		p := filepath.Join(dir, name)
		if old, err := os.ReadFile(p); err == nil && string(old) == string(b) {
			continue
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ttGet(path string) ([]byte, int, error) {
	res, err := ttHTTP.Get("http://127.0.0.1:" + strconv.Itoa(m.TT.APIPort) + path)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return b, res.StatusCode, err
}

// ttServing waits for the endpoint to answer after a start; when it does not,
// the reason comes from its log.
func (m *Manager) ttServing() error {
	for i := 0; i < 40; i++ {
		if _, code, err := m.ttGet("/health-check"); err == nil && code == 200 {
			return nil
		}
		if !m.TT.Running() && i > 5 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	logs := m.TT.Logs()
	for i := len(logs) - 1; i >= 0 && i >= len(logs)-30; i-- {
		l := strings.ToLower(logs[i])
		if strings.Contains(l, "error") || strings.Contains(l, "panicked") || strings.Contains(l, "couldn't") || strings.Contains(l, "failed") {
			return fmt.Errorf("trusttunnel does not serve: %s", strings.TrimSpace(logs[i]))
		}
	}
	return fmt.Errorf("trusttunnel does not serve: %s", lastLines(strings.Join(logs, "\n"), 3))
}

// ttClient is one entry of /clients.
type ttClient struct {
	Username string  `json:"username"`
	IP       *string `json:"ip"`
	Sessions int     `json:"sessions"`
	Inbound  int64   `json:"inbound"`  // downloaded by the user
	Outbound int64   `json:"outbound"` // uploaded by the user
}

// ttCollect adds each user's traffic since the last look, marks devices.
func (m *Manager) ttCollect(byName map[string]*store.User, ud, id map[int64]store.Traffic) {
	m.mu.Lock()
	plan := m.ttNow
	m.mu.Unlock()
	if plan == nil || !m.TT.Running() {
		return
	}
	gen := m.TT.StartedAt()
	b, code, err := m.ttGet("/clients")
	if err != nil || code != 200 {
		return
	}
	var list []ttClient
	if json.Unmarshal(b, &list) != nil {
		return
	}
	now := time.Now().Unix()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ttPrev == nil || !m.ttGen.Equal(gen) {
		m.ttPrev, m.ttGen = map[string]store.Traffic{}, gen // a new process counts from zero
	}
	for _, c := range list {
		t := store.Traffic{Up: c.Outbound, Down: c.Inbound}
		prev := m.ttPrev[c.Username]
		m.ttPrev[c.Username] = t
		up, down := t.Up-prev.Up, t.Down-prev.Down
		if up < 0 || down < 0 {
			up, down = t.Up, t.Down
		}
		u := byName[c.Username]
		if u == nil {
			continue
		}
		if (c.Sessions > 0 || up > 0 || down > 0) && c.IP != nil && *c.IP != "" {
			m.dev.see(u.Name, *c.IP, now)
		}
		if up <= 0 && down <= 0 {
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

// TTConfig is the endpoint's main settings, for the panel.
func (m *Manager) TTConfig() []byte {
	b, _ := m.build()
	if b.tt == nil {
		return []byte("# TrustTunnel is not running\n")
	}
	return b.tt.Main
}
