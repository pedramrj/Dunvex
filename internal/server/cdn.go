package server

import (
	"context"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/cdn"
	"github.com/pedramrj/dunvex/internal/domains"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// ---------------------------------------------------------------- links

// cdnVariant is another way users reach an inbound: through a CDN name (or
// one of its clean addresses), or at another direct name of this server.
type cdnVariant struct {
	in   *store.Inbound
	name string
	env  protocol.Env
	via  string // "cdn" (a CDN name), a clean address, or "domain" (another name of this server)
}

// cdnName is a CDN name that carries configs.
type cdnName struct {
	d       domains.Domain
	ech     *protocol.ECH // the CDN's ECH for configs through it (nil = none)
	echFrom string        // the name it was published under
}

// cdnLinks is what the domain list means for the links of this server.
type cdnLinks struct {
	list    domains.List
	cdns    []cdnName              // the CDN names with configs, in order
	mirrors []domains.Domain       // the other direct names with configs
	domain  string                 // the first CDN name ("" = none)
	enabled bool                   // the first CDN name carries configs
	split   string                 // how XHTTP through the CDN sends its downloads (protocol.Split*)
	covers  func(name string) bool // this server's certificate is for the name
}

func (s *Server) cdnLinks(local protocol.Env) cdnLinks {
	l := domains.Load(s.St)
	c := cdnLinks{list: l, split: s.split(), mirrors: l.Mirrors(), covers: func(string) bool { return false }}
	if len(c.mirrors) > 0 && !local.SelfSigned {
		if info := s.certInfo(local.CertFile); info != nil {
			c.covers = info.Covers
		}
	}
	if f := l.FirstCDN(); f != nil {
		c.domain, c.enabled = f.Name, f.Configs
	}
	ech := s.setting(echCDNSetting, "false") == "true"
	for _, d := range l.CDNs() {
		n := cdnName{d: d}
		if ech {
			n.ech, n.echFrom = cdnECH(d.Name)
		}
		c.cdns = append(c.cdns, n)
	}
	return c
}

// first is the first CDN name with configs (nil = none).
func (c cdnLinks) first() *cdnName {
	if len(c.cdns) == 0 {
		return nil
	}
	return &c.cdns[0]
}

// on reports whether an inbound's users reach it through the CDN.
func (c cdnLinks) on(in *store.Inbound, p *protocol.Preset) bool {
	return len(c.cdns) > 0 && p.Group == "cdn" && in.NodeID == 0
}

// direct reports whether users also get the config straight to the server.
func (c cdnLinks) direct(in *store.Inbound, p *protocol.Preset) bool {
	return !c.on(in, p) || c.list.CDNDirect
}

// variants are the other configs of an inbound: through each CDN name (the
// name itself, or the inbound's own link address, then each clean address)
// and at each other direct name of this server. Through a CDN, clients check
// the CDN's certificate, never this server's, and carry the CDN's ECH, never
// this server's (the CDN ends their TLS). With the XHTTP split, an XHTTP
// config sends its downloads (or its uploads) straight to the server instead.
func (c cdnLinks) variants(in *store.Inbound, p *protocol.Preset, title string, env protocol.Env) []cdnVariant {
	var out []cdnVariant
	if c.on(in, p) {
		for i, n := range c.cdns {
			out = append(out, c.throughCDN(in, p, title, env, n, i == 0)...)
		}
		if c.split != protocol.SplitOff && protocol.Splittable(p, in) {
			direct := directEnv(in, env)
			for i, v := range out {
				cdnRoute := &protocol.Route{In: v.in, Env: v.env}
				if c.split == protocol.SplitDown {
					// uploads through the CDN, downloads straight from the server
					out[i].env.Download = &protocol.Route{In: in, Env: direct}
				} else {
					// uploads straight to the server, downloads through the CDN
					e := direct
					e.Download = cdnRoute
					out[i].in, out[i].env = in, e
				}
			}
		}
	}
	if c.direct(in, p) {
		out = append(out, c.atMirrors(in, p, title, env)...)
	}
	return out
}

// throughCDN are the copies of an inbound through one CDN name. The first
// name's configs keep the "CDN" label they always had.
func (c cdnLinks) throughCDN(in *store.Inbound, p *protocol.Preset, title string, env protocol.Env, n cdnName, first bool) []cdnVariant {
	env.SelfSigned, env.CertSHA256 = false, ""
	env.Domain, env.Host = n.d.Name, n.d.Name
	env.ECH, env.Download = n.ech, nil
	name := in.Remark
	if name == "" {
		name = p.Name
	}
	label := func(l string) string {
		if title != "" {
			return title + " · " + l + " · " + name
		}
		return l + " · " + name
	}
	main := n.d.Name
	if a := strings.TrimSpace(in.Str("address")); a != "" && first {
		main = a
	}
	head := "CDN"
	if !first || n.d.Label != "" {
		head = n.d.Title()
	}
	out := []cdnVariant{{cdnCopy(in, main, n.d.Name), label(head), env, "cdn"}}
	for _, a := range n.d.Addresses {
		out = append(out, cdnVariant{cdnCopy(in, a.Address, n.d.Name), label(a.Label()), env, a.Address})
	}
	return out
}

// atMirrors are the copies of an inbound of this server at its other direct
// names: the TLS protocols (whose name users' apps send), or every protocol
// for a name with All. An inbound with its own link address keeps it.
func (c cdnLinks) atMirrors(in *store.Inbound, p *protocol.Preset, title string, env protocol.Env) []cdnVariant {
	if len(c.mirrors) == 0 || in.NodeID != 0 || strings.TrimSpace(in.Str("address")) != "" {
		return nil
	}
	tls := p.Security == "tls"
	name := in.Remark
	if name == "" {
		name = p.Name
	}
	// the SNI follows the name unless the protocol has one of its own (a new
	// TLS protocol starts with the main domain's)
	own := strings.TrimSpace(in.Str("sni"))
	follows := tls && (own == "" || own == c.list.MainName())
	var out []cdnVariant
	for _, m := range c.mirrors {
		if !tls && !m.All {
			continue
		}
		e := directEnv(in, env)
		e.Host = m.Name
		at := in
		if follows && (env.SelfSigned || c.covers(m.Name)) {
			// the app asks for this name: a real certificate must cover it
			// (until it does, the config keeps the main name as its SNI)
			e.Domain = m.Name
			at = withParam(in, "sni", m.Name)
		}
		label := m.Title() + " · " + name
		if title != "" {
			label = title + " · " + label
		}
		out = append(out, cdnVariant{at, label, e, "domain"})
	}
	return out
}

// withParam is a copy of an inbound with one parameter changed.
func withParam(in *store.Inbound, k string, v any) *store.Inbound {
	c := *in
	c.Params = make(map[string]any, len(in.Params)+1)
	for x, y := range in.Params {
		c.Params[x] = y
	}
	c.Params[k] = v
	return &c
}

// cdnCopy is an inbound as reached at address, with the proxied name as its
// SNI and Host (the CDN routes by them).
func cdnCopy(in *store.Inbound, address, domain string) *store.Inbound {
	c := *in
	c.Params = make(map[string]any, len(in.Params)+3)
	for k, v := range in.Params {
		c.Params[k] = v
	}
	c.Params["address"] = address
	c.Params["sni"] = domain
	if strings.TrimSpace(in.Str("host")) == "" {
		c.Params["host"] = domain
	}
	return &c
}

// ---------------------------------------------------------------- API

type cdnInboundView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Port      int    `json:"port"`
	Transport string `json:"transport"`
	TLS       bool   `json:"tls"`
	Enabled   bool   `json:"enabled"`
	PortOK    bool   `json:"portOK"`         // Cloudflare proxies this port (with TLS or without, as it needs)
	Host      string `json:"host,omitempty"` // a Host the server insists on
	Port443   bool   `json:"-"`              // on 443 (Flexible SSL speaks plain HTTP there)
	inbound   *store.Inbound
}

type cdnView struct {
	Settings    cdn.Settings     `json:"settings"`
	Domain      string           `json:"domain"`      // the proxied name in effect
	PanelDomain string           `json:"panelDomain"` // the panel's domain (Settings)
	ServerIP    string           `json:"serverIP"`
	SelfSigned  bool             `json:"selfSigned"`
	Inbounds    []cdnInboundView `json:"inbounds"`
}

func (s *Server) cdnState() cdnView { return s.cdnStateFor("") }

// cdnStateFor is the CDN view of one CDN name ("" = the first one; with
// none, the panel's domain, as before there was a list of domains).
func (s *Server) cdnStateFor(name string) cdnView {
	env := s.Env()
	l := domains.Load(s.St)
	set := cdn.Settings{Direct: l.CDNDirect, Addresses: []cdn.Address{}}
	d := l.FirstCDN()
	if name != "" {
		if f := l.Find(name); f != nil && f.Role == domains.CDN {
			d = f
		}
	}
	if d != nil {
		set.Enabled, set.Domain = d.Configs, d.Name
		if d.Addresses != nil {
			set.Addresses = d.Addresses
		}
	}
	v := cdnView{Settings: set, Domain: set.DomainFor(env.Domain), PanelDomain: env.Domain, SelfSigned: env.SelfSigned, Inbounds: []cdnInboundView{}}
	if name != "" && d == nil {
		v.Domain = domains.Normalize(name)
	}
	// the address set in Settings (the one users' links use), else the detected one
	if h := net.ParseIP(strings.TrimSpace(s.St.GetSetting("host", ""))); h != nil && h.To4() != nil {
		v.ServerIP = h.String()
	} else if s.Sys != nil {
		v.ServerIP = s.Sys.PublicIP()
	}
	ins, _ := s.St.Inbounds()
	for _, in := range ins {
		p := protocol.Get(in.Preset)
		if p == nil || p.Group != "cdn" || in.NodeID != 0 {
			continue
		}
		name := in.Remark
		if name == "" {
			name = p.Name
		}
		tls := p.Security == "tls"
		v.Inbounds = append(v.Inbounds, cdnInboundView{ID: in.ID, Name: name, Port: in.Port, Transport: p.Transport(), TLS: tls, Enabled: in.Enabled,
			PortOK: cdn.ProxiedPort(in.Port, tls), Host: strings.TrimSpace(in.Str("host")), Port443: in.Port == 443 && tls, inbound: in})
	}
	return v
}

func (s *Server) getCDN(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.cdnStateFor(r.URL.Query().Get("name")))
}

// putCDN saves the CDN settings of the API before the list of domains: they
// become the first CDN name (an empty name means the panel's own domain).
func (s *Server) putCDN(w http.ResponseWriter, r *http.Request) {
	var in cdn.Settings
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	if err := in.Clean(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if in.Enabled && in.DomainFor(s.Env().Domain) == "" {
		fail(w, 400, "set the CDN domain first (or your domain in Settings)")
		return
	}
	l := domains.Load(s.St)
	l.TakeCDN(in)
	if _, err := domains.Save(s.St, l); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.St.Log("settings", "%s changed the CDN settings", adminOf(r).Username)
	s.afterDomains()
	writeJSON(w, 200, s.cdnStateFor(in.Domain))
}

// ---------------------------------------------------------------- check

type cdnAddr struct {
	IP         string `json:"ip"`
	Cloudflare bool   `json:"cloudflare"`
	Server     bool   `json:"server"` // this server's own address (not proxied)
}

type cdnInboundCheck struct {
	ID     int64      `json:"id"`
	Origin cdn.Result `json:"origin"` // straight to this server
	CDN    cdn.Result `json:"cdn"`    // through the proxied name
}

type cdnAddrCheck struct {
	Address string     `json:"address"`
	Name    string     `json:"name"`
	Inbound int64      `json:"inbound"`
	Result  cdn.Result `json:"result"`
}

type cdnCheck struct {
	Domain    string            `json:"domain"`
	DNS       []cdnAddr         `json:"dns"`
	DNSError  string            `json:"dnsError,omitempty"`
	Inbounds  []cdnInboundCheck `json:"inbounds"`
	Addresses []cdnAddrCheck    `json:"addresses"`
}

// checkCDN tries every leg from this server: the name's DNS, each CDN
// protocol through the CDN and straight to the server, and each clean
// address. What users in another country see can differ (filtering), so this
// shows whether the setup itself works.
func (s *Server) checkCDN(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Name string `json:"name"`
	}
	if r.ContentLength > 0 {
		readJSON(r, &q)
	}
	v := s.cdnStateFor(q.Name)
	if v.Domain == "" {
		fail(w, 400, "set the CDN domain first (or your domain in Settings)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	out := cdnCheck{Domain: v.Domain, DNS: []cdnAddr{}, Inbounds: []cdnInboundCheck{}, Addresses: []cdnAddrCheck{}}
	dctx, dcancel := context.WithTimeout(ctx, 6*time.Second)
	ips, err := net.DefaultResolver.LookupIPAddr(dctx, v.Domain)
	dcancel()
	if err != nil {
		out.DNSError = err.Error()
	}
	for _, ip := range ips {
		out.DNS = append(out.DNS, cdnAddr{IP: ip.IP.String(), Cloudflare: cdn.IsCloudflare(ip.IP), Server: ip.IP.String() == v.ServerIP})
	}

	listen := strings.TrimSpace(s.Env().Listen)
	if listen == "" || listen == "0.0.0.0" || listen == "::" {
		listen = "127.0.0.1"
	}
	target := func(iv cdnInboundView, dial string, insecure bool) cdn.Target {
		in := iv.inbound
		host := v.Domain
		if iv.Host != "" {
			host = iv.Host
		}
		return cdn.Target{Transport: iv.Transport, TLS: iv.TLS, Dial: dial, Host: host, Path: in.Str("path"), Service: in.Str("serviceName"), Insecure: insecure}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	limit := make(chan struct{}, 8)
	run := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			f()
		}()
	}
	var live []cdnInboundView
	for _, iv := range v.Inbounds {
		if iv.Enabled {
			live = append(live, iv)
		}
	}
	out.Inbounds = make([]cdnInboundCheck, len(live))
	for i, iv := range live {
		i, iv := i, iv
		out.Inbounds[i].ID = iv.ID
		run(func() {
			r := cdn.Probe(ctx, target(iv, net.JoinHostPort(listen, strconv.Itoa(iv.Port)), true))
			mu.Lock()
			out.Inbounds[i].Origin = r
			mu.Unlock()
		})
		if out.DNSError != "" {
			out.Inbounds[i].CDN = cdn.Result{Code: "dns", Error: out.DNSError}
			continue
		}
		run(func() {
			r := cdn.Probe(ctx, target(iv, net.JoinHostPort(v.Domain, strconv.Itoa(iv.Port)), false))
			mu.Lock()
			out.Inbounds[i].CDN = r
			mu.Unlock()
		})
	}
	wg.Wait()
	// clean addresses are tried with the first protocol that works through the
	// CDN (or the first one, so the result still explains what fails)
	if len(live) > 0 {
		ref := live[0]
		for i, c := range out.Inbounds {
			if c.CDN.OK {
				ref = live[i]
				break
			}
		}
		out.Addresses = make([]cdnAddrCheck, len(v.Settings.Addresses))
		for i, a := range v.Settings.Addresses {
			i, a := i, a
			out.Addresses[i] = cdnAddrCheck{Address: a.Address, Name: a.Name, Inbound: ref.ID}
			run(func() {
				r := cdn.Probe(ctx, target(ref, net.JoinHostPort(a.Address, strconv.Itoa(ref.Port)), false))
				mu.Lock()
				out.Addresses[i].Result = r
				mu.Unlock()
			})
		}
		wg.Wait()
	}
	sort.SliceStable(out.DNS, func(i, j int) bool {
		return !strings.Contains(out.DNS[i].IP, ":") && strings.Contains(out.DNS[j].IP, ":")
	})
	writeJSON(w, 200, out)
}

// ---------------------------------------------------------------- Cloudflare

// setupCloudflare points the CDN name at this server through Cloudflare's
// proxy with the owner's API token, which is used for this request only and
// never stored or logged. Records that point elsewhere change only with
// "replace" (the panel asks first).
func (s *Server) setupCloudflare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token      string `json:"token"`
		Domain     string `json:"domain"`
		Replace    bool   `json:"replace"`
		SSL        bool   `json:"ssl"`
		WebSockets bool   `json:"websockets"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	v := s.cdnStateFor(in.Domain)
	set := v.Settings
	set.Domain = in.Domain
	if err := set.Clean(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	domain := set.DomainFor(v.PanelDomain)
	if domain == "" {
		fail(w, 400, "enter the name to put behind Cloudflare (for example cdn.example.com)")
		return
	}
	ip := v.ServerIP
	if a := net.ParseIP(ip); a == nil || a.To4() == nil {
		fail(w, 502, "this server's public IPv4 address is not known yet")
		return
	}
	port443 := false
	for _, iv := range v.Inbounds {
		port443 = port443 || iv.Port443
	}
	token := s.cfToken(in.Token)
	if token == "" {
		fail(w, 400, "paste your Cloudflare API token")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	cf := &cdn.Cloudflare{Base: s.CloudflareAPI, Token: token}
	res, err := cf.Setup(ctx, cdn.SetupOptions{Domain: domain, IP: ip, Replace: in.Replace, SSL: in.SSL, SelfSigned: v.SelfSigned, WebSockets: in.WebSockets}, port443)
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	if res.Done {
		set.Enabled = true
		l := domains.Load(s.St)
		if d := l.Find(domain); d != nil && d.Role == domains.CDN {
			d.Configs = true
		} else if d == nil {
			l.Domains = append(l.Domains, domains.Domain{Name: domain, Role: domains.CDN, Configs: true})
		} else {
			l.TakeCDN(set)
		}
		if _, err := domains.Save(s.St, l); err != nil {
			fail(w, 500, err.Error())
			return
		}
		s.St.Log("settings", "%s put %s behind Cloudflare", adminOf(r).Username, domain)
		s.afterDomains()
	}
	writeJSON(w, 200, map[string]any{"result": res, "state": s.cdnStateFor(domain)})
}

// cdnPorts are the ports a new CDN protocol tries after its default, in
// order: Cloudflare's HTTPS (or HTTP) ports, 443 and 80 last (Reality and
// websites usually hold them).
func cdnPorts(p *protocol.Preset) []int {
	if p.Group != "cdn" {
		return nil
	}
	if p.Security == "tls" {
		return []int{2053, 2083, 2087, 2096, 8443, 443}
	}
	return []int{8080, 8880, 2052, 2082, 2086, 2095, 80}
}
