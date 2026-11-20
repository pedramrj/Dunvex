package server

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/billing"
	"github.com/pedramrj/dunvex/internal/store"
)

// The shop is public, under the subscription path (…/sub/shop/), so the
// panel's path stays secret. Buyers pick a plan, a payment method, and land
// on their order's page, which shows what to pay and, once paid, the
// subscription link.

// ShopPath is the shop's prefix, e.g. "/sub/shop/".
func (s *Server) ShopPath() string { return s.SubPath() + "shop/" }

// ShopURL is the shop's full address (r may be nil).
func (s *Server) ShopURL(r *http.Request) string { return s.PublicBase(r) + s.ShopPath() }

// Bill is the billing service (made on first use when nobody set one).
func (s *Server) bill() *billing.Service {
	s.billOnce.Do(func() {
		if s.Bill == nil {
			s.Bill = &billing.Service{St: s.St, NewUser: s.newUser, ReceiptDir: s.ReceiptDir(),
				Apply: func() {
					if s.Mgr != nil {
						s.Mgr.RequestApply()
					}
				}}
		}
	})
	return s.Bill
}

// ReceiptDir holds the receipts buyers upload.
func (s *Server) ReceiptDir() string { return filepath.Join(s.Home, "receipts") }

// allow is a small per-key rate limit: at most n events per window.
func (s *Server) allow(key string, n int, window time.Duration) bool {
	s.limMu.Lock()
	defer s.limMu.Unlock()
	if s.hits == nil {
		s.hits = map[string][]time.Time{}
	}
	cut := time.Now().Add(-window)
	var keep []time.Time
	for _, t := range s.hits[key] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= n {
		s.hits[key] = keep
		return false
	}
	s.hits[key] = append(keep, time.Now())
	return true
}

type planView struct {
	ID                            int64
	Name, Note, Price, Data, Days string
	Devices                       string
	Start                         bool
}

type methodView struct{ ID, Name, Note string }

type orderView struct {
	Code, Token, Kind, What, Amount, MethodName string
	Status, StatusText, StatusClass, Note       string
	Cards                                       []billing.Card
	CryptoAmount, Address                       string
	AddressQR, SubQR                            template.HTML
	SubURL                                      string
	ShowCards, ShowZarinpal, ShowUSDT, TG       bool
}

type shopPage struct {
	Title, Currency, Support, FontBase, Brand, Base string
	Accent                                          template.CSS
	Mode                                            string // plans | buy | order | closed
	Renew, RenewToken                               string
	Plans                                           []planView
	Plan                                            planView
	Methods                                         []methodView
	Order                                           *orderView
	Err, Msg                                        string
	Poll                                            bool
}

// Money writes 150000 as "150,000".
func Money(v int64) string {
	s := strconv.FormatInt(v, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func viewPlan(p *store.Plan) planView {
	v := planView{ID: p.ID, Name: p.Name, Note: p.Note, Price: Money(p.Price), Data: "نامحدود", Days: "بدون محدودیت زمانی",
		Start: p.StartOnFirst && p.Days > 0}
	if p.DataLimit > 0 {
		v.Data = billing.DataText(p.DataLimit, true)
	}
	if p.Days > 0 {
		v.Days = strconv.Itoa(p.Days) + " روز"
	}
	if p.IPLimit > 0 {
		v.Devices = strconv.Itoa(p.IPLimit)
	}
	return v
}

var methodNames = map[string]methodView{
	store.PayCard:     {store.PayCard, "کارت‌به‌کارت", "واریز به کارت و ارسال رسید؛ بعد از تأیید فعال می‌شود."},
	store.PayZarinpal: {store.PayZarinpal, "پرداخت آنلاین (زرین‌پال)", "همهٔ کارت‌های بانکی؛ خودکار فعال می‌شود."},
	store.PayUSDT:     {store.PayUSDT, "تتر (USDT روی ترون)", "انتقال از کیف پول یا صرافی؛ خودکار تأیید می‌شود."},
	store.PayWallet:   {store.PayWallet, "کیف پول نماینده", ""},
	store.PayManual:   {store.PayManual, "ثبت دستی", ""},
}

var statusNames = map[string][2]string{
	store.OrderPending:   {"در انتظار پرداخت", "warn"},
	store.OrderReview:    {"در انتظار تأیید رسید", "vio"},
	store.OrderPaid:      {"پرداخت‌شده", "ok"},
	store.OrderRejected:  {"رسید تأیید نشد", "bad"},
	store.OrderCancelled: {"لغو شد", "bad"},
	store.OrderExpired:   {"منقضی شد", "bad"},
}

func (s *Server) shopBase(set billing.Settings) shopPage {
	title := set.Title
	if title == "" {
		title = s.St.GetSetting("sub_title", "Dunvex")
	}
	return shopPage{Title: title, Currency: set.Currency, Support: set.Support, FontBase: s.SubPath() + "_font/",
		Brand: s.SubPath() + "_brand/", Base: s.ShopPath(), Accent: template.CSS(accentOr(s.St.GetSetting("sub_accent", "")))}
}

func (s *Server) renderShop(w http.ResponseWriter, code int, p shopPage) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := shopTmpl.Execute(w, p); err != nil {
		fmt.Fprint(w, err.Error())
	}
}

// serveShop answers everything under the shop's path.
func (s *Server) serveShop(w http.ResponseWriter, r *http.Request, rest string) {
	set := billing.LoadSettings(s.St)
	page := s.shopBase(set)
	switch {
	case rest == "" || rest == "buy" || rest == "order":
		if !set.Shop {
			page.Mode = "closed"
			s.renderShop(w, http.StatusNotFound, page)
			return
		}
		switch rest {
		case "":
			s.shopPlans(w, r, set, page)
		case "buy":
			s.shopBuy(w, r, set, page)
		default:
			s.shopOrder(w, r, set, page)
		}
	case strings.HasPrefix(rest, "o/"):
		token, action, _ := strings.Cut(strings.TrimPrefix(rest, "o/"), "/")
		o, err := s.St.OrderByToken(token)
		if err != nil {
			decoy(w)
			return
		}
		switch action {
		case "":
			s.orderPage(w, r, set, page, o)
		case "status":
			writeJSON(w, 200, map[string]string{"status": o.Status})
		case "receipt":
			s.shopReceipt(w, r, o)
		case "pay":
			s.shopZarinpal(w, r, o)
		case "zp":
			s.shopZarinpalBack(w, r, o)
		default:
			decoy(w)
		}
	default:
		decoy(w)
	}
}

// renewTarget is the user a ?renew= token names (nil when none or unknown).
func (s *Server) renewTarget(r *http.Request) *store.User {
	t := r.FormValue("renew")
	if t == "" {
		return nil
	}
	u, err := s.St.UserByToken(t)
	if err != nil {
		return nil
	}
	return u
}

func (s *Server) shopPlans(w http.ResponseWriter, r *http.Request, set billing.Settings, page shopPage) {
	page.Mode = "plans"
	if u := s.renewTarget(r); u != nil {
		page.Renew, page.RenewToken = u.Name, u.SubToken
	} else if r.FormValue("renew") != "" {
		page.Err = "این اشتراک پیدا نشد."
	}
	plans, _ := s.St.Plans(true)
	for _, p := range plans {
		page.Plans = append(page.Plans, viewPlan(p))
	}
	s.renderShop(w, 200, page)
}

func (s *Server) shopBuy(w http.ResponseWriter, r *http.Request, set billing.Settings, page shopPage) {
	id, _ := strconv.ParseInt(r.FormValue("plan"), 10, 64)
	p, err := s.St.PlanByID(id)
	if err != nil || !p.Enabled {
		http.Redirect(w, r, s.ShopPath(), http.StatusSeeOther)
		return
	}
	page.Mode, page.Plan = "buy", viewPlan(p)
	if u := s.renewTarget(r); u != nil {
		page.Renew, page.RenewToken = u.Name, u.SubToken
	}
	for _, m := range set.Methods() {
		page.Methods = append(page.Methods, methodNames[m])
	}
	if e := r.FormValue("e"); e != "" {
		page.Err = e
	}
	s.renderShop(w, 200, page)
}

func (s *Server) shopOrder(w http.ResponseWriter, r *http.Request, set billing.Settings, page shopPage) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, s.ShopPath(), http.StatusSeeOther)
		return
	}
	back := func(msg string) {
		q := url.Values{"plan": {r.FormValue("plan")}, "e": {msg}}
		if t := r.FormValue("renew"); t != "" {
			q.Set("renew", t)
		}
		http.Redirect(w, r, s.ShopPath()+"buy?"+q.Encode(), http.StatusSeeOther)
	}
	if !s.allow("order:"+clientIP(r), 12, time.Hour) {
		back("سفارش‌های زیادی ثبت شد؛ کمی بعد دوباره امتحان کنید.")
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("plan"), 10, 64)
	req := billing.Request{Kind: store.OrderNew, PlanID: id, Method: r.FormValue("method"), Contact: r.FormValue("contact")}
	if t := r.FormValue("renew"); t != "" {
		u := s.renewTarget(r)
		if u == nil {
			back("این اشتراک پیدا نشد.")
			return
		}
		req.Kind, req.UserID = store.OrderRenew, u.ID
	}
	o, err := s.bill().Create(req)
	if err != nil {
		back(err.Error())
		return
	}
	http.Redirect(w, r, s.ShopPath()+"o/"+o.Token, http.StatusSeeOther)
}

func (s *Server) orderPage(w http.ResponseWriter, r *http.Request, set billing.Settings, page shopPage, o *store.Order) {
	page.Mode = "order"
	st := statusNames[o.Status]
	v := &orderView{Code: o.Code, Token: o.Token, Kind: o.Kind, Amount: Money(o.Amount), MethodName: methodNames[o.Method].Name,
		Status: o.Status, StatusText: st[0], StatusClass: st[1], TG: o.TGChat != 0}
	switch o.Kind {
	case store.OrderTopUp:
		v.What = "شارژ کیف پول نماینده"
	case store.OrderRenew:
		v.What = "تمدید با پلن " + o.PlanName
		if u, err := s.St.UserByID(o.UserID); err == nil {
			v.What += " برای " + u.Name
		}
	default:
		v.What = "خرید پلن " + o.PlanName
	}
	if o.Status == store.OrderRejected {
		v.Note = strings.TrimSpace(strings.Split(o.Note, " (")[0])
	}
	open := o.Status == store.OrderPending || o.Status == store.OrderRejected
	switch o.Method {
	case store.PayCard:
		v.ShowCards, v.Cards = open, set.Cards
	case store.PayZarinpal:
		v.ShowZarinpal = o.Status == store.OrderPending
	case store.PayUSDT:
		v.ShowUSDT = o.Status == store.OrderPending
		v.CryptoAmount, v.Address, v.AddressQR = o.CryptoAmount, set.USDTAddress, template.HTML(QRSVG(set.USDTAddress))
	}
	if o.Status == store.OrderPaid && o.Kind != store.OrderTopUp {
		if u, err := s.St.UserByID(o.UserID); err == nil {
			v.SubURL = s.SubURL(r, u)
			v.SubQR = template.HTML(QRSVG(v.SubURL))
		}
	}
	page.Order = v
	page.Poll = o.Status == store.OrderReview || (o.Status == store.OrderPending && o.Method == store.PayUSDT)
	page.Err, page.Msg = r.FormValue("e"), r.FormValue("m")
	s.renderShop(w, 200, page)
}

func (s *Server) shopReceipt(w http.ResponseWriter, r *http.Request, o *store.Order) {
	back := func(key, msg string) {
		http.Redirect(w, r, s.ShopPath()+"o/"+o.Token+"?"+url.Values{key: {msg}}.Encode(), http.StatusSeeOther)
	}
	if r.Method != http.MethodPost {
		back("e", "")
		return
	}
	if !s.allow("receipt:"+clientIP(r), 20, time.Hour) {
		back("e", "رسیدهای زیادی فرستاده شد؛ کمی بعد دوباره امتحان کنید.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	if err := r.ParseMultipartForm(6 << 20); err != nil {
		back("e", "فایل بزرگ‌تر از ۵ مگابایت است.")
		return
	}
	name := ""
	if f, _, err := r.FormFile("receipt"); err == nil {
		defer f.Close()
		name, err = s.saveReceipt(o, f)
		if err != nil {
			back("e", err.Error())
			return
		}
	}
	if err := s.bill().SubmitReceipt(o, name, r.FormValue("note")); err != nil {
		if name != "" {
			os.Remove(filepath.Join(s.ReceiptDir(), name))
		}
		back("e", "رسید ثبت نشد: "+err.Error())
		return
	}
	back("m", "رسید ثبت شد.")
}

// saveReceipt keeps an uploaded image (checked by its content) and returns its name.
func (s *Server) saveReceipt(o *store.Order, f io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(f, 5<<20+1))
	if err != nil {
		return "", err
	}
	name, err := s.bill().SaveReceipt(o, data)
	switch err {
	case billing.ErrReceiptTooBig:
		return "", fmt.Errorf("فایل بزرگ‌تر از ۵ مگابایت است.")
	case billing.ErrReceiptType:
		return "", fmt.Errorf("رسید باید عکس JPG، PNG یا WebP باشد.")
	}
	return name, err
}

func (s *Server) shopZarinpal(w http.ResponseWriter, r *http.Request, o *store.Order) {
	page := s.ShopPath() + "o/" + o.Token
	if !s.allow("pay:"+clientIP(r), 30, time.Hour) {
		http.Redirect(w, r, page+"?e="+url.QueryEscape("تلاش‌های زیادی شد؛ کمی بعد دوباره امتحان کنید."), http.StatusSeeOther)
		return
	}
	to, err := s.bill().ZarinpalStart(o, s.ShopURL(r)+"o/"+o.Token+"/zp")
	if err != nil {
		http.Redirect(w, r, page+"?e="+url.QueryEscape("درگاه در دسترس نیست: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) shopZarinpalBack(w http.ResponseWriter, r *http.Request, o *store.Order) {
	page := s.ShopPath() + "o/" + o.Token
	if err := s.bill().ZarinpalReturn(o, r.FormValue("Authority"), r.FormValue("Status")); err != nil {
		http.Redirect(w, r, page+"?e="+url.QueryEscape("پرداخت انجام نشد: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, page, http.StatusSeeOther)
}

var shopTmpl = template.Must(template.New("shop").Funcs(template.FuncMap{
	"safeURL": func(s string) template.URL { return template.URL(s) },
	// 16-digit card numbers read in fours; IBANs and the rest stay as given
	"group": func(s string) string {
		if len(s) != 16 || strings.Trim(s, "0123456789") != "" {
			return s
		}
		return s[:4] + " " + s[4:8] + " " + s[8:12] + " " + s[12:]
	},
}).Parse(shopHTML))

//go:embed shop.html
var shopHTML string
