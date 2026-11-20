package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/geodata"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/routing"
	"github.com/pedramrj/dunvex/internal/warp"
)

const (
	warpTag = "warp"
	// RouterPort is Xray's loopback SOCKS inbound that Mieru and TrustTunnel
	// send their traffic through, so the routing rules apply to them too.
	RouterPort = 62795
	// WarpTestPort is a loopback SOCKS inbound that always leaves through WARP:
	// the panel checks WARP through it.
	WarpTestPort = 62796
)

// routePlan is one generation's routing: the choices, the WARP device and
// where the site lists are.
type routePlan struct {
	set      routing.Settings
	acct     *warp.Account // nil: no WARP, its rules fall back to direct
	dat      string        // directory with geosite.dat and geoip.dat, "" when missing
	router   int           // port of the routing inbound
	warpTest int           // port of the WARP check inbound
	usque    int           // port of the MASQUE SOCKS proxy (usque)
	serves   bool          // this server runs protocols the routing applies to
}

func (m *Manager) routePlan() *routePlan {
	rp := &routePlan{set: routing.Load(m.Store), router: m.RouterPort, warpTest: m.WarpTestPort, usque: m.Usque.APIPort}
	if fileExists(filepath.Join(m.Xray.BinDir, "geosite.dat")) && fileExists(filepath.Join(m.Xray.BinDir, "geoip.dat")) {
		rp.dat = m.Xray.BinDir
	}
	// a registered device is always reachable through the check port, even
	// before any traffic is sent to it. A server without such protocols (a
	// relay, or one still empty) never registers WARP or installs usque.
	rp.serves = m.servesProtocols()
	rp.acct = routing.Account(m.Store)
	switch {
	case !rp.serves:
	case rp.acct == nil && rp.set.UsesWarp():
		m.registerWarpLater()
	case rp.acct != nil && rp.acct.Masque() && !m.Usque.Installed():
		m.installUsqueLater()
	}
	return rp
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// servesProtocols reports whether this server runs protocols the routing
// applies to (AmneziaWG and the Telegram proxy do not take routing rules).
func (m *Manager) servesProtocols() bool {
	ins, _ := m.Store.Inbounds()
	for _, in := range ins {
		if p := protocol.Get(in.Preset); in.Enabled && in.NodeID == 0 && p != nil && p.Engine != "awg" && p.Engine != "mtg" {
			return true
		}
	}
	return false
}

// warp reports whether WARP is in use and ready.
func (rp *routePlan) warp() bool { return rp.acct != nil && rp.set.UsesWarp() }

// useRouter reports whether the other protocols should send their traffic through Xray.
func (rp *routePlan) useRouter() bool {
	return rp.set.Active() && (rp.dat != "" || rp.warp() || len(rp.set.BlockDomains) > 0)
}

// iranOut is where Iranian sites go ("" = no rule needed).
func (rp *routePlan) iranOut() string {
	switch {
	case rp.dat == "":
		return ""
	case rp.set.Iran == routing.IranBlock:
		return "block"
	case rp.set.Iran == routing.IranWarp && rp.warp():
		return warpTag
	case rp.set.Warp == routing.WarpAll && rp.warp():
		return "direct" // everything else leaves through WARP
	}
	return ""
}

// ---------------------------------------------------------------- Xray

func domainList(prefix string, ds []string) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, prefix+d)
	}
	return out
}

// xrayRouting adds the routing rules, outbounds and loopback inbounds to an Xray config.
func (rp *routePlan) xrayRouting(blockTorrent bool, mitaOrTT bool) (rules []obj, outbounds []obj, inbounds []obj, strategy string) {
	strategy = "AsIs"
	direct := obj{"protocol": "freedom", "tag": "direct"}
	block := obj{"protocol": "blackhole", "tag": "block"}
	outbounds = []obj{direct, block}
	if rp.acct != nil {
		w := rp.acct.XrayOutbound(warpTag)
		if rp.acct.Masque() {
			// usque carries WARP; Xray hands it the traffic over local SOCKS
			w = obj{"protocol": "socks", "tag": warpTag, "settings": obj{"servers": []obj{{"address": "127.0.0.1", "port": rp.usque}}}}
		}
		if rp.warp() && rp.set.Warp == routing.WarpAll {
			outbounds = []obj{w, direct, block} // the first outbound takes what no rule matched
		} else {
			outbounds = append(outbounds, w)
		}
		inbounds = append(inbounds, obj{"tag": "warp-test", "listen": "127.0.0.1", "port": rp.warpTest, "protocol": "socks",
			"settings": obj{"auth": "noauth", "udp": false}})
		rules = append(rules, obj{"inboundTag": []string{"warp-test"}, "outboundTag": warpTag})
	}
	if mitaOrTT && rp.useRouter() {
		inbounds = append(inbounds, obj{"tag": "router", "listen": "127.0.0.1", "port": rp.router, "protocol": "socks",
			"settings": obj{"auth": "noauth", "udp": true, "ip": "127.0.0.1"},
			"sniffing": obj{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}})
	}
	geo := rp.dat != ""
	if geo {
		rules = append(rules, obj{"ip": []string{"geoip:private"}, "outboundTag": "block"})
	}
	if blockTorrent {
		rules = append(rules, obj{"protocol": []string{"bittorrent"}, "outboundTag": "block"})
	}
	s := rp.set
	if geo && s.BlockAds {
		rules = append(rules, obj{"domain": []string{"geosite:" + routing.SiteAds}, "outboundTag": "block"})
	}
	if geo && s.BlockAdult {
		rules = append(rules, obj{"domain": []string{"geosite:" + routing.SiteAdult}, "outboundTag": "block"})
	}
	if len(s.BlockDomains) > 0 {
		rules = append(rules, obj{"domain": domainList("domain:", s.BlockDomains), "outboundTag": "block"})
	}
	if len(s.DirectDomains) > 0 && rp.warp() {
		rules = append(rules, obj{"domain": domainList("domain:", s.DirectDomains), "outboundTag": "direct"})
	}
	if out := rp.iranOut(); out != "" {
		rules = append(rules,
			obj{"domain": []string{"geosite:" + routing.SiteIran, "domain:ir"}, "outboundTag": out},
			obj{"ip": []string{"geoip:" + routing.IPIran}, "outboundTag": out})
		strategy = "IPIfNonMatch" // a name not on the list is resolved and checked against Iran's addresses
	}
	if rp.warp() && s.Warp == routing.WarpSites {
		var ds []string
		if geo {
			for _, c := range s.WarpSiteCodes() {
				ds = append(ds, "geosite:"+c)
			}
		}
		ds = append(ds, domainList("domain:", s.WarpDomains)...)
		if len(ds) > 0 {
			rules = append(rules, obj{"domain": ds, "outboundTag": warpTag})
		}
	}
	return
}

// ---------------------------------------------------------------- sing-box

// singboxRouting returns the route rules, rule-sets and endpoints for sing-box.
func (m *Manager) singboxRouting(rp *routePlan, blockTorrent bool) (rules, ruleSets, endpoints, outbounds []obj, final string, resolver bool, err error) {
	rules = []obj{{"action": "sniff"}, {"ip_is_private": true, "action": "reject"}}
	if blockTorrent {
		rules = append(rules, obj{"protocol": []string{"bittorrent"}, "action": "reject"})
	}
	final = "direct"
	seen := map[string]bool{}
	site := func(code string, extra ...string) string {
		tag := "site-" + safeName(code)
		if !seen[tag] && err == nil {
			var path string
			if path, err = m.siteRuleSet(rp.dat, code, tag, extra); err == nil {
				ruleSets = append(ruleSets, obj{"type": "local", "tag": tag, "format": "source", "path": path})
				seen[tag] = true
			}
		}
		return tag
	}
	s := rp.set
	geo := rp.dat != ""
	if geo && s.BlockAds {
		rules = append(rules, obj{"rule_set": []string{site(routing.SiteAds)}, "action": "reject"})
	}
	if geo && s.BlockAdult {
		rules = append(rules, obj{"rule_set": []string{site(routing.SiteAdult)}, "action": "reject"})
	}
	if len(s.BlockDomains) > 0 {
		rules = append(rules, obj{"domain_suffix": s.BlockDomains, "action": "reject"})
	}
	if rp.warp() {
		if rp.acct.Masque() {
			outbounds = append(outbounds, obj{"type": "socks", "tag": warpTag, "server": "127.0.0.1", "server_port": rp.usque, "version": "5"})
		} else {
			endpoints = append(endpoints, rp.acct.SingboxEndpoint(warpTag))
		}
		if s.Warp == routing.WarpAll {
			final = warpTag
		}
		if len(s.DirectDomains) > 0 {
			rules = append(rules, obj{"domain_suffix": s.DirectDomains, "outbound": "direct"})
		}
	}
	irOut := rp.iranOut()
	act := func(out string) obj {
		if out == "block" {
			return obj{"action": "reject"}
		}
		return obj{"outbound": out}
	}
	if irOut != "" {
		r := act(irOut)
		r["rule_set"] = []string{site(routing.SiteIran, "ir")}
		rules = append(rules, r)
	}
	if rp.warp() && s.Warp == routing.WarpSites {
		r := obj{"outbound": warpTag}
		var tags []string
		if geo {
			for _, c := range s.WarpSiteCodes() {
				tags = append(tags, site(c))
			}
		}
		if len(tags) > 0 {
			r["rule_set"] = tags
		}
		if len(s.WarpDomains) > 0 {
			r["domain_suffix"] = s.WarpDomains
		}
		if len(tags) > 0 || len(s.WarpDomains) > 0 {
			rules = append(rules, r)
		}
	}
	if irOut != "" {
		// names not on the list are resolved and checked against Iran's addresses
		tag := "ip-" + routing.IPIran
		if path, e := m.ipRuleSet(rp.dat, routing.IPIran, tag); e == nil {
			ruleSets = append(ruleSets, obj{"type": "local", "tag": tag, "format": "source", "path": path})
			r := act(irOut)
			r["rule_set"] = []string{tag}
			rules = append(rules, obj{"action": "resolve"}, r)
			resolver = true
		} else if err == nil {
			err = e
		}
	}
	return
}

func safeName(code string) string {
	return strings.NewReplacer("!", "not-", "@", "-", "/", "-").Replace(strings.ToLower(code))
}

var ruleCache sync.Map // file path → hash of what it was built from

// siteRuleSet writes a sing-box source rule-set for a geosite code (plus extra suffixes).
func (m *Manager) siteRuleSet(dat, code, tag string, extra []string) (string, error) {
	return m.ruleFile(dat, "geosite.dat", tag, code+"|"+strings.Join(extra, ","), func() (obj, error) {
		ds, err := geodata.Site(filepath.Join(dat, "geosite.dat"), code)
		if err != nil {
			return nil, err
		}
		var full, suffix, kw, re []string
		suffix = append(suffix, extra...)
		for _, d := range ds {
			switch d.Kind {
			case geodata.Full:
				full = append(full, d.Value)
			case geodata.Suffix:
				suffix = append(suffix, d.Value)
			case geodata.Keyword:
				kw = append(kw, d.Value)
			case geodata.Regex:
				re = append(re, d.Value)
			}
		}
		r := obj{}
		for k, v := range map[string][]string{"domain": full, "domain_suffix": suffix, "domain_keyword": kw, "domain_regex": re} {
			if len(v) > 0 {
				r[k] = v
			}
		}
		return obj{"version": 3, "rules": []obj{r}}, nil
	})
}

// ipRuleSet writes a sing-box source rule-set for a geoip code.
func (m *Manager) ipRuleSet(dat, code, tag string) (string, error) {
	return m.ruleFile(dat, "geoip.dat", tag, code, func() (obj, error) {
		nets, err := geodata.IP(filepath.Join(dat, "geoip.dat"), code)
		if err != nil {
			return nil, err
		}
		cidrs := make([]string, 0, len(nets))
		for _, n := range nets {
			cidrs = append(cidrs, n.String())
		}
		return obj{"version": 3, "rules": []obj{{"ip_cidr": cidrs}}}, nil
	})
}

// ruleFile writes a rule-set next to the sing-box config, rebuilding it only
// when the .dat file or the request changed.
func (m *Manager) ruleFile(dat, datName, tag, what string, make func() (obj, error)) (string, error) {
	st, err := os.Stat(filepath.Join(dat, datName))
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(m.SingBox.ConfigPath), "rules")
	path := filepath.Join(dir, tag+".json")
	key := fmt.Sprintf("%s|%d|%d", what, st.Size(), st.ModTime().UnixNano())
	h := sha256.Sum256([]byte(key))
	if v, ok := ruleCache.Load(path); ok && v == h && fileExists(path) {
		return path, nil
	}
	cfg, err := make()
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(cfg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if old, err := os.ReadFile(path); err != nil || string(old) != string(data) {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return "", err
		}
	}
	ruleCache.Store(path, h)
	return path, nil
}

// ---------------------------------------------------------------- WARP

var usqueInst struct {
	sync.Mutex
	busy bool
	last time.Time
}

// installUsqueLater downloads usque in the background (a server whose WARP
// device uses MASQUE but has no usque yet), then applies the configs again.
func (m *Manager) installUsqueLater() {
	usqueInst.Lock()
	defer usqueInst.Unlock()
	if usqueInst.busy || time.Since(usqueInst.last) < 10*time.Minute || m.InstallUsque == nil {
		return
	}
	usqueInst.busy, usqueInst.last = true, time.Now()
	go func() {
		defer func() { usqueInst.Lock(); usqueInst.busy = false; usqueInst.Unlock() }()
		if err := m.InstallUsque(); err != nil {
			m.Store.Log("core", "usque (WARP) could not be installed: %v", err)
			return
		}
		m.RequestApply()
	}()
}

var warpReg struct {
	sync.Mutex
	busy bool
	last time.Time
}

// registerWarpLater registers a WARP device in the background (a node whose
// panel turned WARP on), then applies the configs again.
func (m *Manager) registerWarpLater() {
	warpReg.Lock()
	defer warpReg.Unlock()
	if warpReg.busy || time.Since(warpReg.last) < 10*time.Minute {
		return
	}
	warpReg.busy, warpReg.last = true, time.Now()
	go func() {
		defer func() { warpReg.Lock(); warpReg.busy = false; warpReg.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		c := m.WarpClient
		if c == nil {
			c = &warp.Client{}
		}
		a, err := c.Register(ctx)
		if err != nil {
			m.Store.Log("core", "WARP registration failed: %v", err)
			return
		}
		if routing.Account(m.Store) != nil {
			return // registered meanwhile
		}
		if err := routing.SaveAccount(m.Store, a); err == nil {
			m.Store.Log("core", "WARP device registered (%s)", a.AddressV4)
			m.RequestApply()
		}
	}()
}

// routerAddr is the loopback SOCKS address of the routing inbound.
func (rp *routePlan) routerAddr() string { return "127.0.0.1:" + strconv.Itoa(rp.router) }
