package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/store"
)

func TestClean(t *testing.T) {
	h := Hook{URL: " https://hooks.example.com/dunvex ", Kinds: []string{"user", "core", "user"}}
	if err := h.Clean(); err != nil {
		t.Fatal(err)
	}
	if h.Name != "hooks.example.com" || h.Format != JSON || len(h.Kinds) != 2 || h.ID == "" || !strings.HasPrefix(h.Secret, "whsec_") {
		t.Fatalf("%+v", h)
	}
	for _, bad := range []Hook{{URL: "ftp://x.com"}, {URL: "not a url"}, {URL: "https://x.com", Format: "xml"}, {URL: "https://x.com", Kinds: []string{"nope"}}} {
		if err := bad.Clean(); err == nil {
			t.Errorf("%+v passed", bad)
		}
	}
	if !(Hook{Enabled: true}).Wants("core") || (Hook{Enabled: true, Kinds: []string{"user"}}).Wants("core") || (Hook{}).Wants("user") {
		t.Fatal("wants")
	}
}

func TestSignature(t *testing.T) {
	body := []byte(`{"a":1}`)
	h := Sign("whsec_x", time.Now().Unix(), body)
	if !Verify("whsec_x", h, body, time.Minute) || Verify("whsec_y", h, body, time.Minute) || Verify("whsec_x", h, []byte(`{"a":2}`), time.Minute) {
		t.Fatal("verify")
	}
	if Verify("whsec_x", Sign("whsec_x", time.Now().Add(-time.Hour).Unix(), body), body, time.Minute) {
		t.Fatal("an old signature passed")
	}
}

type hit struct {
	header http.Header
	body   []byte
}

func TestSend(t *testing.T) {
	var mu sync.Mutex
	var hits []hit
	fails := 2 // the JSON receiver answers 503 twice before it takes the event
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		hits = append(hits, hit{r.Header.Clone(), b})
		switch r.URL.Path {
		case "/json":
			if fails > 0 {
				fails--
				w.WriteHeader(503)
				return
			}
		case "/gone":
			w.WriteHeader(410)
			return
		}
		w.WriteHeader(204)
	}))
	defer recv.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hooks := []Hook{
		{Name: "app", URL: recv.URL + "/json", Enabled: true, Kinds: []string{"core"}},
		{Name: "chat", URL: recv.URL + "/slack", Format: Slack, Enabled: true},
		{Name: "gone", URL: recv.URL + "/gone", Format: Discord, Enabled: true},
		{Name: "off", URL: recv.URL + "/off", Enabled: false},
	}
	for i := range hooks {
		if err := hooks[i].Clean(); err != nil {
			t.Fatal(err)
		}
	}
	Save(st, hooks)
	s := &Sender{St: st, Panel: func() string { return "Dunvex Test" }, Version: "v9", Backoff: []time.Duration{50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond}}

	s.Send("user", "sara is now limited and was disconnected") // not for "app"
	s.Send("core", "xray: port 443 is busy")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.Log(hooks[0].ID)) == 3 && len(s.Log(hooks[1].ID)) == 2 && len(s.Log(hooks[2].ID)) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// the JSON hook: two 503s, then delivered, signed, only the core event
	l := s.Log(hooks[0].ID)
	if len(l) != 3 || !l[0].OK || l[0].Attempt != 3 || l[1].OK || l[1].Status != 503 || l[1].Retry == 0 {
		t.Fatalf("json log: %+v", l)
	}
	mu.Lock()
	var got []hit
	for _, h := range hits {
		if h.header.Get("X-Dunvex-Event") != "" {
			got = append(got, h)
		}
	}
	mu.Unlock()
	last := got[len(got)-1]
	var ev Event
	json.Unmarshal(last.body, &ev)
	if ev.Type != "core" || ev.Panel != "Dunvex Test" || ev.Version != "v9" || ev.Message != "xray: port 443 is busy" || last.header.Get("X-Dunvex-Delivery") != ev.ID {
		t.Fatalf("event: %+v %v", ev, last.header)
	}
	if !Verify(hooks[0].Secret, last.header.Get("X-Dunvex-Signature"), last.body, time.Minute) {
		t.Fatal("bad signature")
	}
	// the Slack hook gets both, as text, unsigned
	mu.Lock()
	var slack []string
	for _, h := range hits {
		if strings.Contains(string(h.body), `"text"`) {
			slack = append(slack, string(h.body))
			if h.header.Get("X-Dunvex-Signature") != "" {
				t.Error("chat messages are not signed")
			}
		}
	}
	mu.Unlock()
	if len(slack) != 2 || !strings.Contains(slack[0]+slack[1], "Dunvex Test · core: xray: port 443 is busy") {
		t.Fatalf("slack: %v", slack)
	}
	// a 410 is final: no retry
	if l := s.Log(hooks[2].ID); len(l) != 2 || l[0].OK || l[0].Status != 410 || l[0].Retry != 0 {
		t.Fatalf("gone: %+v", l)
	}
	if len(s.Log(hooks[3].ID)) != 0 {
		t.Fatal("a disabled hook was called")
	}
	ok, failed := s.Counts()
	if ok != 3 || failed != 4 {
		t.Fatalf("counts %d %d", ok, failed)
	}
	// a test is sent at once and never retried
	d := s.Test(hooks[2])
	if d.OK || d.Event != "test" || d.Retry != 0 {
		t.Fatalf("test: %+v", d)
	}
	if b := Body(Discord, Event{Panel: "P", Type: "user", Message: strings.Repeat("x", 3000)}); len(b) > 2100 || !strings.Contains(string(b), `"content"`) {
		t.Fatalf("discord body: %d", len(b))
	}
}
