package store

import (
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

// Support inbox: one thread per Telegram chat that wrote to the bot's
// support, with its messages (text and photos). The person's messages carry
// receipts (seen by an admin, answered); answers carry the admin who wrote
// them, which the person never sees.

// SupportThread is one person's conversation with support.
type SupportThread struct {
	ID        int64  `json:"id"`
	Chat      int64  `json:"chat"`      // the Telegram chat (a private chat's id is the person's id)
	UserID    int64  `json:"userId"`    // the subscription linked to the chat when it last wrote (0 = none)
	OwnerID   int64  `json:"ownerId"`   // that user's reseller (0 = the owner's)
	Name      string `json:"name"`      // Telegram first name
	Username  string `json:"username"`  // Telegram @username, without the @
	Open      bool   `json:"open"`      // closed threads open again when the person writes
	Unread    int    `json:"unread"`    // the person's messages no admin has seen yet
	LastAt    int64  `json:"lastAt"`    // the newest message
	LastText  string `json:"lastText"`  // its first characters ("📷" for a photo)
	LastStaff bool   `json:"lastStaff"` // the newest message is an answer
	CreatedAt int64  `json:"createdAt"`
	ClosedAt  int64  `json:"closedAt"`
	ClosedBy  string `json:"closedBy"`
	Messages  int    `json:"messages"` // messages kept
}

// Waiting says whether the person waits for an answer.
func (t *SupportThread) Waiting() bool { return t.Open && !t.LastStaff && t.LastAt > 0 }

// SupportMessage is one message of a thread.
type SupportMessage struct {
	ID       int64  `json:"id"`
	ThreadID int64  `json:"threadId"`
	At       int64  `json:"at"`
	Staff    bool   `json:"staff"`        // an answer (else the person wrote it)
	By       string `json:"by,omitempty"` // the admin who answered
	Text     string `json:"text"`
	Photo    string `json:"photo,omitempty"` // the file in the support folder
	TGMsg    int64  `json:"-"`               // its id in the person's chat (receipts are set on it)
	Seen     bool   `json:"seen"`            // the person's message: an admin opened the thread since
	Answered bool   `json:"answered"`        // the person's message: an answer went out since
}

var supportTables = []string{
	`CREATE TABLE IF NOT EXISTS support_threads (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		chat INTEGER NOT NULL UNIQUE,
		user_id INTEGER NOT NULL DEFAULT 0,
		owner_id INTEGER NOT NULL DEFAULT 0,
		name TEXT NOT NULL DEFAULT '',
		username TEXT NOT NULL DEFAULT '',
		open INTEGER NOT NULL DEFAULT 1,
		unread INTEGER NOT NULL DEFAULT 0,
		last_at INTEGER NOT NULL DEFAULT 0,
		last_text TEXT NOT NULL DEFAULT '',
		last_staff INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		closed_at INTEGER NOT NULL DEFAULT 0,
		closed_by TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS support_threads_last ON support_threads(last_at)`,
	`CREATE TABLE IF NOT EXISTS support_messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		thread_id INTEGER NOT NULL,
		at INTEGER NOT NULL,
		staff INTEGER NOT NULL DEFAULT 0,
		by_admin TEXT NOT NULL DEFAULT '',
		text TEXT NOT NULL DEFAULT '',
		photo TEXT NOT NULL DEFAULT '',
		tg_msg INTEGER NOT NULL DEFAULT 0,
		seen INTEGER NOT NULL DEFAULT 0,
		answered INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS support_messages_thread ON support_messages(thread_id, id)`,
	`CREATE INDEX IF NOT EXISTS support_messages_at ON support_messages(at)`,
}

// supportPreview is the start of a message, for the thread list.
func supportPreview(text, photo string) string {
	p := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(p) > 120 {
		p = string([]rune(p)[:120]) + "…"
	}
	if photo != "" {
		if p == "" {
			return "📷"
		}
		return "📷 " + p
	}
	return p
}

const threadCols = `t.id, t.chat, t.user_id, t.owner_id, t.name, t.username, t.open, t.unread, t.last_at, t.last_text,
	t.last_staff, t.created_at, t.closed_at, t.closed_by, (SELECT COUNT(*) FROM support_messages m WHERE m.thread_id = t.id)`

func scanThread(r scanner) (*SupportThread, error) {
	t := &SupportThread{}
	var open, staff int
	err := r.Scan(&t.ID, &t.Chat, &t.UserID, &t.OwnerID, &t.Name, &t.Username, &open, &t.Unread, &t.LastAt, &t.LastText,
		&staff, &t.CreatedAt, &t.ClosedAt, &t.ClosedBy, &t.Messages)
	if err != nil {
		return nil, err
	}
	t.Open, t.LastStaff = open == 1, staff == 1
	return t, nil
}

func (s *Store) oneThread(where string, args ...any) (*SupportThread, error) {
	t, err := scanThread(s.DB.QueryRow(`SELECT `+threadCols+` FROM support_threads t `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// SupportThreadByID loads a thread.
func (s *Store) SupportThreadByID(id int64) (*SupportThread, error) {
	return s.oneThread(`WHERE t.id=?`, id)
}

// SupportThreadByChat loads the thread of a Telegram chat.
func (s *Store) SupportThreadByChat(chat int64) (*SupportThread, error) {
	return s.oneThread(`WHERE t.chat=?`, chat)
}

// SupportIn is a message the person sent.
type SupportIn struct {
	Chat     int64
	UserID   int64
	OwnerID  int64
	Name     string
	Username string
	Text     string
	Photo    string
	TGMsg    int64
	At       int64
}

// SupportIncoming keeps a message from the person: it makes their thread on
// the first one, opens it again when it was closed and counts it unread.
func (s *Store) SupportIncoming(in SupportIn) (*SupportThread, *SupportMessage, error) {
	s.mu.Lock()
	tx, err := s.DB.Begin()
	if err != nil {
		s.mu.Unlock()
		return nil, nil, err
	}
	defer tx.Rollback()
	// update first: an upsert would use up an id on every message
	var tid int64
	err = tx.QueryRow(`SELECT id FROM support_threads WHERE chat=?`, in.Chat).Scan(&tid)
	switch {
	case err == nil:
		_, err = tx.Exec(`UPDATE support_threads SET user_id=?, owner_id=?, name=CASE WHEN ?<>'' THEN ? ELSE name END,
			username=CASE WHEN ?<>'' THEN ? ELSE username END WHERE id=?`, in.UserID, in.OwnerID, in.Name, in.Name, in.Username, in.Username, tid)
	case errors.Is(err, sql.ErrNoRows):
		var res sql.Result
		res, err = tx.Exec(`INSERT INTO support_threads(chat, user_id, owner_id, name, username, created_at) VALUES(?,?,?,?,?,?)`,
			in.Chat, in.UserID, in.OwnerID, in.Name, in.Username, in.At)
		if err == nil {
			tid, _ = res.LastInsertId()
		}
	}
	if err != nil {
		s.mu.Unlock()
		return nil, nil, err
	}
	m := &SupportMessage{ThreadID: tid, At: in.At, Text: in.Text, Photo: in.Photo, TGMsg: in.TGMsg}
	res, err := tx.Exec(`INSERT INTO support_messages(thread_id, at, staff, text, photo, tg_msg) VALUES(?,?,0,?,?,?)`,
		tid, in.At, in.Text, in.Photo, in.TGMsg)
	if err != nil {
		s.mu.Unlock()
		return nil, nil, err
	}
	m.ID, _ = res.LastInsertId()
	if _, err := tx.Exec(`UPDATE support_threads SET open=1, closed_at=0, closed_by='', unread=unread+1, last_at=?, last_text=?,
		last_staff=0 WHERE id=?`, in.At, supportPreview(in.Text, in.Photo), tid); err != nil {
		s.mu.Unlock()
		return nil, nil, err
	}
	err = tx.Commit()
	s.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	t, err := s.SupportThreadByID(tid)
	return t, m, err
}

// pendingIDs are the Telegram ids of the person's messages still missing a
// receipt (field is "seen" or "answered"), the newest 50.
func pendingIDs(tx *sql.Tx, thread int64, field string) []int64 {
	rows, err := tx.Query(`SELECT tg_msg FROM support_messages WHERE thread_id=? AND staff=0 AND `+field+`=0 AND tg_msg>0
		ORDER BY id DESC LIMIT 50`, thread)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

// SupportMarkSeen marks the person's messages seen; it returns the Telegram
// ids of those that were not yet (for the 👀 receipt).
func (s *Store) SupportMarkSeen(thread int64) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil
	}
	defer tx.Rollback()
	ids := pendingIDs(tx, thread, "seen")
	tx.Exec(`UPDATE support_messages SET seen=1 WHERE thread_id=? AND staff=0 AND seen=0`, thread)
	tx.Exec(`UPDATE support_threads SET unread=0 WHERE id=?`, thread)
	if tx.Commit() != nil {
		return nil
	}
	return ids
}

// SupportAnswer keeps an answer that went out. Everything the person wrote
// before it is now seen and answered; it returns the Telegram ids of the
// messages that were waiting for an answer (for the 👌 receipt).
func (s *Store) SupportAnswer(thread int64, by, text, photo string, at int64) (*SupportMessage, []int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	ids := pendingIDs(tx, thread, "answered")
	res, err := tx.Exec(`INSERT INTO support_messages(thread_id, at, staff, by_admin, text, photo) VALUES(?,?,1,?,?,?)`, thread, at, by, text, photo)
	if err != nil {
		return nil, nil, err
	}
	m := &SupportMessage{ThreadID: thread, At: at, Staff: true, By: by, Text: text, Photo: photo}
	m.ID, _ = res.LastInsertId()
	tx.Exec(`UPDATE support_messages SET seen=1, answered=1 WHERE thread_id=? AND staff=0`, thread)
	tx.Exec(`UPDATE support_threads SET unread=0, last_at=?, last_text=?, last_staff=1 WHERE id=?`, at, supportPreview(text, photo), thread)
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return m, ids, nil
}

// SupportFilter picks threads.
type SupportFilter struct {
	Owner  int64  // a reseller: only their users' threads (0 = every thread)
	Q      string // the user's name, the Telegram name or @username, or the chat id
	Status string // "open", "waiting" (open, the person wrote last), "closed"; "" = all
	Limit  int    // 0 = 200
}

// the reseller a thread belongs to: its user's, while the user is there
const threadOwner = `COALESCE((SELECT u.owner_id FROM users u WHERE u.id = t.user_id), t.owner_id)`

func supportWhere(f SupportFilter) (string, []any) {
	var conds []string
	var args []any
	if f.Owner != 0 {
		conds = append(conds, threadOwner+` = ?`)
		args = append(args, f.Owner)
	}
	switch f.Status {
	case "open":
		conds = append(conds, `t.open=1`)
	case "waiting":
		conds = append(conds, `t.open=1 AND t.last_staff=0`)
	case "closed":
		conds = append(conds, `t.open=0`)
	}
	if q := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f.Q), "@")); q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
		conds = append(conds, `(LOWER(t.name) LIKE ? ESCAPE '\' OR LOWER(t.username) LIKE ? ESCAPE '\' OR CAST(t.chat AS TEXT) LIKE ? ESCAPE '\'
			OR EXISTS(SELECT 1 FROM users u WHERE u.id = t.user_id AND (LOWER(u.name) LIKE ? ESCAPE '\' OR LOWER(u.note) LIKE ? ESCAPE '\')))`)
		args = append(args, like, like, like, like, like)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// SupportThreads lists threads, the newest message first.
func (s *Store) SupportThreads(f SupportFilter) ([]*SupportThread, error) {
	if f.Limit <= 0 {
		f.Limit = 200
	}
	where, args := supportWhere(f)
	rows, err := s.DB.Query(`SELECT `+threadCols+` FROM support_threads t`+where+` ORDER BY t.last_at DESC, t.id DESC LIMIT ?`,
		append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SupportThread{}
	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SupportOwnerOf is the reseller a thread belongs to now (0 = the owner).
func (s *Store) SupportOwnerOf(t *SupportThread) int64 {
	var owner int64
	s.DB.QueryRow(`SELECT `+threadOwner+` FROM support_threads t WHERE t.id=?`, t.ID).Scan(&owner)
	return owner
}

// SupportCounts are the numbers on the support page and its menu badge.
type SupportCounts struct {
	Open    int `json:"open"`
	Waiting int `json:"waiting"` // open, the person wrote last
	Unread  int `json:"unread"`  // threads with messages nobody opened
	Closed  int `json:"closed"`
}

// SupportCount counts the threads an admin sees (owner as in SupportFilter).
func (s *Store) SupportCount(owner int64) SupportCounts {
	var c SupportCounts
	where, args := supportWhere(SupportFilter{Owner: owner})
	s.DB.QueryRow(`SELECT COALESCE(SUM(t.open=1),0), COALESCE(SUM(t.open=1 AND t.last_staff=0),0), COALESCE(SUM(t.unread>0),0),
		COALESCE(SUM(t.open=0),0) FROM support_threads t`+where, args...).Scan(&c.Open, &c.Waiting, &c.Unread, &c.Closed)
	return c
}

// SupportMessages is a thread's newest messages (up to limit, before the
// message id before when it is not 0), oldest first, and whether older ones
// are there.
func (s *Store) SupportMessages(thread, before int64, limit int) ([]*SupportMessage, bool, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT id, thread_id, at, staff, by_admin, text, photo, tg_msg, seen, answered FROM support_messages WHERE thread_id=?`
	args := []any{thread}
	if before > 0 {
		q += ` AND id<?`
		args = append(args, before)
	}
	rows, err := s.DB.Query(q+` ORDER BY id DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var list []*SupportMessage
	for rows.Next() {
		m := &SupportMessage{}
		var staff, seen, answered int
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.At, &staff, &m.By, &m.Text, &m.Photo, &m.TGMsg, &seen, &answered); err != nil {
			return nil, false, err
		}
		m.Staff, m.Seen, m.Answered = staff == 1, seen == 1, answered == 1
		list = append(list, m)
	}
	more := len(list) > limit
	if more {
		list = list[:limit]
	}
	out := make([]*SupportMessage, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		out = append(out, list[i])
	}
	return out, more, rows.Err()
}

// SupportPhotoThread is the thread a stored photo belongs to.
func (s *Store) SupportPhotoThread(photo string) (int64, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT thread_id FROM support_messages WHERE photo=? AND photo<>''`, photo).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// SupportSentSince counts what a chat sent to support since at (rate limit).
func (s *Store) SupportSentSince(chat, at int64) int {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM support_messages m JOIN support_threads t ON t.id = m.thread_id
		WHERE t.chat=? AND m.staff=0 AND m.at>=?`, chat, at).Scan(&n)
	return n
}

// SetSupportOpen closes a thread (by an admin) or opens it again.
func (s *Store) SetSupportOpen(thread int64, open bool, by string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if open {
		_, err = s.DB.Exec(`UPDATE support_threads SET open=1, closed_at=0, closed_by='' WHERE id=?`, thread)
	} else {
		_, err = s.DB.Exec(`UPDATE support_threads SET open=0, closed_at=?, closed_by=? WHERE id=?`, at, by, thread)
	}
	return err
}

func photosOf(tx *sql.Tx, where string, args ...any) []string {
	rows, err := tx.Query(`SELECT photo FROM support_messages WHERE photo<>'' AND `+where, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			out = append(out, p)
		}
	}
	return out
}

// DeleteSupportThread removes a thread and its messages; it returns the
// photos to remove from disk.
func (s *Store) DeleteSupportThread(thread int64) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	photos := photosOf(tx, `thread_id=?`, thread)
	tx.Exec(`DELETE FROM support_messages WHERE thread_id=?`, thread)
	tx.Exec(`DELETE FROM support_threads WHERE id=?`, thread)
	return photos, tx.Commit()
}

// PruneSupport applies the retention: messages older than before go (0 =
// keep any age), and each thread keeps its newest keep messages (0 = all).
// Threads left with no message go too. It returns the photos to remove.
func (s *Store) PruneSupport(keep int, before int64) ([]string, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var photos []string
	removed := 0
	drop := func(where string, args ...any) error {
		photos = append(photos, photosOf(tx, where, args...)...)
		res, err := tx.Exec(`DELETE FROM support_messages WHERE `+where, args...)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		removed += int(n)
		return nil
	}
	if before > 0 {
		if err := drop(`at < ?`, before); err != nil {
			return nil, 0, err
		}
	}
	if keep > 0 {
		over := `id IN (SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY thread_id ORDER BY id DESC) AS rn
			FROM support_messages) WHERE rn > ?)`
		if err := drop(over, keep); err != nil {
			return nil, 0, err
		}
	}
	if removed > 0 {
		tx.Exec(`DELETE FROM support_threads WHERE NOT EXISTS(SELECT 1 FROM support_messages m WHERE m.thread_id = support_threads.id)`)
		tx.Exec(`UPDATE support_threads SET unread=(SELECT COUNT(*) FROM support_messages m
			WHERE m.thread_id = support_threads.id AND m.staff=0 AND m.seen=0)`)
	}
	return photos, removed, tx.Commit()
}
