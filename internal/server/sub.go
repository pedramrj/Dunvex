package server

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

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
	case "info":
		writeJSON(w, 200, s.view(r, u, s.Mgr.OnlineUsers(60)))
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
		out = append(out, c.Link)
	}
	return out
}

type profileItem struct {
	p    *protocol.Preset
	in   *store.Inbound
	name string
}

func (s *Server) items(u *store.User) []profileItem {
	inbounds, _ := s.St.Inbounds()
	title := s.St.GetSetting("sub_title", "Dunvex")
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
		name := protocol.Remark(in, p, u, title)
		if seen[name]++; seen[name] > 1 {
			name = fmt.Sprintf("%s %d", name, seen[name])
		}
		out = append(out, profileItem{p, in, name})
	}
	return out
}

func (s *Server) clashProfile(u *store.User, now int64) []byte {
	env := s.Env()
	var proxies []obj
	var names []string
	if blockedReason(u, now) == "" {
		for _, it := range s.items(u) {
			if c := protocol.Clash(it.p, it.in, u, env, it.name); c != nil {
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
	b.WriteString("rules:\n  - GEOIP,private,DIRECT,no-resolve\n  - MATCH,🚀 Dunvex\n")
	return b.Bytes()
}

func (s *Server) singboxProfile(u *store.User, now int64) []byte {
	env := s.Env()
	var outbounds, endpoints []obj
	var tags []string
	if blockedReason(u, now) == "" {
		for _, it := range s.items(u) {
			kind, items := protocol.SingboxOutbound(it.p, it.in, u, env, it.name)
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
	cfg := obj{
		"log": obj{"level": "warn"},
		"dns": obj{
			"servers": []obj{
				{"type": "https", "tag": "remote", "server": "1.1.1.1", "detour": "proxy"},
				{"type": "local", "tag": "local"},
			},
			"final": "remote",
		},
		"inbounds": []obj{
			{"type": "tun", "tag": "tun-in", "address": []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"}, "auto_route": true, "strict_route": true},
			{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080},
		},
		"outbounds": all,
		"route": obj{
			"rules": []obj{
				{"action": "sniff"},
				{"protocol": "dns", "action": "hijack-dns"},
				{"ip_is_private": true, "outbound": "direct"},
			},
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
	}
	var cfgs []cfg
	if blockedReason(u, now) == "" {
		for _, c := range s.configsFor(u) {
			cfgs = append(cfgs, cfg{c.Name, c.Link, template.HTML(QRSVG(c.Link))})
		}
	}
	data := map[string]any{
		"Title": title, "User": u, "View": v, "Sub": sub, "SubQR": template.HTML(QRSVG(sub)),
		"Used": util.HumanBytes(u.Used()), "Limit": limitText(u.DataLimit), "Pct": fmt.Sprintf("%.1f", pct),
		"Expire": expire, "Reason": blockedReason(u, now), "Configs": cfgs, "Apps": appLinks(sub, title),
		"Support": s.St.GetSetting("support_url", ""), "Announce": s.St.GetSetting("announce", ""),
		"Days": daysText(v.DaysLeft), "Pending": v.Status == store.StatusPending,
		"FontBase": s.SubPath() + "_font/", "RingColor": template.CSS(ringColor(pct)), "PctCSS": template.CSS(fmt.Sprintf("%.1f", pct)),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	if err := subTmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func ringColor(pct float64) string {
	switch {
	case pct >= 90:
		return "var(--bad)"
	case pct >= 75:
		return "var(--udp)"
	}
	return "var(--cdn)"
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
