package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// Free trial: a person new to the bot gets a short test account (some data,
// some hours, a device or two) once per Telegram account, with its link and
// QR. When it ends, the bot offers a plan if the shop is on; ended trials
// nobody bought are removed after a few days.

// Trial settings.
const (
	keyTrialOn       = "trial_on"
	keyTrialDataMB   = "trial_data_mb"
	keyTrialHours    = "trial_hours"
	keyTrialDevices  = "trial_devices"
	keyTrialInbounds = "trial_inbounds"
	keyTrialDaily    = "trial_daily"
	keyTrialChannel  = "trial_channel"
	keyTrialCleanup  = "trial_cleanup_days"
)

// TrialSettings is what the owner sets for free trials.
type TrialSettings struct {
	On          bool    `json:"on"`
	DataMB      int     `json:"dataMB"`      // data in MB (0 = no limit)
	Hours       int     `json:"hours"`       // how long it lasts
	Devices     int     `json:"devices"`     // devices at once (0 = no limit)
	Inbounds    []int64 `json:"inbounds"`    // the protocols it may use (empty = all)
	Daily       int     `json:"daily"`       // trials a day at most (0 = no cap)
	Channel     string  `json:"channel"`     // a channel to join first ("@name", "" = none)
	CleanupDays int     `json:"cleanupDays"` // remove ended trials nobody bought after this many days (0 = keep)
}

// LoadTrial reads the trial settings, with their defaults.
func LoadTrial(st *store.Store) TrialSettings {
	n := func(k string, def int) int {
		v, err := strconv.Atoi(st.GetSetting(k, strconv.Itoa(def)))
		if err != nil {
			return def
		}
		return v
	}
	t := TrialSettings{On: st.GetSetting(keyTrialOn, "false") == "true", DataMB: n(keyTrialDataMB, 1024), Hours: n(keyTrialHours, 24),
		Devices: n(keyTrialDevices, 1), Daily: n(keyTrialDaily, 50), Channel: st.GetSetting(keyTrialChannel, ""), CleanupDays: n(keyTrialCleanup, 3)}
	json.Unmarshal([]byte(st.GetSetting(keyTrialInbounds, "[]")), &t.Inbounds)
	if t.Inbounds == nil {
		t.Inbounds = []int64{}
	}
	return t
}

// Check says what is wrong with the settings, if anything.
func (t *TrialSettings) Check() error {
	t.Channel = strings.TrimSpace(t.Channel)
	if strings.HasPrefix(t.Channel, "https://t.me/") || strings.HasPrefix(t.Channel, "t.me/") {
		t.Channel = "@" + t.Channel[strings.LastIndex(t.Channel, "/")+1:]
	}
	switch {
	case t.DataMB < 0 || t.DataMB > 1<<20:
		return errors.New("the trial's data must be between 0 (no limit) and 1048576 MB")
	case t.Hours < 1 || t.Hours > 24*30:
		return errors.New("a trial lasts between 1 hour and 30 days")
	case t.Devices < 0 || t.Devices > 100:
		return errors.New("devices must be between 0 (no limit) and 100")
	case t.Daily < 0 || t.Daily > 100000:
		return errors.New("trials a day must be between 0 (no cap) and 100000")
	case t.CleanupDays < 0 || t.CleanupDays > 365:
		return errors.New("removal after 0 (never) to 365 days")
	case t.Channel != "" && (!strings.HasPrefix(t.Channel, "@") || len(t.Channel) < 5 || strings.ContainsAny(t.Channel, " /")):
		return errors.New(`the channel is its public name, like @mychannel`)
	}
	return nil
}

// SaveTrial stores the settings.
func SaveTrial(st *store.Store, t TrialSettings) {
	st.SetSetting(keyTrialOn, strconv.FormatBool(t.On))
	st.SetSetting(keyTrialDataMB, strconv.Itoa(t.DataMB))
	st.SetSetting(keyTrialHours, strconv.Itoa(t.Hours))
	st.SetSetting(keyTrialDevices, strconv.Itoa(t.Devices))
	st.SetSetting(keyTrialDaily, strconv.Itoa(t.Daily))
	st.SetSetting(keyTrialChannel, t.Channel)
	st.SetSetting(keyTrialCleanup, strconv.Itoa(t.CleanupDays))
	ids, _ := json.Marshal(t.Inbounds)
	st.SetSetting(keyTrialInbounds, string(ids))
}

// trialOffered: this chat may still take a trial (no account, none taken).
func (b *Bot) trialOffered(chat int64) bool {
	if !LoadTrial(b.d.St).On || b.d.St.HasTrial(chat) {
		return false
	}
	_, err := b.d.St.UserByTG(chat)
	return err != nil
}

func (b *Bot) trialButton() []button { return []button{{Text: b.t("b_trial"), Data: "tr"}} }

// trialSize describes the trial ("1 GB · 24 hours · 1 device").
func (b *Bot) trialSize(t TrialSettings) string {
	data := b.t("unlimited")
	if t.DataMB > 0 {
		data = util.HumanBytes(int64(t.DataMB) << 20)
	}
	parts := []string{data}
	if t.Hours%24 == 0 {
		parts = append(parts, fmt.Sprintf("%d %s", t.Hours/24, b.t("days")))
	} else {
		parts = append(parts, fmt.Sprintf("%d %s", t.Hours, b.t("hours")))
	}
	if t.Devices > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", t.Devices, b.t("devices_n")))
	}
	return strings.Join(parts, " · ")
}

// inChannel says whether the person joined the channel. When the bot cannot
// tell (it is not in the channel), the trial is not held back: the owner
// hears about it instead.
func (b *Bot) inChannel(channel string, chat int64) bool {
	raw, err := b.call("getChatMember", map[string]any{"chat_id": channel, "user_id": chat})
	if err != nil {
		b.mu.Lock()
		first := b.channelErr != err.Error()
		b.channelErr = err.Error()
		b.mu.Unlock()
		if first {
			b.d.St.Log("settings", "the bot cannot check who joined %s (make it an admin of the channel): %s", channel, err.Error())
		}
		return true
	}
	var m struct {
		Status string `json:"status"`
	}
	json.Unmarshal(raw, &m)
	switch m.Status {
	case "creator", "administrator", "member", "restricted":
		return true
	}
	return false
}

// giveTrial makes the free trial of a chat, after every check.
func (b *Bot) giveTrial(chat int64) {
	t := LoadTrial(b.d.St)
	switch {
	case !t.On:
		b.send(chat, b.t("trial_off"), nil)
		return
	case b.d.St.HasTrial(chat):
		b.send(chat, b.t("trial_taken"), b.afterTrialMenu(chat))
		return
	}
	if u, err := b.d.St.UserByTG(chat); err == nil {
		b.send(chat, b.t("trial_have_account"), nil)
		b.sendUserCard(chat, u)
		return
	}
	now := time.Now().Unix()
	if t.Daily > 0 && b.d.St.TrialsSince(now-86400) >= t.Daily {
		b.send(chat, b.t("trial_busy"), b.afterTrialMenu(chat))
		return
	}
	if t.Channel != "" && !b.inChannel(t.Channel, chat) {
		b.send(chat, fmt.Sprintf(b.t("trial_join"), esc(t.Channel)), keyboard{
			{{Text: b.t("b_join"), URL: "https://t.me/" + strings.TrimPrefix(t.Channel, "@")}},
			{{Text: b.t("b_joined"), Data: "tr"}},
		})
		return
	}
	if !b.d.St.ClaimTrial(chat, now) { // a second tap while the first is being made
		return
	}
	var u *store.User
	var err error
	for i := 0; i < 5; i++ {
		u = b.d.NewUser("trial-" + util.RandomLower(6))
		if t.DataMB > 0 {
			u.DataLimit = int64(t.DataMB) << 20
		}
		u.ExpireAt = now + int64(t.Hours)*3600
		u.IPLimit = t.Devices
		u.InboundIDs = append([]int64{}, t.Inbounds...)
		u.TGChat = chat
		u.Note = "🎁 Telegram trial"
		if err = b.d.St.CreateUser(u); err == nil {
			break
		}
	}
	if err != nil {
		b.d.St.DropTrial(chat)
		b.send(chat, b.t("trial_error"), nil)
		b.d.St.Log("user", "a free trial for Telegram %d could not be made: %s", chat, err.Error())
		return
	}
	b.d.St.SetTrialUser(chat, u.ID, u.Name, u.ExpireAt)
	b.d.St.Log("user", "free trial %s given to Telegram %d (%s)", u.Name, chat, b.trialSize(t))
	b.d.Mgr.RequestApply()
	b.send(chat, fmt.Sprintf(b.t("trial_given"), b.trialSize(t)), nil)
	b.sendUserCard(chat, u)
}

// afterTrialMenu: the shop, when there is one.
func (b *Bot) afterTrialMenu(chat int64) keyboard {
	if b.shopOn() {
		return keyboard{{{Text: b.t("b_buy"), Data: "sh"}}}
	}
	return nil
}

// cleanTrials removes ended trials nobody bought, CleanupDays after their end.
func (b *Bot) cleanTrials() {
	t := LoadTrial(b.d.St)
	if t.CleanupDays == 0 {
		return
	}
	now := time.Now().Unix()
	trials, _ := b.d.St.Trials(0)
	removed := 0
	for _, tr := range trials {
		if tr.UserID == 0 || tr.Bought || tr.Ends == 0 || now < tr.Ends+int64(t.CleanupDays)*86400 {
			continue
		}
		u, err := b.d.St.UserByID(tr.UserID)
		if err != nil {
			continue // already gone
		}
		if st := u.Status(now); st == store.StatusActive || st == store.StatusPending {
			continue // renewed by hand: a customer now
		}
		if b.d.St.DeleteUser(u.ID) == nil {
			removed++
		}
	}
	if removed > 0 {
		b.d.St.Log("user", "%d ended free trials removed", removed)
		b.d.Mgr.RequestApply()
	}
}
