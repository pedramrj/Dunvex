// Package store keeps all Dunvex state in a single SQLite file.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database.
type Store struct {
	DB *sql.DB
	mu sync.Mutex // serialises writes; SQLite allows one writer

	OnLog func(kind, msg string) // called for every logged event
}

// Open opens (and migrates) the database at path.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS admins (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		uuid TEXT NOT NULL,
		password TEXT NOT NULL,
		sub_token TEXT NOT NULL UNIQUE,
		enabled INTEGER NOT NULL DEFAULT 1,
		data_limit INTEGER NOT NULL DEFAULT 0,
		used_up INTEGER NOT NULL DEFAULT 0,
		used_down INTEGER NOT NULL DEFAULT 0,
		lifetime_used INTEGER NOT NULL DEFAULT 0,
		expire_at INTEGER NOT NULL DEFAULT 0,
		duration_days INTEGER NOT NULL DEFAULT 0,
		first_use_at INTEGER NOT NULL DEFAULT 0,
		reset_period TEXT NOT NULL DEFAULT 'none',
		last_reset_at INTEGER NOT NULL DEFAULT 0,
		inbound_ids TEXT NOT NULL DEFAULT '[]',
		note TEXT NOT NULL DEFAULT '',
		wg_private TEXT NOT NULL DEFAULT '',
		wg_public TEXT NOT NULL DEFAULT '',
		wg_index INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		last_online_at INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS inbounds (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		preset TEXT NOT NULL,
		remark TEXT NOT NULL DEFAULT '',
		port INTEGER NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		params TEXT NOT NULL DEFAULT '{}',
		up INTEGER NOT NULL DEFAULT 0,
		down INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS usage (
		user_id INTEGER NOT NULL,
		hour INTEGER NOT NULL,
		up INTEGER NOT NULL DEFAULT 0,
		down INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (user_id, hour)
	)`,
	`CREATE INDEX IF NOT EXISTS usage_hour ON usage(hour)`,
	`CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		at INTEGER NOT NULL,
		kind TEXT NOT NULL,
		message TEXT NOT NULL
	)`,
}

// columns added after the first release
var addedColumns = []struct{ table, column, def string }{
	{"users", "ip_limit", "INTEGER NOT NULL DEFAULT 0"},
	{"users", "paused_until", "INTEGER NOT NULL DEFAULT 0"},
	{"users", "owner_id", "INTEGER NOT NULL DEFAULT 0"},
	{"users", "tg_chat", "INTEGER NOT NULL DEFAULT 0"},
	{"users", "notified", "TEXT NOT NULL DEFAULT ''"},
	{"admins", "role", "TEXT NOT NULL DEFAULT 'owner'"},
	{"admins", "totp_secret", "TEXT NOT NULL DEFAULT ''"},
	{"admins", "max_users", "INTEGER NOT NULL DEFAULT 0"},
	{"admins", "max_data", "INTEGER NOT NULL DEFAULT 0"},
	{"admins", "enabled", "INTEGER NOT NULL DEFAULT 1"},
}

func (s *Store) migrate() error {
	for _, m := range migrations {
		if _, err := s.DB.Exec(m); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	for _, c := range addedColumns {
		if s.hasColumn(c.table, c.column) {
			continue
		}
		if _, err := s.DB.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, c.table, c.column, c.def)); err != nil {
			return fmt.Errorf("migrate %s.%s: %w", c.table, c.column, err)
		}
	}
	return nil
}

func (s *Store) hasColumn(table, column string) bool {
	rows, err := s.DB.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && n == column {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- settings

// GetSetting returns a setting or def when missing.
func (s *Store) GetSetting(key, def string) string {
	var v string
	if err := s.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

// SetSetting upserts a setting.
func (s *Store) SetSetting(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// AllSettings returns every setting.
func (s *Store) AllSettings() map[string]string {
	out := map[string]string{}
	rows, err := s.DB.Query(`SELECT key,value FROM settings`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			out[k] = v
		}
	}
	return out
}

// ---------------------------------------------------------------- admins

// Admin roles.
const (
	RoleOwner    = "owner"    // everything
	RoleAdmin    = "admin"    // users and protocols, no server settings
	RoleReseller = "reseller" // only the users they created, within a quota
)

// Admin is a panel administrator.
type Admin struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	CreatedAt    int64  `json:"createdAt"`
	Role         string `json:"role"`
	TOTPSecret   string `json:"-"`
	TOTP         bool   `json:"totp"`
	MaxUsers     int    `json:"maxUsers"`
	MaxData      int64  `json:"maxData"` // bytes a reseller may hand out in total (0 = unlimited)
	Enabled      bool   `json:"enabled"`
}

// Can reports whether the admin's role reaches the given role.
func (a *Admin) Can(role string) bool {
	rank := map[string]int{RoleReseller: 1, RoleAdmin: 2, RoleOwner: 3}
	return rank[a.Role] >= rank[role]
}

const adminCols = `id,username,password_hash,created_at,role,totp_secret,max_users,max_data,enabled`

func scanAdmin(r scanner) (*Admin, error) {
	a := &Admin{}
	var enabled int
	if err := r.Scan(&a.ID, &a.Username, &a.PasswordHash, &a.CreatedAt, &a.Role, &a.TOTPSecret, &a.MaxUsers, &a.MaxData, &enabled); err != nil {
		return nil, err
	}
	a.Enabled, a.TOTP = enabled == 1, a.TOTPSecret != ""
	if a.Role == "" {
		a.Role = RoleOwner
	}
	return a, nil
}

func (s *Store) oneAdmin(where string, args ...any) (*Admin, error) {
	a, err := scanAdmin(s.DB.QueryRow(`SELECT `+adminCols+` FROM admins `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// AdminCount returns the number of admins.
func (s *Store) AdminCount() int {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&n)
	return n
}

// AdminByName finds an admin.
func (s *Store) AdminByName(name string) (*Admin, error) { return s.oneAdmin(`WHERE username=?`, name) }

// AdminByID finds an admin.
func (s *Store) AdminByID(id int64) (*Admin, error) { return s.oneAdmin(`WHERE id=?`, id) }

// FirstAdmin returns the oldest owner.
func (s *Store) FirstAdmin() (*Admin, error) {
	return s.oneAdmin(`WHERE role='owner' ORDER BY id LIMIT 1`)
}

// Admins lists every admin.
func (s *Store) Admins() ([]*Admin, error) {
	rows, err := s.DB.Query(`SELECT ` + adminCols + ` FROM admins ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Admin{}
	for rows.Next() {
		a, err := scanAdmin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertAdmin creates an owner or replaces its credentials and turns 2FA off,
// so `dunvex admin` from SSH always gets the operator back in.
func (s *Store) UpsertAdmin(username, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`INSERT INTO admins(username,password_hash,created_at,role) VALUES(?,?,?,'owner')
		ON CONFLICT(username) DO UPDATE SET password_hash=excluded.password_hash, totp_secret='', enabled=1, role='owner'`,
		username, hash, time.Now().Unix())
	return err
}

// CreateAdmin inserts a new admin.
func (s *Store) CreateAdmin(a *Admin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.CreatedAt = time.Now().Unix()
	res, err := s.DB.Exec(`INSERT INTO admins(username,password_hash,created_at,role,max_users,max_data,enabled) VALUES(?,?,?,?,?,?,?)`,
		a.Username, a.PasswordHash, a.CreatedAt, a.Role, a.MaxUsers, a.MaxData, b2i(a.Enabled))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("an admin named %q already exists", a.Username)
		}
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

// SaveAdmin stores every field of an admin.
func (s *Store) SaveAdmin(a *Admin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`UPDATE admins SET username=?,password_hash=?,role=?,totp_secret=?,max_users=?,max_data=?,enabled=? WHERE id=?`,
		a.Username, a.PasswordHash, a.Role, a.TOTPSecret, a.MaxUsers, a.MaxData, b2i(a.Enabled), a.ID)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("an admin named %q already exists", a.Username)
	}
	return err
}

// UpdateAdmin changes username and/or password of an admin.
func (s *Store) UpdateAdmin(id int64, username, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`UPDATE admins SET username=?, password_hash=? WHERE id=?`, username, hash, id)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("an admin named %q already exists", username)
	}
	return err
}

// DeleteAdmin removes an admin; their users pass to the owner.
func (s *Store) DeleteAdmin(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	tx.Exec(`UPDATE users SET owner_id=0 WHERE owner_id=?`, id)
	tx.Exec(`DELETE FROM admins WHERE id=?`, id)
	return tx.Commit()
}

// ---------------------------------------------------------------- users

// User is a VPN subscriber.
type User struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	UUID         string  `json:"uuid"`
	Password     string  `json:"password"`
	SubToken     string  `json:"subToken"`
	Enabled      bool    `json:"enabled"`
	DataLimit    int64   `json:"dataLimit"`
	UsedUp       int64   `json:"usedUp"`
	UsedDown     int64   `json:"usedDown"`
	LifetimeUsed int64   `json:"lifetimeUsed"`
	ExpireAt     int64   `json:"expireAt"`
	DurationDays int     `json:"durationDays"`
	FirstUseAt   int64   `json:"firstUseAt"`
	ResetPeriod  string  `json:"resetPeriod"`
	LastResetAt  int64   `json:"lastResetAt"`
	InboundIDs   []int64 `json:"inboundIds"`
	Note         string  `json:"note"`
	WGPrivate    string  `json:"-"`
	WGPublic     string  `json:"-"`
	WGIndex      int     `json:"-"`
	CreatedAt    int64   `json:"createdAt"`
	LastOnlineAt int64   `json:"lastOnlineAt"`
	IPLimit      int     `json:"ipLimit"`     // devices (IPs) at once, 0 = unlimited
	PausedUntil  int64   `json:"pausedUntil"` // set when the user went over IPLimit
	OwnerID      int64   `json:"ownerId"`     // admin who created the user (0 = owner)
	TGChat       int64   `json:"-"`           // Telegram chat bound to this user
	Notified     string  `json:"-"`           // warnings already sent (comma list)

	// ResetUsage makes the next UpdateUser zero the period counters. Without
	// it UpdateUser leaves them alone, so traffic counted meanwhile is kept.
	ResetUsage bool `json:"-"`
}

// Used returns total consumed bytes for the current period.
func (u *User) Used() int64 { return u.UsedUp + u.UsedDown }

// User statuses.
const (
	StatusActive   = "active"
	StatusPending  = "pending" // waits for first connection to start its timer
	StatusLimited  = "limited"
	StatusExpired  = "expired"
	StatusDisabled = "disabled"
	StatusPaused   = "paused" // too many devices at once; back after a short pause
)

// Status computes the user's current status.
func (u *User) Status(now int64) string {
	switch {
	case !u.Enabled:
		return StatusDisabled
	case u.DataLimit > 0 && u.Used() >= u.DataLimit:
		return StatusLimited
	case u.ExpireAt > 0 && now >= u.ExpireAt:
		return StatusExpired
	case u.PausedUntil > now:
		return StatusPaused
	case u.ExpireAt == 0 && u.DurationDays > 0 && u.FirstUseAt == 0:
		return StatusPending
	}
	return StatusActive
}

// CanConnect reports whether the user should be present in core configs.
func (u *User) CanConnect(now int64) bool {
	st := u.Status(now)
	return st == StatusActive || st == StatusPending
}

// AllowsInbound reports whether the user may use the given inbound.
func (u *User) AllowsInbound(id int64) bool {
	if len(u.InboundIDs) == 0 {
		return true
	}
	for _, x := range u.InboundIDs {
		if x == id {
			return true
		}
	}
	return false
}

const userCols = `id,name,uuid,password,sub_token,enabled,data_limit,used_up,used_down,lifetime_used,expire_at,duration_days,
	first_use_at,reset_period,last_reset_at,inbound_ids,note,wg_private,wg_public,wg_index,created_at,last_online_at,
	ip_limit,paused_until,owner_id,tg_chat,notified`

type scanner interface{ Scan(dest ...any) error }

func scanUser(r scanner) (*User, error) {
	u := &User{}
	var enabled int
	var ids string
	err := r.Scan(&u.ID, &u.Name, &u.UUID, &u.Password, &u.SubToken, &enabled, &u.DataLimit, &u.UsedUp, &u.UsedDown,
		&u.LifetimeUsed, &u.ExpireAt, &u.DurationDays, &u.FirstUseAt, &u.ResetPeriod, &u.LastResetAt, &ids, &u.Note,
		&u.WGPrivate, &u.WGPublic, &u.WGIndex, &u.CreatedAt, &u.LastOnlineAt,
		&u.IPLimit, &u.PausedUntil, &u.OwnerID, &u.TGChat, &u.Notified)
	if err != nil {
		return nil, err
	}
	u.Enabled = enabled == 1
	_ = json.Unmarshal([]byte(ids), &u.InboundIDs)
	if u.InboundIDs == nil {
		u.InboundIDs = []int64{}
	}
	return u, nil
}

// Users returns all users ordered by id.
func (s *Store) Users() ([]*User, error) {
	rows, err := s.DB.Query(`SELECT ` + userCols + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserByID loads a user.
func (s *Store) UserByID(id int64) (*User, error) {
	u, err := scanUser(s.DB.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// UserByTG finds the user bound to a Telegram chat.
func (s *Store) UserByTG(chat int64) (*User, error) {
	u, err := scanUser(s.DB.QueryRow(`SELECT `+userCols+` FROM users WHERE tg_chat=? AND tg_chat<>0`, chat))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// UserByName finds a user by name.
func (s *Store) UserByName(name string) (*User, error) {
	u, err := scanUser(s.DB.QueryRow(`SELECT `+userCols+` FROM users WHERE name=?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// UserByToken loads a user by subscription token.
func (s *Store) UserByToken(token string) (*User, error) {
	u, err := scanUser(s.DB.QueryRow(`SELECT `+userCols+` FROM users WHERE sub_token=?`, token))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

// NextWGIndex returns the next free WireGuard address index.
func (s *Store) NextWGIndex() int {
	var n sql.NullInt64
	s.DB.QueryRow(`SELECT MAX(wg_index) FROM users`).Scan(&n)
	if !n.Valid || n.Int64 < 1 {
		return 2
	}
	return int(n.Int64) + 1
}

// CreateUser inserts a user and fills its ID.
func (s *Store) CreateUser(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, _ := json.Marshal(nonNil(u.InboundIDs))
	if u.CreatedAt == 0 {
		u.CreatedAt = time.Now().Unix()
	}
	if u.ResetPeriod == "" {
		u.ResetPeriod = "none"
	}
	res, err := s.DB.Exec(`INSERT INTO users(name,uuid,password,sub_token,enabled,data_limit,used_up,used_down,lifetime_used,
		expire_at,duration_days,first_use_at,reset_period,last_reset_at,inbound_ids,note,wg_private,wg_public,wg_index,created_at,last_online_at,
		ip_limit,paused_until,owner_id,tg_chat,notified)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.Name, u.UUID, u.Password, u.SubToken, b2i(u.Enabled), u.DataLimit, u.UsedUp, u.UsedDown, u.LifetimeUsed,
		u.ExpireAt, u.DurationDays, u.FirstUseAt, u.ResetPeriod, u.LastResetAt, string(ids), u.Note,
		u.WGPrivate, u.WGPublic, u.WGIndex, u.CreatedAt, u.LastOnlineAt,
		u.IPLimit, u.PausedUntil, u.OwnerID, u.TGChat, u.Notified)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("a user named %q already exists", u.Name)
		}
		return err
	}
	u.ID, _ = res.LastInsertId()
	return nil
}

// UpdateUser saves every editable field of a user.
func (s *Store) UpdateUser(u *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, _ := json.Marshal(nonNil(u.InboundIDs))
	reset := b2i(u.ResetUsage)
	u.ResetUsage = false
	_, err := s.DB.Exec(`UPDATE users SET name=?,uuid=?,password=?,sub_token=?,enabled=?,data_limit=?,
		used_up=CASE WHEN ?=1 THEN 0 ELSE used_up END, used_down=CASE WHEN ?=1 THEN 0 ELSE used_down END,
		expire_at=?,duration_days=?,first_use_at=?,reset_period=?,last_reset_at=?,inbound_ids=?,note=?,
		ip_limit=?,paused_until=?,owner_id=?,tg_chat=?,notified=? WHERE id=?`,
		u.Name, u.UUID, u.Password, u.SubToken, b2i(u.Enabled), u.DataLimit, reset, reset,
		u.ExpireAt, u.DurationDays, u.FirstUseAt, u.ResetPeriod, u.LastResetAt, string(ids), u.Note,
		u.IPLimit, u.PausedUntil, u.OwnerID, u.TGChat, u.Notified, u.ID)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("a user named %q already exists", u.Name)
	}
	return err
}

// DeleteUser removes a user and its usage history.
func (s *Store) DeleteUser(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	tx.Exec(`DELETE FROM usage WHERE user_id=?`, id)
	tx.Exec(`DELETE FROM users WHERE id=?`, id)
	return tx.Commit()
}

// Traffic is a byte delta for one user or inbound.
type Traffic struct{ Up, Down int64 }

// AddTraffic applies collected deltas in one transaction.
// users maps user id → delta, inbounds maps inbound id → delta.
func (s *Store) AddTraffic(now time.Time, users map[int64]Traffic, inbounds map[int64]Traffic) error {
	if len(users) == 0 && len(inbounds) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	hour := now.Unix() / 3600
	for id, t := range users {
		if t.Up == 0 && t.Down == 0 {
			continue
		}
		if _, err := tx.Exec(`UPDATE users SET used_up=used_up+?, used_down=used_down+?, lifetime_used=lifetime_used+?,
			last_online_at=? WHERE id=?`, t.Up, t.Down, t.Up+t.Down, now.Unix(), id); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO usage(user_id,hour,up,down) VALUES(?,?,?,?)
			ON CONFLICT(user_id,hour) DO UPDATE SET up=up+excluded.up, down=down+excluded.down`, id, hour, t.Up, t.Down); err != nil {
			return err
		}
	}
	for id, t := range inbounds {
		if _, err := tx.Exec(`UPDATE inbounds SET up=up+?, down=down+? WHERE id=?`, t.Up, t.Down, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UsagePoint is one hourly bucket.
type UsagePoint struct {
	Hour int64 `json:"t"`
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// UsageSeries returns hourly totals since `since` (unix seconds). userID 0 = all users.
func (s *Store) UsageSeries(userID int64, since int64) ([]UsagePoint, error) {
	q := `SELECT hour, SUM(up), SUM(down) FROM usage WHERE hour>=?`
	args := []any{since / 3600}
	if userID > 0 {
		q += ` AND user_id=?`
		args = append(args, userID)
	}
	q += ` GROUP BY hour ORDER BY hour`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsagePoint{}
	for rows.Next() {
		var p UsagePoint
		if err := rows.Scan(&p.Hour, &p.Up, &p.Down); err != nil {
			return nil, err
		}
		p.Hour *= 3600
		out = append(out, p)
	}
	return out, rows.Err()
}

// TopUser is a ranking row.
type TopUser struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// TopUsers ranks users by traffic since `since`.
func (s *Store) TopUsers(since int64, limit int) ([]TopUser, error) {
	rows, err := s.DB.Query(`SELECT u.id, u.name, SUM(g.up+g.down) AS b FROM usage g JOIN users u ON u.id=g.user_id
		WHERE g.hour>=? GROUP BY u.id ORDER BY b DESC LIMIT ?`, since/3600, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TopUser{}
	for rows.Next() {
		var t TopUser
		if err := rows.Scan(&t.ID, &t.Name, &t.Bytes); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TotalSince returns total traffic of all users since `since`.
func (s *Store) TotalSince(since int64) (up, down int64) {
	s.DB.QueryRow(`SELECT COALESCE(SUM(up),0), COALESCE(SUM(down),0) FROM usage WHERE hour>=?`, since/3600).Scan(&up, &down)
	return
}

// PruneUsage drops hourly rows older than `before`.
func (s *Store) PruneUsage(before int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.DB.Exec(`DELETE FROM usage WHERE hour<?`, before/3600)
	s.DB.Exec(`DELETE FROM events WHERE at<?`, before)
}

// ---------------------------------------------------------------- inbounds

// Inbound is one listening protocol instance.
type Inbound struct {
	ID        int64          `json:"id"`
	Preset    string         `json:"preset"`
	Remark    string         `json:"remark"`
	Port      int            `json:"port"`
	Enabled   bool           `json:"enabled"`
	Params    map[string]any `json:"params"`
	Up        int64          `json:"up"`
	Down      int64          `json:"down"`
	CreatedAt int64          `json:"createdAt"`
}

// Tag is the core-side tag of the inbound.
func (in *Inbound) Tag() string { return fmt.Sprintf("in-%d", in.ID) }

// Str returns a string parameter.
func (in *Inbound) Str(key string) string {
	if v, ok := in.Params[key]; ok {
		switch x := v.(type) {
		case string:
			return x
		case float64:
			return fmt.Sprintf("%v", x)
		case bool:
			if x {
				return "true"
			}
			return "false"
		}
	}
	return ""
}

// Bool returns a boolean parameter.
func (in *Inbound) Bool(key string) bool {
	switch x := in.Params[key].(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1"
	case float64:
		return x != 0
	}
	return false
}

// Int returns an integer parameter.
func (in *Inbound) Int(key string) int {
	switch x := in.Params[key].(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		var n int
		fmt.Sscanf(x, "%d", &n)
		return n
	}
	return 0
}

func scanInbound(r scanner) (*Inbound, error) {
	in := &Inbound{}
	var enabled int
	var params string
	if err := r.Scan(&in.ID, &in.Preset, &in.Remark, &in.Port, &enabled, &params, &in.Up, &in.Down, &in.CreatedAt); err != nil {
		return nil, err
	}
	in.Enabled = enabled == 1
	in.Params = map[string]any{}
	_ = json.Unmarshal([]byte(params), &in.Params)
	return in, nil
}

const inboundCols = `id,preset,remark,port,enabled,params,up,down,created_at`

// Inbounds returns all inbounds.
func (s *Store) Inbounds() ([]*Inbound, error) {
	rows, err := s.DB.Query(`SELECT ` + inboundCols + ` FROM inbounds ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Inbound
	for rows.Next() {
		in, err := scanInbound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// InboundByID loads an inbound.
func (s *Store) InboundByID(id int64) (*Inbound, error) {
	in, err := scanInbound(s.DB.QueryRow(`SELECT `+inboundCols+` FROM inbounds WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return in, err
}

// CreateInbound inserts an inbound.
func (s *Store) CreateInbound(in *Inbound) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, _ := json.Marshal(in.Params)
	if in.CreatedAt == 0 {
		in.CreatedAt = time.Now().Unix()
	}
	res, err := s.DB.Exec(`INSERT INTO inbounds(preset,remark,port,enabled,params,created_at) VALUES(?,?,?,?,?,?)`,
		in.Preset, in.Remark, in.Port, b2i(in.Enabled), string(p), in.CreatedAt)
	if err != nil {
		return err
	}
	in.ID, _ = res.LastInsertId()
	return nil
}

// UpdateInbound saves an inbound.
func (s *Store) UpdateInbound(in *Inbound) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, _ := json.Marshal(in.Params)
	_, err := s.DB.Exec(`UPDATE inbounds SET remark=?,port=?,enabled=?,params=? WHERE id=?`,
		in.Remark, in.Port, b2i(in.Enabled), string(p), in.ID)
	return err
}

// DeleteInbound removes an inbound.
func (s *Store) DeleteInbound(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`DELETE FROM inbounds WHERE id=?`, id)
	return err
}

// ---------------------------------------------------------------- events

// Event is an activity log row.
type Event struct {
	ID      int64  `json:"id"`
	At      int64  `json:"at"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Log records an event (and passes it to OnLog, e.g. the Telegram bot).
func (s *Store) Log(kind, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.mu.Lock()
	s.DB.Exec(`INSERT INTO events(at,kind,message) VALUES(?,?,?)`, time.Now().Unix(), kind, msg)
	hook := s.OnLog
	s.mu.Unlock()
	if hook != nil {
		go hook(kind, msg)
	}
}

// Events returns the latest events.
func (s *Store) Events(limit int) []Event {
	out := []Event{}
	rows, err := s.DB.Query(`SELECT id,at,kind,message FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		if rows.Scan(&e.ID, &e.At, &e.Kind, &e.Message) == nil {
			out = append(out, e)
		}
	}
	return out
}

// Backup writes a consistent copy of the database to path.
func (s *Store) Backup(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`VACUUM INTO ?`, path)
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nonNil(v []int64) []int64 {
	if v == nil {
		return []int64{}
	}
	return v
}
