package store

import (
	"database/sql"
	"errors"
)

// Passkey is a WebAuthn credential an admin signs in with (a fingerprint, a
// face, a phone or a security key). Data is the credential as the WebAuthn
// library keeps it (public key, sign counter, flags), JSON; the panel never
// sees a private key.
type Passkey struct {
	ID         int64  `json:"id"`
	AdminID    int64  `json:"-"`
	CredID     string `json:"-"` // base64url, unique
	Data       string `json:"-"`
	Name       string `json:"name"`
	RPID       string `json:"rpId"` // the domain it was made for
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt"`
}

var passkeyTables = []string{
	`CREATE TABLE IF NOT EXISTS passkeys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		admin_id INTEGER NOT NULL,
		cred_id TEXT NOT NULL UNIQUE,
		data TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		rp_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		last_used_at INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS passkeys_admin ON passkeys(admin_id)`,
}

// MaxPasskeys is how many passkeys one admin may keep.
const MaxPasskeys = 20

// CreatePasskey stores a new passkey.
func (s *Store) CreatePasskey(p *Passkey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.DB.Exec(`INSERT INTO passkeys (admin_id, cred_id, data, name, rp_id, created_at, last_used_at) VALUES (?,?,?,?,?,?,?)`,
		p.AdminID, p.CredID, p.Data, p.Name, p.RPID, p.CreatedAt, p.LastUsedAt)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	return nil
}

const passkeyCols = `id, admin_id, cred_id, data, name, rp_id, created_at, last_used_at`

func scanPasskey(r interface{ Scan(...any) error }) (*Passkey, error) {
	p := &Passkey{}
	err := r.Scan(&p.ID, &p.AdminID, &p.CredID, &p.Data, &p.Name, &p.RPID, &p.CreatedAt, &p.LastUsedAt)
	return p, err
}

// PasskeysOf lists an admin's passkeys, the newest first.
func (s *Store) PasskeysOf(adminID int64) ([]*Passkey, error) {
	rows, err := s.DB.Query(`SELECT `+passkeyCols+` FROM passkeys WHERE admin_id=? ORDER BY id DESC`, adminID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Passkey{}
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PasskeyByCredID finds a passkey by its credential ID (base64url).
func (s *Store) PasskeyByCredID(credID string) (*Passkey, error) {
	p, err := scanPasskey(s.DB.QueryRow(`SELECT `+passkeyCols+` FROM passkeys WHERE cred_id=?`, credID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// UsePasskey keeps the credential's new state (sign counter, flags) after a sign-in.
func (s *Store) UsePasskey(id int64, data string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`UPDATE passkeys SET data=?, last_used_at=? WHERE id=?`, data, at, id)
	return err
}

// DeletePasskey removes one of an admin's passkeys; false when it is not theirs.
func (s *Store) DeletePasskey(id, adminID int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.DB.Exec(`DELETE FROM passkeys WHERE id=? AND admin_id=?`, id, adminID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
