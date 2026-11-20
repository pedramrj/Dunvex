package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/pedramrj/dunvex/internal/geodata"
	"github.com/pedramrj/dunvex/internal/routing"
)

// iranLists are the Iranian sites and addresses that subscriptions send
// straight from the device instead of through the VPN.
type iranLists struct {
	suffix, full, keyword, regex []string
	cidr4                        []string // IPv4 only: keeps the profile small
}

var iranCache struct {
	sync.Mutex
	key   string
	lists *iranLists
}

// iranDirect returns the lists when subscriptions should route Iranian sites
// directly (nil when the owner turned that off).
func (s *Server) iranDirect() *iranLists {
	if !routing.Load(s.St).ClientIran {
		return nil
	}
	dir := ""
	if s.Mgr != nil {
		dir = s.Mgr.Xray.BinDir
	}
	site, ip := filepath.Join(dir, "geosite.dat"), filepath.Join(dir, "geoip.dat")
	key := ""
	for _, p := range []string{site, ip} {
		if st, err := os.Stat(p); err == nil {
			key += fmt.Sprintf("%d:%d;", st.Size(), st.ModTime().UnixNano())
		}
	}
	iranCache.Lock()
	defer iranCache.Unlock()
	if iranCache.lists != nil && iranCache.key == key {
		return iranCache.lists
	}
	l := &iranLists{suffix: []string{"ir"}} // the whole .ir domain, even without the lists
	if dir != "" {
		if ds, err := geodata.Site(site, routing.SiteIran); err == nil {
			for _, d := range ds {
				switch d.Kind {
				case geodata.Suffix:
					l.suffix = append(l.suffix, d.Value)
				case geodata.Full:
					l.full = append(l.full, d.Value)
				case geodata.Keyword:
					l.keyword = append(l.keyword, d.Value)
				case geodata.Regex:
					l.regex = append(l.regex, d.Value)
				}
			}
		}
		if nets, err := geodata.IP(ip, routing.IPIran); err == nil {
			for _, n := range nets {
				if n.IP.To4() != nil && len(n.IP) == 4 {
					l.cidr4 = append(l.cidr4, n.String())
				}
			}
		}
	}
	iranCache.key, iranCache.lists = key, l
	return l
}

// singboxDomains is the domain part as a sing-box rule.
func (l *iranLists) singboxDomains() obj {
	r := obj{"domain_suffix": l.suffix}
	if len(l.full) > 0 {
		r["domain"] = l.full
	}
	if len(l.keyword) > 0 {
		r["domain_keyword"] = l.keyword
	}
	if len(l.regex) > 0 {
		r["domain_regex"] = l.regex
	}
	return r
}

// clashRules are Clash Meta rule lines that send Iranian sites direct.
func (l *iranLists) clashRules() []string {
	var out []string
	for _, d := range l.suffix {
		out = append(out, "DOMAIN-SUFFIX,"+d+",DIRECT")
	}
	for _, d := range l.full {
		out = append(out, "DOMAIN,"+d+",DIRECT")
	}
	for _, d := range l.keyword {
		out = append(out, "DOMAIN-KEYWORD,"+d+",DIRECT")
	}
	for _, d := range l.regex {
		out = append(out, "DOMAIN-REGEX,"+d+",DIRECT")
	}
	return append(out, "GEOIP,IR,DIRECT")
}
