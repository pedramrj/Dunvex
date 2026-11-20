// Package routing holds the server's routing choices: what is blocked, how
// Iranian sites are handled, and what leaves through Cloudflare WARP.
package routing

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/warp"
)

// Setting keys.
const (
	Key        = "routing"
	AccountKey = "warp_account"
)

// What Iranian sites do on the server.
const (
	IranDirect = "direct" // through the server's own address
	IranBlock  = "block"
	IranWarp   = "warp"
)

// What goes through WARP.
const (
	WarpOff   = "off"
	WarpSites = "sites"
	WarpAll   = "all"
)

// Settings are the routing choices.
type Settings struct {
	BlockAds      bool     `json:"blockAds"`
	BlockAdult    bool     `json:"blockAdult"`
	Iran          string   `json:"iran"`
	ClientIran    bool     `json:"clientIran"` // subscriptions send Iranian sites straight from the device
	Warp          string   `json:"warp"`
	WarpGroups    []string `json:"warpGroups"`
	WarpDomains   []string `json:"warpDomains"`
	BlockDomains  []string `json:"blockDomains"`
	DirectDomains []string `json:"directDomains"` // never through WARP
}

// Group is a list of sites that can go through WARP together.
type Group struct {
	ID   string `json:"id"`
	Site string `json:"-"` // the geosite code
}

// Groups are the site lists offered for WARP, in display order.
var Groups = []Group{
	{"ai", "category-ai-!cn"}, // ChatGPT, Gemini, Claude, Copilot…
	{"google", "google"},
	{"youtube", "youtube"},
	{"netflix", "netflix"},
	{"disney", "disney"},
	{"spotify", "spotify"},
	{"meta", "meta"},
	{"x", "x"},
}

// Site lists used by the rules.
const (
	SiteIran  = "category-ir"
	SiteAds   = "category-ads-all"
	SiteAdult = "category-porn"
	IPIran    = "ir"
)

// GroupSite is the geosite code of a group id.
func GroupSite(id string) string {
	for _, g := range Groups {
		if g.ID == id {
			return g.Site
		}
	}
	return ""
}

// Defaults: everything direct, Iranian sites direct on the devices too.
func Defaults() Settings {
	return Settings{Iran: IranDirect, ClientIran: true, Warp: WarpOff}
}

// Load reads the settings (defaults when none were saved).
func Load(st *store.Store) Settings {
	s := Defaults()
	if raw := st.GetSetting(Key, ""); raw != "" {
		json.Unmarshal([]byte(raw), &s)
	}
	if s.Iran == "" {
		s.Iran = IranDirect
	}
	if s.Warp == "" {
		s.Warp = WarpOff
	}
	return s
}

// Account is the server's WARP device, nil when none is registered.
func Account(st *store.Store) *warp.Account {
	raw := st.GetSetting(AccountKey, "")
	if raw == "" {
		return nil
	}
	var a warp.Account
	if json.Unmarshal([]byte(raw), &a) != nil || a.PrivateKey == "" || a.PeerKey == "" {
		return nil
	}
	return &a
}

// SaveAccount stores (or, with nil, forgets) the WARP device.
func SaveAccount(st *store.Store, a *warp.Account) error {
	if a == nil {
		return st.SetSetting(AccountKey, "")
	}
	b, _ := json.Marshal(a)
	return st.SetSetting(AccountKey, string(b))
}

// UsesWarp reports whether any traffic is sent through WARP.
func (s Settings) UsesWarp() bool { return s.Warp != WarpOff || s.Iran == IranWarp }

// Active reports whether the server applies rules beyond the basic ones
// (private addresses and BitTorrent), so every protocol should follow them.
func (s Settings) Active() bool {
	return s.BlockAds || s.BlockAdult || s.Iran != IranDirect || s.UsesWarp() || len(s.BlockDomains) > 0
}

// WarpSiteCodes are the geosite codes sent through WARP.
func (s Settings) WarpSiteCodes() []string {
	if s.Warp != WarpSites {
		return nil
	}
	var out []string
	for _, id := range s.WarpGroups {
		if c := GroupSite(id); c != "" {
			out = append(out, c)
		}
	}
	return out
}

var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// CleanDomains turns what people paste (URLs, *.example.com, one per line or
// comma) into bare domain names.
func CleanDomains(list []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range list {
		for _, d := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' || r == '\t' || r == '،' }) {
			d = strings.ToLower(strings.TrimSpace(d))
			if strings.Contains(d, "://") {
				if u, err := url.Parse(d); err == nil {
					d = u.Hostname()
				}
			}
			d = strings.TrimPrefix(strings.TrimPrefix(d, "*."), ".")
			if i := strings.IndexAny(d, "/:"); i >= 0 {
				d = d[:i]
			}
			if d == "" || seen[d] {
				continue
			}
			if !domainRe.MatchString(d) || len(d) > 253 {
				return nil, fmt.Errorf("%q is not a domain name", d)
			}
			seen[d] = true
			out = append(out, d)
		}
	}
	if len(out) > 500 {
		return nil, fmt.Errorf("at most 500 domains per list")
	}
	return out, nil
}

// Clean checks and normalises settings before they are saved.
func (s *Settings) Clean() error {
	switch s.Iran {
	case IranDirect, IranBlock, IranWarp:
	case "":
		s.Iran = IranDirect
	default:
		return fmt.Errorf("unknown choice for Iranian sites: %s", s.Iran)
	}
	switch s.Warp {
	case WarpOff, WarpSites, WarpAll:
	case "":
		s.Warp = WarpOff
	default:
		return fmt.Errorf("unknown WARP choice: %s", s.Warp)
	}
	var groups []string
	for _, g := range s.WarpGroups {
		if GroupSite(g) == "" {
			return fmt.Errorf("unknown site group: %s", g)
		}
		groups = append(groups, g)
	}
	s.WarpGroups = groups
	var err error
	if s.WarpDomains, err = CleanDomains(s.WarpDomains); err != nil {
		return err
	}
	if s.BlockDomains, err = CleanDomains(s.BlockDomains); err != nil {
		return err
	}
	if s.DirectDomains, err = CleanDomains(s.DirectDomains); err != nil {
		return err
	}
	if s.Warp == WarpSites && len(s.WarpGroups) == 0 && len(s.WarpDomains) == 0 {
		return fmt.Errorf("choose at least one site or domain for WARP, or send everything through it")
	}
	return nil
}

// Save checks and stores the settings.
func Save(st *store.Store, s Settings) (Settings, error) {
	if err := s.Clean(); err != nil {
		return s, err
	}
	b, _ := json.Marshal(s)
	return s, st.SetSetting(Key, string(b))
}
