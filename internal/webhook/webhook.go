// Package webhook sends Dunvex's events to other services: signed JSON for
// your own code, or ready-made messages for Slack and Discord. Deliveries
// that fail are tried again a few times, and the last ones are kept for the
// panel to show.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/store"
)

// Setting is where the webhooks are kept.
const Setting = "webhooks"

// Max is how many webhooks a panel keeps.
const Max = 20

// Kinds are the event kinds a webhook can follow.
var Kinds = []string{"user", "limit", "auth", "billing", "node", "core", "tls", "inbound", "health", "settings", "update"}

// Formats of the request body.
const (
	JSON    = "json"    // signed JSON for your own code
	Slack   = "slack"   // {"text": …} (Slack, Mattermost, Rocket.Chat…)
	Discord = "discord" // {"content": …}
)

// Hook is one webhook.
type Hook struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Format  string   `json:"format"`
	Kinds   []string `json:"kinds"` // the kinds it follows; empty = all
	Secret  string   `json:"secret"`
	Enabled bool     `json:"enabled"`
}

// Wants reports whether the hook follows events of kind.
func (h Hook) Wants(kind string) bool {
	if !h.Enabled {
		return false
	}
	if len(h.Kinds) == 0 {
		return true
	}
	for _, k := range h.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// NewSecret makes a signing secret.
func NewSecret() string { return "whsec_" + randomHex(24) }

// Clean checks a hook and fills what it lacks (id, secret, format).
func (h *Hook) Clean() error {
	h.Name = strings.Join(strings.Fields(h.Name), " ")
	h.URL = strings.TrimSpace(h.URL)
	u, err := url.Parse(h.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) address", h.URL)
	}
	if len([]rune(h.Name)) > 40 {
		return errors.New("the name is longer than 40 characters")
	}
	if h.Name == "" {
		h.Name = u.Host
	}
	switch h.Format {
	case "":
		h.Format = JSON
	case JSON, Slack, Discord:
	default:
		return fmt.Errorf("the format is %s, %s or %s", JSON, Slack, Discord)
	}
	seen := map[string]bool{}
	kinds := []string{}
	for _, k := range h.Kinds {
		ok := false
		for _, x := range Kinds {
			ok = ok || x == k
		}
		if !ok {
			return fmt.Errorf("%q is not an event kind", k)
		}
		if !seen[k] {
			seen[k] = true
			kinds = append(kinds, k)
		}
	}
	h.Kinds = kinds
	if h.ID == "" {
		h.ID = randomHex(6)
	}
	if h.Secret == "" {
		h.Secret = NewSecret()
	}
	return nil
}

// Load reads the webhooks.
func Load(st *store.Store) []Hook {
	out := []Hook{}
	if v := st.GetSetting(Setting, ""); v != "" {
		json.Unmarshal([]byte(v), &out)
	}
	return out
}

// Save stores the webhooks.
func Save(st *store.Store, hooks []Hook) error {
	if len(hooks) > Max {
		return fmt.Errorf("at most %d webhooks", Max)
	}
	b, _ := json.Marshal(hooks)
	return st.SetSetting(Setting, string(b))
}

// ---------------------------------------------------------------- sending

// Event is what a webhook carries.
type Event struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // the event kind (user, core, …) or "test"
	Message string `json:"message"`
	At      int64  `json:"at"`
	Panel   string `json:"panel"`
	Version string `json:"version"`
}

// Delivery is one attempt to deliver an event.
type Delivery struct {
	ID      string `json:"id"`
	Event   string `json:"event"`
	Message string `json:"message"`
	At      int64  `json:"at"`
	Attempt int    `json:"attempt"`
	OK      bool   `json:"ok"`
	Status  int    `json:"status,omitempty"`
	Error   string `json:"error,omitempty"`
	Ms      int64  `json:"ms"`
	Retry   int64  `json:"retry,omitempty"` // when it is tried again
}

// Keep is how many deliveries are kept per webhook.
const Keep = 30

// Sender delivers events to the webhooks.
type Sender struct {
	St      *store.Store
	Panel   func() string // the panel's name in events
	Version string
	HTTP    *http.Client
	// Backoff are the waits before each retry (default 10 s, 1 min, 5 min).
	Backoff []time.Duration

	mu   sync.Mutex
	logs map[string][]Delivery
	sent int64 // deliveries made, for the metrics
	fail int64
}

type job struct {
	hook    Hook
	ev      Event
	attempt int
}

// Send hands an event to every webhook that follows its kind.
func (s *Sender) Send(kind, message string) {
	for _, h := range Load(s.St) {
		if h.Wants(kind) {
			go s.deliver(job{hook: h, ev: s.event(kind, message), attempt: 1})
		}
	}
}

func (s *Sender) event(kind, message string) Event {
	name := "Dunvex"
	if s.Panel != nil {
		if n := s.Panel(); n != "" {
			name = n
		}
	}
	return Event{ID: "evt_" + randomHex(8), Type: kind, Message: message, At: time.Now().Unix(), Panel: name, Version: s.Version}
}

// Test delivers a test event to one webhook now and returns the result.
func (s *Sender) Test(h Hook) Delivery {
	return s.attempt(context.Background(), job{hook: h, ev: s.event("test", "This is a test from Dunvex: the webhook works."), attempt: 1})
}

// Log returns a webhook's latest deliveries, newest first.
func (s *Sender) Log(id string) []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Delivery{}, s.logs[id]...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Counts returns how many deliveries succeeded and failed since the start.
func (s *Sender) Counts() (ok, failed int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent, s.fail
}

// Forget drops the log of a removed webhook.
func (s *Sender) Forget(id string) {
	s.mu.Lock()
	delete(s.logs, id)
	s.mu.Unlock()
}

func (s *Sender) deliver(j job) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	d := s.attempt(ctx, j)
	cancel()
	if d.OK || d.Retry == 0 {
		return
	}
	time.Sleep(time.Until(time.Unix(d.Retry, 0)))
	// it may have been changed or removed meanwhile
	for _, h := range Load(s.St) {
		if h.ID == j.hook.ID && h.Enabled {
			j.hook, j.attempt = h, j.attempt+1
			s.deliver(j)
			return
		}
	}
}

func (s *Sender) backoff() []time.Duration {
	if s.Backoff != nil {
		return s.Backoff
	}
	return []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute}
}

// attempt makes one request and records it.
func (s *Sender) attempt(ctx context.Context, j job) Delivery {
	d := Delivery{ID: j.ev.ID, Event: j.ev.Type, Message: j.ev.Message, At: time.Now().Unix(), Attempt: j.attempt}
	start := time.Now()
	body := Body(j.hook.Format, j.ev)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.hook.URL, bytes.NewReader(body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Dunvex-Webhook/"+s.Version)
		if j.hook.Format == JSON {
			req.Header.Set("X-Dunvex-Event", j.ev.Type)
			req.Header.Set("X-Dunvex-Delivery", j.ev.ID)
			req.Header.Set("X-Dunvex-Signature", Sign(j.hook.Secret, time.Now().Unix(), body))
		}
		hc := s.HTTP
		if hc == nil {
			hc = &http.Client{Timeout: 10 * time.Second}
		}
		var resp *http.Response
		if resp, err = hc.Do(req); err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			d.Status = resp.StatusCode
			if resp.StatusCode >= 300 {
				err = fmt.Errorf("HTTP %d", resp.StatusCode)
			}
		} else {
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
		}
	}
	d.Ms = time.Since(start).Milliseconds()
	d.OK = err == nil
	if err != nil {
		d.Error = err.Error()
		// network errors, 429 and 5xx are tried again; other answers are final
		retry := d.Status == 0 || d.Status == http.StatusTooManyRequests || d.Status >= 500
		if b := s.backoff(); retry && j.ev.Type != "test" && j.attempt <= len(b) {
			d.Retry = time.Now().Add(b[j.attempt-1]).Unix()
		}
	}
	s.mu.Lock()
	if s.logs == nil {
		s.logs = map[string][]Delivery{}
	}
	l := append(s.logs[j.hook.ID], d)
	if len(l) > Keep {
		l = l[len(l)-Keep:]
	}
	s.logs[j.hook.ID] = l
	if d.OK {
		s.sent++
	} else {
		s.fail++
	}
	s.mu.Unlock()
	return d
}

// Body is the request body of an event in a format.
func Body(format string, ev Event) []byte {
	var v any = ev
	line := fmt.Sprintf("%s · %s: %s", ev.Panel, ev.Type, ev.Message)
	switch format {
	case Slack:
		v = map[string]string{"text": line}
	case Discord:
		if r := []rune(line); len(r) > 1900 {
			line = string(r[:1900]) + "…"
		}
		v = map[string]string{"content": line}
	}
	b, _ := json.Marshal(v)
	return b
}

// Sign is the X-Dunvex-Signature header of a body: "t=<unix>,v1=<hex>",
// where v1 is HMAC-SHA256 of "<unix>.<body>" with the webhook's secret.
func Sign(secret string, t int64, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(m, "%d.", t)
	m.Write(body)
	return "t=" + strconv.FormatInt(t, 10) + ",v1=" + hex.EncodeToString(m.Sum(nil))
}

// Verify checks a signature header against a body (for receivers written in
// Go, and the tests); it refuses signatures older than tolerance.
func Verify(secret, header string, body []byte, tolerance time.Duration) bool {
	var t int64
	var sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			t, _ = strconv.ParseInt(v, 10, 64)
		case "v1":
			sig = v
		}
	}
	if t == 0 || sig == "" || time.Since(time.Unix(t, 0)) > tolerance {
		return false
	}
	want := Sign(secret, t, body)
	return hmac.Equal([]byte(want), []byte("t="+strconv.FormatInt(t, 10)+",v1="+sig))
}
