package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/billing"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
)

// shopGet and shopPost are a browser on the shop: they do not follow
// redirects, so the test sees where each step leads.
var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func shopGet(t *testing.T, u string) (int, string, string) {
	t.Helper()
	res, err := noRedirect.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Location"), string(b)
}

func shopPost(t *testing.T, u string, form url.Values) (int, string) {
	t.Helper()
	res, err := noRedirect.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode, res.Header.Get("Location")
}

func receiptPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{200, 10, 10, 255})
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func TestShopEndToEnd(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, filepath.Join(dir, "bin"), filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	// a stand-in ZarinPal
	zp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pg/v4/payment/request.json":
			w.Write([]byte(`{"data":{"authority":"S0000000000000000000000000000000zp01","code":100},"errors":[]}`))
		case "/pg/v4/payment/verify.json":
			w.Write([]byte(`{"data":{"code":100,"ref_id":555},"errors":[]}`))
		}
	}))
	defer zp.Close()
	srv.bill().ZarinpalBase = zp.URL
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	shop := ts.URL + "/sub/shop/"

	owner := newClient(t, ts.URL)
	owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"})

	// closed until the owner opens it
	if code, _, _ := shopGet(t, shop); code != 404 {
		t.Fatalf("closed shop answered %d", code)
	}
	if code, out := owner.do("PUT", "/billing/settings", map[string]any{"shop": true, "card": true,
		"cards":    []map[string]string{{"number": "6037-9900-1111-2222", "holder": "Pedram", "bank": "Melli"}},
		"zarinpal": true, "zarinpalMerchant": "1344b5d4-0048-11e8-94db-005056a205be", "wallet": true, "discount": 10, "orderHours": 24}); code != 200 {
		t.Fatalf("settings: %d %v", code, out)
	}
	code, out := owner.do("POST", "/billing/plans", map[string]any{"name": "ماهانه ۵۰ گیگ", "price": 150000, "dataLimit": 50 << 30, "days": 30, "ipLimit": 2, "startOnFirstUse": true, "enabled": true})
	if code != 200 {
		t.Fatalf("plan: %d %v", code, out)
	}
	planID := int64(out["id"].(float64))

	// the buyer: plans → checkout → order
	code, _, page := shopGet(t, shop)
	if code != 200 || !strings.Contains(page, "ماهانه ۵۰ گیگ") || !strings.Contains(page, "۱۵۰٬۰۰۰") {
		t.Fatalf("plans page %d", code)
	}
	_, _, page = shopGet(t, shop+"buy?plan="+itoa(planID))
	if !strings.Contains(page, `value="card"`) || !strings.Contains(page, `value="zarinpal"`) || strings.Contains(page, `value="usdt"`) {
		t.Fatal("checkout methods")
	}
	code, loc := shopPost(t, shop+"order", url.Values{"plan": {itoa(planID)}, "method": {"card"}, "contact": {"sara"}})
	if code != 303 || !strings.HasPrefix(loc, "/sub/shop/o/") {
		t.Fatalf("order: %d %s", code, loc)
	}
	orderPage := ts.URL + loc
	_, _, page = shopGet(t, orderPage)
	if !strings.Contains(page, "6037990011112222") || !strings.Contains(page, "ارسال رسید") {
		i := strings.Index(page, "<h1>")
		t.Fatalf("order page shows no card: %.1500s", page[max(i, 0):])
	}

	// the receipt: only images
	upload := func(name string, data []byte) string {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("receipt", name)
		fw.Write(data)
		mw.WriteField("note", "4421")
		mw.Close()
		req, _ := http.NewRequest("POST", orderPage+"/receipt", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		res, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.Header.Get("Location")
	}
	if l := upload("x.html", []byte("<html><script>alert(1)</script>")); !strings.Contains(l, "e=") {
		t.Fatalf("a non-image receipt was taken: %s", l)
	}
	if l := upload("r.png", receiptPNG()); !strings.Contains(l, "m=") {
		t.Fatalf("receipt: %s", l)
	}

	// the admin sees it, its image, and approves it
	_, out = owner.do("GET", "/billing/orders?status=review", nil)
	list := out["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("orders waiting: %v", out)
	}
	ord := list[0].(map[string]any)
	oid := itoa(int64(ord["id"].(float64)))
	if !strings.HasPrefix(ord["page"].(string), ts.URL+"/sub/shop/o/") || ord["receiptNote"] != "4421" {
		t.Errorf("order view: %v", ord)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/dunvex/api/billing/orders/"+oid+"/receipt", nil)
	res, _ := owner.hc.Do(req)
	img, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Equal(img, receiptPNG()) || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("receipt image: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if code, out := owner.do("POST", "/billing/orders/"+oid+"/approve", nil); code != 200 || out["status"] != "paid" || out["userName"] != "sara" {
		t.Fatalf("approve: %d %v", code, out)
	}
	_, _, page = shopGet(t, orderPage)
	u, _ := st.UserByName("sara")
	if u == nil || !strings.Contains(page, "/sub/"+u.SubToken) || !strings.Contains(page, "پرداخت تأیید شد") {
		t.Fatal("the paid order page has no subscription link")
	}

	// renewing from the user's own page
	req, _ = http.NewRequest("GET", ts.URL+"/sub/"+u.SubToken, nil)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	res, _ = http.DefaultClient.Do(req)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), "/sub/shop/?renew="+u.SubToken) {
		t.Fatal("the user page has no renew button")
	}
	_, loc = shopPost(t, shop+"order", url.Values{"plan": {itoa(planID)}, "method": {"zarinpal"}, "renew": {u.SubToken}})
	payPage := ts.URL + loc
	code, loc, _ = shopGet(t, payPage+"/pay")
	if code != 303 || loc != zp.URL+"/pg/StartPay/S0000000000000000000000000000000zp01" {
		t.Fatalf("to the gateway: %d %s", code, loc)
	}
	shopGet(t, payPage+"/zp?Authority=S0000000000000000000000000000000zp01&Status=OK")
	u2, _ := st.UserByName("sara")
	if u2.DataLimit != 100<<30 || u2.DurationDays != 60 {
		t.Errorf("renewed: limit %d days %d", u2.DataLimit>>30, u2.DurationDays)
	}
	if n, _ := st.Users(); len(n) != 1 {
		t.Error("a renewal made a new user")
	}

	// a reseller's wallet
	owner.do("POST", "/admins", map[string]any{"username": "rez", "password": "reseller-pass-1", "role": "reseller", "enabled": true})
	rez := newClient(t, ts.URL)
	rez.do("POST", "/login", map[string]string{"username": "rez", "password": "reseller-pass-1"})
	if code, out := rez.do("POST", "/users", map[string]any{"name": "ali"}); code != 400 || !strings.Contains(out["error"].(string), "plan") {
		t.Errorf("a reseller made a user without a plan: %d %v", code, out)
	}
	if code, _ := rez.do("POST", "/users", map[string]any{"name": "ali", "planId": planID}); code != 400 {
		t.Error("an empty wallet paid")
	}
	_, out = rez.do("POST", "/wallet/topup", map[string]any{"amount": 300000, "method": "card"})
	topup := itoa(int64(out["id"].(float64)))
	if code, _ := rez.do("POST", "/billing/orders/"+topup+"/approve", nil); code != 403 {
		t.Error("a reseller approved its own top-up")
	}
	owner.do("POST", "/billing/orders/"+topup+"/approve", nil)
	if code, out := rez.do("POST", "/users", map[string]any{"name": "ali", "planId": planID}); code != 200 {
		t.Fatalf("wallet purchase: %d %v", code, out)
	}
	_, out = rez.do("GET", "/wallet", nil)
	if out["balance"] != float64(300000-135000) {
		t.Errorf("balance after a 10%% off plan: %v", out["balance"])
	}
	ali, _ := st.UserByName("ali")
	if code, _ := rez.do("POST", "/users/"+itoa(ali.ID)+"/renew", map[string]any{"planId": planID}); code != 200 {
		t.Error("wallet renewal")
	}
	if code, _ := rez.do("POST", "/users/"+itoa(u.ID)+"/renew", map[string]any{"planId": planID}); code != 404 {
		t.Error("a reseller renewed someone else's user")
	}
	_, out = rez.do("GET", "/billing/orders", nil)
	for _, o := range out["list"].([]any) {
		if o.(map[string]any)["adminName"] != "rez" {
			t.Errorf("a reseller sees another's order: %v", o)
		}
	}
	_, out = rez.do("GET", "/wallet", nil)
	if out["balance"] != float64(300000-2*135000) {
		t.Errorf("balance after the renewal: %v", out["balance"])
	}

	// a rejected receipt can be sent again
	_, loc = shopPost(t, shop+"order", url.Values{"plan": {itoa(planID)}, "method": {"card"}})
	orderPage = ts.URL + loc
	upload("r.png", receiptPNG())
	o2, _ := st.OrderByToken(strings.TrimPrefix(loc, "/sub/shop/o/"))
	owner.do("POST", "/billing/orders/"+itoa(o2.ID)+"/reject", map[string]string{"reason": "wrong amount"})
	_, _, page = shopGet(t, orderPage)
	if !strings.Contains(page, "wrong amount") || !strings.Contains(page, "ارسال رسید") {
		t.Error("a rejected order cannot send another receipt")
	}
	upload("r.png", receiptPNG())
	if o2, _ = st.OrderByID(o2.ID); o2.Status != store.OrderReview {
		t.Errorf("after a second receipt: %s", o2.Status)
	}

	// money summary
	_, out = owner.do("GET", "/billing", nil)
	stats := out["stats"].(map[string]any)
	if stats["todayCount"] != float64(2) || stats["todayTotal"] != float64(300000) || stats["review"] != float64(1) {
		t.Errorf("stats: %v", stats)
	}
	if s := out["settings"].(map[string]any); s["zarinpalMerchant"] != "1344b5d4-0048-11e8-94db-005056a205be" {
		t.Errorf("settings: %v", s)
	}
	b2, _ := json.Marshal(billing.LoadSettings(st).Methods())
	if string(b2) != `["card","zarinpal"]` {
		t.Errorf("methods: %s", b2)
	}
}
