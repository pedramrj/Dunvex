package server

import (
	"net/http"
	"time"

	"github.com/pedramrj/dunvex/internal/bot"
	"github.com/pedramrj/dunvex/internal/store"
)

// Free trials the bot gives (see bot/trial.go): settings, numbers and the
// latest ones. Owner only.

type trialView struct {
	store.Trial
	Status string `json:"status"` // the trial user's state now ("deleted" once removed)
}

func (s *Server) getTrial(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Unix()
	total, day, bought := s.St.TrialStats(now - 86400)
	active := 0
	list, _ := s.St.Trials(0)
	recent := []trialView{}
	for i, t := range list {
		v := trialView{Trial: t, Status: "deleted"}
		if u, err := s.St.UserByID(t.UserID); err == nil && t.UserID > 0 {
			v.Status = u.Status(now)
			if v.Status == store.StatusActive || v.Status == store.StatusPending {
				active++
			}
		}
		if i < 20 {
			recent = append(recent, v)
		}
	}
	out := map[string]any{"settings": bot.LoadTrial(s.St), "recent": recent,
		"stats": map[string]int{"total": total, "day": day, "bought": bought, "active": active}}
	if s.Bot != nil {
		st := s.Bot.Status()
		out["bot"] = map[string]any{"configured": st["configured"], "username": st["username"]}
	}
	writeJSON(w, 200, out)
}

func (s *Server) putTrial(w http.ResponseWriter, r *http.Request) {
	var in bot.TrialSettings
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	if err := in.Check(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if in.Inbounds == nil {
		in.Inbounds = []int64{}
	}
	bot.SaveTrial(s.St, in)
	s.St.Log("settings", "%s changed the free trial (%s)", adminOf(r).Username, map[bool]string{true: "on", false: "off"}[in.On])
	s.getTrial(w, r)
}
