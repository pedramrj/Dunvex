// Package billing sells Dunvex subscriptions: plans, orders paid by card (with
// a receipt an admin approves), the ZarinPal gateway or USDT on TRON, and the
// wallets resellers pay from. A paid order is applied exactly once: a new user,
// more time and data for a user, or credit in a wallet.
package billing

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// Card is a bank card buyers pay to.
type Card struct {
	Number string `json:"number"`
	Holder string `json:"holder"`
	Bank   string `json:"bank"`
}

// Settings are the shop's settings (kept with the others, keys "bill_…").
type Settings struct {
	Shop     bool   `json:"shop"`     // the web shop and the renew button
	Bot      bool   `json:"bot"`      // selling in the Telegram bot
	Title    string `json:"title"`    // shown on the shop
	Currency string `json:"currency"` // label of the money (Toman)
	Support  string `json:"support"`  // where buyers ask (a Telegram link…)

	Card  bool   `json:"card"`
	Cards []Card `json:"cards"`

	Zarinpal         bool   `json:"zarinpal"`
	ZarinpalMerchant string `json:"zarinpalMerchant"`
	ZarinpalSandbox  bool   `json:"zarinpalSandbox"`

	USDT        bool   `json:"usdt"`
	USDTAddress string `json:"usdtAddress"`
	USDTRate    int64  `json:"usdtRate"`   // money per 1 USDT
	TronAPIKey  string `json:"tronApiKey"` // optional TronGrid key

	Wallet   bool `json:"wallet"`   // resellers pay from a wallet
	Discount int  `json:"discount"` // resellers' discount on plans, percent

	OrderHours int `json:"orderHours"` // an unpaid order is dropped after this
}

var settingKeys = map[string]string{
	"shop": "bill_shop", "bot": "bill_bot", "title": "bill_title", "currency": "bill_currency", "support": "bill_support",
	"card": "bill_card", "cards": "bill_cards",
	"zarinpal": "bill_zarinpal", "zarinpalMerchant": "bill_zp_merchant", "zarinpalSandbox": "bill_zp_sandbox",
	"usdt": "bill_usdt", "usdtAddress": "bill_usdt_address", "usdtRate": "bill_usdt_rate", "tronApiKey": "bill_tron_key",
	"wallet": "bill_wallet", "discount": "bill_discount", "orderHours": "bill_order_hours",
}

// LoadSettings reads the shop settings.
func LoadSettings(st *store.Store) Settings {
	b := func(k string) bool { return st.GetSetting(settingKeys[k], "false") == "true" }
	n := func(k string, def int64) int64 {
		v, err := strconv.ParseInt(st.GetSetting(settingKeys[k], ""), 10, 64)
		if err != nil {
			return def
		}
		return v
	}
	s := Settings{
		Shop: b("shop"), Bot: b("bot"), Title: st.GetSetting("bill_title", ""), Currency: st.GetSetting("bill_currency", "تومان"),
		Support: st.GetSetting("bill_support", ""),
		Card:    b("card"), Zarinpal: b("zarinpal"), ZarinpalMerchant: st.GetSetting("bill_zp_merchant", ""),
		ZarinpalSandbox: b("zarinpalSandbox"), USDT: b("usdt"), USDTAddress: st.GetSetting("bill_usdt_address", ""),
		USDTRate: n("usdtRate", 0), TronAPIKey: st.GetSetting("bill_tron_key", ""), Wallet: b("wallet"),
		Discount: int(n("discount", 0)), OrderHours: int(n("orderHours", 24)),
	}
	json.Unmarshal([]byte(st.GetSetting("bill_cards", "[]")), &s.Cards)
	if s.Cards == nil {
		s.Cards = []Card{}
	}
	if s.OrderHours < 1 {
		s.OrderHours = 24
	}
	if strings.TrimSpace(s.Currency) == "" {
		s.Currency = "تومان"
	}
	return s
}

// SaveSettings checks and stores the shop settings.
func SaveSettings(st *store.Store, s Settings) error {
	s.USDTAddress = strings.TrimSpace(s.USDTAddress)
	s.ZarinpalMerchant = strings.TrimSpace(s.ZarinpalMerchant)
	var cards []Card
	for _, c := range s.Cards {
		c.Number = cardNumber(c.Number)
		if c.Number == "" {
			continue
		}
		cards = append(cards, Card{Number: c.Number, Holder: strings.TrimSpace(c.Holder), Bank: strings.TrimSpace(c.Bank)})
	}
	switch {
	case s.Card && len(cards) == 0:
		return errors.New("add at least one card for card-to-card payments")
	case s.Zarinpal && len(s.ZarinpalMerchant) != 36:
		return errors.New("the ZarinPal merchant ID has 36 characters")
	case s.USDT && !ValidTronAddress(s.USDTAddress):
		return errors.New("the USDT address is not a TRON address (it starts with T)")
	case s.USDT && s.USDTRate <= 0:
		return errors.New("set the price of 1 USDT for USDT payments")
	case s.Discount < 0 || s.Discount > 90:
		return errors.New("the reseller discount is between 0 and 90 percent")
	case s.OrderHours < 1 || s.OrderHours > 720:
		return errors.New("unpaid orders are kept between 1 and 720 hours")
	}
	cj, _ := json.Marshal(nonNil(cards))
	bs := func(v bool) string { return strconv.FormatBool(v) }
	for k, v := range map[string]string{
		"bill_shop": bs(s.Shop), "bill_bot": bs(s.Bot), "bill_title": strings.TrimSpace(s.Title),
		"bill_currency": strings.TrimSpace(s.Currency), "bill_support": strings.TrimSpace(s.Support),
		"bill_card": bs(s.Card), "bill_cards": string(cj),
		"bill_zarinpal": bs(s.Zarinpal), "bill_zp_merchant": s.ZarinpalMerchant, "bill_zp_sandbox": bs(s.ZarinpalSandbox),
		"bill_usdt": bs(s.USDT), "bill_usdt_address": s.USDTAddress, "bill_usdt_rate": strconv.FormatInt(s.USDTRate, 10),
		"bill_tron_key": strings.TrimSpace(s.TronAPIKey), "bill_wallet": bs(s.Wallet),
		"bill_discount": strconv.Itoa(s.Discount), "bill_order_hours": strconv.Itoa(s.OrderHours),
	} {
		if err := st.SetSetting(k, v); err != nil {
			return err
		}
	}
	return nil
}

// cardNumber keeps only the digits of a card number written with spaces or
// dashes; anything else (an IBAN) is kept as written.
func cardNumber(s string) string {
	s = strings.TrimSpace(s)
	digits := strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9':
			return r
		case r >= '۰' && r <= '۹': // Persian digits
			return '0' + (r - '۰')
		case r == ' ' || r == '-':
			return -1
		}
		return 'x'
	}, s)
	if strings.ContainsRune(digits, 'x') {
		return strings.Join(strings.Fields(s), "")
	}
	return digits
}

func nonNil(c []Card) []Card {
	if c == nil {
		return []Card{}
	}
	return c
}

// Methods lists the payment methods buyers can use now.
func (s Settings) Methods() []string {
	var out []string
	if s.Card && len(s.Cards) > 0 {
		out = append(out, store.PayCard)
	}
	if s.Zarinpal && s.ZarinpalMerchant != "" {
		out = append(out, store.PayZarinpal)
	}
	if s.USDT && s.USDTAddress != "" && s.USDTRate > 0 {
		out = append(out, store.PayUSDT)
	}
	return out
}

// Has reports whether a method can be used now.
func (s Settings) Has(method string) bool {
	for _, m := range s.Methods() {
		if m == method {
			return true
		}
	}
	return false
}

// ResellerPrice is what a reseller's wallet pays for a plan.
func (s Settings) ResellerPrice(p *store.Plan) int64 { return p.Price - p.Price*int64(s.Discount)/100 }

// ---------------------------------------------------------------- service

// Events an order goes through, for notifications.
const (
	EventNew      = "new"      // created
	EventReview   = "review"   // a receipt waits for an admin
	EventPaid     = "paid"     // paid and applied
	EventRejected = "rejected" // receipt refused
	EventExpired  = "expired"  // not paid in time
	EventFailed   = "failed"   // paid, but could not be applied (an admin must look)
)

// Service runs the shop.
type Service struct {
	St         *store.Store
	NewUser    func(name string) *store.User // fresh credentials, not saved
	Apply      func()                        // bring the cores in line after a change of users
	ReceiptDir string

	mu       sync.Mutex
	cryptoMu sync.Mutex // one USDT amount is handed out at a time
	handlers []func(o *store.Order, event string)

	HTTP         *http.Client
	ZarinpalBase string // tests: instead of payment/sandbox.zarinpal.com
	TronBase     string // tests: instead of api.trongrid.io
	now          func() time.Time
}

// OnEvent adds a handler called for every order event (bot, logs).
func (s *Service) OnEvent(fn func(o *store.Order, event string)) {
	s.mu.Lock()
	s.handlers = append(s.handlers, fn)
	s.mu.Unlock()
}

func (s *Service) emit(o *store.Order, event string) {
	s.mu.Lock()
	hs := append([]func(*store.Order, string){}, s.handlers...)
	s.mu.Unlock()
	for _, h := range hs {
		h(o, event)
	}
}

func (s *Service) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

const codeChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/I

func newCode() string {
	b := make([]byte, 8)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(codeChars))))
		b[i] = codeChars[n.Int64()]
	}
	return string(b)
}

// Request asks for an order.
type Request struct {
	Kind    string // new | renew | topup
	PlanID  int64
	UserID  int64 // renew: whose subscription
	AdminID int64 // topup: whose wallet
	Amount  int64 // topup: how much
	Method  string
	Contact string
	TGChat  int64
}

// Create makes an order waiting for its payment.
func (s *Service) Create(req Request) (*store.Order, error) {
	set := LoadSettings(s.St)
	if !set.Has(req.Method) {
		return nil, fmt.Errorf("this payment method is not available")
	}
	o := &store.Order{Kind: req.Kind, Method: req.Method, Status: store.OrderPending, Contact: clip(strings.TrimSpace(req.Contact), 120),
		TGChat: req.TGChat, Code: newCode(), Token: util.RandomString(24), CreatedAt: s.clock().Unix()}
	switch req.Kind {
	case store.OrderNew, store.OrderRenew:
		p, err := s.St.PlanByID(req.PlanID)
		if err != nil || !p.Enabled {
			return nil, fmt.Errorf("this plan is not for sale")
		}
		o.PlanID, o.PlanName, o.Amount = p.ID, p.Name, p.Price
		if req.Kind == store.OrderRenew {
			if _, err := s.St.UserByID(req.UserID); err != nil {
				return nil, fmt.Errorf("no such subscription")
			}
			o.UserID = req.UserID
		}
	case store.OrderTopUp:
		a, err := s.St.AdminByID(req.AdminID)
		if err != nil || a.Role != store.RoleReseller {
			return nil, fmt.Errorf("only resellers have a wallet")
		}
		if !set.Wallet {
			return nil, fmt.Errorf("wallets are off")
		}
		if req.Amount < 1000 || req.Amount > 10_000_000_000 {
			return nil, fmt.Errorf("the amount is between 1,000 and 10,000,000,000")
		}
		o.AdminID, o.Amount = a.ID, req.Amount
	default:
		return nil, fmt.Errorf("unknown order kind")
	}
	if o.Amount <= 0 {
		return nil, fmt.Errorf("this plan has no price")
	}
	if req.Method == store.PayUSDT {
		s.cryptoMu.Lock()
		amt, err := s.cryptoAmount(o.Amount, set.USDTRate)
		if err == nil {
			o.CryptoAmount = amt
			err = s.St.CreateOrder(o)
		}
		s.cryptoMu.Unlock()
		if err != nil {
			return nil, err
		}
	} else if err := s.St.CreateOrder(o); err != nil {
		return nil, err
	}
	s.emit(o, EventNew)
	return o, nil
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// cryptoAmount is the USDT to ask for: the price rounded up to a cent, plus a
// few thousandths that no other open order uses, so the transfer that arrives
// tells which order it pays. Call it with cryptoMu held.
func (s *Service) cryptoAmount(amount, rate int64) (string, error) {
	if rate <= 0 {
		return "", fmt.Errorf("the USDT price is not set")
	}
	micro := (amount*1_000_000 + rate - 1) / rate // round up
	micro = (micro + 9_999) / 10_000 * 10_000     // to a cent
	for i := int64(1); i < 1000; i++ {
		v := FormatUSDT(micro + i*1000)
		if !s.St.CryptoAmountTaken(v, s.clock().Unix()-86400) {
			return v, nil
		}
	}
	return "", fmt.Errorf("too many open USDT orders; try again later")
}

// FormatUSDT writes micro-USDT with three decimals ("12.347").
func FormatUSDT(micro int64) string {
	return fmt.Sprintf("%d.%03d", micro/1_000_000, micro%1_000_000/1000)
}

// ParseUSDT reads "12.347" as micro-USDT.
func ParseUSDT(s string) (int64, bool) {
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || len(frac) > 6 {
		return 0, false
	}
	frac += strings.Repeat("0", 6-len(frac))
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, false
	}
	return w*1_000_000 + f, true
}

// Receipt problems.
var (
	ErrReceiptTooBig = errors.New("the receipt is larger than 5 MB")
	ErrReceiptType   = errors.New("the receipt must be a JPG, PNG or WebP image")
)

var receiptKinds = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}

// SaveReceipt keeps a receipt image (checked by its content, at most 5 MB)
// and returns its file name in ReceiptDir.
func (s *Service) SaveReceipt(o *store.Order, data []byte) (string, error) {
	if len(data) > 5<<20 {
		return "", ErrReceiptTooBig
	}
	ext, ok := receiptKinds[http.DetectContentType(data)]
	if !ok || len(data) == 0 {
		return "", ErrReceiptType
	}
	if s.ReceiptDir == "" {
		return "", errors.New("no folder for receipts")
	}
	if err := os.MkdirAll(s.ReceiptDir, 0o700); err != nil {
		return "", err
	}
	name := o.Code + "-" + util.RandomLower(6) + ext
	return name, os.WriteFile(s.ReceiptDir+"/"+name, data, 0o600)
}

// SubmitReceipt records the buyer's receipt and hands the order to an admin.
func (s *Service) SubmitReceipt(o *store.Order, file, note string) error {
	if o.Method != store.PayCard {
		return fmt.Errorf("this order is not paid by card")
	}
	if file == "" && strings.TrimSpace(note) == "" {
		return fmt.Errorf("send the receipt or its tracking number")
	}
	if o.Status != store.OrderPending && o.Status != store.OrderReview && o.Status != store.OrderRejected {
		return fmt.Errorf("this order is %s", o.Status)
	}
	if file != "" {
		if o.Receipt != "" && o.Receipt != file && s.ReceiptDir != "" {
			os.Remove(s.ReceiptDir + "/" + o.Receipt)
		}
		o.Receipt = file
	}
	o.ReceiptNote = clip(strings.TrimSpace(note), 200)
	if err := s.St.UpdateOrder(o); err != nil {
		return err
	}
	if ok, err := s.St.SetOrderStatus(o.ID, store.OrderReview, store.OrderPending, store.OrderRejected); err != nil {
		return err
	} else if ok {
		o.Status = store.OrderReview
	}
	s.St.Log("billing", "order %s (%s, %d) sent a receipt; it waits for approval", o.Code, o.PlanName, o.Amount)
	s.emit(o, EventReview)
	return nil
}

// Pay marks an order paid and applies it, exactly once: a second call (two
// admins, or an admin and a watcher) does nothing.
func (s *Service) Pay(o *store.Order, ref, by string) error {
	from := []string{store.OrderPending, store.OrderReview}
	if o.Method == store.PayUSDT || by != "" {
		from = append(from, store.OrderExpired) // a late transfer, or an admin's decision
	}
	if by != "" {
		from = append(from, store.OrderRejected)
	}
	ok, err := s.St.SetOrderStatus(o.ID, store.OrderPaid, from...)
	if err != nil {
		return err
	}
	if !ok {
		cur, _ := s.St.OrderByID(o.ID)
		if cur != nil && cur.Status == store.OrderPaid {
			*o = *cur
			return nil
		}
		return fmt.Errorf("this order can no longer be paid")
	}
	o.Status, o.PaidAt = store.OrderPaid, s.clock().Unix()
	if ref != "" {
		o.PayRef = ref
	}
	if by != "" {
		o.Note = strings.TrimSpace(o.Note + " approved by " + by)
	}
	if err := s.fulfil(o); err != nil {
		o.Note = strings.TrimSpace(o.Note + " · not applied: " + err.Error())
		s.St.UpdateOrder(o)
		s.St.Log("billing", "order %s was paid but could not be applied: %v", o.Code, err)
		s.emit(o, EventFailed)
		return err
	}
	o.DoneAt = s.clock().Unix()
	s.St.UpdateOrder(o)
	s.St.Log("billing", "order %s paid (%s, %d) and applied", o.Code, o.Method, o.Amount)
	s.emit(o, EventPaid)
	return nil
}

// Reject refuses an order's receipt (the buyer may send another).
func (s *Service) Reject(o *store.Order, reason, by string) error {
	ok, err := s.St.SetOrderStatus(o.ID, store.OrderRejected, store.OrderPending, store.OrderReview)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("this order is already %s", o.Status)
	}
	o.Status = store.OrderRejected
	o.Note = strings.TrimSpace(strings.TrimSpace(reason) + " (" + by + ")")
	s.St.UpdateOrder(o)
	s.St.Log("billing", "order %s rejected by %s", o.Code, by)
	s.emit(o, EventRejected)
	return nil
}

// Cancel drops an unpaid order.
func (s *Service) Cancel(o *store.Order) error {
	ok, err := s.St.SetOrderStatus(o.ID, store.OrderCancelled, store.OrderPending, store.OrderReview, store.OrderRejected)
	if err == nil && ok {
		o.Status = store.OrderCancelled
	}
	return err
}

func (s *Service) fulfil(o *store.Order) error {
	switch o.Kind {
	case store.OrderTopUp:
		_, err := s.St.WalletMove(o.AdminID, o.Amount, o.ID, "top-up "+o.Code)
		return err
	case store.OrderNew, store.OrderRenew:
		p, err := s.St.PlanByID(o.PlanID)
		if err != nil {
			return fmt.Errorf("the plan was deleted")
		}
		if o.Kind == store.OrderRenew {
			u, err := s.St.UserByID(o.UserID)
			if err != nil {
				return fmt.Errorf("the subscription was deleted")
			}
			ApplyPlan(u, p, s.clock().Unix(), true)
			if o.TGChat != 0 && u.TGChat == 0 {
				u.TGChat = o.TGChat
			}
			if err := s.St.UpdateUser(u); err != nil {
				return err
			}
		} else {
			u, err := s.createUser(o, p)
			if err != nil {
				return err
			}
			o.UserID = u.ID
		}
		if s.Apply != nil {
			s.Apply()
		}
		return nil
	}
	return fmt.Errorf("unknown order kind")
}

// createUser makes the subscription a new order bought.
func (s *Service) createUser(o *store.Order, p *store.Plan) (*store.User, error) {
	if s.NewUser == nil {
		return nil, fmt.Errorf("cannot create users here")
	}
	name := "c-" + strings.ToLower(o.Code)
	if n := util.SanitizeName(o.Contact); len(n) >= 3 && len(n) <= 24 {
		if _, err := s.St.UserByName(n); err != nil {
			name = n
		}
	}
	u := s.NewUser(name)
	u.Note = strings.TrimSpace("shop order " + o.Code + " " + o.Contact)
	u.TGChat = o.TGChat
	if o.Method == store.PayWallet {
		u.OwnerID = o.AdminID
	}
	ApplyPlan(u, p, s.clock().Unix(), false)
	if err := s.St.CreateUser(u); err != nil {
		return nil, err
	}
	return u, nil
}

// DataText is a plan's data in plain words: "50 گیگ" or "50 GB", and
// megabytes below one gigabyte, without needless decimals.
func DataText(b int64, fa bool) string {
	v, unit := float64(b)/(1<<30), "GB"
	if v < 1 {
		v, unit = float64(b)/(1<<20), "MB"
	}
	if fa {
		unit = map[string]string{"GB": "گیگ", "MB": "مگ"}[unit]
	}
	s := strings.TrimRight(strings.TrimRight(strconv.FormatFloat(v, 'f', 2, 64), "0"), ".")
	return s + " " + unit
}

// ApplyPlan gives a user what a plan holds. A renewal keeps the data left
// (an active user does not lose it), adds the days to the time left and
// starts counting usage again.
func ApplyPlan(u *store.User, p *store.Plan, now int64, renew bool) {
	days := int64(p.Days) * 86400
	if !renew {
		u.DataLimit, u.IPLimit = p.DataLimit, p.IPLimit
		u.InboundIDs = append([]int64{}, p.InboundIDs...)
		switch {
		case p.Days <= 0:
			u.DurationDays, u.ExpireAt = 0, 0
		case p.StartOnFirst:
			u.DurationDays, u.ExpireAt = p.Days, 0
		default:
			u.DurationDays, u.ExpireAt = p.Days, now+days
		}
		return
	}
	left := int64(0)
	if u.CanConnect(now) && u.DataLimit > 0 && u.Used() < u.DataLimit {
		left = u.DataLimit - u.Used()
	}
	if p.DataLimit == 0 {
		u.DataLimit = 0
	} else {
		u.DataLimit = p.DataLimit + left
	}
	u.UsedUp, u.UsedDown, u.LastResetAt, u.ResetUsage = 0, 0, now, true
	switch {
	case p.Days <= 0:
		u.DurationDays, u.ExpireAt = 0, 0
	case u.ExpireAt > now:
		u.ExpireAt += days
	case u.ExpireAt == 0 && u.DurationDays > 0 && u.FirstUseAt == 0: // has not started yet
		u.DurationDays += p.Days
	default:
		u.DurationDays, u.ExpireAt = p.Days, now+days
	}
	if p.IPLimit > 0 {
		u.IPLimit = p.IPLimit
	}
	if len(p.InboundIDs) > 0 {
		u.InboundIDs = append([]int64{}, p.InboundIDs...)
	}
	u.Enabled, u.PausedUntil, u.Notified = true, 0, ""
}

// WalletBuy has a reseller's wallet pay for count new users (u == nil) or for
// renewing u, and records it as a paid order. It returns the cost.
func (s *Service) WalletBuy(a *store.Admin, p *store.Plan, count int, u *store.User) (int64, error) {
	set := LoadSettings(s.St)
	if !set.Wallet {
		return 0, fmt.Errorf("wallets are off")
	}
	if !p.Enabled {
		return 0, fmt.Errorf("this plan is not for sale")
	}
	cost := set.ResellerPrice(p) * int64(count)
	o := &store.Order{Kind: store.OrderNew, Method: store.PayWallet, Status: store.OrderPending, PlanID: p.ID, PlanName: p.Name,
		AdminID: a.ID, Amount: cost, Code: newCode(), Token: util.RandomString(24), CreatedAt: s.clock().Unix(),
		Contact: a.Username}
	if u != nil {
		o.Kind, o.UserID, o.Contact = store.OrderRenew, u.ID, a.Username+" → "+u.Name
	}
	if err := s.St.CreateOrder(o); err != nil {
		return 0, err
	}
	if _, err := s.St.WalletMove(a.ID, -cost, o.ID, fmt.Sprintf("%s ×%d (%s)", p.Name, count, o.Code)); err != nil {
		s.St.SetOrderStatus(o.ID, store.OrderCancelled, store.OrderPending)
		return 0, err
	}
	s.St.SetOrderStatus(o.ID, store.OrderPaid, store.OrderPending)
	o.Status, o.PaidAt, o.DoneAt = store.OrderPaid, s.clock().Unix(), s.clock().Unix()
	s.St.UpdateOrder(o)
	return cost, nil
}

// ExpireOld drops orders nobody paid in time. Orders with a receipt wait for
// an admin and do not expire.
func (s *Service) ExpireOld() {
	set := LoadSettings(s.St)
	now := s.clock().Unix()
	list, err := s.St.Orders(store.OrderFilter{Status: store.OrderPending, Limit: 1000})
	if err != nil {
		return
	}
	for _, o := range list {
		ttl := int64(set.OrderHours) * 3600
		if o.Method == store.PayUSDT {
			ttl = 2 * 3600
		}
		if o.Method == store.PayWallet || now-o.CreatedAt < ttl {
			continue
		}
		if ok, _ := s.St.SetOrderStatus(o.ID, store.OrderExpired, store.OrderPending); ok {
			o.Status = store.OrderExpired
			s.emit(o, EventExpired)
		}
	}
}

// Run checks USDT payments and expires old orders until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := s.CheckUSDT(ctx); err != nil {
			log.Printf("billing: usdt: %v", err)
		}
		if n++; n%20 == 0 {
			s.ExpireOld()
		}
	}
}
