package server

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/billing"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

type obj = map[string]any

func (s *Server) serveSub(w http.ResponseWriter, r *http.Request, rest string) {
	token, format, _ := strings.Cut(rest, "/")
	u, err := s.St.UserByToken(token)
	if err != nil || len(token) < 8 {
		decoy(w)
		return
	}
	if f := r.URL.Query().Get("format"); f != "" {
		format = f
	}
	ua := strings.ToLower(r.UserAgent())
	if format == "" {
		switch {
		case strings.Contains(r.Header.Get("Accept"), "text/html") && strings.Contains(ua, "mozilla"):
			format = "html"
		case containsAny(ua, "clash", "mihomo", "stash", "flclash", "koala"):
			format = "clash"
		case containsAny(ua, "sing-box", "sfa/", "sfi/", "sfm/", "sft/"):
			format = "singbox"
		default:
			format = "links"
		}
	}
	now := time.Now().Unix()
	title := s.St.GetSetting("sub_title", "Dunvex")
	w.Header().Set("Subscription-Userinfo", fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", u.UsedUp, u.UsedDown, u.DataLimit, u.ExpireAt))
	w.Header().Set("Profile-Update-Interval", s.St.GetSetting("sub_update_hours", "12"))
	w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(title+" · "+u.Name)))
	w.Header().Set("Profile-Web-Page-Url", s.SubURL(r, u))
	if sup := s.St.GetSetting("support_url", ""); sup != "" {
		w.Header().Set("Support-Url", sup)
	}
	if ann := s.St.GetSetting("announce", ""); ann != "" {
		w.Header().Set("Announce", "base64:"+base64.StdEncoding.EncodeToString([]byte(ann)))
	}
	w.Header().Set("Cache-Control", "no-store")

	switch format {
	case "html":
		s.subPage(w, r, u)
	case "clash":
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(title))
		w.Write(s.clashProfile(u, now))
	case "singbox", "sing-box", "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(s.singboxProfile(u, now))
	case "xray", "xray-json", "v2ray":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(s.xrayProfile(u, now))
	case "info":
		writeJSON(w, 200, s.view(r, u, s.Mgr.OnlineUsers(60)))
	case "conf":
		s.confFile(w, r, u, now)
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(s.subLinks(u, now), "\n")))))
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// statusLine summarises remaining data/time for the info entry.
func statusLine(u *store.User, now int64) string {
	data := "∞"
	if u.DataLimit > 0 {
		left := u.DataLimit - u.Used()
		if left < 0 {
			left = 0
		}
		data = util.HumanBytes(left)
	}
	days := "∞"
	switch {
	case u.ExpireAt > 0:
		d := (u.ExpireAt - now + 86399) / 86400
		if d < 0 {
			d = 0
		}
		days = fmt.Sprintf("%d", d)
	case u.DurationDays > 0:
		days = fmt.Sprintf("%d", u.DurationDays)
	}
	return fmt.Sprintf("📊 %s · ⏳ %s days", data, days)
}

func blockedReason(u *store.User, now int64) string {
	switch u.Status(now) {
	case store.StatusLimited:
		return "⛔ Data limit reached"
	case store.StatusExpired:
		return "⛔ Subscription expired"
	case store.StatusDisabled:
		return "⛔ Account disabled"
	}
	return ""
}

// pageReason is blockedReason for the user page, in its language.
func pageReason(u *store.User, now int64, l pageLang) string {
	switch u.Status(now) {
	case store.StatusLimited:
		return l.t("limited")
	case store.StatusExpired:
		return l.t("expired")
	case store.StatusDisabled:
		return l.t("disabled")
	}
	return ""
}

func dummyLink(name string) string {
	return "vless://00000000-0000-0000-0000-000000000000@127.0.0.1:1?type=tcp&security=none#" + url.PathEscape(name)
}

func (s *Server) subLinks(u *store.User, now int64) []string {
	if reason := blockedReason(u, now); reason != "" {
		return []string{dummyLink(reason)}
	}
	var out []string
	if s.St.GetSetting("sub_info_entry", "true") == "true" {
		out = append(out, dummyLink(statusLine(u, now)))
	}
	for _, c := range s.configsFor(u) {
		if strings.HasPrefix(c.Link, "tg://") || strings.HasPrefix(c.Link, "slipnet://") || c.File != "" {
			continue // Telegram proxies, SlipNet profiles and .conf files are not for these VPN apps (all are on the user page)
		}
		out = append(out, c.Link)
	}
	return out
}

type profileItem struct {
	p    *protocol.Preset
	in   *store.Inbound
	name string
	env  protocol.Env // the address of the server the inbound runs on
	via  string       // "cdn" or "relay" for those copies of a config, "" for the config itself
}

func (s *Server) items(u *store.User) []profileItem {
	inbounds, _ := s.St.Inbounds()
	title := s.St.GetSetting("sub_title", "Dunvex")
	local, nodes := s.Env(), s.nodeMap()
	cl := s.cdnLinks(local)
	var out []profileItem
	seen := map[string]int{}
	for _, in := range inbounds {
		if !in.Enabled || !u.AllowsInbound(in.ID) {
			continue
		}
		p := protocol.Get(in.Preset)
		if p == nil {
			continue
		}
		if in.NodeID != 0 && nodes[in.NodeID] == nil {
			continue // its server was removed
		}
		env := s.envFor(in, local, nodes)
		relays := relaysFor(in, p, nodes)
		if !onlyViaRelay(relays) && cl.direct(in, p) {
			name := remarkFor(in, p, u, title, nodes)
			if seen[name]++; seen[name] > 1 {
				name = fmt.Sprintf("%s %d", name, seen[name])
			}
			out = append(out, profileItem{p, in, name, directEnv(in, env), ""})
		}
		for _, v := range cl.variants(in, p, title, env) {
			name := v.name
			if seen[name]++; seen[name] > 1 {
				name = fmt.Sprintf("%s %d", name, seen[name])
			}
			out = append(out, profileItem{p, v.in, name, v.env, "cdn"})
		}
		for _, r := range relays {
			name := relayRemark(in, p, title, r)
			if seen[name]++; seen[name] > 1 {
				name = fmt.Sprintf("%s %d", name, seen[name])
			}
			out = append(out, profileItem{p, viaRelay(in, r), name, env, "relay"})
		}
	}
	return out
}

func (s *Server) clashProfile(u *store.User, now int64) []byte {
	var proxies []obj
	var names []string
	if blockedReason(u, now) == "" {
		for _, it := range s.items(u) {
			if c := protocol.Clash(it.p, it.in, u, it.env, it.name); c != nil {
				proxies = append(proxies, c)
				names = append(names, it.name)
			}
		}
	}
	var b bytes.Buffer
	b.WriteString("mixed-port: 7890\nallow-lan: false\nmode: rule\nlog-level: warning\nipv6: true\n")
	b.WriteString("dns:\n  enable: true\n  enhanced-mode: fake-ip\n  nameserver: [https://1.1.1.1/dns-query, https://8.8.8.8/dns-query]\n")
	b.WriteString("proxies:\n")
	for _, p := range proxies {
		j, _ := json.Marshal(p)
		b.WriteString("  - " + string(j) + "\n")
	}
	if len(proxies) == 0 {
		b.WriteString("  []\n")
	}
	group := func(name, typ string, extra string) {
		list, _ := json.Marshal(names)
		if len(names) == 0 {
			list = []byte(`["DIRECT"]`)
		}
		fmt.Fprintf(&b, "  - {\"name\":%q,\"type\":%q,\"proxies\":%s%s}\n", name, typ, list, extra)
	}
	b.WriteString("proxy-groups:\n")
	sel, _ := json.Marshal(append([]string{"⚡ Auto"}, names...))
	fmt.Fprintf(&b, "  - {\"name\":\"🚀 Dunvex\",\"type\":\"select\",\"proxies\":%s}\n", sel)
	group("⚡ Auto", "url-test", `,"url":"https://www.gstatic.com/generate_204","interval":300,"tolerance":50`)
	b.WriteString("rules:\n  - GEOIP,private,DIRECT,no-resolve\n")
	if ir := s.iranDirect(); ir != nil {
		// Iranian sites go straight from the device
		for _, r := range ir.clashRules() {
			b.WriteString("  - " + r + "\n")
		}
	}
	b.WriteString("  - MATCH,🚀 Dunvex\n")
	return b.Bytes()
}

// xrayProfile is the Xray JSON subscription: one whole Xray config per config
// of the user, for v2rayNG, v2rayN and Streisand. Unlike plain links it
// carries the routing (local networks and, when on, Iranian sites direct),
// the TLS fragment for every TCP config with TLS or Reality, VMess included,
// and Mux.Cool when multiplexing is on.
// Configs Xray cannot open (TUIC, AnyTLS, Mieru, AmneziaWG…) are left out.
func (s *Server) xrayProfile(u *store.User, now int64) []byte {
	var domains, ips []string
	if ir := s.iranDirect(); ir != nil {
		domains, ips = ir.xrayDomains(), ir.cidr4
	}
	out := []map[string]any{}
	placeholder := func(name string) {
		o, _ := protocol.XrayOutbound(dummyLink(name), "proxy")
		out = append(out, protocol.XrayClientConfig(name, o, nil, nil))
	}
	if reason := blockedReason(u, now); reason != "" {
		placeholder(reason)
	} else {
		if s.St.GetSetting("sub_info_entry", "true") == "true" {
			placeholder(statusLine(u, now))
		}
		for _, it := range s.items(u) {
			o, err := protocol.XrayOutbound(protocol.Link(it.p, it.in, u, it.env, it.name), "proxy")
			if err != nil {
				continue
			}
			protocol.XrayExtras(o, it.p, it.env) // the fragment (VMess too) and Mux.Cool
			out = append(out, protocol.XrayClientConfig(it.name, o, domains, ips))
		}
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return b
}

func (s *Server) singboxProfile(u *store.User, now int64) []byte {
	var outbounds, endpoints []obj
	var tags []string
	if blockedReason(u, now) == "" {
		for _, it := range s.items(u) {
			kind, items := protocol.SingboxOutbound(it.p, it.in, u, it.env, it.name)
			if len(items) == 0 {
				continue
			}
			tags = append(tags, it.name)
			if kind == "endpoint" {
				endpoints = append(endpoints, items...)
			} else {
				outbounds = append(outbounds, items...)
			}
		}
	}
	if len(tags) == 0 {
		tags = []string{"direct"}
	}
	all := append([]obj{
		{"type": "selector", "tag": "proxy", "outbounds": append([]string{"auto"}, tags...), "default": "auto"},
		{"type": "urltest", "tag": "auto", "outbounds": tags, "url": "https://www.gstatic.com/generate_204", "interval": "5m"},
	}, outbounds...)
	all = append(all, obj{"type": "direct", "tag": "direct"})
	dns := obj{
		"servers": []obj{
			{"type": "https", "tag": "remote", "server": "1.1.1.1", "detour": "proxy"},
			{"type": "local", "tag": "local"},
		},
		"final": "remote",
	}
	rules := []obj{
		{"action": "sniff"},
		{"protocol": "dns", "action": "hijack-dns"},
		{"ip_is_private": true, "outbound": "direct"},
	}
	if ir := s.iranDirect(); ir != nil {
		// Iranian sites go straight from the device and are resolved by its own DNS,
		// so Iranian CDNs answer with their Iranian addresses
		dr := ir.singboxDomains()
		dr["server"] = "local"
		dns["rules"] = []obj{dr}
		r := ir.singboxDomains()
		r["outbound"] = "direct"
		rules = append(rules, r)
		if len(ir.cidr4) > 0 {
			rules = append(rules, obj{"ip_cidr": ir.cidr4, "outbound": "direct"})
		}
	}
	cfg := obj{
		"log": obj{"level": "warn"},
		"dns": dns,
		"inbounds": []obj{
			{"type": "tun", "tag": "tun-in", "address": []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"}, "auto_route": true, "strict_route": true},
			{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080},
		},
		"outbounds": all,
		"route": obj{
			"rules":                   rules,
			"final":                   "proxy",
			"auto_detect_interface":   true,
			"default_domain_resolver": "local",
		},
	}
	if len(endpoints) > 0 {
		cfg["endpoints"] = endpoints
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return b
}

// confFile sends one AmneziaWG .conf of the user (?i=inbound id, else the first).
func (s *Server) confFile(w http.ResponseWriter, r *http.Request, u *store.User, now int64) {
	if reason := blockedReason(u, now); reason != "" {
		http.Error(w, reason, http.StatusForbidden)
		return
	}
	want, via := r.URL.Query().Get("i"), r.URL.Query().Get("r")
	for _, c := range s.configsFor(u) {
		if c.File == "" || (want != "" && want != fmt.Sprint(c.InboundID)) || (via != "" && via != fmt.Sprint(c.RelayID)) || (via == "" && want != "" && c.RelayID != 0) {
			continue
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+c.File+`"`)
		w.Write([]byte(c.Link))
		return
	}
	http.NotFound(w, r)
}

// ---------------------------------------------------------------- user page

type appLink struct {
	Name, OS, URL string
}

func appLinks(sub, name string) []appLink {
	e := url.QueryEscape(sub)
	n := url.PathEscape(name)
	b64 := base64.StdEncoding.EncodeToString([]byte(sub))
	return []appLink{
		{"Hiddify", "all", "hiddify://import/" + sub + "#" + n},
		{"v2rayNG", "android", "v2rayng://install-config?url=" + e + "#" + n},
		{"v2rayNG · Xray JSON", "android", "v2rayng://install-config?url=" + url.QueryEscape(sub+"/xray") + "#" + n},
		{"Happ", "all", "happ://add/" + sub},
		{"NekoBox", "android", "sn://subscription?url=" + e + "&name=" + n},
		{"Streisand", "ios", "streisand://import/" + sub + "#" + n},
		{"V2Box", "ios", "v2box://install-sub?url=" + e + "&name=" + n},
		{"Shadowrocket", "ios", "sub://" + b64 + "#" + n},
		{"sing-box", "all", "sing-box://import-remote-profile?url=" + e + "#" + n},
		{"Clash / FlClash", "all", "clash://install-config?url=" + e + "&name=" + n},
	}
}

func (s *Server) subPage(w http.ResponseWriter, r *http.Request, u *store.User) {
	now := time.Now().Unix()
	sub := s.SubURL(r, u)
	title := s.St.GetSetting("sub_title", "Dunvex")
	l := s.pageLangFor(w, r)
	v := s.view(r, u, s.Mgr.OnlineUsers(60))
	pct := 0.0
	if u.DataLimit > 0 {
		pct = float64(u.Used()) * 100 / float64(u.DataLimit)
		if pct > 100 {
			pct = 100
		}
	}
	expire := ""
	if u.ExpireAt > 0 {
		expire = time.Unix(u.ExpireAt, 0).Format("2006-01-02")
	}
	type cfg struct {
		Name, Link string
		QR         template.HTML
		File       string // a .conf: shown as text with a download button
		Download   string
		Hint, Note string // how to add it, and what to type in by hand (DNS tunnels)
	}
	var cfgs []cfg
	if blockedReason(u, now) == "" {
		for _, c := range s.configsFor(u) {
			if c.File != "" {
				dl := sub + "/conf?i=" + fmt.Sprint(c.InboundID)
				if c.RelayID != 0 {
					dl += "&r=" + fmt.Sprint(c.RelayID)
				}
				cfgs = append(cfgs, cfg{c.Name, c.Link, template.HTML(c.QR), c.File, dl, "", ""})
				continue
			}
			hint := ""
			switch {
			case strings.HasPrefix(c.Link, "ssh://"):
				hint = l.t("sshHint")
			case strings.HasPrefix(c.Link, "slipnet://"):
				hint = l.t("slipHint")
				if c.Note != "" {
					hint += l.t("slipByHand")
				}
			}
			cfgs = append(cfgs, cfg{c.Name, c.Link, template.HTML(QRSVG(c.Link)), "", "", hint, c.Note})
		}
	}
	data := map[string]any{
		"Title": title, "User": u, "View": v, "Sub": sub, "SubQR": template.HTML(QRSVG(sub)),
		"Used": util.HumanBytes(u.Used()), "Limit": limitText(u.DataLimit), "Pct": fmt.Sprintf("%.1f", pct),
		"Expire": expire, "Reason": pageReason(u, now, l), "Configs": cfgs, "Apps": appLinks(sub, title),
		"Support": s.St.GetSetting("support_url", ""), "Announce": s.St.GetSetting("announce", ""),
		"Days": daysText(v.DaysLeft), "Pending": v.Status == store.StatusPending,
		"FontBase": s.SubPath() + "_font/", "RingColor": template.CSS(ringColor(pct)), "PctCSS": template.CSS(fmt.Sprintf("%.1f", pct)),
		"Brand": s.SubPath() + "_brand/", "Paused": "", "DevLimit": "", "Bot": "", "Renew": "",
		"L": l.code(), "Dir": l.dir(), "T": l.texts(), "Accent": template.CSS(accentOr(s.St.GetSetting("sub_accent", ""))),
	}
	if u.PausedUntil > now {
		data["Paused"] = l.num(time.Unix(u.PausedUntil, 0).Format("15:04"))
	}
	s.pageFacts(data, u, time.Unix(now, 0), l)
	if billing.LoadSettings(s.St).Shop {
		if plans, _ := s.St.Plans(true); len(plans) > 0 {
			data["Renew"] = s.ShopPath() + "?renew=" + u.SubToken
		}
	}
	if u.IPLimit > 0 {
		data["DevLimit"] = fmt.Sprint(u.IPLimit)
	}
	if s.Bot != nil && s.St.GetSetting("tg_user_bot", "true") == "true" {
		if name, _ := s.Bot.Status()["username"].(string); name != "" {
			data["Bot"] = "https://t.me/" + name + "?start=" + u.SubToken
		}
	}
	var page bytes.Buffer
	if err := subTmpl.Execute(&page, data); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Vary", "Accept-Encoding")
	// the QR codes make the page large; compressed it is about a fifth, which
	// matters on slow mobile networks
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		zw.Write(page.Bytes())
		zw.Close()
		return
	}
	w.Write(page.Bytes())
}

// accentOr returns a safe CSS colour for the portal accent.
func accentOr(v string) string {
	if accentRe.MatchString(v) {
		return v
	}
	return "#397bff"
}

func ringColor(pct float64) string {
	switch {
	case pct >= 90:
		return "var(--bad)"
	case pct >= 75:
		return "var(--udp)"
	}
	return "var(--accent)"
}

func limitText(b int64) string {
	if b <= 0 {
		return "∞"
	}
	return util.HumanBytes(b)
}

func daysText(d *int) string {
	if d == nil {
		return "∞"
	}
	return fmt.Sprintf("%d", *d)
}

var subTmpl = template.Must(template.New("sub").Funcs(template.FuncMap{
	"safeURL": func(s string) template.URL { return template.URL(s) },
}).Parse(subHTML))

//go:embed userpage.html
var subHTML string
