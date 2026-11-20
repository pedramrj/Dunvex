package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

const GB = int64(1) << 30

func setup(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &Service{St: st, Apply: func() {}, NewUser: func(name string) *store.User {
		return &store.User{Name: name, UUID: util.UUID(), Password: util.RandomString(20), SubToken: util.RandomString(22), Enabled: true, ResetPeriod: "none"}
	}}
	if err := SaveSettings(st, Settings{Shop: true, Card: true, Cards: []Card{{Number: "6037 9900 0000 0000", Holder: "Pedram"}},
		Wallet: true, Discount: 20, OrderHours: 24}); err != nil {
		t.Fatal(err)
	}
	return s, st
}

func plan(t *testing.T, st *store.Store, price, data int64, days int, first bool) *store.Plan {
	t.Helper()
	p := &store.Plan{Name: "P" + util.RandomLower(3), Price: price, DataLimit: data, Days: days, IPLimit: 2, StartOnFirst: first, Enabled: true}
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSettingsChecks(t *testing.T) {
	_, st := setup(t)
	for _, bad := range []Settings{
		{Card: true}, // no card
		{Zarinpal: true, ZarinpalMerchant: "short"}, // not a merchant id
		{USDT: true, USDTAddress: "0xabc", USDTRate: 90000},
		{USDT: true, USDTAddress: "TV6MuMXfmLbBqPZvBHdwFsDnQeVfnmiuSi"}, // no rate
		{Discount: 95},
	} {
		bad.OrderHours = 24
		if SaveSettings(st, bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	got := LoadSettings(st)
	if len(got.Cards) != 1 || got.Cards[0].Number != "6037990000000000" || !got.Has(store.PayCard) || got.Has(store.PayUSDT) {
		t.Fatalf("settings: %+v", got)
	}
}

func TestCardOrderNewAndRenew(t *testing.T) {
	s, st := setup(t)
	var mu sync.Mutex
	var events []string
	s.OnEvent(func(o *store.Order, e string) { mu.Lock(); events = append(events, e); mu.Unlock() })
	p := plan(t, st, 150000, 50*GB, 30, true)

	if _, err := s.Create(Request{Kind: store.OrderNew, PlanID: p.ID, Method: store.PayZarinpal}); err == nil {
		t.Fatal("an order with a method that is off")
	}
	o, err := s.Create(Request{Kind: store.OrderNew, PlanID: p.ID, Method: store.PayCard, Contact: "  sara_tg "})
	if err != nil {
		t.Fatal(err)
	}
	if o.Amount != 150000 || o.Status != store.OrderPending || len(o.Code) != 8 || len(o.Token) < 20 {
		t.Fatalf("order: %+v", o)
	}
	if err := s.SubmitReceipt(o, "", ""); err == nil {
		t.Error("an empty receipt was taken")
	}
	if err := s.SubmitReceipt(o, "r1.jpg", "tracking 123"); err != nil || o.Status != store.OrderReview {
		t.Fatalf("receipt: %v %s", err, o.Status)
	}

	// two admins approving at once: one user, one payment
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cp, _ := st.OrderByID(o.ID)
			s.Pay(cp, "", "boss")
		}()
	}
	wg.Wait()
	o, _ = st.OrderByID(o.ID)
	users, _ := st.Users()
	if o.Status != store.OrderPaid || o.UserID == 0 || o.DoneAt == 0 || len(users) != 1 {
		t.Fatalf("after approval: %+v, %d users", o, len(users))
	}
	u := users[0]
	if u.Name != "sara_tg" || u.DataLimit != 50*GB || u.DurationDays != 30 || u.ExpireAt != 0 || u.IPLimit != 2 {
		t.Fatalf("new user: %+v", u)
	}
	if err := s.Reject(o, "late", "boss"); err == nil {
		t.Error("a paid order was rejected")
	}

	// renewal of an active user: keeps the data left, adds the days
	now := time.Now().Unix()
	u.FirstUseAt, u.ExpireAt = now-86400, now+10*86400
	st.UpdateUser(u)
	st.DB.Exec(`UPDATE users SET used_down=? WHERE id=?`, 20*GB, u.ID)
	u, _ = st.UserByID(u.ID)
	r, _ := s.Create(Request{Kind: store.OrderRenew, PlanID: p.ID, UserID: u.ID, Method: store.PayCard})
	if err := s.Pay(r, "", "boss"); err != nil {
		t.Fatal(err)
	}
	u, _ = st.UserByID(u.ID)
	if u.DataLimit != 80*GB || u.Used() != 0 || u.ExpireAt < now+39*86400 || u.ExpireAt > now+41*86400 {
		t.Fatalf("renewed user: limit %d used %d expires in %d days", u.DataLimit/GB, u.Used(), (u.ExpireAt-now)/86400)
	}
	if strings.Join(events, ",") != "new,review,paid,new,paid" {
		t.Errorf("events: %v", events)
	}
}

func TestApplyPlanRenewals(t *testing.T) {
	now := time.Now().Unix()
	p := &store.Plan{DataLimit: 10 * GB, Days: 30}
	// expired, with data left: the old data is gone, the month starts now
	u := &store.User{Enabled: true, DataLimit: 5 * GB, ExpireAt: now - 100, FirstUseAt: now - 40*86400}
	ApplyPlan(u, p, now, true)
	if u.DataLimit != 10*GB || u.ExpireAt != now+30*86400 {
		t.Errorf("expired: %+v", u)
	}
	// not started yet: the days wait for the first connection too
	u = &store.User{Enabled: true, DurationDays: 30}
	ApplyPlan(u, p, now, true)
	if u.DurationDays != 60 || u.ExpireAt != 0 {
		t.Errorf("pending: %+v", u)
	}
	// switched off by an admin, or over its devices: on again
	u = &store.User{Enabled: false, PausedUntil: now + 600, ExpireAt: now + 86400}
	ApplyPlan(u, p, now, true)
	if !u.Enabled || u.PausedUntil != 0 {
		t.Errorf("not switched on: %+v", u)
	}
	// unlimited plan
	u = &store.User{Enabled: true, DataLimit: 5 * GB}
	ApplyPlan(u, &store.Plan{}, now, true)
	if u.DataLimit != 0 || u.ExpireAt != 0 {
		t.Errorf("unlimited: %+v", u)
	}
}

func TestWallet(t *testing.T) {
	s, st := setup(t)
	r := &store.Admin{Username: "rez", PasswordHash: "x", Role: store.RoleReseller, Enabled: true}
	st.CreateAdmin(r)
	p := plan(t, st, 100000, 20*GB, 30, false)

	if _, err := s.WalletBuy(r, p, 1, nil); err != store.ErrLowBalance {
		t.Fatalf("an empty wallet paid: %v", err)
	}
	o, err := s.Create(Request{Kind: store.OrderTopUp, AdminID: r.ID, Amount: 250000, Method: store.PayCard})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Pay(o, "", "boss"); err != nil || st.Balance(r.ID) != 250000 {
		t.Fatalf("top-up: %v, balance %d", err, st.Balance(r.ID))
	}
	cost, err := s.WalletBuy(r, p, 3, nil) // 3 × 80,000 (20% off)
	if err != nil || cost != 240000 || st.Balance(r.ID) != 10000 {
		t.Fatalf("buy: cost %d err %v balance %d", cost, err, st.Balance(r.ID))
	}
	if _, err := s.WalletBuy(r, p, 1, nil); err != store.ErrLowBalance || st.Balance(r.ID) != 10000 {
		t.Fatalf("overdraft: %v %d", err, st.Balance(r.ID))
	}
	txs, _ := st.WalletTxs(r.ID, 10)
	if len(txs) != 2 || txs[0].Amount != -240000 || txs[1].Amount != 250000 {
		t.Errorf("ledger: %+v", txs)
	}
	admin := &store.Admin{Username: "adm", PasswordHash: "x", Role: store.RoleAdmin, Enabled: true}
	st.CreateAdmin(admin)
	if _, err := s.Create(Request{Kind: store.OrderTopUp, AdminID: admin.ID, Amount: 5000, Method: store.PayCard}); err == nil {
		t.Error("an admin who is not a reseller got a wallet top-up")
	}
}

func TestUSDTMatchesTheRightTransfer(t *testing.T) {
	s, st := setup(t)
	set := LoadSettings(st)
	set.USDT, set.USDTAddress, set.USDTRate = true, "TV6MuMXfmLbBqPZvBHdwFsDnQeVfnmiuSi", 100000
	if err := SaveSettings(st, set); err != nil {
		t.Fatal(err)
	}
	p := plan(t, st, 500000, 10*GB, 30, true)

	// amounts: the price in USDT plus thousandths that no other open order has
	a, _ := s.Create(Request{Kind: store.OrderNew, PlanID: p.ID, Method: store.PayUSDT})
	b, _ := s.Create(Request{Kind: store.OrderNew, PlanID: p.ID, Method: store.PayUSDT})
	if a.CryptoAmount != "5.001" || b.CryptoAmount != "5.002" {
		t.Fatalf("amounts %s %s", a.CryptoAmount, b.CryptoAmount)
	}

	// a real TronGrid answer (fetched for this test): transfers of 5.020, 5.940, …
	fixture, err := os.ReadFile("testdata/trongrid-trc20.json")
	if err != nil {
		t.Fatal(err)
	}
	var queries []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.String())
		w.Write(fixture)
	}))
	defer tg.Close()
	s.TronBase = tg.URL
	var doc struct {
		Data []trc20Transfer `json:"data"`
	}
	json.Unmarshal(fixture, &doc)
	first := doc.Data[0] // 5.020 USDT
	// order a asks exactly that amount and was made just before the transfer
	st.DB.Exec(`UPDATE orders SET crypto_amount='5.020', created_at=? WHERE id=?`, first.Timestamp/1000-60, a.ID)
	st.DB.Exec(`UPDATE orders SET created_at=? WHERE id=?`, first.Timestamp/1000-60, b.ID)
	if err := s.CheckUSDT(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, _ = st.OrderByID(a.ID)
	b, _ = st.OrderByID(b.ID)
	if a.Status != store.OrderPaid || a.PayRef != first.TxID || b.Status != store.OrderPending {
		t.Fatalf("a %s %s, b %s", a.Status, a.PayRef, b.Status)
	}
	if !strings.Contains(queries[0], "/v1/accounts/TV6MuMXfmLbBqPZvBHdwFsDnQeVfnmiuSi/transactions/trc20") || !strings.Contains(queries[0], "only_confirmed=true") {
		t.Errorf("query: %s", queries[0])
	}

	// the same transfer never pays a second order
	st.DB.Exec(`UPDATE orders SET crypto_amount='5.020' WHERE id=?`, b.ID)
	s.CheckUSDT(context.Background())
	if b, _ = st.OrderByID(b.ID); b.Status != store.OrderPending {
		t.Fatalf("one transfer paid two orders")
	}
	// a transfer older than the order does not pay it
	c, _ := s.Create(Request{Kind: store.OrderNew, PlanID: p.ID, Method: store.PayUSDT})
	st.DB.Exec(`UPDATE orders SET crypto_amount=?, created_at=? WHERE id=?`, FormatUSDT(5940000), doc.Data[1].Timestamp/1000+3600, c.ID)
	s.CheckUSDT(context.Background())
	if c, _ = st.OrderByID(c.ID); c.Status != store.OrderPending {
		t.Fatalf("an older transfer paid a newer order")
	}
	if v, ok := ParseUSDT("12.5"); !ok || v != 12500000 || FormatUSDT(12347000) != "12.347" {
		t.Error("usdt parsing")
	}
}

func TestZarinpal(t *testing.T) {
	s, st := setup(t)
	set := LoadSettings(st)
	set.Zarinpal, set.ZarinpalMerchant = true, "1344b5d4-0048-11e8-94db-005056a205be"
	SaveSettings(st, set)
	p := plan(t, st, 120000, 10*GB, 30, true)
	var got []map[string]any
	zp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body)
		switch r.URL.Path {
		case "/pg/v4/payment/request.json":
			w.Write([]byte(`{"data":{"authority":"S000000000000000000000000000000abcde","code":100,"message":"Success"},"errors":[]}`))
		case "/pg/v4/payment/verify.json":
			if body["authority"] != "S000000000000000000000000000000abcde" || body["amount"] != float64(1200000) {
				w.Write([]byte(`{"data":{},"errors":{"message":"bad","code":-50,"validations":[]}}`))
				return
			}
			w.Write([]byte(`{"data":{"code":100,"message":"Paid","card_pan":"6037******1234","ref_id":201,"fee":0},"errors":[]}`))
		}
	}))
	defer zp.Close()
	s.ZarinpalBase = zp.URL
	o, _ := s.Create(Request{Kind: store.OrderNew, PlanID: p.ID, Method: store.PayZarinpal})
	pay, err := s.ZarinpalStart(o, "https://shop.example/cb")
	if err != nil || pay != zp.URL+"/pg/StartPay/S000000000000000000000000000000abcde" {
		t.Fatalf("start: %v %s", err, pay)
	}
	if got[0]["amount"] != float64(1200000) || got[0]["merchant_id"] != set.ZarinpalMerchant || got[0]["callback_url"] != "https://shop.example/cb" {
		t.Errorf("request: %v", got[0])
	}
	if err := s.ZarinpalReturn(o, "S000000000000000000000000000000abcde", "NOK"); err == nil {
		t.Error("a cancelled payment was taken")
	}
	if err := s.ZarinpalReturn(o, "S0000000000000000000000000000000other", "OK"); err == nil {
		t.Error("another order's payment was taken")
	}
	if err := s.ZarinpalReturn(o, "S000000000000000000000000000000abcde", "OK"); err != nil {
		t.Fatal(err)
	}
	o, _ = st.OrderByID(o.ID)
	if o.Status != store.OrderPaid || o.PayRef != "zarinpal:201" || o.UserID == 0 {
		t.Fatalf("paid: %+v", o)
	}
	// coming back twice (a refresh) changes nothing
	if err := s.ZarinpalReturn(o, "S000000000000000000000000000000abcde", "OK"); err != nil {
		t.Error(err)
	}
}

func TestExpire(t *testing.T) {
	s, st := setup(t)
	p := plan(t, st, 1000, 0, 0, false)
	old, _ := s.Create(Request{Kind: store.OrderNew, PlanID: p.ID, Method: store.PayCard})
	waiting, _ := s.Create(Request{Kind: store.OrderNew, PlanID: p.ID, Method: store.PayCard})
	fresh, _ := s.Create(Request{Kind: store.OrderNew, PlanID: p.ID, Method: store.PayCard})
	s.SubmitReceipt(waiting, "", "ref 9")
	st.DB.Exec(`UPDATE orders SET created_at=? WHERE id IN (?,?)`, time.Now().Unix()-25*3600, old.ID, waiting.ID)
	s.ExpireOld()
	for o, want := range map[int64]string{old.ID: store.OrderExpired, waiting.ID: store.OrderReview, fresh.ID: store.OrderPending} {
		if got, _ := st.OrderByID(o); got.Status != want {
			t.Errorf("order %d: %s, want %s", o, got.Status, want)
		}
	}
}

func TestDataText(t *testing.T) {
	for _, c := range []struct {
		b    int64
		fa   bool
		want string
	}{
		{50 << 30, true, "50 گیگ"},
		{150 << 30, false, "150 GB"},
		{3 << 29, false, "1.5 GB"},
		{512 << 20, true, "512 مگ"},
	} {
		if got := DataText(c.b, c.fa); got != c.want {
			t.Errorf("DataText(%d, %v) = %q, want %q", c.b, c.fa, got, c.want)
		}
	}
}
