package bot

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pedramrj/dunvex/internal/billing"
	"github.com/pedramrj/dunvex/internal/store"
)

// Selling in the bot: buyers pick a plan and a payment method, send a photo of
// their card receipt (or pay online, or in USDT), and get their subscription
// here once it is paid. Admins approve receipts with one tap.

// shopOn reports whether the bot sells now.
func (b *Bot) shopOn() bool {
	if b.d.Bill == nil {
		return false
	}
	set := billing.LoadSettings(b.d.St)
	if !set.Bot || len(set.Methods()) == 0 {
		return false
	}
	plans, _ := b.d.St.Plans(true)
	return len(plans) > 0
}

// buyerMenu is what a customer sees.
func (b *Bot) buyerMenu(chat int64) keyboard {
	kb := keyboard{{{Text: b.t("b_buy"), Data: "sh"}}}
	if b.trialOffered(chat) {
		kb = append(keyboard{b.trialButton()}, kb...)
	}
	if _, err := b.d.St.UserByTG(chat); err == nil {
		kb = append(kb, []button{{Text: b.t("b_renew"), Data: "sh:r"}, {Text: b.t("b_mine"), Data: "me"}})
	}
	return kb
}

func money(v int64) string {
	s := strconv.FormatInt(v, 10)
	var out strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(c)
	}
	return out.String()
}

func (b *Bot) planLine(p *store.Plan, cur string) string {
	data := b.t("unlimited")
	if p.DataLimit > 0 {
		b.mu.Lock()
		fa := b.lang != "en"
		b.mu.Unlock()
		data = billing.DataText(p.DataLimit, fa)
	}
	days := b.t("no_expiry")
	if p.Days > 0 {
		days = strconv.Itoa(p.Days) + " " + b.t("days")
	}
	return fmt.Sprintf("%s · %s · %s · %s %s", p.Name, data, days, money(p.Price), cur)
}

// onShopButton handles the buyer's buttons; it reports whether it did.
func (b *Bot) onShopButton(chat, msg int64, data string) bool {
	parts := strings.Split(data, ":")
	switch parts[0] {
	case "sh": // the plans (sh:r = for renewing)
		if !b.shopOn() {
			b.send(chat, b.t("shop_closed"), nil)
			return true
		}
		renew := arg(parts, 1) == "r"
		if renew {
			if _, err := b.d.St.UserByTG(chat); err != nil {
				b.send(chat, b.t("renew_unknown"), nil)
				return true
			}
		}
		set := billing.LoadSettings(b.d.St)
		plans, _ := b.d.St.Plans(true)
		var kb keyboard
		for _, p := range plans {
			d := "sp:" + strconv.FormatInt(p.ID, 10)
			if renew {
				d += ":r"
			}
			kb = append(kb, []button{{Text: b.planLine(p, set.Currency), Data: d}})
		}
		b.send(chat, map[bool]string{false: b.t("pick_plan"), true: b.t("pick_renew")}[renew], kb)
	case "sp": // a plan: how to pay
		id, _ := strconv.ParseInt(arg(parts, 1), 10, 64)
		p, err := b.d.St.PlanByID(id)
		if err != nil || !p.Enabled || !b.shopOn() {
			b.send(chat, b.t("shop_closed"), nil)
			return true
		}
		set := billing.LoadSettings(b.d.St)
		r := ""
		if arg(parts, 2) == "r" {
			r = ":r"
		}
		var kb keyboard
		for _, m := range set.Methods() {
			kb = append(kb, []button{{Text: b.t("pay_" + m), Data: "so:" + strconv.FormatInt(p.ID, 10) + ":" + m + r}})
		}
		b.send(chat, "<b>"+esc(b.planLine(p, set.Currency))+"</b>\n\n"+b.t("pick_method"), kb)
	case "so": // order it
		id, _ := strconv.ParseInt(arg(parts, 1), 10, 64)
		req := billing.Request{Kind: store.OrderNew, PlanID: id, Method: arg(parts, 2), TGChat: chat, Contact: "telegram " + strconv.FormatInt(chat, 10)}
		if arg(parts, 3) == "r" {
			u, err := b.d.St.UserByTG(chat)
			if err != nil {
				b.send(chat, b.t("renew_unknown"), nil)
				return true
			}
			req.Kind, req.UserID = store.OrderRenew, u.ID
		}
		if !b.shopOn() {
			b.send(chat, b.t("shop_closed"), nil)
			return true
		}
		o, err := b.d.Bill.Create(req)
		if err != nil {
			b.send(chat, "⚠️ "+esc(err.Error()), nil)
			return true
		}
		b.sendPayment(chat, o)
	case "sx": // cancel my order
		id, _ := strconv.ParseInt(arg(parts, 1), 10, 64)
		if o, err := b.d.St.OrderByID(id); err == nil && o.TGChat == chat {
			if b.d.Bill.Cancel(o) == nil && o.Status == store.OrderCancelled {
				b.forget(chat)
				b.send(chat, fmt.Sprintf(b.t("cancelled"), o.Code), b.buyerMenu(chat))
			}
		}
	default:
		return false
	}
	return true
}

func (b *Bot) orderPage(o *store.Order) string {
	if b.d.ShopURL == nil {
		return ""
	}
	return b.d.ShopURL() + "o/" + o.Token
}

// sendPayment tells the buyer how to pay an order.
func (b *Bot) sendPayment(chat int64, o *store.Order) {
	set := billing.LoadSettings(b.d.St)
	head := fmt.Sprintf(b.t("order_head"), o.Code, esc(o.PlanName), money(o.Amount), esc(set.Currency))
	cancel := []button{{Text: b.t("b_cancel"), Data: "sx:" + strconv.FormatInt(o.ID, 10)}}
	switch o.Method {
	case store.PayCard:
		var cards strings.Builder
		for _, c := range set.Cards {
			fmt.Fprintf(&cards, "\n<code>%s</code>\n%s", esc(c.Number), esc(strings.TrimSpace(c.Holder+" "+c.Bank)))
		}
		b.mu.Lock()
		b.waiting[chat] = "rc:" + strconv.FormatInt(o.ID, 10)
		b.mu.Unlock()
		b.send(chat, head+"\n"+fmt.Sprintf(b.t("pay_card_text"), money(o.Amount), esc(set.Currency))+"\n"+cards.String()+"\n\n"+b.t("send_receipt"), keyboard{cancel})
	case store.PayZarinpal:
		b.send(chat, head+"\n"+b.t("pay_zp_text"), keyboard{{{Text: b.t("b_pay"), URL: b.orderPage(o) + "/pay"}}, cancel})
	case store.PayUSDT:
		b.sendQR(chat, set.USDTAddress, head+"\n"+fmt.Sprintf(b.t("pay_usdt_text"), esc(o.CryptoAmount), esc(set.USDTAddress)),
			keyboard{{{Text: b.t("b_order_page"), URL: b.orderPage(o)}}, cancel})
	}
}

// forget drops what the chat was asked for.
func (b *Bot) forget(chat int64) {
	b.mu.Lock()
	delete(b.waiting, chat)
	b.mu.Unlock()
}

// onReceipt takes a receipt for the order the chat is paying: a photo (its
// file id) or a text (tracking number). It reports whether it did.
func (b *Bot) onReceipt(chat int64, photo, text string) bool {
	b.mu.Lock()
	want := b.waiting[chat]
	b.mu.Unlock()
	if !strings.HasPrefix(want, "rc:") || b.d.Bill == nil {
		return false
	}
	id, _ := strconv.ParseInt(strings.TrimPrefix(want, "rc:"), 10, 64)
	o, err := b.d.St.OrderByID(id)
	if err != nil || o.TGChat != chat {
		b.forget(chat)
		return false
	}
	file := ""
	if photo != "" {
		data, err := b.download(photo)
		if err == nil {
			file, err = b.d.Bill.SaveReceipt(o, data)
		}
		if err != nil {
			b.send(chat, "⚠️ "+esc(err.Error()), nil)
			return true
		}
	}
	if err := b.d.Bill.SubmitReceipt(o, file, text); err != nil {
		b.send(chat, "⚠️ "+esc(err.Error()), nil)
		return true
	}
	b.forget(chat)
	b.send(chat, fmt.Sprintf(b.t("receipt_ok"), o.Code), nil)
	return true
}

// download fetches a file the chat sent.
func (b *Bot) download(fileID string) ([]byte, error) {
	raw, err := b.call("getFile", map[string]any{"file_id": fileID})
	if err != nil {
		return nil, err
	}
	var f struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	json.Unmarshal(raw, &f)
	if f.Path == "" {
		return nil, fmt.Errorf("telegram did not give the file")
	}
	if f.Size > 5<<20 {
		return nil, billing.ErrReceiptTooBig
	}
	b.mu.Lock()
	url := b.api + "/file/bot" + b.token + "/" + f.Path
	b.mu.Unlock()
	res, err := b.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("could not fetch the file: %s", cleanErr(err, b.token))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not fetch the file: %s", res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, 5<<20+1))
}

// onOrder hears the shop's events: it tells buyers and admins.
func (b *Bot) onOrder(o *store.Order, event string) {
	b.mu.Lock()
	running := b.token != ""
	b.mu.Unlock()
	if !running {
		return
	}
	set := billing.LoadSettings(b.d.St)
	switch event {
	case billing.EventReview:
		b.tellAdminsReview(o, set)
	case billing.EventPaid:
		if o.TGChat != 0 && o.Kind != store.OrderTopUp {
			if u, err := b.d.St.UserByID(o.UserID); err == nil {
				b.send(o.TGChat, fmt.Sprintf(b.t(map[bool]string{true: "paid_renew", false: "paid_new"}[o.Kind == store.OrderRenew]), o.Code), nil)
				b.sendUserCard(o.TGChat, u)
			}
		}
		if o.Method != store.PayCard && o.Method != store.PayWallet { // card approvals are the admins' own doing
			for _, id := range b.adminList() {
				b.send(id, fmt.Sprintf(b.t("adm_paid"), o.Code, esc(o.PlanName), money(o.Amount), esc(set.Currency), b.t("pay_"+o.Method)), nil)
			}
		}
	case billing.EventRejected:
		if o.TGChat != 0 {
			b.mu.Lock()
			b.waiting[o.TGChat] = "rc:" + strconv.FormatInt(o.ID, 10)
			b.mu.Unlock()
			reason := strings.TrimSpace(strings.Split(o.Note, " (")[0])
			b.send(o.TGChat, fmt.Sprintf(b.t("rejected"), o.Code, esc(reason)), nil)
		}
	case billing.EventExpired:
		if o.TGChat != 0 {
			b.send(o.TGChat, fmt.Sprintf(b.t("expired"), o.Code), nil)
		}
	case billing.EventFailed:
		for _, id := range b.adminList() {
			b.send(id, fmt.Sprintf(b.t("adm_failed"), o.Code, esc(o.Note)), nil)
		}
	}
}

func (b *Bot) orderText(o *store.Order, set billing.Settings) string {
	who := o.Contact
	if o.Kind == store.OrderRenew {
		if u, err := b.d.St.UserByID(o.UserID); err == nil {
			who = u.Name + " · " + who
		}
	}
	if o.Kind == store.OrderTopUp {
		if a, err := b.d.St.AdminByID(o.AdminID); err == nil {
			who = "🧑‍💼 " + a.Username
		}
	}
	t := fmt.Sprintf(b.t("adm_review"), o.Code, b.t("kind_"+o.Kind), esc(o.PlanName), money(o.Amount), esc(set.Currency), esc(who))
	if o.ReceiptNote != "" {
		t += "\n📝 <code>" + esc(o.ReceiptNote) + "</code>"
	}
	return t
}

func (b *Bot) reviewButtons(o *store.Order) keyboard {
	id := strconv.FormatInt(o.ID, 10)
	return keyboard{{{Text: b.t("b_approve"), Data: "oa:" + id}, {Text: b.t("b_reject"), Data: "or:" + id}}}
}

// tellAdminsReview sends a waiting receipt to every admin, with the photo.
func (b *Bot) tellAdminsReview(o *store.Order, set billing.Settings) {
	text, kb := b.orderText(o, set), b.reviewButtons(o)
	var photo []byte
	if o.Receipt != "" && b.d.Bill != nil {
		photo, _ = os.ReadFile(filepath.Join(b.d.Bill.ReceiptDir, filepath.Base(o.Receipt)))
	}
	for _, id := range b.adminList() {
		if len(photo) > 0 {
			m, _ := json.Marshal(map[string]any{"inline_keyboard": kb})
			fields := map[string]string{"chat_id": strconv.FormatInt(id, 10), "caption": text, "parse_mode": "HTML", "reply_markup": string(m)}
			if b.upload("sendPhoto", fields, "photo", "receipt"+filepath.Ext(o.Receipt), photo) == nil {
				continue
			}
		}
		b.send(id, text, kb)
	}
}

// onOrderAdmin handles the admins' order buttons; it reports whether it did.
func (b *Bot) onOrderAdmin(chat, msg int64, data string) bool {
	parts := strings.Split(data, ":")
	if b.d.Bill == nil {
		return false
	}
	switch parts[0] {
	case "ol": // orders waiting for approval
		list, _ := b.d.St.Orders(store.OrderFilter{Status: store.OrderReview, Limit: 10})
		if len(list) == 0 {
			b.send(chat, b.t("no_orders"), b.backMenu())
			return true
		}
		set := billing.LoadSettings(b.d.St)
		for _, o := range list {
			b.send(chat, b.orderText(o, set), b.reviewButtons(o))
		}
	case "oa", "or":
		id, _ := strconv.ParseInt(arg(parts, 1), 10, 64)
		o, err := b.d.St.OrderByID(id)
		if err != nil {
			return true
		}
		by := "telegram " + strconv.FormatInt(chat, 10)
		if parts[0] == "oa" {
			err = b.d.Bill.Pay(o, "", by)
		} else {
			err = b.d.Bill.Reject(o, b.t("reject_reason"), by)
		}
		b.call("editMessageReplyMarkup", map[string]any{"chat_id": chat, "message_id": msg, "reply_markup": map[string]any{"inline_keyboard": [][]button{}}})
		if err != nil {
			b.send(chat, "⚠️ "+o.Code+": "+esc(err.Error()), nil)
			return true
		}
		b.send(chat, fmt.Sprintf(b.t(map[bool]string{true: "adm_approved", false: "adm_rejected"}[parts[0] == "oa"]), o.Code), nil)
	default:
		return false
	}
	return true
}
