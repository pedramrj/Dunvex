package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// Broadcast: one message to many users through the bot. It reaches users who
// linked their Telegram (the bot's link on their subscription page), at a pace
// Telegram accepts, one broadcast at a time.

// Audiences.
const (
	AudienceAll      = "all"      // every linked user
	AudienceActive   = "active"   // users who can connect now (active or waiting for their first connection)
	AudienceEnding   = "ending"   // active, but the time or the data is running out
	AudienceEnded    = "ended"    // over the data limit or expired
	AudienceSelected = "selected" // the users picked in the panel
	AudienceTag      = "tag"      // the users with a tag
)

// BroadcastRequest is what an admin sends.
type BroadcastRequest struct {
	Text     string  `json:"text"`
	HTML     bool    `json:"html"`     // the text uses Telegram's HTML tags (else it is sent as written)
	Audience string  `json:"audience"` // one of the Audience values
	UserIDs  []int64 `json:"userIds"`  // for AudienceSelected
	Tag      string  `json:"tag"`      // for AudienceTag
	Days     int     `json:"days"`     // AudienceEnding: expires within this many days (0 = 7)
	Button   bool    `json:"button"`   // add the "my account" button under the message
	By       string  `json:"-"`        // the admin who sends it
	Owner    int64   `json:"-"`        // a reseller's id: only their users (0 = everyone)
}

// Broadcast is one sent (or sending) message.
type Broadcast struct {
	ID          int64    `json:"id"`
	At          int64    `json:"at"`   // when it started
	Done        int64    `json:"done"` // when it ended (0 while sending)
	By          string   `json:"by"`
	Owner       int64    `json:"owner,omitempty"`
	Audience    string   `json:"audience"`
	Tag         string   `json:"tag,omitempty"`
	Text        string   `json:"text"`
	HTML        bool     `json:"html"`
	Total       int      `json:"total"`
	Sent        int      `json:"sent"`
	Failed      int      `json:"failed"`
	Blocked     int      `json:"blocked"` // users who blocked the bot or left Telegram: their link was removed
	Canceled    bool     `json:"canceled,omitempty"`
	Interrupted bool     `json:"interrupted,omitempty"` // the panel stopped while it was sending
	Errors      []string `json:"errors,omitempty"`      // the first few, for the admin
}

// Count is an audience's size: its users, and how many of them the bot can reach.
type Count struct {
	Users  int `json:"users"`
	Linked int `json:"linked"`
}

const (
	broadcastHistory = "tg_broadcasts" // setting: the last broadcasts (JSON)
	historyKeep      = 30
	maxText          = 3500 // characters, before the placeholders are filled
)

// broadcastPace keeps under Telegram's limit for bots (about 30 messages a second).
var broadcastPace = 50 * time.Millisecond

type broadcasting struct {
	mu      sync.Mutex
	running *Broadcast
	cancel  chan struct{}
	saveMu  sync.Mutex
}

// ---------------------------------------------------------------- audience

func endingSoon(u *store.User, now int64, days int) bool {
	if days <= 0 {
		days = 7
	}
	return (u.ExpireAt > 0 && u.ExpireAt-now <= int64(days)*86400) || (u.DataLimit > 0 && u.Used()*100 >= u.DataLimit*80)
}

func inAudience(u *store.User, now int64, aud string, days int) bool {
	st := u.Status(now)
	switch aud {
	case AudienceAll, AudienceSelected, AudienceTag:
		return true
	case AudienceActive:
		return st == store.StatusActive || st == store.StatusPending
	case AudienceEnding:
		return st == store.StatusActive && endingSoon(u, now, days)
	case AudienceEnded:
		return st == store.StatusLimited || st == store.StatusExpired
	}
	return false
}

// users of the request's audience (linked to Telegram or not), within the owner's reach.
func (b *Bot) audienceUsers(req BroadcastRequest) ([]*store.User, error) {
	users, err := b.d.St.Users()
	if err != nil {
		return nil, err
	}
	pick := map[int64]bool{}
	for _, id := range req.UserIDs {
		pick[id] = true
	}
	now := time.Now().Unix()
	var out []*store.User
	for _, u := range users {
		if req.Owner != 0 && u.OwnerID != req.Owner {
			continue
		}
		if req.Audience == AudienceSelected && !pick[u.ID] {
			continue
		}
		if req.Audience == AudienceTag && !u.HasTag(req.Tag) {
			continue
		}
		if inAudience(u, now, req.Audience, req.Days) {
			out = append(out, u)
		}
	}
	return out, nil
}

// AudienceCounts sizes every audience for the panel (owner as in BroadcastRequest).
func (b *Bot) AudienceCounts(owner int64, days int) map[string]Count {
	out := map[string]Count{}
	for _, aud := range []string{AudienceAll, AudienceActive, AudienceEnding, AudienceEnded} {
		us, _ := b.audienceUsers(BroadcastRequest{Audience: aud, Owner: owner, Days: days})
		c := Count{Users: len(us)}
		for _, u := range us {
			if u.TGChat != 0 {
				c.Linked++
			}
		}
		out[aud] = c
	}
	return out
}

// TagCount is the audience of one tag.
type TagCount struct {
	Tag string `json:"tag"`
	Count
}

// TagCounts sizes the audience of every tag the owner's users carry.
func (b *Bot) TagCounts(owner int64) []TagCount {
	us, _ := b.audienceUsers(BroadcastRequest{Audience: AudienceAll, Owner: owner})
	out := []TagCount{}
	for _, tc := range store.CountTags(us) {
		c := TagCount{Tag: tc.Tag, Count: Count{Users: tc.Count}}
		for _, u := range us {
			if u.TGChat != 0 && u.HasTag(tc.Tag) {
				c.Linked++
			}
		}
		out = append(out, c)
	}
	return out
}

// SelectedCount sizes a hand-picked audience.
func (b *Bot) SelectedCount(owner int64, ids []int64) Count {
	us, _ := b.audienceUsers(BroadcastRequest{Audience: AudienceSelected, UserIDs: ids, Owner: owner})
	c := Count{Users: len(us)}
	for _, u := range us {
		if u.TGChat != 0 {
			c.Linked++
		}
	}
	return c
}

// ---------------------------------------------------------------- the message

var allowedTags = map[string]bool{"b": true, "strong": true, "i": true, "em": true, "u": true, "ins": true, "s": true,
	"strike": true, "del": true, "a": true, "code": true, "pre": true, "tg-spoiler": true, "span": true, "blockquote": true}

var tagRe = regexp.MustCompile(`<(/?)([a-zA-Z][a-zA-Z0-9-]*)((?:\s[^<>]*)?)>`)

// checkHTML accepts the HTML Telegram takes: its tags, closed in order, and no
// stray "<" (write &lt;).
func checkHTML(s string) error {
	var stack []string
	rest := s
	for {
		loc := tagRe.FindStringSubmatchIndex(rest)
		before := rest
		if loc != nil {
			before = rest[:loc[0]]
		}
		if strings.ContainsAny(before, "<>") {
			return errors.New(`a "<" or ">" that is not part of a tag: write &lt; and &gt;`)
		}
		if loc == nil {
			break
		}
		closing, name := rest[loc[2]:loc[3]] == "/", strings.ToLower(rest[loc[4]:loc[5]])
		if !allowedTags[name] {
			return fmt.Errorf("<%s> is not a tag Telegram knows (b, i, u, s, a, code, pre, blockquote, tg-spoiler)", name)
		}
		if closing {
			if len(stack) == 0 || stack[len(stack)-1] != name {
				return fmt.Errorf("</%s> closes nothing open", name)
			}
			stack = stack[:len(stack)-1]
		} else {
			stack = append(stack, name)
		}
		rest = rest[loc[1]:]
	}
	if len(stack) > 0 {
		return fmt.Errorf("<%s> is not closed", stack[len(stack)-1])
	}
	return nil
}

// Check says what is wrong with a request before anything is sent.
func (r BroadcastRequest) Check() error {
	t := strings.TrimSpace(r.Text)
	if t == "" {
		return errors.New("write the message first")
	}
	if utf8.RuneCountInString(t) > maxText {
		return fmt.Errorf("the message is longer than %d characters", maxText)
	}
	switch r.Audience {
	case AudienceAll, AudienceActive, AudienceEnding, AudienceEnded:
	case AudienceSelected:
		if len(r.UserIDs) == 0 {
			return errors.New("pick the users first")
		}
	case AudienceTag:
		if store.NormalizeTag(r.Tag) == "" {
			return errors.New("pick the tag first")
		}
	default:
		return fmt.Errorf("unknown audience %q", r.Audience)
	}
	if r.HTML {
		return checkHTML(t)
	}
	return nil
}

// fill puts one user's details into the placeholders: {name}, {days} (days
// left), {data} (data left) and {link} (their subscription address).
func (b *Bot) fill(r BroadcastRequest, u *store.User, now int64) string {
	text := strings.TrimSpace(r.Text)
	if !r.HTML {
		text = html.EscapeString(text)
	}
	days, data := "∞", "∞"
	if u.ExpireAt > 0 {
		days = strconv.FormatInt(max(0, (u.ExpireAt-now+86399)/86400), 10)
	} else if u.DurationDays > 0 {
		days = strconv.Itoa(u.DurationDays)
	}
	if u.DataLimit > 0 {
		data = util.HumanBytes(max(0, u.DataLimit-u.Used()))
	}
	link := ""
	if b.d.SubURL != nil {
		link = b.d.SubURL(u)
	}
	return strings.NewReplacer("{name}", html.EscapeString(u.Name), "{days}", days, "{data}", html.EscapeString(data),
		"{link}", html.EscapeString(link)).Replace(text)
}

func (b *Bot) broadcastKeyboard(r BroadcastRequest) keyboard {
	if !r.Button {
		return nil
	}
	kb := keyboard{{{Text: b.t("b_account"), Data: "me"}}}
	if b.shopOn() {
		kb[0] = append(kb[0], button{Text: b.t("b_renew"), Data: "sh:r"})
	}
	return kb
}

// ---------------------------------------------------------------- from the admin bot

// audienceMenu offers the audiences with how many each reaches.
func (b *Bot) audienceMenu() keyboard {
	c := b.AudienceCounts(0, 7)
	item := func(key, aud string) button {
		return button{Text: fmt.Sprintf("%s · %d", b.t(key), c[aud].Linked), Data: "bca:" + aud}
	}
	return keyboard{
		{item("bc_all", AudienceAll), item("bc_active", AudienceActive)},
		{item("bc_ending", AudienceEnding), item("bc_ended", AudienceEnded)},
		{{Text: b.t("b_back"), Data: "m"}},
	}
}

func (b *Bot) broadcastButton(chat, msg int64, parts []string) {
	switch parts[0] {
	case "bc":
		b.edit(chat, msg, b.t("bc_pick"), b.audienceMenu())
	case "bca": // an audience: now the text
		aud := arg(parts, 1)
		if b.AudienceCounts(0, 7)[aud].Linked == 0 {
			b.send(chat, b.t("bc_nobody"), b.backMenu())
			return
		}
		b.mu.Lock()
		b.waiting[chat] = "bc:" + aud
		b.mu.Unlock()
		b.send(chat, b.t("bc_ask"), nil)
	case "bcx":
		b.mu.Lock()
		delete(b.drafts, chat)
		b.mu.Unlock()
		b.edit(chat, msg, b.t("bc_canceled"), b.backMenu())
	case "bcs":
		b.mu.Lock()
		r, ok := b.drafts[chat]
		delete(b.drafts, chat)
		b.mu.Unlock()
		if !ok {
			return
		}
		run, err := b.StartBroadcast(r)
		if err != nil {
			b.send(chat, "⚠️ "+esc(err.Error()), b.backMenu())
			return
		}
		b.edit(chat, msg, fmt.Sprintf(b.t("bc_progress"), 0, run.Total, 0, 0), keyboard{{{Text: b.t("b_stop"), Data: "bcq:" + strconv.FormatInt(run.ID, 10)}}})
		go b.followBroadcast(chat, msg, run.ID)
	case "bcq":
		id, _ := strconv.ParseInt(arg(parts, 1), 10, 64)
		b.CancelBroadcast(id)
	}
}

// broadcastDraft takes the admin's text and shows it as users will get it.
func (b *Bot) broadcastDraft(chat int64, aud, text string) {
	r := BroadcastRequest{Text: text, Audience: aud, Button: true, By: fmt.Sprintf("bot (%d)", chat)}
	if err := r.Check(); err != nil {
		b.send(chat, "⚠️ "+esc(err.Error()), b.backMenu())
		return
	}
	us, _ := b.audienceUsers(r)
	var to []*store.User
	for _, u := range us {
		if u.TGChat != 0 {
			to = append(to, u)
		}
	}
	if len(to) == 0 {
		b.send(chat, b.t("bc_nobody"), b.backMenu())
		return
	}
	b.mu.Lock()
	b.drafts[chat] = r
	b.mu.Unlock()
	b.send(chat, fmt.Sprintf(b.t("bc_preview"), esc(to[0].Name))+"\n\n"+b.fill(r, to[0], time.Now().Unix()),
		keyboard{{{Text: fmt.Sprintf(b.t("bc_send"), len(to)), Data: "bcs"}, {Text: b.t("bc_cancel"), Data: "bcx"}}})
}

// followBroadcast keeps the admin's message up to date until the end.
func (b *Bot) followBroadcast(chat, msg, id int64) {
	for i := 0; i < 2400; i++ { // two hours at most
		time.Sleep(3 * time.Second)
		for _, x := range b.Broadcasts(0) {
			if x.ID != id {
				continue
			}
			switch {
			case x.Done != 0 && x.Canceled:
				b.edit(chat, msg, fmt.Sprintf(b.t("bc_stopped"), x.Sent, x.Total), b.backMenu())
				return
			case x.Done != 0:
				b.edit(chat, msg, fmt.Sprintf(b.t("bc_done"), x.Sent, x.Total, x.Failed, x.Blocked), b.backMenu())
				return
			}
			b.edit(chat, msg, fmt.Sprintf(b.t("bc_progress"), x.Sent+x.Failed+x.Blocked, x.Total, x.Failed, x.Blocked),
				keyboard{{{Text: b.t("b_stop"), Data: "bcq:" + strconv.FormatInt(id, 10)}}})
		}
	}
}

// ---------------------------------------------------------------- sending

// TestBroadcast sends the message to the bot's admins, filled in with the
// first user of the audience (or a sample), so they see it as users will.
func (b *Bot) TestBroadcast(r BroadcastRequest) (int, error) {
	if err := r.Check(); err != nil {
		return 0, err
	}
	b.mu.Lock()
	token, admins := b.token, b.adminList()
	b.mu.Unlock()
	if token == "" {
		return 0, errors.New("the Telegram bot is not set up: add its token in Settings > Telegram")
	}
	if len(admins) == 0 {
		return 0, errors.New("add your chat ID as a bot admin (Settings > Telegram) to get test messages")
	}
	sample := &store.User{Name: "sample", ExpireAt: time.Now().Unix() + 12*86400, DataLimit: 50 << 30, SubToken: "sample"}
	if us, _ := b.audienceUsers(r); len(us) > 0 {
		sample = us[0]
	}
	now := time.Now().Unix()
	for _, id := range admins {
		if err := b.send(id, "🧪 "+b.fill(r, sample, now), b.broadcastKeyboard(r)); err != nil {
			return 0, err
		}
	}
	return len(admins), nil
}

// StartBroadcast begins sending in the background and returns at once.
func (b *Bot) StartBroadcast(r BroadcastRequest) (*Broadcast, error) {
	if err := r.Check(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	token := b.token
	b.mu.Unlock()
	if token == "" {
		return nil, errors.New("the Telegram bot is not set up: add its token in Settings > Telegram")
	}
	us, err := b.audienceUsers(r)
	if err != nil {
		return nil, err
	}
	var to []*store.User
	for _, u := range us {
		if u.TGChat != 0 {
			to = append(to, u)
		}
	}
	if len(to) == 0 {
		return nil, errors.New("nobody in this audience has linked Telegram yet")
	}
	bc := &b.bc
	bc.mu.Lock()
	if bc.running != nil {
		bc.mu.Unlock()
		return nil, errors.New("another message is being sent: wait for it or stop it")
	}
	now := time.Now()
	run := &Broadcast{ID: now.UnixMilli(), At: now.Unix(), // milliseconds: exact in JavaScript (below 2^53)
		By: r.By, Owner: r.Owner, Audience: r.Audience, Tag: map[bool]string{true: store.NormalizeTag(r.Tag)}[r.Audience == AudienceTag],
		Text: strings.TrimSpace(r.Text), HTML: r.HTML, Total: len(to)}
	bc.running, bc.cancel = run, make(chan struct{})
	cancel := bc.cancel
	bc.mu.Unlock()
	b.saveBroadcast(*run)
	go b.sendAll(r, run, to, cancel)
	cp := *run
	return &cp, nil
}

var retryAfter = regexp.MustCompile(`retry after (\d+)`)

// blocked: the user is out of reach for good (blocked the bot, deleted the account).
func blocked(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "bot was blocked") || strings.Contains(m, "user is deactivated") ||
		strings.Contains(m, "chat not found") || strings.Contains(m, "bot can't initiate")
}

func (b *Bot) sendAll(r BroadcastRequest, run *Broadcast, to []*store.User, cancel <-chan struct{}) {
	defer func() {
		if p := recover(); p != nil {
			b.noteError(run, fmt.Sprint(p))
		}
		b.bc.mu.Lock()
		run.Done = time.Now().Unix()
		final := *run
		b.bc.running = nil
		b.bc.mu.Unlock()
		b.saveBroadcast(final)
		who := final.By
		if who == "" {
			who = "an admin"
		}
		b.d.St.Log("user", "%s sent a message to %d users: %d delivered, %d failed, %d had blocked the bot%s",
			who, final.Total, final.Sent, final.Failed, final.Blocked, map[bool]string{true: " (stopped)", false: ""}[final.Canceled])
	}()
	kb := b.broadcastKeyboard(r)
	last := time.Now()
	for i, u := range to {
		select {
		case <-cancel:
			b.bc.mu.Lock()
			run.Canceled = true
			b.bc.mu.Unlock()
			return
		default:
		}
		var err error
		for try := 0; try < 4; try++ {
			if err = b.send(u.TGChat, b.fill(r, u, time.Now().Unix()), kb); err == nil {
				break
			}
			m := retryAfter.FindStringSubmatch(err.Error())
			if m == nil {
				break
			}
			wait, _ := strconv.Atoi(m[1])
			select {
			case <-cancel:
			case <-time.After(time.Duration(wait+1) * time.Second):
			}
		}
		b.bc.mu.Lock()
		switch {
		case err == nil:
			run.Sent++
		case blocked(err):
			run.Blocked++
		default:
			run.Failed++
		}
		b.bc.mu.Unlock()
		if err != nil {
			if blocked(err) {
				if fresh, e := b.d.St.UserByID(u.ID); e == nil && fresh.TGChat == u.TGChat {
					fresh.TGChat = 0 // out of reach: no more alerts to a closed door
					b.d.St.UpdateUser(fresh)
				}
			} else {
				b.noteError(run, u.Name+": "+err.Error())
				// the message itself is wrong (bad HTML): every other one would fail the same way
				if strings.Contains(err.Error(), "can't parse entities") && run.Sent == 0 && i == 0 {
					b.bc.mu.Lock()
					run.Failed += len(to) - 1
					b.bc.mu.Unlock()
					return
				}
			}
		}
		if time.Since(last) > 3*time.Second { // the panel shows progress from the history too
			b.bc.mu.Lock()
			snap := *run
			b.bc.mu.Unlock()
			b.saveBroadcast(snap)
			last = time.Now()
		}
		time.Sleep(broadcastPace)
	}
}

func (b *Bot) noteError(run *Broadcast, msg string) {
	b.bc.mu.Lock()
	defer b.bc.mu.Unlock()
	if len(run.Errors) < 5 {
		run.Errors = append(run.Errors, msg)
	}
}

// CancelBroadcast stops the running broadcast (if it is id); the rest is not sent.
func (b *Bot) CancelBroadcast(id int64) bool {
	b.bc.mu.Lock()
	defer b.bc.mu.Unlock()
	if b.bc.running == nil || b.bc.running.ID != id {
		return false
	}
	select {
	case <-b.bc.cancel:
	default:
		close(b.bc.cancel)
	}
	return true
}

// Broadcasts is the history, newest first, with the running one up to date.
// owner != 0 keeps a reseller's own.
func (b *Bot) Broadcasts(owner int64) []Broadcast {
	list := b.history()
	b.bc.mu.Lock()
	if r := b.bc.running; r != nil {
		for i := range list {
			if list[i].ID == r.ID {
				list[i] = *r
			}
		}
	}
	b.bc.mu.Unlock()
	out := []Broadcast{}
	for _, x := range list {
		if owner == 0 || x.Owner == owner {
			out = append(out, x)
		}
	}
	return out
}

func (b *Bot) history() []Broadcast {
	var list []Broadcast
	json.Unmarshal([]byte(b.d.St.GetSetting(broadcastHistory, "[]")), &list)
	return list
}

func (b *Bot) saveBroadcast(x Broadcast) {
	b.bc.saveMu.Lock()
	defer b.bc.saveMu.Unlock()
	list := b.history()
	found := false
	for i := range list {
		if list[i].ID == x.ID {
			list[i], found = x, true
		}
	}
	if !found {
		list = append([]Broadcast{x}, list...)
	}
	if len(list) > historyKeep {
		list = list[:historyKeep]
	}
	js, _ := json.Marshal(list)
	b.d.St.SetSetting(broadcastHistory, string(js))
}

// markInterrupted closes broadcasts a stopped panel left open.
func (b *Bot) markInterrupted() {
	for _, x := range b.history() {
		if x.Done == 0 {
			x.Done, x.Interrupted = time.Now().Unix(), true
			b.saveBroadcast(x)
		}
	}
}
