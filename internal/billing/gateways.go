package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pedramrj/dunvex/internal/store"
)

// ---------------------------------------------------------------- ZarinPal

// ZarinPal (v4): a payment request gives an authority, the buyer pays on
// ZarinPal's page and comes back to the callback, where Dunvex verifies the
// payment. Amounts go in Rial: the shop's Toman × 10.

func (s *Service) zarinpalBase(set Settings) string {
	if s.ZarinpalBase != "" {
		return s.ZarinpalBase
	}
	if set.ZarinpalSandbox {
		return "https://sandbox.zarinpal.com"
	}
	return "https://payment.zarinpal.com"
}

// zpAnswer is ZarinPal's reply; "errors" is a list when empty, an object otherwise.
type zpAnswer struct {
	Data   json.RawMessage `json:"data"`
	Errors json.RawMessage `json:"errors"`
}

func (s *Service) zarinpal(set Settings, path string, body map[string]any) (map[string]any, error) {
	body["merchant_id"] = set.ZarinpalMerchant
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", s.zarinpalBase(set)+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := s.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("ZarinPal does not answer: %v", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var a zpAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("ZarinPal answered %s", res.Status)
	}
	var e struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	}
	if len(a.Errors) > 0 && a.Errors[0] == '{' && json.Unmarshal(a.Errors, &e) == nil && e.Code != 0 {
		return nil, fmt.Errorf("ZarinPal: %s (%d)", e.Message, e.Code)
	}
	var data map[string]any
	if len(a.Data) > 0 && a.Data[0] == '{' {
		json.Unmarshal(a.Data, &data)
	}
	return data, nil
}

func zpCode(d map[string]any) int {
	if c, ok := d["code"].(float64); ok {
		return int(c)
	}
	return 0
}

// ZarinpalStart asks ZarinPal for a payment and returns the page to send the
// buyer to. callback is where the buyer comes back.
func (s *Service) ZarinpalStart(o *store.Order, callback string) (string, error) {
	set := LoadSettings(s.St)
	if o.Method != store.PayZarinpal || !set.Has(store.PayZarinpal) {
		return "", fmt.Errorf("ZarinPal is not available")
	}
	if o.Status != store.OrderPending {
		return "", fmt.Errorf("this order is %s", o.Status)
	}
	d, err := s.zarinpal(set, "/pg/v4/payment/request.json", map[string]any{
		"amount": o.Amount * 10, "callback_url": callback,
		"description": strings.TrimSpace(orderTitle(set) + " " + o.PlanName + " " + o.Code),
	})
	if err != nil {
		return "", err
	}
	auth, _ := d["authority"].(string)
	if zpCode(d) != 100 || auth == "" {
		return "", fmt.Errorf("ZarinPal did not accept the payment (code %d)", zpCode(d))
	}
	o.PayRef = auth
	if err := s.St.UpdateOrder(o); err != nil {
		return "", err
	}
	return s.zarinpalBase(set) + "/pg/StartPay/" + url.PathEscape(auth), nil
}

func orderTitle(set Settings) string {
	if set.Title != "" {
		return set.Title
	}
	return "Dunvex"
}

// ZarinpalReturn handles the buyer coming back: it verifies the payment with
// ZarinPal and applies the order. A cancelled payment leaves it open.
func (s *Service) ZarinpalReturn(o *store.Order, authority, status string) error {
	set := LoadSettings(s.St)
	if o.Status == store.OrderPaid {
		return nil
	}
	if authority == "" || authority != o.PayRef {
		return fmt.Errorf("this payment is not for this order")
	}
	if status != "OK" {
		return fmt.Errorf("the payment was not completed")
	}
	d, err := s.zarinpal(set, "/pg/v4/payment/verify.json", map[string]any{"amount": o.Amount * 10, "authority": authority})
	if err != nil {
		return err
	}
	if c := zpCode(d); c != 100 && c != 101 { // 101: verified before
		return fmt.Errorf("ZarinPal did not confirm the payment (code %d)", c)
	}
	ref := ""
	if v, ok := d["ref_id"].(float64); ok {
		ref = strconv.FormatInt(int64(v), 10)
	}
	if card, _ := d["card_pan"].(string); card != "" {
		o.Note = strings.TrimSpace(o.Note + " card " + card)
	}
	return s.Pay(o, "zarinpal:"+ref, "")
}

// ---------------------------------------------------------------- USDT (TRC20)

// USDTContract is Tether on TRON.
const USDTContract = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"

// ValidTronAddress checks the look of a TRON address (base58, starts with T).
func ValidTronAddress(a string) bool {
	if len(a) != 34 || a[0] != 'T' {
		return false
	}
	for _, c := range a {
		if !strings.ContainsRune("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz", c) {
			return false
		}
	}
	return true
}

// trc20Transfer is one USDT transfer TronGrid lists.
type trc20Transfer struct {
	TxID      string `json:"transaction_id"`
	To        string `json:"to"`
	Value     string `json:"value"`
	Timestamp int64  `json:"block_timestamp"`
	Token     struct {
		Address string `json:"address"`
	} `json:"token_info"`
}

// CheckUSDT looks for the transfers that pay open USDT orders.
func (s *Service) CheckUSDT(ctx context.Context) error {
	set := LoadSettings(s.St)
	if set.USDTAddress == "" {
		return nil
	}
	var open []*store.Order
	for _, st := range []string{store.OrderPending, store.OrderExpired} {
		l, err := s.St.Orders(store.OrderFilter{Status: st, Method: store.PayUSDT, Limit: 1000})
		if err != nil {
			return err
		}
		for _, o := range l {
			if st == store.OrderPending || s.clock().Unix()-o.CreatedAt < 86400 {
				open = append(open, o)
			}
		}
	}
	if len(open) == 0 {
		return nil
	}
	since := open[0].CreatedAt
	for _, o := range open {
		since = min(since, o.CreatedAt)
	}
	list, err := s.trc20(ctx, set, (since-120)*1000)
	if err != nil {
		return err
	}
	for _, t := range list {
		if t.Token.Address != USDTContract || t.To != set.USDTAddress || s.St.PayRefUsed(store.PayUSDT, t.TxID) {
			continue
		}
		for _, o := range open {
			want, ok := ParseUSDT(o.CryptoAmount)
			if !ok || strconv.FormatInt(want, 10) != t.Value || t.Timestamp < (o.CreatedAt-120)*1000 || o.Status == store.OrderPaid {
				continue
			}
			if err := s.Pay(o, t.TxID, ""); err == nil {
				o.Status = store.OrderPaid
			}
			break
		}
	}
	return nil
}

func (s *Service) trc20(ctx context.Context, set Settings, sinceMs int64) ([]trc20Transfer, error) {
	base := s.TronBase
	if base == "" {
		base = "https://api.trongrid.io"
	}
	q := url.Values{"only_to": {"true"}, "only_confirmed": {"true"}, "limit": {"200"}, "contract_address": {USDTContract},
		"min_timestamp": {strconv.FormatInt(sinceMs, 10)}}
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/v1/accounts/"+url.PathEscape(set.USDTAddress)+"/transactions/trc20?"+q.Encode(), nil)
	req.Header.Set("Accept", "application/json")
	if set.TronAPIKey != "" {
		req.Header.Set("TRON-PRO-API-KEY", set.TronAPIKey)
	}
	res, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("TronGrid answered %s", res.Status)
	}
	var out struct {
		Data    []trc20Transfer `json:"data"`
		Success bool            `json:"success"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, fmt.Errorf("TronGrid did not answer the list")
	}
	return out.Data, nil
}
