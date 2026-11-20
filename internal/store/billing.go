package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Billing: plans people buy, orders for them (and for resellers' wallets), and
// each reseller's wallet with its ledger. Money is a whole number in the
// shop's currency (Toman by default).

var billingTables = []string{
	`CREATE TABLE IF NOT EXISTS plans (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		note TEXT NOT NULL DEFAULT '',
		price INTEGER NOT NULL,
		data_limit INTEGER NOT NULL DEFAULT 0,
		days INTEGER NOT NULL DEFAULT 0,
		ip_limit INTEGER NOT NULL DEFAULT 0,
		inbound_ids TEXT NOT NULL DEFAULT '[]',
		start_on_first INTEGER NOT NULL DEFAULT 1,
		enabled INTEGER NOT NULL DEFAULT 1,
		sort INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS orders (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		code TEXT NOT NULL UNIQUE,
		token TEXT NOT NULL UNIQUE,
		kind TEXT NOT NULL,
		plan_id INTEGER NOT NULL DEFAULT 0,
		plan_name TEXT NOT NULL DEFAULT '',
		user_id INTEGER NOT NULL DEFAULT 0,
		admin_id INTEGER NOT NULL DEFAULT 0,
		amount INTEGER NOT NULL,
		method TEXT NOT NULL,
		status TEXT NOT NULL,
		contact TEXT NOT NULL DEFAULT '',
		tg_chat INTEGER NOT NULL DEFAULT 0,
		receipt TEXT NOT NULL DEFAULT '',
		receipt_note TEXT NOT NULL DEFAULT '',
		pay_ref TEXT NOT NULL DEFAULT '',
		crypto_amount TEXT NOT NULL DEFAULT '',
		note TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		paid_at INTEGER NOT NULL DEFAULT 0,
		done_at INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS orders_status ON orders(status)`,
	`CREATE TABLE IF NOT EXISTS wallet_tx (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		admin_id INTEGER NOT NULL,
		amount INTEGER NOT NULL,
		balance INTEGER NOT NULL,
		order_id INTEGER NOT NULL DEFAULT 0,
		note TEXT NOT NULL DEFAULT '',
		at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS wallet_tx_admin ON wallet_tx(admin_id)`,
}

// ---------------------------------------------------------------- plans

// Plan is something a customer can buy: data, days, devices and protocols.
type Plan struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Note         string  `json:"note"`
	Price        int64   `json:"price"`
	DataLimit    int64   `json:"dataLimit"` // bytes, 0 = unlimited
	Days         int     `json:"days"`      // 0 = no time limit
	IPLimit      int     `json:"ipLimit"`
	InboundIDs   []int64 `json:"inboundIds"` // empty = every protocol
	StartOnFirst bool    `json:"startOnFirstUse"`
	Enabled      bool    `json:"enabled"`
	Sort         int     `json:"sort"`
	CreatedAt    int64   `json:"createdAt"`
}

const planCols = `id,name,note,price,data_limit,days,ip_limit,inbound_ids,start_on_first,enabled,sort,created_at`

func scanPlan(r scanner) (*Plan, error) {
	p := &Plan{}
	var ids string
	var first, enabled int
	if err := r.Scan(&p.ID, &p.Name, &p.Note, &p.Price, &p.DataLimit, &p.Days, &p.IPLimit, &ids, &first, &enabled, &p.Sort, &p.CreatedAt); err != nil {
		return nil, err
	}
	p.StartOnFirst, p.Enabled = first == 1, enabled == 1
	json.Unmarshal([]byte(ids), &p.InboundIDs)
	if p.InboundIDs == nil {
		p.InboundIDs = []int64{}
	}
	return p, nil
}

// Plans lists plans in their order (onlyEnabled for the shop).
func (s *Store) Plans(onlyEnabled bool) ([]*Plan, error) {
	q := `SELECT ` + planCols + ` FROM plans`
	if onlyEnabled {
		q += ` WHERE enabled=1`
	}
	rows, err := s.DB.Query(q + ` ORDER BY sort, price, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Plan{}
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlanByID loads a plan.
func (s *Store) PlanByID(id int64) (*Plan, error) {
	p, err := scanPlan(s.DB.QueryRow(`SELECT `+planCols+` FROM plans WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// SavePlan inserts (ID 0) or updates a plan.
func (s *Store) SavePlan(p *Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, _ := json.Marshal(nonNil(p.InboundIDs))
	if p.ID == 0 {
		p.CreatedAt = time.Now().Unix()
		res, err := s.DB.Exec(`INSERT INTO plans(name,note,price,data_limit,days,ip_limit,inbound_ids,start_on_first,enabled,sort,created_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, p.Name, p.Note, p.Price, p.DataLimit, p.Days, p.IPLimit, string(ids),
			b2i(p.StartOnFirst), b2i(p.Enabled), p.Sort, p.CreatedAt)
		if err != nil {
			return err
		}
		p.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := s.DB.Exec(`UPDATE plans SET name=?,note=?,price=?,data_limit=?,days=?,ip_limit=?,inbound_ids=?,start_on_first=?,enabled=?,sort=? WHERE id=?`,
		p.Name, p.Note, p.Price, p.DataLimit, p.Days, p.IPLimit, string(ids), b2i(p.StartOnFirst), b2i(p.Enabled), p.Sort, p.ID)
	return err
}

// DeletePlan removes a plan (orders keep its name).
func (s *Store) DeletePlan(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`DELETE FROM plans WHERE id=?`, id)
	return err
}

// ---------------------------------------------------------------- orders

// Order kinds, payment methods and statuses.
const (
	OrderNew   = "new"   // a new subscription
	OrderRenew = "renew" // more time and data for a user
	OrderTopUp = "topup" // credit for a reseller's wallet

	PayCard     = "card"     // card to card, with a receipt an admin approves
	PayZarinpal = "zarinpal" // online gateway
	PayUSDT     = "usdt"     // USDT on TRON, found on the chain
	PayWallet   = "wallet"   // a reseller's balance
	PayManual   = "manual"   // recorded by an admin

	OrderPending   = "pending"   // waiting for the payment
	OrderReview    = "review"    // a receipt waits for an admin
	OrderPaid      = "paid"      // paid and applied
	OrderRejected  = "rejected"  // an admin refused the receipt
	OrderCancelled = "cancelled" // the buyer or an admin cancelled it
	OrderExpired   = "expired"   // not paid in time
)

// Order is one purchase.
type Order struct {
	ID           int64  `json:"id"`
	Code         string `json:"code"` // short reference the buyer quotes
	Token        string `json:"-"`    // secret part of the order page's address
	Kind         string `json:"kind"` // new | renew | topup
	PlanID       int64  `json:"planId"`
	PlanName     string `json:"planName"`
	UserID       int64  `json:"userId"`  // renewed, or created when paid
	AdminID      int64  `json:"adminId"` // the reseller of a top-up
	Amount       int64  `json:"amount"`
	Method       string `json:"method"`
	Status       string `json:"status"`
	Contact      string `json:"contact"`
	TGChat       int64  `json:"tgChat"`
	Receipt      string `json:"receipt"` // file name of an uploaded receipt
	ReceiptNote  string `json:"receiptNote"`
	PayRef       string `json:"payRef"`       // gateway authority/reference, transaction hash
	CryptoAmount string `json:"cryptoAmount"` // the exact USDT asked for
	Note         string `json:"note"`
	CreatedAt    int64  `json:"createdAt"`
	PaidAt       int64  `json:"paidAt"`
	DoneAt       int64  `json:"doneAt"`
}

const orderCols = `id,code,token,kind,plan_id,plan_name,user_id,admin_id,amount,method,status,contact,tg_chat,
	receipt,receipt_note,pay_ref,crypto_amount,note,created_at,paid_at,done_at`

func scanOrder(r scanner) (*Order, error) {
	o := &Order{}
	err := r.Scan(&o.ID, &o.Code, &o.Token, &o.Kind, &o.PlanID, &o.PlanName, &o.UserID, &o.AdminID, &o.Amount, &o.Method,
		&o.Status, &o.Contact, &o.TGChat, &o.Receipt, &o.ReceiptNote, &o.PayRef, &o.CryptoAmount, &o.Note,
		&o.CreatedAt, &o.PaidAt, &o.DoneAt)
	return o, err
}

func (s *Store) oneOrder(where string, args ...any) (*Order, error) {
	o, err := scanOrder(s.DB.QueryRow(`SELECT `+orderCols+` FROM orders `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

// OrderByID, OrderByToken and OrderByCode load an order.
func (s *Store) OrderByID(id int64) (*Order, error) { return s.oneOrder(`WHERE id=?`, id) }
func (s *Store) OrderByToken(t string) (*Order, error) {
	if t == "" {
		return nil, ErrNotFound
	}
	return s.oneOrder(`WHERE token=?`, t)
}
func (s *Store) OrderByCode(c string) (*Order, error) {
	return s.oneOrder(`WHERE code=?`, strings.ToUpper(strings.TrimSpace(c)))
}

// CreateOrder inserts an order and fills its ID.
func (s *Store) CreateOrder(o *Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o.CreatedAt == 0 {
		o.CreatedAt = time.Now().Unix()
	}
	res, err := s.DB.Exec(`INSERT INTO orders(code,token,kind,plan_id,plan_name,user_id,admin_id,amount,method,status,contact,tg_chat,
		receipt,receipt_note,pay_ref,crypto_amount,note,created_at,paid_at,done_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.Code, o.Token, o.Kind, o.PlanID, o.PlanName, o.UserID, o.AdminID, o.Amount, o.Method, o.Status, o.Contact, o.TGChat,
		o.Receipt, o.ReceiptNote, o.PayRef, o.CryptoAmount, o.Note, o.CreatedAt, o.PaidAt, o.DoneAt)
	if err != nil {
		return err
	}
	o.ID, _ = res.LastInsertId()
	return nil
}

// UpdateOrder saves the fields a buyer or an admin may change; the status
// changes only through SetOrderStatus.
func (s *Store) UpdateOrder(o *Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.DB.Exec(`UPDATE orders SET user_id=?,contact=?,tg_chat=?,receipt=?,receipt_note=?,pay_ref=?,note=?,done_at=? WHERE id=?`,
		o.UserID, o.Contact, o.TGChat, o.Receipt, o.ReceiptNote, o.PayRef, o.Note, o.DoneAt, o.ID)
	return err
}

// SetOrderStatus moves an order from one of the given statuses to another and
// reports whether it did: of two people (or a person and a watcher) acting on
// the same order at once, exactly one wins.
func (s *Store) SetOrderStatus(id int64, to string, from ...string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(from) == 0 {
		return false, fmt.Errorf("no status to move from")
	}
	set, args := `status=?`, []any{to}
	if to == OrderPaid {
		set, args = `status=?, paid_at=?`, append(args, time.Now().Unix())
	}
	q := `UPDATE orders SET ` + set + ` WHERE id=? AND status IN (?` + strings.Repeat(",?", len(from)-1) + `)`
	args = append(args, id)
	for _, f := range from {
		args = append(args, f)
	}
	res, err := s.DB.Exec(q, args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// OrderFilter selects orders for the panel.
type OrderFilter struct {
	Status  string // "" = all; "open" = pending and review
	AdminID int64  // only this admin's top-ups (resellers)
	Kind    string
	Method  string
	Limit   int
}

// Orders lists orders, newest first.
func (s *Store) Orders(f OrderFilter) ([]*Order, error) {
	var where []string
	var args []any
	switch f.Status {
	case "":
	case "open":
		where = append(where, `status IN ('pending','review')`)
	default:
		where, args = append(where, `status=?`), append(args, f.Status)
	}
	if f.AdminID != 0 {
		where, args = append(where, `admin_id=?`), append(args, f.AdminID)
	}
	if f.Kind != "" {
		where, args = append(where, `kind=?`), append(args, f.Kind)
	}
	if f.Method != "" {
		where, args = append(where, `method=?`), append(args, f.Method)
	}
	q := `SELECT ` + orderCols + ` FROM orders`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	rows, err := s.DB.Query(q+` ORDER BY id DESC LIMIT ?`, append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// CryptoAmountTaken reports whether a USDT order still waiting for its
// transfer asks for amount: open ones, and expired ones created after since
// (a late transfer must still find its own order, not a newer one).
func (s *Store) CryptoAmountTaken(amount string, since int64) bool {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM orders WHERE method='usdt' AND crypto_amount=?
		AND (status='pending' OR (status='expired' AND created_at>?))`, amount, since).Scan(&n)
	return n > 0
}

// PayRefUsed reports whether a payment reference (a transaction hash) already
// paid an order, so one transfer never pays twice.
func (s *Store) PayRefUsed(method, ref string) bool {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM orders WHERE method=? AND pay_ref=? AND status='paid'`, method, ref).Scan(&n)
	return n > 0
}

// OrderStats sums paid orders since a moment (for the panel).
func (s *Store) OrderStats(since int64) (count int, total int64, open int) {
	s.DB.QueryRow(`SELECT COUNT(*), COALESCE(SUM(amount),0) FROM orders WHERE status='paid' AND paid_at>=? AND kind<>'topup' AND method<>'wallet'`, since).Scan(&count, &total)
	s.DB.QueryRow(`SELECT COUNT(*) FROM orders WHERE status='review'`).Scan(&open)
	return
}

// ---------------------------------------------------------------- wallets

// WalletTx is one line of a reseller's wallet.
type WalletTx struct {
	ID      int64  `json:"id"`
	AdminID int64  `json:"adminId"`
	Amount  int64  `json:"amount"` // + credit, - spent
	Balance int64  `json:"balance"`
	OrderID int64  `json:"orderId"`
	Note    string `json:"note"`
	At      int64  `json:"at"`
}

// ErrLowBalance is returned when a wallet cannot pay.
var ErrLowBalance = errors.New("not enough balance in the wallet")

// WalletMove adds amount (negative to spend) to an admin's balance and writes
// the ledger line, in one transaction. Spending more than the balance fails.
func (s *Store) WalletMove(adminID, amount, orderID int64, note string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var bal int64
	if err := tx.QueryRow(`SELECT balance FROM admins WHERE id=?`, adminID).Scan(&bal); err != nil {
		return 0, ErrNotFound
	}
	if bal+amount < 0 {
		return bal, ErrLowBalance
	}
	bal += amount
	if _, err := tx.Exec(`UPDATE admins SET balance=? WHERE id=?`, bal, adminID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO wallet_tx(admin_id,amount,balance,order_id,note,at) VALUES(?,?,?,?,?,?)`,
		adminID, amount, bal, orderID, note, time.Now().Unix()); err != nil {
		return 0, err
	}
	return bal, tx.Commit()
}

// Balance returns an admin's wallet balance.
func (s *Store) Balance(adminID int64) int64 {
	var b int64
	s.DB.QueryRow(`SELECT balance FROM admins WHERE id=?`, adminID).Scan(&b)
	return b
}

// WalletTxs lists an admin's wallet lines, newest first.
func (s *Store) WalletTxs(adminID int64, limit int) ([]*WalletTx, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.DB.Query(`SELECT id,admin_id,amount,balance,order_id,note,at FROM wallet_tx WHERE admin_id=? ORDER BY id DESC LIMIT ?`, adminID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*WalletTx{}
	for rows.Next() {
		t := &WalletTx{}
		if err := rows.Scan(&t.ID, &t.AdminID, &t.Amount, &t.Balance, &t.OrderID, &t.Note, &t.At); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
