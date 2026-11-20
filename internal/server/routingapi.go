package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/routing"
	"github.com/pedramrj/dunvex/internal/warp"
)

type warpView struct {
	Type      string `json:"type"`
	AddressV4 string `json:"addressV4"`
	AddressV6 string `json:"addressV6"`
	Endpoint  string `json:"endpoint"`
	Created   int64  `json:"created"`
	License   bool   `json:"license"` // a WARP+ key is bound
}

type routingView struct {
	Settings     routing.Settings `json:"settings"`
	BlockTorrent bool             `json:"blockTorrent"`
	Warp         *warpView        `json:"warp"`
	Groups       []string         `json:"groups"`
	Geo          bool             `json:"geo"`   // the site and country lists are on the server
	Nodes        int              `json:"nodes"` // other servers (each registers its own WARP device)
}

func (s *Server) warpClient() *warp.Client { return &warp.Client{Base: s.WarpAPI} }

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func (s *Server) routingState() routingView {
	v := routingView{Settings: routing.Load(s.St), BlockTorrent: s.St.GetSetting("block_torrent", "true") == "true"}
	for _, g := range routing.Groups {
		v.Groups = append(v.Groups, g.ID)
	}
	if a := routing.Account(s.St); a != nil {
		v.Warp = &warpView{Type: a.Type, AddressV4: a.AddressV4, AddressV6: a.AddressV6, Endpoint: a.Endpoint, Created: a.Created, License: a.License != ""}
	}
	if s.Mgr != nil {
		v.Geo = fileExists(filepath.Join(s.Mgr.Xray.BinDir, "geosite.dat")) && fileExists(filepath.Join(s.Mgr.Xray.BinDir, "geoip.dat"))
	}
	if nodes, err := s.St.Nodes(); err == nil {
		v.Nodes = len(nodes)
	}
	return v
}

func (s *Server) getRouting(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.routingState())
}

func (s *Server) putRouting(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Settings     routing.Settings `json:"settings"`
		BlockTorrent *bool            `json:"blockTorrent"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	if err := in.Settings.Clean(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if in.Settings.UsesWarp() && routing.Account(s.St) == nil {
		fail(w, 400, "turn WARP on first: the server has no WARP connection yet")
		return
	}
	if _, err := routing.Save(s.St, in.Settings); err != nil {
		fail(w, 500, err.Error())
		return
	}
	if in.BlockTorrent != nil {
		s.St.SetSetting("block_torrent", strconv.FormatBool(*in.BlockTorrent))
	}
	s.St.Log("settings", "%s changed the routing", adminOf(r).Username)
	s.Mgr.RequestApply()
	writeJSON(w, 200, s.routingState())
}

// registerWarp creates the server's WARP device (once).
func (s *Server) registerWarp(w http.ResponseWriter, r *http.Request) {
	if routing.Account(s.St) == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		a, err := s.warpClient().Register(ctx)
		if err != nil {
			fail(w, 502, err.Error())
			return
		}
		if err := routing.SaveAccount(s.St, a); err != nil {
			fail(w, 500, err.Error())
			return
		}
		s.St.Log("settings", "%s turned WARP on (%s)", adminOf(r).Username, a.AddressV4)
		s.Mgr.RequestApply()
	}
	writeJSON(w, 200, s.routingState())
}

// removeWarp stops using WARP and removes the device from Cloudflare.
func (s *Server) removeWarp(w http.ResponseWriter, r *http.Request) {
	set := routing.Load(s.St)
	if set.UsesWarp() {
		set.Warp = routing.WarpOff
		if set.Iran == routing.IranWarp {
			set.Iran = routing.IranDirect
		}
		if _, err := routing.Save(s.St, set); err != nil {
			fail(w, 500, err.Error())
			return
		}
	}
	if a := routing.Account(s.St); a != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		s.warpClient().Delete(ctx, a) // best effort: the device is forgotten either way
		cancel()
	}
	routing.SaveAccount(s.St, nil)
	s.St.Log("settings", "%s turned WARP off", adminOf(r).Username)
	s.Mgr.RequestApply()
	writeJSON(w, 200, s.routingState())
}

// warpLicense binds a WARP+ key from the 1.1.1.1 app.
func (s *Server) warpLicense(w http.ResponseWriter, r *http.Request) {
	var in struct{ License string }
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.License) == "" {
		fail(w, 400, "paste the WARP+ key from the 1.1.1.1 app")
		return
	}
	a := routing.Account(s.St)
	if a == nil {
		fail(w, 400, "turn WARP on first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.warpClient().SetLicense(ctx, a, in.License); err != nil {
		fail(w, 502, err.Error())
		return
	}
	routing.SaveAccount(s.St, a)
	s.St.Log("settings", "%s added a WARP+ key (%s)", adminOf(r).Username, a.Type)
	writeJSON(w, 200, s.routingState())
}

// checkWarp fetches Cloudflare's trace page through the WARP outbound and
// reports the address the internet sees.
func (s *Server) checkWarp(w http.ResponseWriter, r *http.Request) {
	if routing.Account(s.St) == nil {
		fail(w, 400, "turn WARP on first")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	via, err := warp.Trace(ctx, "127.0.0.1:"+strconv.Itoa(s.Mgr.WarpTestPort))
	if err != nil {
		fail(w, 502, "WARP did not answer: "+err.Error())
		return
	}
	direct, _ := warp.Trace(ctx, "")
	writeJSON(w, 200, map[string]any{"warp": via, "direct": direct})
}
