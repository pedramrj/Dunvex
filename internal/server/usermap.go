package server

// Where users connect from: the city and network of each address a user was
// seen at, and which server (this one or a node) saw it. Everything is looked
// up on this server from the offline data files; no address leaves it. The
// map answers with places and user names, never addresses; the user detail
// (for admins who may manage that user) also carries the addresses.

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/coremgr"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/geoip"
	"github.com/pedramrj/dunvex/internal/store"
)

// cityData is the optional city and network file.
func (s *Server) cityData() *geoip.CityLoader {
	s.cityOnce.Do(func() {
		s.city = &geoip.CityLoader{Path: filepath.Join(s.Home, coremgr.GeoCityFile)}
	})
	return s.city
}

// where is what the server knows about one address.
type where struct {
	Place   *geoip.Place   `json:"place,omitempty"`
	Net     *geoip.Network `json:"net,omitempty"`
	Precise bool           `json:"precise"` // a city, not just a country centre
}

// locate finds an address: the city file first, the country file after.
func (s *Server) locate(ip net.IP) where {
	var w where
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() {
		return w
	}
	if db, _ := s.cityData().Get(); db != nil {
		if p, ok := db.Place(ip); ok {
			w.Place, w.Precise = &p, true
		}
		if n, ok := db.Network(ip); ok {
			w.Net = &n
		}
	}
	if w.Place == nil {
		if db, _ := s.geoData().Get(); db != nil {
			cc := db.Lookup(ip)
			if lat, lon, ok := geoip.Point(cc); ok {
				w.Place = &geoip.Place{CC: cc, Lat: lat, Lon: lon}
			}
		}
	}
	return w
}

// mapServer is a pin on the map: this server, a node or a relay.
type mapServer struct {
	ID      int64        `json:"id"` // 0 is this server
	Name    string       `json:"name"`
	Role    string       `json:"role"` // server or relay
	Target  int64        `json:"target,omitempty"`
	Online  bool         `json:"online"`
	Place   *geoip.Place `json:"place,omitempty"`
	Network string       `json:"network,omitempty"`
	ip      string
}

// servers lists this server and every node with where they are. Node
// addresses may be names; they are resolved with a short timeout and cached.
func (s *Server) mapServers() []mapServer {
	s.srvMu.Lock()
	defer s.srvMu.Unlock()
	if time.Since(s.srvAt) < 2*time.Minute && s.srvCache != nil {
		return s.srvCache
	}
	name := s.Sys.Get().Hostname
	if name == "" {
		name = "Dunvex"
	}
	out := []mapServer{{ID: 0, Name: name, Role: "server", Online: true, ip: s.Sys.PublicIP()}}
	nodes, _ := s.St.Nodes()
	now := time.Now().Unix()
	for _, n := range nodes {
		role := n.Role
		if role == "" {
			role = "server"
		}
		m := mapServer{ID: n.ID, Name: n.Name, Role: role, Target: n.RelayTarget, Online: n.Enabled && now-n.LastSeen < 180}
		host := n.Address
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if ip := net.ParseIP(host); ip != nil {
			m.ip = ip.String()
		} else if host != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if ips, err := net.DefaultResolver.LookupIPAddr(ctx, host); err == nil && len(ips) > 0 {
				m.ip = ips[0].IP.String()
			}
			cancel()
		}
		out = append(out, m)
	}
	for i := range out {
		if w := s.locate(net.ParseIP(out[i].ip)); w.Place != nil {
			out[i].Place = w.Place
			if w.Net != nil {
				out[i].Network = w.Net.Org
			}
		}
	}
	s.srvCache, s.srvAt = out, time.Now()
	return out
}

// relayOf returns the relay whose address ip is: users behind a relay reach
// the server from the relay's address, so their own place is not known.
func relayOf(servers []mapServer, ip string) int64 {
	for _, m := range servers {
		if m.Role == "relay" && m.ip != "" && m.ip == ip {
			return m.ID
		}
	}
	return 0
}

// mapDevice is one user at one place.
type mapDevice struct {
	User   string  `json:"user"`
	UserID int64   `json:"uid"`
	Via    []int64 `json:"via"`
	Last   int64   `json:"last"`
	ASN    uint32  `json:"asn,omitempty"`
	Org    string  `json:"org,omitempty"`
}

type mapPlace struct {
	geoip.Place
	Precise bool        `json:"precise"`
	Devices []mapDevice `json:"devices"`
}

type userMap struct {
	Servers  []mapServer `json:"servers"`
	Places   []mapPlace  `json:"places"`
	Devices  int         `json:"devices"`
	Users    int         `json:"users"`
	Relayed  int         `json:"relayed"`  // behind a relay: the place is the relay's
	Unplaced int         `json:"unplaced"` // seen, but no place (private or unknown)
	City     bool        `json:"city"`     // the city file is loaded
	Source   string      `json:"source,omitempty"`
	Window   int         `json:"window"`
}

// usersMap places every address a user (that the admin may see) connected
// from in the last 5 minutes, grouped by place, with the servers they reach.
func (s *Server) usersMap(w http.ResponseWriter, r *http.Request) {
	users, err := s.scopedUsers(adminOf(r))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	ids := map[string]int64{}
	for _, u := range users {
		ids[u.Name] = u.ID
	}
	servers := s.mapServers()
	out := userMap{Servers: servers, Places: []mapPlace{}, Window: 300}
	if db, _ := s.cityData().Get(); db != nil {
		out.City, out.Source = true, db.Source
	} else if db, _ := s.geoData().Get(); db != nil {
		out.Source = db.Source
	}
	byKey := map[string]*mapPlace{}
	seenUsers := map[string]bool{}
	for _, d := range s.Mgr.AllDevices() {
		uid, ok := ids[d.User]
		if !ok {
			continue
		}
		out.Devices++
		seenUsers[d.User] = true
		dev := mapDevice{User: d.User, UserID: uid, Via: d.Via, Last: d.LastSeen}
		if dev.Via == nil {
			dev.Via = []int64{}
		}
		if rid := relayOf(servers, d.IP); rid != 0 {
			out.Relayed++
			dev.Via = append(dev.Via, rid)
			continue
		}
		loc := s.locate(net.ParseIP(d.IP))
		if loc.Net != nil {
			dev.ASN, dev.Org = loc.Net.ASN, loc.Net.Org
		}
		if loc.Place == nil {
			out.Unplaced++
			continue
		}
		key := loc.Place.CC + "|" + loc.Place.Region + "|" + loc.Place.City
		p := byKey[key]
		if p == nil {
			p = &mapPlace{Place: *loc.Place, Precise: loc.Precise}
			byKey[key] = p
		}
		p.Devices = append(p.Devices, dev)
	}
	out.Users = len(seenUsers)
	for _, p := range byKey {
		sort.Slice(p.Devices, func(i, j int) bool { return p.Devices[i].User < p.Devices[j].User })
		out.Places = append(out.Places, *p)
	}
	sort.Slice(out.Places, func(i, j int) bool {
		if len(out.Places[i].Devices) != len(out.Places[j].Devices) {
			return len(out.Places[i].Devices) > len(out.Places[j].Devices)
		}
		return out.Places[i].City < out.Places[j].City
	})
	writeJSON(w, 200, out)
}

// deviceView is one address of a user, with where it is and what it reached.
type deviceView struct {
	engine.Device
	where
	Relay   int64    `json:"relay,omitempty"`
	Servers []string `json:"servers"`
}

// deviceViews enriches a user's addresses for the user detail.
func (s *Server) deviceViews(u *store.User) []deviceView {
	list := s.Mgr.Devices(u.Name)
	out := make([]deviceView, 0, len(list))
	if len(list) == 0 {
		return out
	}
	servers := s.mapServers()
	names := map[int64]string{}
	for _, m := range servers {
		names[m.ID] = m.Name
	}
	for _, d := range list {
		v := deviceView{Device: d, Servers: []string{}}
		if v.Via == nil {
			v.Via = []int64{}
		}
		for _, id := range d.Via {
			if n, ok := names[id]; ok {
				v.Servers = append(v.Servers, n)
			}
		}
		if rid := relayOf(servers, d.IP); rid != 0 {
			v.Relay = rid
			v.Servers = append(v.Servers, names[rid])
		} else {
			v.where = s.locate(net.ParseIP(d.IP))
		}
		out = append(out, v)
	}
	return out
}

// userLoc is the list's one-line answer to "where is this user": the place of
// the most recent address and how many places in the window.
type userLoc struct {
	City   string `json:"city,omitempty"`
	CC     string `json:"cc,omitempty"`
	Places int    `json:"places"`
	Org    string `json:"org,omitempty"`
}

// userLocs places every user seen in the window, by name.
func (s *Server) userLocs() map[string]*userLoc {
	type last struct {
		at int64
		ip string
	}
	latest := map[string]last{}
	places := map[string]map[string]bool{}
	var mu sync.Mutex
	for _, d := range s.Mgr.AllDevices() {
		loc := s.locate(net.ParseIP(d.IP))
		key := "?"
		if loc.Place != nil {
			key = loc.Place.CC + "|" + loc.Place.City
		}
		mu.Lock()
		if places[d.User] == nil {
			places[d.User] = map[string]bool{}
		}
		places[d.User][key] = true
		if d.LastSeen > latest[d.User].at {
			latest[d.User] = last{d.LastSeen, d.IP}
		}
		mu.Unlock()
	}
	out := map[string]*userLoc{}
	for name, l := range latest {
		loc := s.locate(net.ParseIP(l.ip))
		v := &userLoc{Places: len(places[name])}
		if loc.Place != nil {
			v.City, v.CC = loc.Place.City, loc.Place.CC
		}
		if loc.Net != nil {
			v.Org = loc.Net.Org
		}
		out[name] = v
	}
	return out
}

// maskIP keeps the network part of an address, for the user's own page.
func maskIP(ip string) string {
	a := net.ParseIP(ip)
	if a == nil {
		return ""
	}
	if v4 := a.To4(); v4 != nil {
		p := strings.Split(v4.String(), ".")
		return p[0] + "." + p[1] + ".×.×"
	}
	p := strings.Split(a.String(), ":")
	if len(p) > 2 {
		return p[0] + ":" + p[1] + ":…"
	}
	return "…"
}
