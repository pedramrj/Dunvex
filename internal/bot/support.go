package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pedramrj/dunvex/internal/billing"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// Support inbox: a person whose Telegram is linked to their subscription
// taps "Support" (or sends /support) and writes, text or a photo. Every
// message lands in the panel's Support page; the answer goes back through
// the bot, signed "Support" (the person never sees which admin wrote it).
// The person's messages get receipts as reactions: 🕊 arrived, 👀 seen,
// 👌 answered. The bot's admins hear about each message and can answer from
// Telegram too.

// Support settings.
const (
	keySupportOn     = "support_on"
	keySupportGuests = "support_guests"
	keySupportAlert  = "support_alert"
	keySupportRate   = "support_rate"
	keySupportMaxLen = "support_max_len"
	keySupportKeep   = "support_keep"
	keySupportDays   = "support_days"
	keySupportNote   = "support_note"
)

// SupportSettings is what the owner sets for the support inbox.
type SupportSettings struct {
	On     bool   `json:"on"`
	Guests bool   `json:"guests"` // people without a linked subscription may write too (only the owner and admins see them)
	Alert  bool   `json:"alert"`  // each new message goes to the bot's admins, with Answer
	Rate   int    `json:"rate"`   // messages a person may send in an hour (0 = no limit)
	MaxLen int    `json:"maxLen"` // characters in one message
	Keep   int    `json:"keep"`   // messages kept in each conversation (0 = all)
	Days   int    `json:"days"`   // messages older than this many days are removed (0 = never)
	Note   string `json:"note"`   // a line on the person's support screen (hours, what to send)
}

// Limits of the support inbox.
const (
	MaxSupportAnswer = 3500 // characters in an answer (Telegram takes 4096 with the header)
	maxSupportNote   = 300
	supportPhotoMax  = 5 << 20
	supportWriteFor  = 30 * time.Minute // what is sent this long after "Support" goes to support
	supportScreenN   = 4                // messages on the person's support screen
)

// LoadSupport reads the support settings, with their defaults.
func LoadSupport(st *store.Store) SupportSettings {
	n := func(k string, def int) int {
		v, err := strconv.Atoi(st.GetSetting(k, strconv.Itoa(def)))
		if err != nil {
			return def
		}
		return v
	}
	return SupportSettings{On: st.GetSetting(keySupportOn, "true") == "true", Guests: st.GetSetting(keySupportGuests, "false") == "true",
		Alert: st.GetSetting(keySupportAlert, "true") == "true", Rate: n(keySupportRate, 20), MaxLen: n(keySupportMaxLen, 2000),
		Keep: n(keySupportKeep, 300), Days: n(keySupportDays, 180), Note: st.GetSetting(keySupportNote, "")}
}

// Check says what is wrong with the settings, if anything.
func (s *SupportSettings) Check() error {
	s.Note = strings.TrimSpace(s.Note)
	switch {
	case s.Rate < 0 || s.Rate > 1000:
		return errors.New("messages an hour must be between 0 (no limit) and 1000")
	case s.MaxLen < 50 || s.MaxLen > 4000:
		return errors.New("a message may be 50 to 4000 characters long")
	case s.Keep != 0 && (s.Keep < 10 || s.Keep > 100000):
		return errors.New("keep 10 to 100000 messages in each conversation, or 0 for all")
	case s.Days < 0 || s.Days > 3650:
		return errors.New("remove messages after 1 to 3650 days, or 0 for never")
	case utf8.RuneCountInString(s.Note) > maxSupportNote:
		return fmt.Errorf("the note is longer than %d characters", maxSupportNote)
	}
	return nil
}

// SaveSupport stores the settings.
func SaveSupport(st *store.Store, s SupportSettings) {
	st.SetSetting(keySupportOn, strconv.FormatBool(s.On))
	st.SetSetting(keySupportGuests, strconv.FormatBool(s.Guests))
	st.SetSetting(keySupportAlert, strconv.FormatBool(s.Alert))
	st.SetSetting(keySupportRate, strconv.Itoa(s.Rate))
	st.SetSetting(keySupportMaxLen, strconv.Itoa(s.MaxLen))
	st.SetSetting(keySupportKeep, strconv.Itoa(s.Keep))
	st.SetSetting(keySupportDays, strconv.Itoa(s.Days))
	st.SetSetting(keySupportNote, s.Note)
}

// supporting is the bot's support state, under b.mu.
type supporting struct {
	writing map[int64]time.Time // chat → until when its messages go to support
	noted   map[int64]time.Time // chat → the last "it arrived" note (one per burst)
	warned  map[int64]time.Time // chat → the last "too many messages" note
	typing  map[int64]time.Time // chat → the last "typing…" sent to it
}

// once reports whether the last time for chat in m is older than d, and makes it now.
func (b *Bot) once(m *map[int64]time.Time, chat int64, d time.Duration) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if *m == nil {
		*m = map[int64]time.Time{}
	}
	now := time.Now()
	if last, ok := (*m)[chat]; ok && now.Sub(last) < d {
		return false
	}
	(*m)[chat] = now
	return true
}

func (b *Bot) setWriting(chat int64, on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sp.writing == nil {
		b.sp.writing = map[int64]time.Time{}
	}
	if on {
		b.sp.writing[chat] = time.Now().Add(supportWriteFor)
	} else {
		delete(b.sp.writing, chat)
	}
}

// supportWriting says whether the chat's next message goes to support.
func (b *Bot) supportWriting(chat int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	until, ok := b.sp.writing[chat]
	if ok && time.Now().After(until) {
		delete(b.sp.writing, chat)
		return false
	}
	return ok
}

// supportAllowed: may this chat write to support? It gives the linked user, if any.
func (b *Bot) supportAllowed(chat int64) (*store.User, bool) {
	set := LoadSupport(b.d.St)
	if !set.On {
		return nil, false
	}
	if u, err := b.d.St.UserByTG(chat); err == nil {
		return u, true
	}
	return nil, set.Guests
}

// withSupport adds the Support button when the chat may write to support.
func (b *Bot) withSupport(chat int64, kb keyboard) keyboard {
	if _, ok := b.supportAllowed(chat); ok {
		return append(kb, []button{{Text: b.t("b_support"), Data: "hp"}})
	}
	return kb
}

// supportWho names the person: their user, else their Telegram name.
func supportWho(th *store.SupportThread, u *store.User) string {
	switch {
	case u != nil:
		return u.Name
	case th.Username != "":
		return "@" + th.Username
	case th.Name != "":
		return th.Name
	}
	return strconv.FormatInt(th.Chat, 10)
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func stamp(at int64) string {
	t, now := time.Unix(at, 0), time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("01/02 15:04")
}

// ---------------------------------------------------------------- the person

// openSupport shows the person their conversation and takes what they write next.
func (b *Bot) openSupport(chat int64) {
	if _, ok := b.supportAllowed(chat); !ok {
		if !LoadSupport(b.d.St).On {
			b.send(chat, b.t("sp_off"), nil)
		} else {
			b.send(chat, b.t("sp_linked_only"), nil)
		}
		return
	}
	b.setWriting(chat, true)
	b.send(chat, b.supportScreen(chat), keyboard{{{Text: b.t("b_sp_cancel"), Data: "hpx"}}})
}

// supportScreen is the person's view: their last messages and where things stand.
func (b *Bot) supportScreen(chat int64) string {
	var sb strings.Builder
	sb.WriteString(b.t("sp_title") + "\n<i>" + b.t("sp_private") + "</i>")
	if note := LoadSupport(b.d.St).Note; note != "" {
		sb.WriteString("\n\nℹ️ " + esc(note))
	}
	if th, err := b.d.St.SupportThreadByChat(chat); err == nil {
		msgs, _, _ := b.d.St.SupportMessages(th.ID, 0, supportScreenN)
		if len(msgs) > 0 {
			sb.WriteString("\n")
			for _, m := range msgs {
				who := "🙋 " + b.t("sp_you")
				if m.Staff {
					who = "🛟 " + b.t("sp_staff")
				}
				body := clip(m.Text, 300)
				if m.Photo != "" {
					body = strings.TrimSpace("📷 " + body)
				}
				fmt.Fprintf(&sb, "\n<b>%s</b> · %s\n%s\n", who, stamp(m.At), esc(body))
			}
			last := msgs[len(msgs)-1]
			switch {
			case last.Staff:
				sb.WriteString("\n" + b.t("sp_answered"))
			case last.Seen:
				sb.WriteString("\n" + b.t("sp_seen"))
			default:
				sb.WriteString("\n" + b.t("sp_waiting"))
			}
		}
	}
	sb.WriteString("\n\n" + b.t("sp_write"))
	return sb.String()
}

type sender struct {
	msg      int64 // the message id in the chat
	name     string
	username string
}

// onSupportMessage takes a message meant for support: the person's while
// they write to support, or an admin's answer after "Answer". It reports
// whether it did.
func (b *Bot) onSupportMessage(chat int64, from sender, text, file string) bool {
	if strings.HasPrefix(text, "/") {
		return false // commands stay commands
	}
	b.mu.Lock()
	want := b.waiting[chat]
	b.mu.Unlock()
	if b.isAdmin(chat) {
		if !strings.HasPrefix(want, "hpr:") {
			return false
		}
		b.forget(chat)
		id, _ := strconv.ParseInt(strings.TrimPrefix(want, "hpr:"), 10, 64)
		b.adminAnswer(chat, id, text, file)
		return true
	}
	if strings.HasPrefix(want, "rc:") || !b.supportWriting(chat) {
		return false // a receipt for an order comes first
	}
	b.supportIncoming(chat, from, text, file)
	return true
}

func (b *Bot) cancelKB() keyboard { return keyboard{{{Text: b.t("b_sp_cancel"), Data: "hpx"}}} }

// supportIncoming keeps what the person wrote, after the limits.
func (b *Bot) supportIncoming(chat int64, from sender, text, file string) {
	set := LoadSupport(b.d.St)
	u, ok := b.supportAllowed(chat)
	if !ok {
		b.setWriting(chat, false)
		b.openSupport(chat) // says why
		return
	}
	text = strings.TrimSpace(text)
	if text == "" && file == "" {
		b.send(chat, b.t("sp_only_text"), b.cancelKB())
		return
	}
	if n := utf8.RuneCountInString(text); n > set.MaxLen {
		b.send(chat, fmt.Sprintf(b.t("sp_too_long"), n, set.MaxLen), b.cancelKB())
		return
	}
	now := time.Now()
	if set.Rate > 0 && b.d.St.SupportSentSince(chat, now.Unix()-3600) >= set.Rate {
		if b.once(&b.sp.warned, chat, 10*time.Minute) {
			b.send(chat, b.t("sp_slow"), nil)
		}
		return
	}
	photo := ""
	if file != "" {
		data, err := b.download(file)
		switch {
		case errors.Is(err, billing.ErrReceiptTooBig) || len(data) > supportPhotoMax:
			b.send(chat, b.t("sp_photo_big"), b.cancelKB())
			return
		case err != nil:
			b.send(chat, b.t("sp_error"), b.cancelKB())
			return
		}
		if photo, err = b.saveSupportPhoto(data); err != nil {
			b.send(chat, b.t("sp_photo_bad"), b.cancelKB())
			return
		}
	}
	in := store.SupportIn{Chat: chat, Name: from.name, Username: from.username, Text: text, Photo: photo, TGMsg: from.msg, At: now.Unix()}
	if u != nil {
		in.UserID, in.OwnerID = u.ID, u.OwnerID
	}
	th, m, err := b.d.St.SupportIncoming(in)
	if err != nil {
		b.removeSupportPhotos(photo)
		b.send(chat, b.t("sp_error"), b.cancelKB())
		return
	}
	b.setWriting(chat, true) // more may follow
	go b.react(chat, from.msg, "🕊")
	if b.once(&b.sp.noted, chat, 3*time.Minute) {
		b.send(chat, b.t("sp_delivered"), keyboard{{{Text: b.t("b_sp_done"), Data: "hpx"}}})
	}
	preview := clip(strings.Join(strings.Fields(text), " "), 120)
	if photo != "" {
		preview = strings.TrimSpace("[photo] " + preview)
	}
	b.d.St.Log("support", "%s wrote to support: %s", supportWho(th, u), preview)
	if set.Alert {
		go b.alertSupport(th, m, u)
	}
}

// react sets a receipt on one of the person's messages (best effort).
func (b *Bot) react(chat, msg int64, emoji string) {
	if msg == 0 {
		return
	}
	b.call("setMessageReaction", map[string]any{"chat_id": chat, "message_id": msg,
		"reaction": []map[string]string{{"type": "emoji", "emoji": emoji}}})
}

// SupportReact sets a receipt on several of the person's messages.
func (b *Bot) SupportReact(chat int64, msgs []int64, emoji string) {
	for _, m := range msgs {
		b.react(chat, m, emoji)
	}
}

// SupportTyping shows "typing…" in the person's chat while an admin writes.
func (b *Bot) SupportTyping(thread int64) {
	th, err := b.d.St.SupportThreadByID(thread)
	if err != nil || !b.once(&b.sp.typing, th.Chat, 4*time.Second) {
		return
	}
	b.call("sendChatAction", map[string]any{"chat_id": th.Chat, "action": "typing"})
}

// ---------------------------------------------------------------- photos

var photoExt = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif"}

// SupportPhotoType is the content type of a photo support takes ("" = not one).
func SupportPhotoType(data []byte) string {
	ct := http.DetectContentType(data)
	if _, ok := photoExt[ct]; ok {
		return ct
	}
	return ""
}

func (b *Bot) saveSupportPhoto(data []byte) (string, error) {
	ct := SupportPhotoType(data)
	if ct == "" {
		return "", errors.New("only JPEG, PNG, WebP and GIF pictures")
	}
	if len(data) > supportPhotoMax {
		return "", errors.New("the picture is larger than 5 MB")
	}
	if b.d.SupportDir == "" {
		return "", errors.New("no folder for support photos")
	}
	if err := os.MkdirAll(b.d.SupportDir, 0o700); err != nil {
		return "", err
	}
	name := util.RandomHex(16) + photoExt[ct]
	if err := os.WriteFile(filepath.Join(b.d.SupportDir, name), data, 0o600); err != nil {
		return "", err
	}
	return name, nil
}

func (b *Bot) removeSupportPhotos(names ...string) {
	for _, n := range names {
		if n != "" && b.d.SupportDir != "" {
			os.Remove(filepath.Join(b.d.SupportDir, filepath.Base(n)))
		}
	}
}

// ---------------------------------------------------------------- answers

// SupportReply sends an answer to the person and keeps it (by is the admin;
// the person sees only "Support"). photo is a picture to send with it.
func (b *Bot) SupportReply(thread int64, by, text string, photo []byte) (*store.SupportMessage, error) {
	text = strings.TrimSpace(text)
	if text == "" && len(photo) == 0 {
		return nil, errors.New("write the answer first")
	}
	if n := utf8.RuneCountInString(text); n > MaxSupportAnswer {
		return nil, fmt.Errorf("the answer is longer than %d characters", MaxSupportAnswer)
	}
	th, err := b.d.St.SupportThreadByID(thread)
	if err != nil {
		return nil, errors.New("no such conversation")
	}
	b.mu.Lock()
	token := b.token
	b.mu.Unlock()
	if token == "" {
		return nil, errors.New("the Telegram bot is not set up: add its token in Settings > Telegram")
	}
	name := ""
	if len(photo) > 0 {
		if name, err = b.saveSupportPhoto(photo); err != nil {
			return nil, err
		}
	}
	head := "🛟 <b>" + b.t("sp_staff") + "</b>"
	body := head
	if text != "" {
		body += "\n\n" + esc(text)
	}
	kb := keyboard{{{Text: b.t("b_sp_reply"), Data: "hpw"}}}
	if name != "" {
		caption, rest := body, ""
		if utf8.RuneCountInString(body) > 1000 { // a photo's caption takes 1024
			caption, rest = head, body
		}
		m, _ := json.Marshal(map[string]any{"inline_keyboard": kb})
		fields := map[string]string{"chat_id": strconv.FormatInt(th.Chat, 10), "caption": caption, "parse_mode": "HTML"}
		if rest == "" {
			fields["reply_markup"] = string(m)
		}
		err = b.upload("sendPhoto", fields, "photo", name, photo)
		if err == nil && rest != "" {
			err = b.send(th.Chat, rest, kb)
		}
	} else {
		err = b.send(th.Chat, body, kb)
	}
	if err != nil {
		b.removeSupportPhotos(name)
		if blocked(err) {
			return nil, errors.New("the answer was not delivered: this person blocked the bot or left Telegram")
		}
		return nil, fmt.Errorf("the answer was not delivered: %s", err.Error())
	}
	msg, answered, err := b.d.St.SupportAnswer(thread, by, text, name, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	go b.SupportReact(th.Chat, answered, "👌")
	return msg, nil
}

// ---------------------------------------------------------------- the admins in Telegram

// supportHead names the person for the admins.
func (b *Bot) supportHead(th *store.SupportThread, u *store.User) string {
	var sb strings.Builder
	sb.WriteString(b.t("sp_new"))
	if u != nil {
		st := u.Status(time.Now().Unix())
		fmt.Fprintf(&sb, "\n👤 <b>%s</b> · %s %s", esc(u.Name), statusIcon[st], b.t("st_"+st))
	} else {
		sb.WriteString("\n👤 " + b.t("sp_no_sub"))
	}
	tg := strings.TrimSpace(th.Name)
	if th.Username != "" {
		tg = strings.TrimSpace(tg + " @" + th.Username)
	}
	if tg == "" {
		tg = "—"
	}
	fmt.Fprintf(&sb, "\n✈️ %s · <code>%d</code>", esc(tg), th.Chat)
	return sb.String()
}

func (b *Bot) supportButtons(th *store.SupportThread) keyboard {
	id := strconv.FormatInt(th.ID, 10)
	return keyboard{{{Text: b.t("b_sp_answer"), Data: "hpr:" + id}, {Text: b.t("b_sp_open"), Data: "hpv:" + id}}}
}

// alertSupport tells the bot's admins about a new message, with the photo.
func (b *Bot) alertSupport(th *store.SupportThread, m *store.SupportMessage, u *store.User) {
	b.mu.Lock()
	token, admins := b.token, b.adminList()
	b.mu.Unlock()
	if token == "" || len(admins) == 0 {
		return
	}
	text := b.supportHead(th, u)
	if m.Text != "" {
		text += "\n\n" + esc(clip(m.Text, 3000))
	}
	kb := b.supportButtons(th)
	var photo []byte
	if m.Photo != "" && b.d.SupportDir != "" {
		photo, _ = os.ReadFile(filepath.Join(b.d.SupportDir, m.Photo))
	}
	for _, id := range admins {
		if id == th.Chat {
			continue // an admin trying it out on themself
		}
		if len(photo) > 0 {
			km, _ := json.Marshal(map[string]any{"inline_keyboard": kb})
			fields := map[string]string{"chat_id": strconv.FormatInt(id, 10), "caption": clip(text, 1000), "parse_mode": "HTML", "reply_markup": string(km)}
			if b.upload("sendPhoto", fields, "photo", m.Photo, photo) == nil {
				continue
			}
		}
		b.send(id, text, kb)
	}
}

// adminAnswer sends what an admin wrote after "Answer".
func (b *Bot) adminAnswer(chat, thread int64, text, file string) {
	var photo []byte
	if file != "" {
		data, err := b.download(file)
		if err != nil {
			b.send(chat, "⚠️ "+esc(err.Error()), nil)
			return
		}
		photo = data
	}
	th, err := b.d.St.SupportThreadByID(thread)
	if err != nil {
		return
	}
	if _, err := b.SupportReply(thread, fmt.Sprintf("bot (%d)", chat), text, photo); err != nil {
		b.send(chat, "⚠️ "+esc(err.Error()), b.supportButtons(th))
		return
	}
	var u *store.User
	if th.UserID > 0 {
		u, _ = b.d.St.UserByID(th.UserID)
	}
	b.send(chat, fmt.Sprintf(b.t("sp_sent"), esc(supportWho(th, u))), keyboard{{{Text: b.t("b_sp_open"), Data: "hpv:" + strconv.FormatInt(thread, 10)},
		{Text: b.t("b_sp_list"), Data: "hpl"}}})
}

// supportMenuButton is the admin's main-menu entry, with what waits.
func (b *Bot) supportMenuButton() button {
	label := b.t("b_support")
	if c := b.d.St.SupportCount(0); c.Waiting > 0 {
		label += " · " + strconv.Itoa(c.Waiting)
	}
	return button{Text: label, Data: "hpl"}
}

// supportList shows the open conversations to an admin.
func (b *Bot) supportList(chat, msg int64) {
	c := b.d.St.SupportCount(0)
	list, _ := b.d.St.SupportThreads(store.SupportFilter{Status: "open", Limit: 10})
	kb := keyboard{}
	for _, th := range list {
		var u *store.User
		if th.UserID > 0 {
			u, _ = b.d.St.UserByID(th.UserID)
		}
		icon := "✅"
		switch {
		case th.Unread > 0:
			icon = "🔴"
		case th.Waiting():
			icon = "🟡"
		}
		label := icon + " " + supportWho(th, u)
		if th.Unread > 0 {
			label += " · " + strconv.Itoa(th.Unread)
		}
		kb = append(kb, []button{{Text: label, Data: "hpv:" + strconv.FormatInt(th.ID, 10)}})
	}
	text := fmt.Sprintf(b.t("sp_list"), c.Waiting, c.Open)
	if len(list) == 0 {
		text += "\n\n" + b.t("sp_none")
	}
	kb = append(kb, []button{{Text: b.t("b_back"), Data: "m"}})
	if msg > 0 {
		b.edit(chat, msg, text, kb)
	} else {
		b.send(chat, text, kb)
	}
}

// supportView shows one conversation to an admin (which marks it seen).
func (b *Bot) supportView(chat, msg, thread int64) {
	th, err := b.d.St.SupportThreadByID(thread)
	if err != nil {
		b.send(chat, b.t("sp_none"), b.backMenu())
		return
	}
	var u *store.User
	if th.UserID > 0 {
		u, _ = b.d.St.UserByID(th.UserID)
	}
	var sb strings.Builder
	sb.WriteString(b.supportHead(th, u) + "\n")
	msgs, _, _ := b.d.St.SupportMessages(th.ID, 0, 6)
	for _, m := range msgs {
		who := "🙋 " + esc(supportWho(th, u))
		if m.Staff {
			who = "🛟 " + esc(m.By)
		}
		body := clip(m.Text, 400)
		if m.Photo != "" {
			body = strings.TrimSpace("📷 " + body)
		}
		fmt.Fprintf(&sb, "\n<b>%s</b> · %s\n%s\n", who, stamp(m.At), esc(body))
	}
	if ids := b.d.St.SupportMarkSeen(th.ID); len(ids) > 0 {
		go b.SupportReact(th.Chat, ids, "👀")
	}
	id := strconv.FormatInt(th.ID, 10)
	toggle := button{Text: b.t("b_sp_close"), Data: "hpc:" + id}
	if !th.Open {
		toggle = button{Text: b.t("b_sp_reopen"), Data: "hpo:" + id}
	}
	kb := keyboard{{{Text: b.t("b_sp_answer"), Data: "hpr:" + id}, toggle}, {{Text: b.t("b_back"), Data: "hpl"}}}
	text := strings.TrimRight(sb.String(), "\n")
	if msg > 0 {
		b.edit(chat, msg, text, kb)
	} else {
		b.send(chat, text, kb)
	}
}

// onSupportButton handles the support buttons; it reports whether it did.
func (b *Bot) onSupportButton(chat, msg int64, data string) bool {
	parts := strings.Split(data, ":")
	switch parts[0] {
	case "hp", "hpw": // the person: write to support (hpw: "Reply" under an answer)
		b.openSupport(chat)
	case "hpx": // the person: done writing
		b.setWriting(chat, false)
		b.call("editMessageReplyMarkup", map[string]any{"chat_id": chat, "message_id": msg, "reply_markup": map[string]any{"inline_keyboard": [][]button{}}})
		kb := keyboard{}
		if _, err := b.d.St.UserByTG(chat); err == nil {
			kb = append(kb, []button{{Text: b.t("b_account"), Data: "me"}})
		}
		if kb = b.withSupport(chat, kb); len(kb) == 0 {
			kb = nil
		}
		b.send(chat, b.t("sp_done"), kb)
	case "hpl", "hpv", "hpr", "hpc", "hpo":
		if !b.isAdmin(chat) {
			return true
		}
		id, _ := strconv.ParseInt(arg(parts, 1), 10, 64)
		switch parts[0] {
		case "hpl":
			b.supportList(chat, msg)
		case "hpv":
			b.supportView(chat, 0, id)
		case "hpr":
			th, err := b.d.St.SupportThreadByID(id)
			if err != nil {
				return true
			}
			var u *store.User
			if th.UserID > 0 {
				u, _ = b.d.St.UserByID(th.UserID)
			}
			b.mu.Lock()
			b.waiting[chat] = "hpr:" + strconv.FormatInt(id, 10)
			b.mu.Unlock()
			b.send(chat, fmt.Sprintf(b.t("sp_ask_answer"), esc(supportWho(th, u))), nil)
		case "hpc", "hpo":
			if b.d.St.SetSupportOpen(id, parts[0] == "hpo", fmt.Sprintf("bot (%d)", chat), time.Now().Unix()) == nil {
				b.supportView(chat, msg, id)
			}
		}
	default:
		return false
	}
	return true
}

// ---------------------------------------------------------------- retention

// pruneSupport removes what the retention settings say is too old or too many.
func (b *Bot) pruneSupport() {
	set := LoadSupport(b.d.St)
	var before int64
	if set.Days > 0 {
		before = time.Now().Unix() - int64(set.Days)*86400
	}
	if set.Keep == 0 && before == 0 {
		return
	}
	photos, _, err := b.d.St.PruneSupport(set.Keep, before)
	if err == nil {
		b.removeSupportPhotos(photos...)
	}
}
