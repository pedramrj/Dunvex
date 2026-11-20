package engine

import (
	"fmt"
	"strconv"
)

// mtgConfig is the Telegram proxy's configuration (mtg 2, TOML). On the shared
// port the edge router sends it the client's address in a PROXY header.
func mtgConfig(secret, bind string, proxyProtocol bool, statsPort int) []byte {
	return []byte(fmt.Sprintf(`# written by Dunvex
secret = %q
bind-to = %q
proxy-protocol-listener = %t
concurrency = 8192
prefer-ip = "prefer-ipv4"
auto-update = false
tolerate-time-skewness = "5s"

[stats.prometheus]
enabled = true
bind-to = %q
http-path = "/"
metric-prefix = "mtg"
`, secret, bind, proxyProtocol, "127.0.0.1:"+strconv.Itoa(statsPort)))
}

// MTGConfig is the Telegram proxy's current configuration, for the panel.
func (m *Manager) MTGConfig() []byte {
	b, _ := m.build()
	return b.mtg
}

// Cores lists the cores this server has or uses: Xray and sing-box always,
// the Telegram proxy once it is installed or wanted.
func (m *Manager) Cores() []*Core {
	out := []*Core{m.Xray, m.SingBox}
	if m.MTG.Installed() || m.MTG.Running() || m.MTG.Status().Error != "" {
		out = append(out, m.MTG)
	}
	return out
}

// CoreStatuses is the state of every core in Cores, and of AmneziaWG once
// it is installed or wanted.
func (m *Manager) CoreStatuses() []Status {
	var out []Status
	for _, c := range m.Cores() {
		out = append(out, c.Status())
	}
	if m.AWG.Installed() || m.AWG.Running() || m.AWG.LastError() != "" {
		out = append(out, m.awgStatus())
	}
	return out
}

// CoreByName finds a core ("xray", "sing-box" or "mtg").
func (m *Manager) CoreByName(name string) *Core {
	switch name {
	case "sing-box":
		return m.SingBox
	case "mtg":
		return m.MTG
	}
	return m.Xray
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
