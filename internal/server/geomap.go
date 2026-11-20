package server

import (
	"net"
	"net/http"
	"path/filepath"
	"sort"

	"github.com/pedramrj/dunvex/internal/coremgr"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/geoip"
)

// geoData is the offline country data: the full file from the Dunvex release
// first, Xray's geoip.dat (fewer countries) when that is missing.
func (s *Server) geoData() *geoip.Loader {
	s.geoOnce.Do(func() {
		s.geo2 = &geoip.Loader{Paths: []string{
			filepath.Join(s.Home, coremgr.GeoIPFile),
			filepath.Join(s.Home, "bin", "geoip.dat"),
		}}
	})
	return s.geo2
}

// clientCountry is one origin of the map's arcs.
type clientCountry struct {
	CC      string  `json:"cc"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Devices int     `json:"devices"` // user + address pairs seen in the window
	Users   int     `json:"users"`
}

// clientMap is what the map draws: arcs by country, never addresses.
type clientMap struct {
	Countries []clientCountry `json:"countries"`
	Unplaced  int             `json:"unplaced"` // seen, but no country (private, unknown or missing data)
	Devices   int             `json:"devices"`
	Users     int             `json:"users"`
	Window    int             `json:"window"`
	Source    string          `json:"source"`
	Built     int64           `json:"built"`
	Missing   string          `json:"missing,omitempty"`
}

// clients groups the addresses users connected from in the last 5 minutes by
// country. Lookups use only the data on this server; no address leaves it, and
// the answer carries counts, never addresses.
func (s *Server) clients(w http.ResponseWriter, r *http.Request) {
	db, err := s.geoData().Get()
	out := groupClients(s.Mgr.AllDevices(), db)
	if err != nil {
		out.Missing = err.Error()
	}
	writeJSON(w, 200, out)
}

func groupClients(seen []engine.Seen, db *geoip.DB) clientMap {
	out := clientMap{Countries: []clientCountry{}, Window: 300}
	if db != nil {
		out.Source = db.Source
		if !db.Built.IsZero() {
			out.Built = db.Built.Unix()
		}
	}
	type acc struct {
		devices int
		users   map[string]bool
	}
	by := map[string]*acc{}
	allUsers := map[string]bool{}
	for _, d := range seen {
		out.Devices++
		allUsers[d.User] = true
		ip := net.ParseIP(d.IP)
		cc := ""
		if db != nil && ip != nil && !ip.IsPrivate() && !ip.IsLoopback() {
			cc = db.Lookup(ip)
		}
		if _, _, ok := geoip.Point(cc); !ok {
			out.Unplaced++
			continue
		}
		a := by[cc]
		if a == nil {
			a = &acc{users: map[string]bool{}}
			by[cc] = a
		}
		a.devices++
		a.users[d.User] = true
	}
	out.Users = len(allUsers)
	for cc, a := range by {
		lat, lon, _ := geoip.Point(cc)
		out.Countries = append(out.Countries, clientCountry{CC: cc, Lat: lat, Lon: lon, Devices: a.devices, Users: len(a.users)})
	}
	sort.Slice(out.Countries, func(i, j int) bool {
		if out.Countries[i].Devices != out.Countries[j].Devices {
			return out.Countries[i].Devices > out.Countries[j].Devices
		}
		return out.Countries[i].CC < out.Countries[j].CC
	})
	return out
}
