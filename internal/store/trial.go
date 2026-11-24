package store

import "database/sql"

// Trial is a free trial the bot gave: one per Telegram account (a private
// chat's id is the person's id), kept after the trial user is gone so the
// same account cannot take another.
type Trial struct {
	Chat   int64  `json:"chat"`
	UserID int64  `json:"userId"`
	Name   string `json:"name"`
	At     int64  `json:"at"`
	Ends   int64  `json:"ends"`
	Bought bool   `json:"bought"` // an order from this account (or for this user) went through
}

var trialTables = []string{
	`CREATE TABLE IF NOT EXISTS trials (
		chat INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL DEFAULT 0,
		name TEXT NOT NULL DEFAULT '',
		at INTEGER NOT NULL,
		ends INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS trials_at ON trials(at)`,
}

// ClaimTrial records that chat takes its trial now; false when it already had one.
func (s *Store) ClaimTrial(chat, at int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO trials(chat, at) VALUES(?, ?)`, chat, at)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// SetTrialUser fills in the user a claimed trial made.
func (s *Store) SetTrialUser(chat, userID int64, name string, ends int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`UPDATE trials SET user_id=?, name=?, ends=? WHERE chat=?`, userID, name, ends, chat)
	return err
}

// DropTrial forgets a claim whose user could not be made, so the person can try again.
func (s *Store) DropTrial(chat int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.DB.Exec(`DELETE FROM trials WHERE chat=? AND user_id=0`, chat)
}

// HasTrial says whether chat already had its trial.
func (s *Store) HasTrial(chat int64) bool {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM trials WHERE chat=?`, chat).Scan(&n)
	return n > 0
}

// TrialsSince counts the trials given since at.
func (s *Store) TrialsSince(at int64) int {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM trials WHERE at >= ?`, at).Scan(&n)
	return n
}

const trialCols = `t.chat, t.user_id, t.name, t.at, t.ends,
	EXISTS(SELECT 1 FROM orders o WHERE o.done_at > 0 AND (o.tg_chat = t.chat OR (t.user_id > 0 AND o.user_id = t.user_id)))`

func scanTrials(rows *sql.Rows, err error) ([]Trial, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Trial{}
	for rows.Next() {
		var t Trial
		if err := rows.Scan(&t.Chat, &t.UserID, &t.Name, &t.At, &t.Ends, &t.Bought); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Trials is the newest trials (limit 0 = all).
func (s *Store) Trials(limit int) ([]Trial, error) {
	q := `SELECT ` + trialCols + ` FROM trials t ORDER BY t.at DESC`
	if limit > 0 {
		return scanTrials(s.DB.Query(q+` LIMIT ?`, limit))
	}
	return scanTrials(s.DB.Query(q))
}

// TrialStats: trials given, how many in the last day since dayAgo, and how many bought a plan.
func (s *Store) TrialStats(dayAgo int64) (total, day, bought int) {
	s.DB.QueryRow(`SELECT COUNT(*), COALESCE(SUM(at >= ?), 0) FROM trials`, dayAgo).Scan(&total, &day)
	s.DB.QueryRow(`SELECT COUNT(*) FROM trials t WHERE EXISTS(SELECT 1 FROM orders o WHERE o.done_at > 0 AND
		(o.tg_chat = t.chat OR (t.user_id > 0 AND o.user_id = t.user_id)))`).Scan(&bought)
	return
}

// TrialUsers maps the users that are free trials to when their trial ends.
func (s *Store) TrialUsers() map[int64]int64 {
	out := map[int64]int64{}
	rows, err := s.DB.Query(`SELECT user_id, ends FROM trials WHERE user_id > 0`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, ends int64
		if rows.Scan(&id, &ends) == nil {
			out[id] = ends
		}
	}
	return out
}
