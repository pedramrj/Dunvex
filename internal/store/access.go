package store

import "database/sql"

// Session is one signed-in browser of an admin. The cookie carries its id, so
// a session can be listed and signed out from another device.
type Session struct {
	ID        string `json:"id"`
	AdminID   int64  `json:"adminId"`
	Admin     string `json:"admin"`
	IP        string `json:"ip"`
	Agent     string `json:"agent"`
	CreatedAt int64  `json:"createdAt"`
	LastSeen  int64  `json:"lastSeen"`
	ExpiresAt int64  `json:"expiresAt"`
}

// APIKey lets a script use the panel API as one admin. Only a hash is kept;
// the key itself is shown once, when it is made.
type APIKey struct {
	ID        int64  `json:"id"`
	AdminID   int64  `json:"adminId"`
	Admin     string `json:"admin"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"` // first characters, to recognise the key
	Hash      string `json:"-"`
	ReadOnly  bool   `json:"readOnly"`
	CreatedAt int64  `json:"createdAt"`
	LastUsed  int64  `json:"lastUsed"`
	ExpiresAt int64  `json:"expiresAt"` // 0 = never
}

var accessTables = []string{
	`CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		admin_id INTEGER NOT NULL,
		ip TEXT NOT NULL DEFAULT '',
		agent TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		last_seen INTEGER NOT NULL,
		expires_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS sessions_admin ON sessions(admin_id)`,
	`CREATE TABLE IF NOT EXISTS api_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		admin_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		prefix TEXT NOT NULL,
		hash TEXT NOT NULL UNIQUE,
		read_only INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		last_used INTEGER NOT NULL DEFAULT 0,
		expires_at INTEGER NOT NULL DEFAULT 0
	)`,
}

// CreateSession stores a new session.
func (s *Store) CreateSession(x *Session) error {
	_, err := s.DB.Exec(`INSERT INTO sessions (id, admin_id, ip, agent, created_at, last_seen, expires_at) VALUES (?,?,?,?,?,?,?)`,
		x.ID, x.AdminID, x.IP, x.Agent, x.CreatedAt, x.LastSeen, x.ExpiresAt)
	return err
}

const sessionCols = `s.id, s.admin_id, COALESCE(a.username, ''), s.ip, s.agent, s.created_at, s.last_seen, s.expires_at`

func scanSession(sc interface{ Scan(...any) error }) (*Session, error) {
	x := &Session{}
	err := sc.Scan(&x.ID, &x.AdminID, &x.Admin, &x.IP, &x.Agent, &x.CreatedAt, &x.LastSeen, &x.ExpiresAt)
	return x, err
}

// SessionByID returns one session.
func (s *Store) SessionByID(id string) (*Session, error) {
	return scanSession(s.DB.QueryRow(`SELECT `+sessionCols+` FROM sessions s LEFT JOIN admins a ON a.id = s.admin_id WHERE s.id = ?`, id))
}

// Sessions lists live sessions, newest activity first; adminID 0 lists everyone's.
func (s *Store) Sessions(adminID, now int64) ([]*Session, error) {
	q := `SELECT ` + sessionCols + ` FROM sessions s LEFT JOIN admins a ON a.id = s.admin_id WHERE s.expires_at > ?`
	args := []any{now}
	if adminID > 0 {
		q += ` AND s.admin_id = ?`
		args = append(args, adminID)
	}
	rows, err := s.DB.Query(q+` ORDER BY s.last_seen DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Session{}
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// TouchSession records activity on a session.
func (s *Store) TouchSession(id, ip string, at int64) error {
	_, err := s.DB.Exec(`UPDATE sessions SET last_seen = ?, ip = ? WHERE id = ?`, at, ip, id)
	return err
}

// DeleteSession signs one session out.
func (s *Store) DeleteSession(id string) error {
	_, err := s.DB.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// DeleteSessions signs out every session of an admin except keep ("" = all).
func (s *Store) DeleteSessions(adminID int64, keep string) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM sessions WHERE admin_id = ? AND id != ?`, adminID, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneSessions forgets sessions that ended before `before`.
func (s *Store) PruneSessions(before int64) {
	s.DB.Exec(`DELETE FROM sessions WHERE expires_at < ?`, before)
}

// CreateAPIKey stores a new key (its hash).
func (s *Store) CreateAPIKey(k *APIKey) error {
	res, err := s.DB.Exec(`INSERT INTO api_keys (admin_id, name, prefix, hash, read_only, created_at, expires_at) VALUES (?,?,?,?,?,?,?)`,
		k.AdminID, k.Name, k.Prefix, k.Hash, b2i(k.ReadOnly), k.CreatedAt, k.ExpiresAt)
	if err != nil {
		return err
	}
	k.ID, _ = res.LastInsertId()
	return nil
}

const keyCols = `k.id, k.admin_id, COALESCE(a.username, ''), k.name, k.prefix, k.hash, k.read_only, k.created_at, k.last_used, k.expires_at`

func scanKey(sc interface{ Scan(...any) error }) (*APIKey, error) {
	k := &APIKey{}
	var ro int
	err := sc.Scan(&k.ID, &k.AdminID, &k.Admin, &k.Name, &k.Prefix, &k.Hash, &ro, &k.CreatedAt, &k.LastUsed, &k.ExpiresAt)
	k.ReadOnly = ro == 1
	return k, err
}

// APIKeyByHash finds the key with this hash.
func (s *Store) APIKeyByHash(hash string) (*APIKey, error) {
	return scanKey(s.DB.QueryRow(`SELECT `+keyCols+` FROM api_keys k LEFT JOIN admins a ON a.id = k.admin_id WHERE k.hash = ?`, hash))
}

// APIKeys lists keys, newest first; adminID 0 lists everyone's.
func (s *Store) APIKeys(adminID int64) ([]*APIKey, error) {
	q := `SELECT ` + keyCols + ` FROM api_keys k LEFT JOIN admins a ON a.id = k.admin_id`
	var args []any
	if adminID > 0 {
		q += ` WHERE k.admin_id = ?`
		args = append(args, adminID)
	}
	rows, err := s.DB.Query(q+` ORDER BY k.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIKey{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// APIKeyByID returns one key.
func (s *Store) APIKeyByID(id int64) (*APIKey, error) {
	return scanKey(s.DB.QueryRow(`SELECT `+keyCols+` FROM api_keys k LEFT JOIN admins a ON a.id = k.admin_id WHERE k.id = ?`, id))
}

// TouchAPIKey records a use of the key.
func (s *Store) TouchAPIKey(id, at int64) error {
	_, err := s.DB.Exec(`UPDATE api_keys SET last_used = ? WHERE id = ?`, at, id)
	return err
}

// DeleteAPIKey removes a key.
func (s *Store) DeleteAPIKey(id int64) error {
	res, err := s.DB.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
