package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/billing"
	"github.com/pedramrj/dunvex/internal/store"
)

// The panel's side of the shop: plans, orders, payment settings and the
// resellers' wallets.

type orderAPIView struct {
	*store.Order
	UserName  string `json:"userName"`
	AdminName string `json:"adminName"`
	Page      string `json:"page"` // the buyer's order page
}

func (s *Server) orderView(r *http.Request, o *store.Order) orderAPIView {
	v := orderAPIView{Order: o, Page: s.ShopURL(r) + "o/" + o.Token}
	if o.UserID != 0 {
		if u, err := s.St.UserByID(o.UserID); err == nil {
			v.UserName = u.Name
		}
	}
	if o.AdminID != 0 {
		if a, err := s.St.AdminByID(o.AdminID); err == nil {
			v.AdminName = a.Username
		}
	}
	return v
}

// billingSummary: settings, the shop's address and sales.
func (s *Server) billingSummary(w http.ResponseWriter, r *http.Request) {
	set := billing.LoadSettings(s.St)
	if adminOf(r).Role != store.RoleOwner {
		set.ZarinpalMerchant, set.TronAPIKey = "", "" // only the owner sees the keys
	} else if set.TronAPIKey != "" {
		set.TronAPIKey = "••••••••" + set.TronAPIKey[max(0, len(set.TronAPIKey)-4):]
	}
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	todayN, todaySum, review := s.St.OrderStats(day)
	monthN, monthSum, _ := s.St.OrderStats(now.Unix() - 30*86400)
	writeJSON(w, 200, map[string]any{
		"settings": set, "shopUrl": s.ShopURL(r), "methods": set.Methods(),
		"stats": map[string]any{"todayCount": todayN, "todayTotal": todaySum, "monthCount": monthN, "monthTotal": monthSum, "review": review},
	})
}

func (s *Server) putBillingSettings(w http.ResponseWriter, r *http.Request) {
	var in billing.Settings
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	old := billing.LoadSettings(s.St)
	if strings.HasPrefix(in.TronAPIKey, "••") {
		in.TronAPIKey = old.TronAPIKey // shown masked, sent back unchanged
	}
	if err := billing.SaveSettings(s.St, in); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.St.Log("billing", "%s changed the shop settings", adminOf(r).Username)
	if s.Bot != nil {
		s.Bot.Reload()
	}
	s.billingSummary(w, r)
}

// ---------------------------------------------------------------- plans

type planAPIView struct {
	*store.Plan
	ResellerPrice int64 `json:"resellerPrice"`
}

func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	set := billing.LoadSettings(s.St)
	plans, err := s.St.Plans(a.Role == store.RoleReseller)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	out := []planAPIView{}
	for _, p := range plans {
		out = append(out, planAPIView{p, set.ResellerPrice(p)})
	}
	writeJSON(w, 200, out)
}

func (s *Server) savePlan(w http.ResponseWriter, r *http.Request) {
	var in store.Plan
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "" || len([]rune(in.Name)) > 60:
		fail(w, 400, "give the plan a name (up to 60 characters)")
		return
	case in.Price <= 0 || in.Price > 10_000_000_000:
		fail(w, 400, "the price must be above zero")
		return
	case in.DataLimit < 0 || in.Days < 0 || in.Days > 3650 || in.IPLimit < 0 || in.IPLimit > 100:
		fail(w, 400, "check the data, days and devices")
		return
	}
	if id := r.PathValue("id"); id != "" {
		in.ID, _ = strconv.ParseInt(id, 10, 64)
		if _, err := s.St.PlanByID(in.ID); err != nil {
			fail(w, 404, "plan not found")
			return
		}
	} else {
		in.ID = 0
	}
	if err := s.St.SavePlan(&in); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, in)
}

func (s *Server) deletePlan(w http.ResponseWriter, r *http.Request) {
	s.St.DeletePlan(pathID(r))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------- orders

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	f := store.OrderFilter{Status: r.URL.Query().Get("status"), Kind: r.URL.Query().Get("kind")}
	f.Limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if a.Role == store.RoleReseller {
		f.AdminID = a.ID // their top-ups and what their wallet bought
	}
	list, err := s.St.Orders(f)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	out := []orderAPIView{}
	for _, o := range list {
		out = append(out, s.orderView(r, o))
	}
	writeJSON(w, 200, out)
}

func (s *Server) orderFor(w http.ResponseWriter, r *http.Request) *store.Order {
	o, err := s.St.OrderByID(pathID(r))
	if err != nil {
		fail(w, 404, "order not found")
		return nil
	}
	return o
}

func (s *Server) approveOrder(w http.ResponseWriter, r *http.Request) {
	o := s.orderFor(w, r)
	if o == nil {
		return
	}
	if o.Method == store.PayWallet {
		fail(w, 400, "wallet orders are paid already")
		return
	}
	if err := s.bill().Pay(o, "", adminOf(r).Username); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, s.orderView(r, o))
}

func (s *Server) rejectOrder(w http.ResponseWriter, r *http.Request) {
	o := s.orderFor(w, r)
	if o == nil {
		return
	}
	var in struct{ Reason string }
	readJSON(r, &in)
	if err := s.bill().Reject(o, in.Reason, adminOf(r).Username); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, s.orderView(r, o))
}

func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request) {
	o := s.orderFor(w, r)
	if o == nil {
		return
	}
	if err := s.bill().Cancel(o); err != nil || o.Status != store.OrderCancelled {
		fail(w, 400, "only unpaid orders can be cancelled")
		return
	}
	writeJSON(w, 200, s.orderView(r, o))
}

func (s *Server) orderReceipt(w http.ResponseWriter, r *http.Request) {
	o := s.orderFor(w, r)
	if o == nil {
		return
	}
	name := filepath.Base(o.Receipt)
	if o.Receipt == "" || name != o.Receipt {
		fail(w, 404, "no receipt")
		return
	}
	data, err := os.ReadFile(filepath.Join(s.ReceiptDir(), name))
	if err != nil {
		fail(w, 404, "the receipt file is missing")
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Write(data)
}

// ---------------------------------------------------------------- wallets

func (s *Server) myWallet(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	set := billing.LoadSettings(s.St)
	txs, _ := s.St.WalletTxs(a.ID, 200)
	writeJSON(w, 200, map[string]any{"enabled": set.Wallet, "balance": s.St.Balance(a.ID), "discount": set.Discount,
		"methods": set.Methods(), "currency": set.Currency, "transactions": txs})
}

func (s *Server) topUp(w http.ResponseWriter, r *http.Request) {
	a := adminOf(r)
	var in struct {
		Amount int64
		Method string
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	o, err := s.bill().Create(billing.Request{Kind: store.OrderTopUp, AdminID: a.ID, Amount: in.Amount, Method: in.Method, Contact: a.Username})
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, s.orderView(r, o))
}

func (s *Server) listWallets(w http.ResponseWriter, r *http.Request) {
	admins, err := s.St.Admins()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	type row struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		Balance  int64  `json:"balance"`
	}
	out := []row{}
	for _, a := range admins {
		if a.Role == store.RoleReseller {
			out = append(out, row{a.ID, a.Username, a.Balance})
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) walletHistory(w http.ResponseWriter, r *http.Request) {
	txs, err := s.St.WalletTxs(pathID(r), 200)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, txs)
}

func (s *Server) adjustWallet(w http.ResponseWriter, r *http.Request) {
	a, err := s.St.AdminByID(pathID(r))
	if err != nil || a.Role != store.RoleReseller {
		fail(w, 404, "reseller not found")
		return
	}
	var in struct {
		Amount int64
		Note   string
	}
	if err := readJSON(r, &in); err != nil || in.Amount == 0 {
		fail(w, 400, "give an amount (negative to take away)")
		return
	}
	note := strings.TrimSpace(in.Note)
	if note == "" {
		note = "by " + adminOf(r).Username
	}
	bal, err := s.St.WalletMove(a.ID, in.Amount, 0, note)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.St.Log("billing", "%s changed the wallet of %s by %d (now %d)", adminOf(r).Username, a.Username, in.Amount, bal)
	writeJSON(w, 200, map[string]int64{"balance": bal})
}

// ---------------------------------------------------------------- plans on users

// planFor reads a plan id for a user change; resellers with wallets must
// name one, and pay for it.
func (s *Server) planFor(a *store.Admin, id int64) (*store.Plan, error) {
	if id == 0 {
		if a.Role == store.RoleReseller && billing.LoadSettings(s.St).Wallet {
			return nil, fmt.Errorf("choose a plan: your wallet pays for new users")
		}
		return nil, nil
	}
	p, err := s.St.PlanByID(id)
	if err != nil {
		return nil, fmt.Errorf("plan not found")
	}
	if a.Role == store.RoleReseller && !p.Enabled {
		return nil, fmt.Errorf("this plan is not for sale")
	}
	return p, nil
}

// renewUser gives a user a plan: resellers pay from their wallet (when
// wallets are on), admins give it.
func (s *Server) renewUser(w http.ResponseWriter, r *http.Request) {
	u := s.userFor(w, r)
	if u == nil {
		return
	}
	var in struct{ PlanID int64 }
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	a := adminOf(r)
	p, err := s.St.PlanByID(in.PlanID)
	if err != nil || (a.Role == store.RoleReseller && !p.Enabled) {
		fail(w, 400, "plan not found")
		return
	}
	billing.ApplyPlan(u, p, time.Now().Unix(), true)
	if err := s.checkQuota(a, 1, u.DataLimit, u.ID); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var cost int64
	if a.Role == store.RoleReseller && billing.LoadSettings(s.St).Wallet {
		if cost, err = s.bill().WalletBuy(a, p, 1, u); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	if err := s.St.UpdateUser(u); err != nil {
		if cost > 0 {
			s.St.WalletMove(a.ID, cost, 0, "refund: renewal of "+u.Name+" not saved")
		}
		fail(w, 400, err.Error())
		return
	}
	s.St.Log("user", "%s renewed %s with %s", a.Username, u.Name, p.Name)
	s.Mgr.RequestApply()
	writeJSON(w, 200, s.view(r, u, s.Mgr.OnlineUsers(60)))
}
