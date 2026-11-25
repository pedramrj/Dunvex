package engine

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/sshproxy"
	"github.com/pedramrj/dunvex/internal/store"
)

// The SSH server runs inside Dunvex (internal/sshproxy), one per server. It
// signs users in itself and hands every forward to a SOCKS inbound of Xray on
// loopback (protocol.SSHSocks), which has no accounts: a change of users
// reaches the SSH server alone (whoever lost access is cut off at once) and
// never restarts Xray. Usage per user and the address of each session come
// from the SSH server; the inbound's own usage from Xray's counters.

// sshPlan is what the SSH server serves.
type sshPlan struct {
	inbound  int64
	listen   string
	hostKey  string
	upstream string            // Xray's SOCKS inbound
	users    map[string]string // name → password
}

func (p *sshPlan) sameServer(q *sshPlan) bool {
	return p != nil && q != nil && p.listen == q.listen && p.hostKey == q.hostKey && p.upstream == q.upstream
}

// sshRun is the SSH server running now (guarded by Manager.applyMu).
type sshRun struct {
	plan *sshPlan
	srv  *sshproxy.Server
}

var sshLog struct {
	mu   sync.Mutex
	logs []string
}

func sshLogf(format string, v ...any) {
	line := time.Now().Format("2006/01/02 15:04:05 ") + fmt.Sprintf(format, v...)
	sshLog.mu.Lock()
	sshLog.logs = append(sshLog.logs, line)
	if len(sshLog.logs) > 200 {
		sshLog.logs = sshLog.logs[len(sshLog.logs)-200:]
	}
	sshLog.mu.Unlock()
}

type sshLogs struct{}

func (sshLogs) Logs() []string {
	sshLog.mu.Lock()
	defer sshLog.mu.Unlock()
	return append([]string(nil), sshLog.logs...)
}

// SSHLogger is the panel's handle on the SSH server's log lines.
var SSHLogger interface{ Logs() []string } = sshLogs{}

// sshApply starts, changes or stops the SSH server; it returns the problem
// of its inbound ("" when it serves).
func (m *Manager) sshApply(p *sshPlan) string {
	cur := m.ssh
	if cur != nil && p.sameServer(cur.plan) {
		cur.srv.Update(p.users)
		cur.plan = p
		return ""
	}
	if cur != nil {
		cur.srv.Close()
		m.ssh = nil
		sshLogf("stopped (%s)", cur.plan.listen)
	}
	defer m.sshSnapshot()
	if p == nil {
		return ""
	}
	key, err := sshproxy.HostKey(p.hostKey)
	if err != nil {
		return "the SSH host key is not valid; clear it to make a new one"
	}
	srv, err := sshproxy.Listen(sshproxy.Config{Listen: p.listen, HostKey: key, Upstream: p.upstream, Users: p.users})
	if err != nil {
		sshLogf("cannot listen on %s: %v", p.listen, err)
		if strings.Contains(strings.ToLower(err.Error()), "in use") || strings.Contains(err.Error(), "Only one usage") {
			return fmt.Sprintf("TCP port %s is used by another program on this server (port 22 is the server's own SSH); choose another port", portOf(p.listen))
		}
		return "the SSH server could not start: " + err.Error()
	}
	srv.Logf = sshLogf
	m.ssh = &sshRun{plan: p, srv: srv}
	sshLogf("listening on %s (%d users)", p.listen, len(p.users))
	return ""
}

func portOf(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[i+1:]
	}
	return addr
}

// sshSnapshot publishes the running SSH server for the collector and status calls.
func (m *Manager) sshSnapshot() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sshNow, m.sshIn = nil, 0
	if m.ssh != nil {
		m.sshNow, m.sshIn = m.ssh.srv, m.ssh.plan.inbound
	}
}

// stopSSH stops the SSH server.
func (m *Manager) stopSSH() {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.sshApply(nil)
	m.sshSnapshot()
}

// sshCollect adds each user's SSH traffic since the last look and marks the
// addresses their sessions come from.
func (m *Manager) sshCollect(byName map[string]*store.User, ud map[int64]store.Traffic) {
	m.mu.Lock()
	srv := m.sshNow
	m.mu.Unlock()
	if srv == nil {
		return
	}
	for _, s := range srv.Sessions() {
		m.dev.see(s.User, s.IP, s.Last)
	}
	for name, t := range srv.Take() {
		u := byName[name]
		if u == nil {
			continue // the config check's user, or one deleted meanwhile
		}
		x := ud[u.ID]
		x.Up, x.Down = x.Up+t.Up, x.Down+t.Down
		ud[u.ID] = x
	}
}

// sshStatus is the built-in SSH server, for the panel's list of cores.
func (m *Manager) sshStatus() (Status, bool) {
	m.mu.Lock()
	srv := m.sshNow
	m.mu.Unlock()
	if srv == nil {
		return Status{}, false
	}
	return Status{Name: "ssh", Installed: true, Running: true, Version: strings.TrimPrefix(sshproxy.Version, "SSH-2.0-"), BuiltIn: true}, true
}

// SSHConfig describes the SSH server, for the panel (no passwords or keys).
func (m *Manager) SSHConfig() []byte {
	b, _ := m.build()
	if b.ssh == nil {
		return []byte("# the SSH server is not running\n")
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "listen: %s\nforwards to: %s (Xray SOCKS inbound in-%d)\nusers: %d\nversion: %s\n", b.ssh.listen, b.ssh.upstream, b.ssh.inbound, len(b.ssh.users), sshproxy.Version)
	if k := sshproxy.PublicKey(b.ssh.hostKey); k != "" {
		fmt.Fprintf(&sb, "host key: %s (%s)\n", k, sshproxy.Fingerprint(b.ssh.hostKey))
	}
	m.mu.Lock()
	srv := m.sshNow
	m.mu.Unlock()
	if srv != nil {
		fmt.Fprintf(&sb, "sessions now: %d\n", len(srv.Sessions()))
	}
	return []byte(sb.String())
}
