package server

import (
	"net/http"
	"sync"

	"github.com/pedramrj/dunvex/internal/webhook"
)

// events counts every logged event by kind (for the metrics) and hands it to
// the webhooks.
type eventCounter struct {
	mu sync.Mutex
	n  map[string]int64
}

// OnEvent is called for every logged event (store.OnLog).
func (s *Server) OnEvent(kind, msg string) {
	s.evCount.mu.Lock()
	if s.evCount.n == nil {
		s.evCount.n = map[string]int64{}
	}
	s.evCount.n[kind]++
	s.evCount.mu.Unlock()
	if s.Hooks != nil {
		s.Hooks.Send(kind, msg)
	}
}

func (s *Server) eventCounts() map[string]int64 {
	s.evCount.mu.Lock()
	defer s.evCount.mu.Unlock()
	out := map[string]int64{}
	for k, v := range s.evCount.n {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------- API

type hookView struct {
	webhook.Hook
	Last *webhook.Delivery `json:"last,omitempty"`
}

func (s *Server) hookViews() []hookView {
	out := []hookView{}
	for _, h := range webhook.Load(s.St) {
		v := hookView{Hook: h}
		if s.Hooks != nil {
			if l := s.Hooks.Log(h.ID); len(l) > 0 {
				v.Last = &l[0]
			}
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) listHooks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"hooks": s.hookViews(), "kinds": webhook.Kinds})
}

// findHook returns the hooks and the index of the one in the path.
func (s *Server) findHook(w http.ResponseWriter, r *http.Request) ([]webhook.Hook, int) {
	hooks := webhook.Load(s.St)
	for i, h := range hooks {
		if h.ID == r.PathValue("id") {
			return hooks, i
		}
	}
	fail(w, 404, "webhook not found")
	return nil, -1
}

func (s *Server) createHook(w http.ResponseWriter, r *http.Request) {
	var h webhook.Hook
	if err := readJSON(r, &h); err != nil {
		fail(w, 400, "bad request")
		return
	}
	h.ID, h.Secret = "", ""
	if err := h.Clean(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	hooks := append(webhook.Load(s.St), h)
	if err := webhook.Save(s.St, hooks); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.St.Log("settings", "%s added the webhook %s", adminOf(r).Username, h.Name)
	writeJSON(w, 200, map[string]any{"hook": h, "hooks": s.hookViews()})
}

func (s *Server) updateHook(w http.ResponseWriter, r *http.Request) {
	hooks, i := s.findHook(w, r)
	if i < 0 {
		return
	}
	var in struct {
		Name    *string   `json:"name"`
		URL     *string   `json:"url"`
		Format  *string   `json:"format"`
		Kinds   *[]string `json:"kinds"`
		Enabled *bool     `json:"enabled"`
		Rotate  bool      `json:"rotateSecret"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	h := hooks[i]
	if in.Name != nil {
		h.Name = *in.Name
	}
	if in.URL != nil {
		h.URL = *in.URL
	}
	if in.Format != nil {
		h.Format = *in.Format
	}
	if in.Kinds != nil {
		h.Kinds = *in.Kinds
	}
	if in.Enabled != nil {
		h.Enabled = *in.Enabled
	}
	if in.Rotate {
		h.Secret = webhook.NewSecret()
	}
	if err := h.Clean(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	hooks[i] = h
	if err := webhook.Save(s.St, hooks); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"hook": h, "hooks": s.hookViews()})
}

func (s *Server) deleteHook(w http.ResponseWriter, r *http.Request) {
	hooks, i := s.findHook(w, r)
	if i < 0 {
		return
	}
	h := hooks[i]
	hooks = append(hooks[:i], hooks[i+1:]...)
	if err := webhook.Save(s.St, hooks); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if s.Hooks != nil {
		s.Hooks.Forget(h.ID)
	}
	s.St.Log("settings", "%s removed the webhook %s", adminOf(r).Username, h.Name)
	writeJSON(w, 200, map[string]any{"hooks": s.hookViews()})
}

func (s *Server) testHook(w http.ResponseWriter, r *http.Request) {
	hooks, i := s.findHook(w, r)
	if i < 0 || s.Hooks == nil {
		return
	}
	writeJSON(w, 200, s.Hooks.Test(hooks[i]))
}

func (s *Server) hookDeliveries(w http.ResponseWriter, r *http.Request) {
	hooks, i := s.findHook(w, r)
	if i < 0 {
		return
	}
	out := []webhook.Delivery{}
	if s.Hooks != nil {
		out = s.Hooks.Log(hooks[i].ID)
	}
	writeJSON(w, 200, out)
}
