package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Node is another server this panel runs protocols on. The panel talks to its
// agent over mutual TLS: Fingerprint pins the node's certificate, and Secret is
// the one-time pairing secret from the node's token (empty once paired).
type Node struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Address     string `json:"address"` // public host or IP, also used in share links
	AgentPort   int    `json:"agentPort"`
	Fingerprint string `json:"fingerprint"` // SHA-256 of the node's agent certificate (hex)
	Secret      string `json:"-"`
	Enabled     bool   `json:"enabled"`
	CertSHA256  string `json:"-"` // the node's TLS certificate for protocols, for pinned links
	StatsSeq    int64  `json:"-"` // last traffic batch applied (exactly-once)
	Epoch       string `json:"-"` // the node install the sequence belongs to
	LastSeen    int64  `json:"lastSeen"`
	CreatedAt   int64  `json:"createdAt"`

	// A relay runs no protocols: it forwards every protocol of its target
	// (0 = this panel's server, else a node) on the same ports.
	Role        string `json:"role"` // server or relay
	RelayTarget int64  `json:"relayTarget"`
	RelayOnly   bool   `json:"relayOnly"`  // users get only the relay's configs for the target
	RelayShift  int    `json:"relayShift"` // the relay listens on each port plus this
	// forward: the relay dials the target; reverse: the target keeps links to
	// the relay's tunnel port (RelayPort), proving itself with RelaySecret
	RelayMode   string `json:"relayMode"`
	RelayPort   int    `json:"relayPort"`
	RelaySecret string `json:"-"`

	// Domain is the node's own name: it gets a Let's Encrypt certificate for
	// it, and TLS protocols (NaiveProxy, the CDN ones) can run on it.
	Domain   string `json:"domain"`
	RealCert bool   `json:"realCert"` // the node serves a real certificate (links do not pin it)

	// The Bridge (relay mode "bridge"): the multi-carrier tunnel's carriers and
	// options, and the relay's own keys (kept secret, never sent to clients).
	BridgeJSON    string `json:"-"`
	BridgePriv    string `json:"-"` // static X25519 private key (base64)
	BridgeDNSPriv string `json:"-"` // dnstt private key (hex)
}

// Relay modes.
const (
	RelayForward = "forward"
	RelayReverse = "reverse"
	RelayBridge  = "bridge" // the multi-carrier Bridge across the border
)

// Reverse reports whether the relay's target dials it over the reverse tunnel.
func (n *Node) Reverse() bool { return n.IsRelay() && n.RelayMode == RelayReverse }

// IsBridge reports whether the relay reaches its target over the Bridge.
func (n *Node) IsBridge() bool { return n.IsRelay() && n.RelayMode == RelayBridge }

// Tunneled reports whether the relay's forwards ride a tunnel the target keeps
// open (reverse tunnel or Bridge), rather than the relay dialing the target.
func (n *Node) Tunneled() bool { return n.Reverse() || n.IsBridge() }

// BridgeCarrierPref is one carrier's panel-side setting.
type BridgeCarrierPref struct {
	Kind           string `json:"kind"` // udp, icmp, faketcp, tls-cdn, dns
	On             bool   `json:"on"`
	Priority       int    `json:"priority,omitempty"`
	CapMBps        int    `json:"capMBps,omitempty"`
	Port           int    `json:"port,omitempty"`
	ServerRequests bool   `json:"serverRequests,omitempty"` // icmp: the relay sends the echo requests
}

// BridgeSettings is the panel-side Bridge configuration kept on a bridge relay.
type BridgeSettings struct {
	Carriers  []BridgeCarrierPref `json:"carriers"`
	TLSDomain string              `json:"tlsDomain,omitempty"` // the relay's CDN-fronted domain for the tls-cdn carrier
	DNSZone   string              `json:"dnsZone,omitempty"`   // the tunnel zone for the dns carrier
}

// DefaultBridgeSettings turns on the carriers that need no extra setup, with
// sensible priorities and the ICMP cap kept under the reported volume trigger.
func DefaultBridgeSettings() BridgeSettings {
	return BridgeSettings{Carriers: []BridgeCarrierPref{
		{Kind: "faketcp", On: true, Priority: 30, Port: 8443},
		{Kind: "udp", On: true, Priority: 25, Port: 8444},
		{Kind: "tls-cdn", On: false, Priority: 20, Port: 8445},
		{Kind: "icmp", On: true, Priority: 15, CapMBps: 22},
		{Kind: "dns", On: false, Priority: 5, CapMBps: 1, Port: 5300},
	}}
}

// Bridge parses the node's stored Bridge settings, filling defaults.
func (n *Node) Bridge() BridgeSettings {
	bs := DefaultBridgeSettings()
	if n.BridgeJSON != "" {
		var got BridgeSettings
		if json.Unmarshal([]byte(n.BridgeJSON), &got) == nil && len(got.Carriers) > 0 {
			bs.Carriers = got.Carriers
			bs.TLSDomain, bs.DNSZone = got.TLSDomain, got.DNSZone
		} else if json.Unmarshal([]byte(n.BridgeJSON), &got) == nil {
			bs.TLSDomain, bs.DNSZone = got.TLSDomain, got.DNSZone
		}
	}
	return bs
}

// SetBridge stores Bridge settings.
func (n *Node) SetBridge(bs BridgeSettings) {
	b, _ := json.Marshal(bs)
	n.BridgeJSON = string(b)
}

// Node roles.
const (
	RoleServer = "server"
	RoleRelay  = "relay"
)

// IsRelay reports whether the node forwards instead of running protocols.
func (n *Node) IsRelay() bool { return n.Role == RoleRelay }

// Paired reports whether the node has accepted this panel.
func (n *Node) Paired() bool { return n.Secret == "" }

var nodeTables = []string{
	`CREATE TABLE IF NOT EXISTS nodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		address TEXT NOT NULL,
		agent_port INTEGER NOT NULL,
		fingerprint TEXT NOT NULL,
		secret TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		cert_sha256 TEXT NOT NULL DEFAULT '',
		stats_seq INTEGER NOT NULL DEFAULT 0,
		epoch TEXT NOT NULL DEFAULT '',
		last_seen INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	)`,
}

const nodeCols = `id,name,address,agent_port,fingerprint,secret,enabled,cert_sha256,stats_seq,epoch,last_seen,created_at,role,relay_target,relay_only,relay_shift,relay_mode,relay_port,relay_secret,domain,cert_real,bridge_json,bridge_priv,bridge_dns_priv`

func scanNode(r scanner) (*Node, error) {
	n := &Node{}
	var en, only, real int
	err := r.Scan(&n.ID, &n.Name, &n.Address, &n.AgentPort, &n.Fingerprint, &n.Secret, &en, &n.CertSHA256, &n.StatsSeq, &n.Epoch, &n.LastSeen, &n.CreatedAt,
		&n.Role, &n.RelayTarget, &only, &n.RelayShift, &n.RelayMode, &n.RelayPort, &n.RelaySecret, &n.Domain, &real, &n.BridgeJSON, &n.BridgePriv, &n.BridgeDNSPriv)
	n.Enabled, n.RelayOnly, n.RealCert = en == 1, only == 1, real == 1
	return n, err
}

// Nodes lists the other servers.
func (s *Store) Nodes() ([]*Node, error) {
	rows, err := s.DB.Query(`SELECT ` + nodeCols + ` FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// NodeByID loads one server.
func (s *Store) NodeByID(id int64) (*Node, error) {
	n, err := scanNode(s.DB.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// CreateNode adds a server.
func (s *Store) CreateNode(n *Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n.CreatedAt == 0 {
		n.CreatedAt = time.Now().Unix()
	}
	if n.Role == "" {
		n.Role = RoleServer
	}
	res, err := s.DB.Exec(`INSERT INTO nodes(name,address,agent_port,fingerprint,secret,enabled,cert_sha256,created_at,role,relay_target,relay_only,relay_shift,relay_mode,relay_port,relay_secret,domain,cert_real,bridge_json,bridge_priv,bridge_dns_priv) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.Name, n.Address, n.AgentPort, n.Fingerprint, n.Secret, b2i(n.Enabled), n.CertSHA256, n.CreatedAt, n.Role, n.RelayTarget, b2i(n.RelayOnly), n.RelayShift,
		or(n.RelayMode, RelayForward), n.RelayPort, n.RelaySecret, n.Domain, b2i(n.RealCert), n.BridgeJSON, n.BridgePriv, n.BridgeDNSPriv)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("a server named %q already exists", n.Name)
		}
		return err
	}
	n.ID, _ = res.LastInsertId()
	return nil
}

// UpdateNode saves a server's settings (not its traffic sequence).
func (s *Store) UpdateNode(n *Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n.Role == "" {
		n.Role = RoleServer
	}
	_, err := s.DB.Exec(`UPDATE nodes SET name=?,address=?,agent_port=?,fingerprint=?,secret=?,enabled=?,cert_sha256=?,last_seen=?,role=?,relay_target=?,relay_only=?,relay_shift=?,relay_mode=?,relay_port=?,relay_secret=?,domain=?,cert_real=?,bridge_json=?,bridge_priv=?,bridge_dns_priv=? WHERE id=?`,
		n.Name, n.Address, n.AgentPort, n.Fingerprint, n.Secret, b2i(n.Enabled), n.CertSHA256, n.LastSeen, n.Role, n.RelayTarget, b2i(n.RelayOnly), n.RelayShift,
		or(n.RelayMode, RelayForward), n.RelayPort, n.RelaySecret, n.Domain, b2i(n.RealCert), n.BridgeJSON, n.BridgePriv, n.BridgeDNSPriv, n.ID)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("a server named %q already exists", n.Name)
	}
	return err
}

// DeleteNode removes a server and the protocols that ran on it.
func (s *Store) DeleteNode(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM inbounds WHERE node_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM nodes WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetNodeEpoch starts counting a node's traffic batches again: the node was
// installed anew, so its sequence restarts at 1.
func (s *Store) SetNodeEpoch(id int64, epoch string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`UPDATE nodes SET epoch=?, stats_seq=0 WHERE id=?`, epoch, id)
	return err
}

// AddNodeTraffic applies one traffic batch from a node exactly once: batches
// carry an increasing sequence, and one at or below the last applied is
// skipped (the node sends it again when our acknowledgement was lost).
// It reports whether the batch was new.
func (s *Store) AddNodeTraffic(nodeID, seq int64, now time.Time, users map[int64]Traffic, inbounds map[int64]Traffic) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE nodes SET stats_seq=?, last_seen=? WHERE id=? AND stats_seq<?`, seq, now.Unix(), nodeID, seq)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if err := addTraffic(tx, now, users, inbounds); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// ---------------------------------------------------------------- node side

// ReplaceInbounds makes the inbound table equal to list, keeping the given ids
// (a node stores its panel's inbounds under the panel's ids).
func (s *Store) ReplaceInbounds(list []*Inbound) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := map[int64]bool{}
	for _, in := range list {
		keep[in.ID] = true
		p, _ := json.Marshal(in.Params)
		if in.CreatedAt == 0 {
			in.CreatedAt = time.Now().Unix()
		}
		// counters (up, down) stay as they are for an inbound that already exists
		if _, err := tx.Exec(`INSERT INTO inbounds(id,preset,remark,port,enabled,params,created_at,node_id) VALUES(?,?,?,?,?,?,?,0)
			ON CONFLICT(id) DO UPDATE SET preset=excluded.preset, remark=excluded.remark, port=excluded.port,
			enabled=excluded.enabled, params=excluded.params`,
			in.ID, in.Preset, in.Remark, in.Port, b2i(in.Enabled), string(p), in.CreatedAt); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT id FROM inbounds`)
	if err != nil {
		return err
	}
	var gone []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil && !keep[id] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	for _, id := range gone {
		if _, err := tx.Exec(`DELETE FROM inbounds WHERE id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SyncUsers makes the user table equal to list, matched by name. Users are kept
// without limits (the panel decides who may connect) and their counters stay.
func (s *Store) SyncUsers(list []*User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	keep := map[string]bool{}
	now := time.Now().Unix()
	for _, u := range list {
		keep[u.Name] = true
		ids, _ := json.Marshal(nonNil(u.InboundIDs))
		if _, err := tx.Exec(`INSERT INTO users(name,uuid,password,sub_token,enabled,inbound_ids,wg_private,wg_public,wg_index,created_at)
			VALUES(?,?,?,?,1,?,?,?,?,?)
			ON CONFLICT(name) DO UPDATE SET uuid=excluded.uuid, password=excluded.password, enabled=1,
			data_limit=0, expire_at=0, duration_days=0, paused_until=0, ip_limit=0, reset_period='none',
			inbound_ids=excluded.inbound_ids, wg_private=excluded.wg_private, wg_public=excluded.wg_public, wg_index=excluded.wg_index`,
			u.Name, u.UUID, u.Password, "node-"+u.Name, string(ids), u.WGPrivate, u.WGPublic, u.WGIndex, now); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT id, name FROM users`)
	if err != nil {
		return err
	}
	var gone []int64
	for rows.Next() {
		var id int64
		var name string
		if rows.Scan(&id, &name) == nil && !keep[name] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	for _, id := range gone {
		// keep the counters' row out of the way: a user who comes back starts from zero
		if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func or(v, def string) string {
	if v != "" {
		return v
	}
	return def
}
