// Package bot is Dunvex's Telegram bot: a pocket panel for admins (status,
// users, new user, backup, alerts, daily report) and a usage card for users.
package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"rsc.io/qr"

	"github.com/pedramrj/dunvex/internal/billing"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// Deps is what the bot needs from the rest of Dunvex.
type Deps struct {
	St      *store.Store
	Mgr     *engine.Manager
	Sys     *sysinfo.Sampler
	SubURL  func(u *store.User) string
	NewUser func(name string) *store.User
	Version string
	Bill    *billing.Service // the shop (nil: no selling in the bot)
	ShopURL func() string    // the web shop's address
}

// Bot polls Telegram and answers. It is safe to Reload at any time.
type Bot struct {
	d      Deps
	client *http.Client

	mu        sync.Mutex
	token     string
	api       string
	admins    map[int64]bool
	lang      string
	userBot   bool
	username  string
	lastErr   string
	running   bool
	cancel    context.CancelFunc
	waiting   map[int64]string // chat → what the next text answers ("new", "find")
	lastDaily string
	sent      []time.Time // alert rate limit

	bc     broadcasting               // one message to many users (see broadcast.go)
	drafts map[int64]BroadcastRequest // admin chat → the broadcast waiting for "send"
}

// New creates the bot; call Start to run it.
func New(d Deps) *Bot {
	return &Bot{d: d, client: &http.Client{Timeout: 75 * time.Second}, waiting: map[int64]string{}, drafts: map[int64]BroadcastRequest{}}
}

// Start reads the settings and starts polling (if a token is set). It also
// subscribes to Dunvex events for alerts.
func (b *Bot) Start() {
	prev := b.d.St.OnLog
	b.d.St.OnLog = func(kind, msg string) {
		if prev != nil {
			prev(kind, msg)
		}
		b.onEvent(kind, msg)
	}
	b.markInterrupted()
	b.Reload()
	if b.d.Bill != nil {
		b.d.Bill.OnEvent(b.onOrder)
	}
	go b.ticker()
}

// Reload applies changed settings.
func (b *Bot) Reload() {
	st := b.d.St
	token := strings.TrimSpace(st.GetSetting("tg_token", ""))
	api := strings.TrimRight(strings.TrimSpace(st.GetSetting("tg_api", "")), "/")
	if api == "" {
		api = "https://api.telegram.org"
	}
	admins := map[int64]bool{}
	for _, f := range strings.FieldsFunc(st.GetSetting("tg_admins", ""), func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		if id, err := strconv.ParseInt(strings.TrimSpace(f), 10, 64); err == nil {
			admins[id] = true
		}
	}
	b.mu.Lock()
	changed := token != b.token || api != b.api
	b.token, b.api, b.admins = token, api, admins
	b.lang = st.GetSetting("tg_lang", "fa")
	b.userBot = st.GetSetting("tg_user_bot", "true") == "true"
	if changed && b.cancel != nil {
		b.cancel()
		b.cancel, b.running, b.username = nil, false, ""
	}
	start := changed && token != ""
	var ctx context.Context
	if start {
		ctx, b.cancel = context.WithCancel(context.Background())
		b.running, b.lastErr = true, ""
	}
	b.mu.Unlock()
	if start {
		go b.poll(ctx, token)
	}
}

// Status reports the bot state for the settings page.
func (b *Bot) Status() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return map[string]any{"running": b.running && b.lastErr == "", "configured": b.token != "",
		"username": b.username, "error": b.lastErr, "admins": len(b.admins)}
}

// Test sends a hello to every admin chat.
func (b *Bot) Test() error {
	b.mu.Lock()
	token, admins := b.token, b.adminList()
	b.mu.Unlock()
	if token == "" {
		return fmt.Errorf("add the bot token first")
	}
	if len(admins) == 0 {
		return fmt.Errorf("add at least one admin chat ID")
	}
	if _, err := b.call("getMe", map[string]any{}); err != nil {
		return err
	}
	for _, id := range admins {
		if err := b.send(id, b.t("hello"), b.mainMenu()); err != nil {
			return fmt.Errorf("chat %d: %v", id, err)
		}
	}
	return nil
}

func (b *Bot) adminList() []int64 {
	out := []int64{}
	for id := range b.admins {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (b *Bot) isAdmin(chat int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.admins[chat]
}

// ---------------------------------------------------------------- Telegram API

type apiResp struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

func (b *Bot) endpoint(method string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.api + "/bot" + b.token + "/" + method
}

func (b *Bot) do(req *http.Request) (json.RawMessage, error) {
	res, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram is not reachable: %v", cleanErr(err, b.token))
	}
	defer res.Body.Close()
	var r apiResp
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&r); err != nil {
		return nil, fmt.Errorf("telegram answered %s", res.Status)
	}
	if !r.OK {
		return nil, fmt.Errorf("telegram: %s", r.Description)
	}
	return r.Result, nil
}

func cleanErr(err error, token string) string {
	s := err.Error()
	if token != "" {
		s = strings.ReplaceAll(s, token, "***")
	}
	return s
}

func (b *Bot) call(method string, body any) (json.RawMessage, error) {
	data, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, b.endpoint(method), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return b.do(req)
}

func (b *Bot) upload(method string, fields map[string]string, field, name string, data []byte) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile(field, name)
	fw.Write(data)
	mw.Close()
	req, err := http.NewRequest(http.MethodPost, b.endpoint(method), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	_, err = b.do(req)
	return err
}

type button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

type keyboard [][]button

func (b *Bot) send(chat int64, html string, kb keyboard) error {
	body := map[string]any{"chat_id": chat, "text": html, "parse_mode": "HTML", "disable_web_page_preview": true}
	if kb != nil {
		body["reply_markup"] = map[string]any{"inline_keyboard": kb}
	}
	_, err := b.call("sendMessage", body)
	return err
}

func (b *Bot) edit(chat, msg int64, html string, kb keyboard) {
	body := map[string]any{"chat_id": chat, "message_id": msg, "text": html, "parse_mode": "HTML", "disable_web_page_preview": true}
	if kb != nil {
		body["reply_markup"] = map[string]any{"inline_keyboard": kb}
	}
	if _, err := b.call("editMessageText", body); err != nil && !strings.Contains(err.Error(), "not modified") {
		b.send(chat, html, kb)
	}
}

func (b *Bot) sendQR(chat int64, text, caption string, kb keyboard) error {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return b.send(chat, caption, kb)
	}
	code.Scale = 8
	fields := map[string]string{"chat_id": strconv.FormatInt(chat, 10), "caption": caption, "parse_mode": "HTML"}
	if kb != nil {
		m, _ := json.Marshal(map[string]any{"inline_keyboard": kb})
		fields["reply_markup"] = string(m)
	}
	return b.upload("sendPhoto", fields, "photo", "qr.png", code.PNG())
}

// ---------------------------------------------------------------- polling

type update struct {
	ID      int64 `json:"update_id"`
	Message *struct {
		ID      int64  `json:"message_id"`
		Text    string `json:"text"`
		Caption string `json:"caption"`
		Photo   []struct {
			FileID   string `json:"file_id"`
			FileSize int64  `json:"file_size"`
		} `json:"photo"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		From struct {
			Name string `json:"first_name"`
		} `json:"from"`
	} `json:"message"`
	Callback *struct {
		ID      string `json:"id"`
		Data    string `json:"data"`
		Message struct {
			ID   int64 `json:"message_id"`
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"callback_query"`
}

func (b *Bot) poll(ctx context.Context, token string) {
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		if b.username == "" {
			if raw, err := b.call("getMe", map[string]any{}); err == nil {
				var me struct {
					Username string `json:"username"`
				}
				json.Unmarshal(raw, &me)
				b.mu.Lock()
				b.username = me.Username
				b.mu.Unlock()
			}
		}
		raw, err := b.call("getUpdates", map[string]any{"offset": offset, "timeout": 50,
			"allowed_updates": []string{"message", "callback_query"}})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			b.mu.Lock()
			if b.token == token {
				b.lastErr = err.Error()
			}
			b.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < time.Minute {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		b.mu.Lock()
		b.lastErr = ""
		b.mu.Unlock()
		var ups []update
		json.Unmarshal(raw, &ups)
		for _, u := range ups {
			offset = u.ID + 1
			b.handle(u)
		}
	}
}

func (b *Bot) handle(u update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("telegram bot: %v", r)
		}
	}()
	if c := u.Callback; c != nil {
		b.call("answerCallbackQuery", map[string]any{"callback_query_id": c.ID})
		b.onButton(c.Message.Chat.ID, c.Message.ID, c.Data)
		return
	}
	if m := u.Message; m != nil && m.Chat.Type == "private" {
		if len(m.Photo) > 0 { // a receipt: the largest size is the last
			if !b.onReceipt(m.Chat.ID, m.Photo[len(m.Photo)-1].FileID, strings.TrimSpace(m.Caption)) {
				b.onText(m.Chat.ID, "")
			}
			return
		}
		b.onText(m.Chat.ID, strings.TrimSpace(m.Text))
	}
}

// ---------------------------------------------------------------- texts

func (b *Bot) onText(chat int64, text string) {
	admin := b.isAdmin(chat)
	if strings.HasPrefix(text, "/start") {
		arg := strings.TrimSpace(strings.TrimPrefix(text, "/start"))
		if arg != "" && b.bindUser(chat, arg) {
			return
		}
		if admin {
			b.send(chat, b.t("hello"), b.mainMenu())
			return
		}
		if u, err := b.d.St.UserByTG(chat); err == nil && b.userBot {
			b.sendUserCard(chat, u)
			return
		}
		b.greet(chat)
		return
	}
	if !admin {
		if text != "" && b.onReceipt(chat, "", text) {
			return
		}
		if u, err := b.d.St.UserByTG(chat); err == nil && b.userBot {
			b.sendUserCard(chat, u)
		} else {
			b.greet(chat)
		}
		return
	}
	b.mu.Lock()
	want := b.waiting[chat]
	delete(b.waiting, chat)
	b.mu.Unlock()
	switch {
	case text == "/status":
		b.send(chat, b.statusText(), b.backMenu())
	case text == "/users":
		b.send(chat, b.t("users"), b.usersMenu(0, ""))
	case text == "/backup":
		b.sendBackup(chat)
	case strings.HasPrefix(text, "/new ") || want == "new":
		b.createUser(chat, strings.TrimSpace(strings.TrimPrefix(text, "/new")))
	case strings.HasPrefix(want, "bc:"):
		b.broadcastDraft(chat, strings.TrimPrefix(want, "bc:"), text)
	case text == "/broadcast":
		b.send(chat, b.t("bc_pick"), b.audienceMenu())
	case strings.HasPrefix(text, "/find ") || want == "find":
		q := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(text, "/find")))
		b.send(chat, b.t("results"), b.usersMenu(0, q))
	default:
		b.send(chat, b.t("hello"), b.mainMenu())
	}
}

func (b *Bot) bindUser(chat int64, token string) bool {
	if !b.userBot {
		return false
	}
	u, err := b.d.St.UserByToken(token)
	if err != nil {
		return false
	}
	u.TGChat = chat
	b.d.St.UpdateUser(u)
	b.d.St.Log("user", "%s linked Telegram", u.Name)
	b.sendUserCard(chat, u)
	return true
}

func (b *Bot) sendUserCard(chat int64, u *store.User) {
	kb := keyboard{{{Text: b.t("refresh"), Data: "me"}}}
	if b.shopOn() {
		kb = append(kb, []button{{Text: b.t("b_renew"), Data: "sh:r"}})
	}
	b.sendQR(chat, b.d.SubURL(u), b.userText(u, false)+"\n\n<code>"+esc(b.d.SubURL(u))+"</code>", kb)
}

// greet answers a chat Dunvex does not know: the shop when it sells here,
// otherwise how to link a subscription.
func (b *Bot) greet(chat int64) {
	if b.shopOn() {
		b.send(chat, b.t("shop_hello"), b.buyerMenu(chat))
		return
	}
	b.send(chat, fmt.Sprintf(b.t("unknown"), chat), nil)
}

// ---------------------------------------------------------------- buttons

func (b *Bot) mainMenu() keyboard {
	return keyboard{
		{{Text: b.t("b_status"), Data: "st"}, {Text: b.t("b_users"), Data: "ul:0"}},
		{{Text: b.t("b_new"), Data: "new"}, {Text: b.t("b_find"), Data: "find"}},
		{{Text: b.t("b_online"), Data: "on"}, {Text: b.t("b_backup"), Data: "bk"}},
		{{Text: b.t("b_orders"), Data: "ol"}, {Text: b.t("b_broadcast"), Data: "bc"}},
	}
}

func (b *Bot) backMenu() keyboard { return keyboard{{{Text: b.t("b_back"), Data: "m"}}} }

func (b *Bot) onButton(chat, msg int64, data string) {
	if data == "me" {
		if u, err := b.d.St.UserByTG(chat); err == nil {
			b.send(chat, b.userText(u, false), keyboard{{{Text: b.t("refresh"), Data: "me"}}})
		}
		return
	}
	if b.onShopButton(chat, msg, data) {
		return
	}
	if !b.isAdmin(chat) {
		return
	}
	if b.onOrderAdmin(chat, msg, data) {
		return
	}
	parts := strings.Split(data, ":")
	switch parts[0] {
	case "m":
		b.edit(chat, msg, b.t("hello"), b.mainMenu())
	case "st":
		b.edit(chat, msg, b.statusText(), keyboard{{{Text: b.t("refresh"), Data: "st"}, {Text: b.t("b_back"), Data: "m"}}})
	case "on":
		b.edit(chat, msg, b.onlineText(), keyboard{{{Text: b.t("refresh"), Data: "on"}, {Text: b.t("b_back"), Data: "m"}}})
	case "ul":
		page, _ := strconv.Atoi(arg(parts, 1))
		b.edit(chat, msg, b.t("users"), b.usersMenu(page, ""))
	case "new":
		b.mu.Lock()
		b.waiting[chat] = "new"
		b.mu.Unlock()
		b.send(chat, b.t("ask_new"), nil)
	case "find":
		b.mu.Lock()
		b.waiting[chat] = "find"
		b.mu.Unlock()
		b.send(chat, b.t("ask_find"), nil)
	case "bk":
		b.sendBackup(chat)
	case "bc", "bca", "bcs", "bcx", "bcq":
		b.broadcastButton(chat, msg, parts)
	case "u":
		id, _ := strconv.ParseInt(arg(parts, 1), 10, 64)
		if u, err := b.d.St.UserByID(id); err == nil {
			b.edit(chat, msg, b.userText(u, true), b.userMenu(u))
		}
	case "ua":
		id, _ := strconv.ParseInt(arg(parts, 1), 10, 64)
		b.userAction(chat, msg, id, arg(parts, 2))
	}
}

func arg(p []string, i int) string {
	if i < len(p) {
		return p[i]
	}
	return ""
}

var statusIcon = map[string]string{store.StatusActive: "🟢", store.StatusPending: "🕓", store.StatusLimited: "🔴",
	store.StatusExpired: "⌛", store.StatusDisabled: "⚫", store.StatusPaused: "⏸"}

func (b *Bot) usersMenu(page int, q string) keyboard {
	users, _ := b.d.St.Users()
	now := time.Now().Unix()
	var list []*store.User
	for i := len(users) - 1; i >= 0; i-- {
		if q == "" || strings.Contains(strings.ToLower(users[i].Name+" "+users[i].Note), q) {
			list = append(list, users[i])
		}
	}
	const per = 10
	if page < 0 || page*per >= len(list) {
		page = 0
	}
	kb := keyboard{}
	row := []button{}
	for i := page * per; i < len(list) && i < (page+1)*per; i++ {
		u := list[i]
		row = append(row, button{Text: statusIcon[u.Status(now)] + " " + u.Name, Data: fmt.Sprintf("u:%d", u.ID)})
		if len(row) == 2 {
			kb = append(kb, row)
			row = []button{}
		}
	}
	if len(row) > 0 {
		kb = append(kb, row)
	}
	nav := []button{}
	if page > 0 {
		nav = append(nav, button{Text: "‹", Data: fmt.Sprintf("ul:%d", page-1)})
	}
	if (page+1)*per < len(list) {
		nav = append(nav, button{Text: "›", Data: fmt.Sprintf("ul:%d", page+1)})
	}
	if len(nav) > 0 {
		kb = append(kb, nav)
	}
	if len(list) == 0 {
		kb = append(kb, []button{{Text: b.t("none"), Data: "m"}})
	}
	return append(kb, []button{{Text: b.t("b_back"), Data: "m"}})
}

func (b *Bot) userMenu(u *store.User) keyboard {
	id := u.ID
	tog := b.t("b_disable")
	if !u.Enabled {
		tog = b.t("b_enable")
	}
	return keyboard{
		{{Text: b.t("b_link"), Data: fmt.Sprintf("ua:%d:link", id)}, {Text: b.t("b_reset"), Data: fmt.Sprintf("ua:%d:reset", id)}},
		{{Text: "+30 " + b.t("days"), Data: fmt.Sprintf("ua:%d:d30", id)}, {Text: "+10 GB", Data: fmt.Sprintf("ua:%d:g10", id)}},
		{{Text: tog, Data: fmt.Sprintf("ua:%d:tog", id)}, {Text: b.t("b_delete"), Data: fmt.Sprintf("ua:%d:del", id)}},
		{{Text: b.t("b_back"), Data: "ul:0"}},
	}
}

func (b *Bot) userAction(chat, msg, id int64, action string) {
	u, err := b.d.St.UserByID(id)
	if err != nil {
		return
	}
	now := time.Now().Unix()
	switch action {
	case "link":
		b.sendQR(chat, b.d.SubURL(u), "<b>"+esc(u.Name)+"</b>\n<code>"+esc(b.d.SubURL(u))+"</code>", nil)
		return
	case "reset":
		u.UsedUp, u.UsedDown, u.LastResetAt, u.PausedUntil, u.Notified, u.ResetUsage = 0, 0, now, 0, "", true
	case "d30":
		if u.ExpireAt == 0 && u.DurationDays > 0 && u.FirstUseAt == 0 {
			u.DurationDays += 30
		} else {
			base := now
			if u.ExpireAt > base {
				base = u.ExpireAt
			}
			u.ExpireAt = base + 30*86400
		}
		u.Notified = ""
	case "g10":
		if u.DataLimit > 0 {
			u.DataLimit += 10 << 30
			u.Notified = ""
		}
	case "tog":
		u.Enabled = !u.Enabled
	case "del":
		b.edit(chat, msg, fmt.Sprintf(b.t("confirm_del"), esc(u.Name)), keyboard{
			{{Text: b.t("b_yes_del"), Data: fmt.Sprintf("ua:%d:delok", id)}, {Text: b.t("b_back"), Data: fmt.Sprintf("u:%d", id)}}})
		return
	case "delok":
		b.d.St.DeleteUser(u.ID)
		b.d.St.Log("user", "user %s deleted from Telegram", u.Name)
		b.d.Mgr.RequestApply()
		b.edit(chat, msg, fmt.Sprintf(b.t("deleted"), esc(u.Name)), b.usersMenu(0, ""))
		return
	default:
		return
	}
	b.d.St.UpdateUser(u)
	b.d.St.Log("user", "%s changed from Telegram (%s)", u.Name, action)
	b.d.Mgr.RequestApply()
	b.edit(chat, msg, b.userText(u, true), b.userMenu(u))
}

// createUser parses "name [GB] [days] [devices]".
func (b *Bot) createUser(chat int64, text string) {
	f := strings.Fields(text)
	if len(f) == 0 {
		b.send(chat, b.t("ask_new"), nil)
		return
	}
	name := util.SanitizeName(f[0])
	if name == "" {
		b.send(chat, b.t("bad_name"), nil)
		return
	}
	num := func(i int) float64 {
		if i >= len(f) {
			return 0
		}
		v, _ := strconv.ParseFloat(f[i], 64)
		return v
	}
	u := b.d.NewUser(name)
	if gb := num(1); gb > 0 {
		u.DataLimit = int64(gb * (1 << 30))
	}
	if days := int(num(2)); days > 0 {
		u.DurationDays = days
		u.ExpireAt = time.Now().Unix() + int64(days)*86400
	}
	u.IPLimit = int(num(3))
	if err := b.d.St.CreateUser(u); err != nil {
		b.send(chat, "⚠️ "+esc(err.Error()), b.backMenu())
		return
	}
	b.d.St.Log("user", "user %s created from Telegram", u.Name)
	b.d.Mgr.RequestApply()
	b.sendQR(chat, b.d.SubURL(u), b.userText(u, true)+"\n\n<code>"+esc(b.d.SubURL(u))+"</code>", b.userMenu(u))
}

func (b *Bot) sendBackup(chat int64) {
	path := filepath.Join(os.TempDir(), fmt.Sprintf("dunvex-%s.db", time.Now().Format("20060102-1504")))
	defer os.Remove(path)
	if err := b.d.St.Backup(path); err != nil {
		b.send(chat, "⚠️ "+esc(err.Error()), nil)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if err := b.upload("sendDocument", map[string]string{"chat_id": strconv.FormatInt(chat, 10),
		"caption": "💾 Dunvex backup · " + time.Now().Format("2006-01-02 15:04")}, "document", filepath.Base(path), data); err != nil {
		b.send(chat, "⚠️ "+esc(err.Error()), nil)
	}
}

// ---------------------------------------------------------------- cards

func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func (b *Bot) userText(u *store.User, admin bool) string {
	now := time.Now().Unix()
	st := u.Status(now)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s <b>%s</b> · %s\n", statusIcon[st], esc(u.Name), b.t("st_"+st))
	limit := "∞"
	if u.DataLimit > 0 {
		limit = util.HumanBytes(u.DataLimit)
	}
	fmt.Fprintf(&sb, "📶 %s: <b>%s</b> / %s", b.t("used"), util.HumanBytes(u.Used()), limit)
	if u.DataLimit > 0 {
		fmt.Fprintf(&sb, " (%d%%)", u.Used()*100/u.DataLimit)
	}
	sb.WriteString("\n")
	switch {
	case u.ExpireAt > 0:
		left := (u.ExpireAt - now + 86399) / 86400
		if left < 0 {
			left = 0
		}
		fmt.Fprintf(&sb, "⏳ %s: <b>%d</b> %s (%s)\n", b.t("left"), left, b.t("days"), time.Unix(u.ExpireAt, 0).Format("2006-01-02"))
	case u.DurationDays > 0:
		fmt.Fprintf(&sb, "⏳ %d %s · %s\n", u.DurationDays, b.t("days"), b.t("from_first"))
	default:
		fmt.Fprintf(&sb, "⏳ %s\n", b.t("no_expiry"))
	}
	if u.PausedUntil > now {
		fmt.Fprintf(&sb, "⏸ %s %s\n", b.t("paused_until"), time.Unix(u.PausedUntil, 0).Format("15:04"))
	}
	if admin {
		devs := b.d.Mgr.Devices(u.Name)
		lim := "∞"
		if u.IPLimit > 0 {
			lim = strconv.Itoa(u.IPLimit)
		}
		fmt.Fprintf(&sb, "📱 %s: %d / %s\n", b.t("devices"), len(devs), lim)
		if u.LastOnlineAt > 0 {
			fmt.Fprintf(&sb, "🕒 %s: %s\n", b.t("last_seen"), ago(now-u.LastOnlineAt, b.language()))
		}
		if u.Note != "" {
			fmt.Fprintf(&sb, "📝 %s\n", esc(u.Note))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func ago(sec int64, lang string) string {
	fa := lang == "fa"
	switch {
	case sec < 90:
		if fa {
			return "همین الان"
		}
		return "just now"
	case sec < 3600:
		if fa {
			return fmt.Sprintf("%d دقیقه پیش", sec/60)
		}
		return fmt.Sprintf("%d min ago", sec/60)
	case sec < 86400:
		if fa {
			return fmt.Sprintf("%d ساعت پیش", sec/3600)
		}
		return fmt.Sprintf("%d h ago", sec/3600)
	}
	if fa {
		return fmt.Sprintf("%d روز پیش", sec/86400)
	}
	return fmt.Sprintf("%d days ago", sec/86400)
}

func (b *Bot) statusText() string {
	now := time.Now()
	sys := b.d.Sys.Get()
	users, _ := b.d.St.Users()
	counts := map[string]int{}
	for _, u := range users {
		counts[u.Status(now.Unix())]++
	}
	online := len(b.d.Mgr.OnlineUsers(60))
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix()
	tu, td := b.d.St.TotalSince(day)
	mu, md := b.d.St.TotalSince(month)
	pct := func(used, total uint64) uint64 {
		if total == 0 {
			return 0
		}
		return used * 100 / total
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "🖥 <b>%s</b> · Dunvex %s\n\n", esc(sys.Hostname), esc(b.d.Version))
	fmt.Fprintf(&sb, "⚙️ CPU %.0f%% · RAM %d%% · %s %d%%\n", sys.CPU, pct(sys.MemUsed, sys.MemTotal), b.t("disk"), pct(sys.DiskUsed, sys.DiskTotal))
	fmt.Fprintf(&sb, "🌐 ↑ %s/s · ↓ %s/s\n", util.HumanBytes(int64(sys.NetUp)), util.HumanBytes(int64(sys.NetDown)))
	fmt.Fprintf(&sb, "⏱ %s: %s\n\n", b.t("uptime"), dur(int64(sys.Uptime), b.language()))
	fmt.Fprintf(&sb, "👥 %s: <b>%d</b> · 🟢 %d · %s <b>%d</b>\n", b.t("users"), len(users), counts[store.StatusActive]+counts[store.StatusPending], b.t("online"), online)
	if n := counts[store.StatusLimited] + counts[store.StatusExpired]; n > 0 {
		fmt.Fprintf(&sb, "🔴 %s: %d\n", b.t("ended"), n)
	}
	fmt.Fprintf(&sb, "📅 %s: %s · %s: %s\n", b.t("today"), util.HumanBytes(tu+td), b.t("month"), util.HumanBytes(mu+md))
	for _, c := range []*engine.Core{b.d.Mgr.Xray, b.d.Mgr.SingBox} {
		s := c.Status()
		icon := "🟢"
		if !s.Running {
			icon = "⚪"
			if s.Error != "" {
				icon = "🔴"
			}
		}
		fmt.Fprintf(&sb, "%s %s %s\n", icon, s.Name, esc(s.Version))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (b *Bot) onlineText() string {
	users, _ := b.d.St.Users()
	online := b.d.Mgr.OnlineUsers(60)
	var sb strings.Builder
	fmt.Fprintf(&sb, "🟢 <b>%s</b>: %d\n\n", b.t("online"), len(online))
	n := 0
	for _, u := range users {
		if !online[u.ID] {
			continue
		}
		r := b.d.Mgr.UserRate(u.ID)
		fmt.Fprintf(&sb, "• %s · ↓ %s/s · 📱 %d\n", esc(u.Name), util.HumanBytes(r.Down), len(b.d.Mgr.Devices(u.Name)))
		if n++; n >= 40 {
			break
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func dur(sec int64, lang string) string {
	d, h := sec/86400, (sec%86400)/3600
	if lang == "fa" {
		return fmt.Sprintf("%d روز و %d ساعت", d, h)
	}
	return fmt.Sprintf("%dd %dh", d, h)
}

// ---------------------------------------------------------------- alerts and reports

// onEvent forwards important events to the admins.
func (b *Bot) onEvent(kind, msg string) {
	important := kind == "limit" || kind == "core" || kind == "auth" || kind == "health" ||
		(kind == "user" && (strings.Contains(msg, " is now ") || strings.Contains(msg, "first time")))
	if !important {
		return
	}
	b.mu.Lock()
	if b.token == "" || len(b.admins) == 0 {
		b.mu.Unlock()
		return
	}
	now := time.Now()
	keep := b.sent[:0]
	for _, t := range b.sent {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	b.sent = keep
	if len(b.sent) >= 20 {
		b.mu.Unlock()
		return
	}
	b.sent = append(b.sent, now)
	admins := b.adminList()
	b.mu.Unlock()
	icon := map[string]string{"limit": "📱", "core": "⚙️", "auth": "🔐", "user": "👤"}[kind]
	go func() {
		for _, id := range admins {
			b.send(id, icon+" "+esc(msg), nil)
		}
	}()
}

func (b *Bot) ticker() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	n := 0
	for range t.C {
		b.mu.Lock()
		on := b.token != ""
		b.mu.Unlock()
		if !on {
			continue
		}
		if n++; n%5 == 0 {
			b.warnUsers()
		}
		b.daily()
	}
}

// daily sends the morning report (and the backup) once a day.
func (b *Bot) daily() {
	now := time.Now()
	hour, _ := strconv.Atoi(b.d.St.GetSetting("tg_daily_hour", "9"))
	today := now.Format("2006-01-02")
	b.mu.Lock()
	due := now.Hour() == hour && b.lastDaily != today && len(b.admins) > 0
	if due {
		b.lastDaily = today
	}
	admins := b.adminList()
	b.mu.Unlock()
	if !due {
		return
	}
	y := now.AddDate(0, 0, -1)
	ys := time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, now.Location()).Unix()
	ts := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	up, down := b.d.St.TotalSince(ys)
	tu, td := b.d.St.TotalSince(ts)
	yesterday := up + down - tu - td
	top, _ := b.d.St.TopUsers(ys, 3)
	var sb strings.Builder
	fmt.Fprintf(&sb, "☀️ <b>%s</b> · %s\n\n%s\n\n", b.t("daily"), y.Format("2006-01-02"), b.statusText())
	fmt.Fprintf(&sb, "📈 %s: <b>%s</b>\n", b.t("yesterday"), util.HumanBytes(yesterday))
	for i, t := range top {
		fmt.Fprintf(&sb, "%d. %s · %s\n", i+1, esc(t.Name), util.HumanBytes(t.Bytes))
	}
	for _, id := range admins {
		b.send(id, strings.TrimRight(sb.String(), "\n"), b.mainMenu())
		if b.d.St.GetSetting("tg_backup", "true") == "true" {
			b.sendBackup(id)
		}
	}
}

// warnUsers tells linked users about a running-out plan, once per threshold.
func (b *Bot) warnUsers() {
	b.mu.Lock()
	userBot := b.userBot
	b.mu.Unlock()
	if !userBot {
		return
	}
	users, err := b.d.St.Users()
	if err != nil {
		return
	}
	now := time.Now().Unix()
	for _, u := range users {
		if u.TGChat == 0 {
			continue
		}
		flags := map[string]bool{}
		for _, f := range strings.Split(u.Notified, ",") {
			if f != "" {
				flags[f] = true
			}
		}
		var key, text string
		st := u.Status(now)
		switch {
		case st == store.StatusLimited:
			key, text = "lim", b.t("w_limited")
		case st == store.StatusExpired:
			key, text = "exp", b.t("w_expired")
		case u.DataLimit > 0 && u.Used()*100 >= u.DataLimit*95:
			key, text = "d95", fmt.Sprintf(b.t("w_data"), 95)
		case u.DataLimit > 0 && u.Used()*100 >= u.DataLimit*80:
			key, text = "d80", fmt.Sprintf(b.t("w_data"), 80)
		case u.ExpireAt > 0 && u.ExpireAt-now <= 86400:
			key, text = "e1", fmt.Sprintf(b.t("w_days"), 1)
		case u.ExpireAt > 0 && u.ExpireAt-now <= 3*86400:
			key, text = "e3", fmt.Sprintf(b.t("w_days"), 3)
		}
		if key == "" || flags[key] {
			continue
		}
		if b.send(u.TGChat, "🔔 "+text+"\n\n"+b.userText(u, false), keyboard{{{Text: b.t("refresh"), Data: "me"}}}) == nil {
			flags[key] = true
			var list []string
			for f := range flags {
				list = append(list, f)
			}
			sort.Strings(list)
			u.Notified = strings.Join(list, ",")
			b.d.St.UpdateUser(u)
		}
	}
}
