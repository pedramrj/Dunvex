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
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// ---------------------------------------------------------------- links

// cdnVariant is a CDN protocol as users reach it through the CDN.
type cdnVariant struct {
	in   *store.Inbound
	name string
	env  protocol.Env
	via  string // "cdn" (the proxied name) or a clean address
}

// cdnLinks is what the CDN settings mean for the links of this server.
type cdnLinks struct {
	set     cdn.Settings
	domain  string        // the proxied name
	ech     *protocol.ECH // the CDN's ECH for configs through it (nil = none)
	echFrom string        // the name it was published under
	split   string        // how XHTTP through the CDN sends its downloads (protocol.Split*)
}

func (s *Server) cdnLinks(local protocol.Env) cdnLinks {
	set := cdn.Load(s.St)
	c := cdnLinks{set: set, domain: set.DomainFor(local.Domain), split: s.split()}
	if set.Enabled && c.domain != "" && s.setting(echCDNSetting, "false") == "true" {
		c.ech, c.echFrom = cdnECH(c.domain)
	}
	return c
}

// on reports whether an inbound's users reach it through the CDN.
func (c cdnLinks) on(in *store.Inbound, p *protocol.Preset) bool {
	return c.set.Enabled && c.domain != "" && p.Group == "cdn" && in.NodeID == 0
}

// direct reports whether users also get the config straight to the server.
func (c cdnLinks) direct(in *store.Inbound, p *protocol.Preset) bool {
	return !c.on(in, p) || c.set.Direct
}

// variants are the configs of a CDN protocol through the CDN: the proxied
// name (or the inbound's own link address), then each clean address. Clients
// check the CDN's certificate, never this server's, and carry the CDN's ECH,
// never this server's (the CDN ends their TLS). With the XHTTP split, an XHTTP
// config sends its downloads (or its uploads) straight to the server instead.
func (c cdnLinks) variants(in *store.Inbound, p *protocol.Preset, title string, env protocol.Env) []cdnVariant {
	if !c.on(in, p) {
		return nil
	}
	out := c.throughCDN(in, p, title, env)
	if c.split == protocol.SplitOff || !protocol.Splittable(p, in) {
		return out
	}
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
	return out
}

// throughCDN are the variants as plain CDN copies.
func (c cdnLinks) throughCDN(in *store.Inbound, p *protocol.Preset, title string, env protocol.Env) []cdnVariant {
	env.SelfSigned, env.CertSHA256 = false, ""
	env.Domain, env.Host = c.domain, c.domain
	env.ECH, env.Download = c.ech, nil
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
	main := strings.TrimSpace(in.Str("address"))
	if main == "" {
		main = c.domain
	}
	out := []cdnVariant{{cdnCopy(in, main, c.domain), label("CDN"), env, "cdn"}}
	for _, a := range c.set.Addresses {
		out = append(out, cdnVariant{cdnCopy(in, a.Address, c.domain), label(a.Label()), env, a.Address})
	}
	return out
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

func (s *Server) cdnState() cdnView {
	env := s.Env()
	set := cdn.Load(s.St)
	v := cdnView{Settings: set, Domain: set.DomainFor(env.Domain), PanelDomain: env.Domain, SelfSigned: env.SelfSigned, Inbounds: []cdnInboundView{}}
	// the address set in Settings (the one users' links use), else the detected one
	if h := net.ParseIP(strings.TrimSpace(s.St.GetSetting("host", ""))); h != nil && h.To4() != nil {
		v.ServerIP = h.String()
	} else if s.Sys != nil {
		v.ServerIP = s.Sys.PublicIP()
	}
	if v.Settings.Addresses == nil {
		v.Settings.Addresses = []cdn.Address{}
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
	writeJSON(w, 200, s.cdnState())
}

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
	if _, err := cdn.Save(s.St, in); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.St.Log("settings", "%s changed the CDN settings", adminOf(r).Username)
	writeJSON(w, 200, s.cdnState())
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
	v := s.cdnState()
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
	in.Token = strings.TrimSpace(in.Token)
	if in.Token == "" {
		fail(w, 400, "paste your Cloudflare API token")
		return
	}
	v := s.cdnState()
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
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	cf := &cdn.Cloudflare{Base: s.CloudflareAPI, Token: in.Token}
	res, err := cf.Setup(ctx, cdn.SetupOptions{Domain: domain, IP: ip, Replace: in.Replace, SSL: in.SSL, SelfSigned: v.SelfSigned, WebSockets: in.WebSockets}, port443)
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	if res.Done {
		set.Enabled = true
		if _, err := cdn.Save(s.St, set); err != nil {
			fail(w, 500, err.Error())
			return
		}
		s.St.Log("settings", "%s put %s behind Cloudflare", adminOf(r).Username, domain)
	}
	writeJSON(w, 200, map[string]any{"result": res, "state": s.cdnState()})
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
