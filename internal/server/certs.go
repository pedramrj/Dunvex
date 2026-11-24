package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/pedramrj/dunvex/internal/cdn"
	"github.com/pedramrj/dunvex/internal/domains"
	"github.com/pedramrj/dunvex/internal/tlsmgr"
	"github.com/pedramrj/dunvex/internal/util"
)

// This server's certificate covers every name the Domains page puts in it
// (Let's Encrypt over ACME; each name validated over HTTP on port 80 or
// through Cloudflare's DNS), or its IP address when it has no name. One
// certificate serves every core: Xray, sing-box and the panel pick it by
// file, so no core needs to choose a certificate by name.

// acmeCertPaths is where the issued certificate is kept.
func (s *Server) acmeCertPaths() (cert, key string) {
	d := filepath.Join(s.CertDir(), "dunvex")
	return filepath.Join(d, "fullchain.pem"), filepath.Join(d, "privkey.pem")
}

// acmeDirectory is the certificate authority's ACME directory.
func (s *Server) acmeDirectory() string {
	if s.dom.acmeDir != "" {
		return s.dom.acmeDir
	}
	switch ca := strings.TrimSpace(s.St.GetSetting("acme_ca", "")); ca {
	case "", "letsencrypt":
		return tlsmgr.LetsEncrypt
	case "staging":
		return tlsmgr.LetsEncryptStaging
	default:
		return ca
	}
}

func (s *Server) issuer() *tlsmgr.Issuer {
	is := &tlsmgr.Issuer{Directory: s.acmeDirectory(), HTTPClient: s.dom.acmeHTTP, Email: strings.TrimSpace(s.St.GetSetting("acme_email", "")),
		KeyFile: filepath.Join(s.CertDir(), "acme-account.pem"), HTTP01: s.http01()}
	if t := s.St.GetSetting(cfTokenSetting, ""); t != "" {
		is.DNS01 = &cfDNS{cf: &cdn.Cloudflare{Base: s.CloudflareAPI, Token: t}, wait: s.dom.dnsWait || s.CloudflareAPI == ""}
	}
	return is
}

// cfDNS sets dns-01 records at Cloudflare and waits until its name servers
// give them out.
type cfDNS struct {
	cf   *cdn.Cloudflare
	wait bool
}

func (c *cfDNS) Present(ctx context.Context, fqdn, value string) error {
	return c.cf.Present(ctx, fqdn, value)
}
func (c *cfDNS) CleanUp(ctx context.Context, fqdn, value string) error {
	return c.cf.CleanUp(ctx, fqdn, value)
}

func (c *cfDNS) Wait(ctx context.Context, fqdn, value string) error {
	if !c.wait {
		return nil
	}
	_, ns := zoneOf(ctx, fqdn)
	if len(ns) == 0 {
		time.Sleep(10 * time.Second)
		return nil
	}
	for {
		for _, n := range ns {
			r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(n, "53"))
			}}
			if txt, err := r.LookupTXT(ctx, fqdn); err == nil {
				for _, t := range txt {
					if t == value {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func zoneOf(ctx context.Context, fqdn string) (string, []string) {
	labels := strings.Split(strings.TrimPrefix(fqdn, "_acme-challenge."), ".")
	for i := 0; i+2 <= len(labels); i++ {
		if ns, err := net.DefaultResolver.LookupNS(ctx, strings.Join(labels[i:], ".")); err == nil && len(ns) > 0 {
			out := []string{}
			for _, n := range ns {
				out = append(out, strings.TrimSuffix(n.Host, "."))
			}
			return strings.Join(labels[i:], "."), out
		}
	}
	return "", nil
}

// ---------------------------------------------------------------- the plan

// certPlan is what the certificate should be, against what it is.
type certPlan struct {
	Mode    string       // acme, custom or self
	Names   []string     // the names the certificate should cover, the main one first
	IP      string       // the IP address to certify instead (no names, with IPCert)
	Have    *tlsmgr.Info // the current certificate (nil = none, or not readable)
	Due     bool         // it needs renewing (or there is none of Let's Encrypt)
	Missing []string     // wanted names it does not cover
	DNS     map[string]bool
}

// renewWindow is how long before its end a certificate is renewed: a third
// of its life (30 days of 90), half of it for short-lived ones.
func renewWindow(in *tlsmgr.Info) time.Duration {
	life := in.NotAfter.Sub(in.NotBefore)
	if life < 10*24*time.Hour {
		return life / 2
	}
	return life / 3
}

func (s *Server) certPlan() certPlan {
	p := certPlan{Mode: s.St.GetSetting("cert_mode", "self"), DNS: map[string]bool{}}
	l := domains.Load(s.St)
	p.Names = l.CertNames()
	for _, n := range p.Names {
		p.DNS[n] = l.DNS01(n)
	}
	if len(p.Names) == 0 && l.IPCert {
		p.IP = s.ipToCertify()
	}
	if p.Mode != "acme" && !(p.Mode == "self" && p.IP != "") {
		if f := s.St.GetSetting("cert_file", ""); f != "" && p.Mode == "custom" {
			p.Have = s.certInfo(f)
		}
		return p
	}
	if p.IP != "" {
		p.Mode = "acme" // the IP's certificate is always Let's Encrypt's
	}
	if f := s.St.GetSetting("cert_file", ""); f != "" {
		if in := s.certInfo(f); in != nil && !in.SelfSigned {
			p.Have = in
		}
	}
	wanted := p.Names
	if p.IP != "" {
		wanted = []string{p.IP}
	}
	if p.Have == nil {
		p.Due = len(wanted) > 0
	} else {
		p.Due = time.Until(p.Have.NotAfter) < renewWindow(p.Have)
	}
	for _, n := range wanted {
		if p.Have == nil || !p.Have.Covers(n) {
			p.Missing = append(p.Missing, n)
		}
	}
	return p
}

// ipToCertify is the public IPv4 address links use when there is no name.
func (s *Server) ipToCertify() string {
	if h := strings.TrimSpace(s.St.GetSetting("host", "")); h != "" {
		if ip := net.ParseIP(h); ip != nil && ip.To4() != nil {
			return ip.String()
		}
		return "" // links use a name the panel does not certify
	}
	if s.Sys != nil {
		if ip := net.ParseIP(s.Sys.PublicIP()); ip != nil && ip.To4() != nil && !ip.IsPrivate() && !ip.IsLoopback() {
			return ip.String()
		}
	}
	return ""
}

// order is what to ask the certificate authority for now; ok is false when
// nothing is due (with force: whatever the waits say).
func (p certPlan) order(st *domState, force bool) (o tlsmgr.Order, ok bool) {
	if p.Mode != "acme" {
		return o, false
	}
	now := time.Now().Unix()
	if p.IP != "" {
		if !force && !p.Due && len(p.Missing) == 0 {
			return o, false
		}
		if !force && (st.RetryAt > now || st.Names[p.IP].waitUntil() > now) {
			return o, false
		}
		return tlsmgr.Order{IPs: []string{p.IP}, Profile: tlsmgr.ShortLived}, true
	}
	if len(p.Names) == 0 || (!force && st.RetryAt > now) {
		return o, false
	}
	waiting := func(n string) bool { return !force && st.Names[n].waitUntil() > now }
	var missing []string
	for _, n := range p.Missing {
		if !waiting(n) {
			missing = append(missing, n)
		}
	}
	if !force && !p.Due && len(missing) == 0 {
		return o, false
	}
	o.DNS = p.DNS
	if !p.Due && p.Have != nil {
		o.Need = missing // only new names are wanted: without one of them, keep the certificate
		if force {
			o.Need = p.Missing
		}
	}
	for _, n := range p.Names {
		covered := p.Have != nil && p.Have.Covers(n)
		switch {
		case covered:
			o.Names = append(o.Names, n)
			// a renewal does not drop a name the certificate has (while there is time)
			if p.Have != nil && time.Until(p.Have.NotAfter) > 7*24*time.Hour {
				o.Require = append(o.Require, n)
			}
		case !waiting(n):
			o.Names = append(o.Names, n)
		}
	}
	return o, len(o.Names) > 0
}

// ---------------------------------------------------------------- the job

// certJob is the issuance running in the background.
type certJob struct {
	mu      sync.Mutex
	running bool
	started int64
	names   []string
	kick    chan struct{}
	hook    func(*certRun) // tests watch issuances
}

// certRun is how an issuance went.
type certRun struct {
	At          int64                        `json:"at"`
	OK          bool                         `json:"ok"`
	Names       []string                     `json:"names"`
	Failed      map[string]*tlsmgr.NameError `json:"failed,omitempty"`
	Error       string                       `json:"error,omitempty"`
	RateLimited bool                         `json:"rateLimited,omitempty"`
	NotAfter    int64                        `json:"notAfter,omitempty"`
	Secs        int64                        `json:"secs"`
}

// kickCert has the renewal loop look at the certificate soon (after a change).
func (s *Server) kickCert() {
	if s.dom.noKick {
		return
	}
	s.http01()
	select {
	case s.dom.job.kick <- struct{}{}:
	default:
	}
}

// startCert starts an issuance in the background; false when one runs.
func (s *Server) startCert(force bool) bool {
	s.http01()
	plan := s.certPlan()
	o, ok := plan.order(s.state(), force)
	if !ok {
		return false
	}
	j := &s.dom.job
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		return false
	}
	j.running, j.started, j.names = true, time.Now().Unix(), append(append([]string{}, o.Names...), o.IPs...)
	j.mu.Unlock()
	go func() {
		run := s.issueCert(o)
		j.mu.Lock()
		j.running = false
		hook := j.hook
		j.mu.Unlock()
		if hook != nil {
			hook(run)
		}
	}()
	return true
}

// issueCert asks for the certificate and puts it in place.
func (s *Server) issueCert(o tlsmgr.Order) *certRun {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	got, err := s.issuer().Issue(ctx, o)
	run := &certRun{At: time.Now().Unix(), Secs: int64(time.Since(start).Seconds())}
	all := append(append([]string{}, o.Names...), o.IPs...)
	var failed map[string]*tlsmgr.NameError
	if err != nil {
		run.Error = err.Error()
		var ie *tlsmgr.IssueError
		if errors.As(err, &ie) {
			run.Error, failed, run.RateLimited = ie.Msg, ie.Failed, ie.RateLimited
			switch {
			case ie.RetryAfter > 0:
				s.editState(func(st *domState) { st.RetryAt = time.Now().Add(ie.RetryAfter).Unix() })
			case ie.RateLimited:
				s.editState(func(st *domState) { st.RetryAt = time.Now().Add(3 * time.Hour).Unix() })
			case len(ie.Failed) == 0:
				// a failure no name explains (the CA, the network): not again before an hour
				s.editState(func(st *domState) { st.RetryAt = time.Now().Add(time.Hour).Unix() })
			}
		}
		log.Printf("certificate for %s: %v", strings.Join(all, ", "), err)
	} else {
		failed = got.Failed
		cert, key := s.acmeCertPaths()
		if werr := tlsmgr.WriteFiles(cert, key, got.CertPEM, got.KeyPEM); werr != nil {
			run.Error = werr.Error()
		} else {
			s.St.SetSetting("cert_file", cert)
			s.St.SetSetting("key_file", key)
			if s.St.GetSetting("cert_mode", "self") != "custom" {
				s.St.SetSetting("cert_mode", "acme")
			}
			run.OK, run.Names, run.NotAfter = true, append(got.Names, got.IPs...), got.NotAfter.Unix()
			s.St.Log("tls", "certificate issued for %s", strings.Join(run.Names, ", "))
			if s.Mgr != nil {
				s.Mgr.CertsRenewed()
			}
		}
	}
	run.Failed = failed
	now := time.Now().Unix()
	s.editState(func(st *domState) {
		st.Last = run
		for _, n := range all {
			if f := failed[n]; f != nil {
				ns := st.Names[n]
				if ns == nil {
					ns = &nameState{}
					st.Names[n] = ns
				}
				ns.Code, ns.Detail, ns.FailedAt, ns.Fails = f.Code, f.Detail, now, ns.Fails+1
				key := "name:" + n
				if st.Alerted[key] != f.Code+f.Detail {
					st.Alerted[key] = f.Code + f.Detail
					s.St.Log("tls", "no certificate for %s yet: %s", n, f.Detail)
				}
			} else if run.OK {
				st.Names[n] = &nameState{OKAt: now}
				delete(st.Alerted, "name:"+n)
			}
		}
		if !run.OK && len(failed) == 0 && st.Alerted["issue"] != run.Error {
			st.Alerted["issue"] = run.Error
			s.St.Log("tls", "the certificate could not be renewed: %s", run.Error)
		}
		if run.OK {
			delete(st.Alerted, "issue")
		}
	})
	return run
}

// ---------------------------------------------------------------- the loop

// RunCertRenewal keeps the certificate fresh (checked every hour, at once
// after a change of the domains), warns before a certificate or a domain
// ends, and checks every name's DNS every six hours.
func (s *Server) RunCertRenewal(stop <-chan struct{}) {
	s.http01()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	first := time.NewTimer(30 * time.Second)
	defer first.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		case <-first.C:
		case <-s.dom.job.kick:
			// a change of the domains: wait a moment for more of them
			select {
			case <-stop:
				return
			case <-time.After(3 * time.Second):
			}
		}
		s.startCert(false)
		s.watchDomains()
	}
}

// watchDomains runs the slower checks when they are due: DNS (every 6 h),
// registrations (daily), and the expiry warnings.
func (s *Server) watchDomains() {
	now := time.Now()
	if st := s.state(); now.Unix()-st.DNSAt > 6*3600 {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		s.runDNSChecks(ctx, nil)
		cancel()
		s.editState(func(st *domState) { st.DNSAt = now.Unix() })
		s.dnsAlerts()
	}
	s.checkRDAP()
	s.expiryAlerts()
}

// warnDays are the days before an end that are warned about.
var warnDays = []int{14, 7, 3, 1, 0}

// threshold is the warning a time left falls under (-1 = none yet).
func threshold(left time.Duration, days []int) int {
	d := int(left.Hours() / 24)
	if left < 0 {
		return 0
	}
	got := -1
	for _, w := range days {
		if d < w || (w == 0 && left < 24*time.Hour) {
			got = w
		}
	}
	return got
}

// expiryAlerts warns once per step (14, 7, 3, 1 days, then expired) before
// this server's certificate ends: an automatic one only reaches 14 days when
// its renewal keeps failing; the owner's own files are never renewed here.
func (s *Server) expiryAlerts() {
	plan := s.certPlan()
	in := plan.Have
	if in == nil {
		return
	}
	days := warnDays
	if in.NotAfter.Sub(in.NotBefore) < 10*24*time.Hour {
		days = []int{1, 0} // short-lived: only when the renewal is about to miss it
	}
	left := time.Until(in.NotAfter)
	w := threshold(left, days)
	what := strings.Join(append(append([]string{}, in.Names...), in.IPs...), ", ")
	key := fmt.Sprintf("%d:%d", in.NotAfter.Unix(), w)
	s.editState(func(st *domState) {
		if w < 0 || st.Alerted["cert"] == key {
			return
		}
		st.Alerted["cert"] = key
		switch {
		case left <= 0:
			s.St.Log("tls", "the certificate for %s has expired", what)
		default:
			s.St.Log("tls", "the certificate for %s expires in %d days", what, int(left.Hours()/24))
		}
	})
}

// dnsAlerts warns when a name stops pointing where its role needs (and when
// it is right again).
func (s *Server) dnsAlerts() {
	l := domains.Load(s.St)
	s.editState(func(st *domState) {
		for _, d := range l.Domains {
			c := st.DNS[d.Name]
			if c == nil {
				continue
			}
			key := "dns:" + d.Name
			prev := st.Alerted[key]
			switch {
			case c.Status == "bad" && len(c.Problems) > 0 && prev != c.Problems[0]:
				st.Alerted[key] = c.Problems[0]
				s.St.Log("tls", "domain %s: %s", d.Name, dnsProblemText(c.Problems[0], c))
			case c.Status != "bad" && prev != "":
				delete(st.Alerted, key)
				s.St.Log("tls", "domain %s points where it should again", d.Name)
			}
		}
	})
}

func dnsProblemText(p string, c *domains.Check) string {
	ips := []string{}
	for _, a := range append(append([]domains.Addr{}, c.A...), c.AAAA...) {
		ips = append(ips, a.IP)
	}
	switch p {
	case domains.ProbNotFound:
		return "it has no DNS record (or the domain has ended)"
	case domains.ProbNotHere:
		return "it points elsewhere (" + strings.Join(ips, ", ") + ")"
	case domains.ProbProxied:
		return "it is proxied by Cloudflare; turn the orange cloud off for a direct name"
	case domains.ProbNotProxied:
		return "it points straight at this server; turn Cloudflare's proxy on for a CDN name"
	case domains.ProbNotCDN:
		return "it does not point at Cloudflare (" + strings.Join(ips, ", ") + ")"
	}
	return p
}

// ---------------------------------------------------------------- registration (RDAP)

// rdapInfo is when a registered domain ends at its registry.
type rdapInfo struct {
	Expires   int64  `json:"expires,omitempty"`
	Registrar string `json:"registrar,omitempty"`
	At        int64  `json:"at"`
	Error     string `json:"error,omitempty"`
}

// registered is the domain a name was registered under (example.com for
// cdn.example.com, example.co.uk for vpn.example.co.uk).
func registered(name string) string {
	d, err := publicsuffix.EffectiveTLDPlusOne(name)
	if err != nil {
		return ""
	}
	return d
}

// checkRDAP looks up, once a day, when each registered domain of the list
// ends (many registries publish it over RDAP; .ir does not).
func (s *Server) checkRDAP() {
	l := domains.Load(s.St)
	want := map[string]bool{}
	for _, d := range l.Domains {
		if r := registered(d.Name); r != "" {
			want[r] = true
		}
	}
	st := s.state()
	now := time.Now().Unix()
	var todo []string
	for r := range want {
		if i := st.RDAP[r]; i == nil || now-i.At > 24*3600 {
			todo = append(todo, r)
		}
	}
	sort.Strings(todo)
	got := map[string]*rdapInfo{}
	for _, r := range todo {
		got[r] = s.lookupRDAP(r)
	}
	s.editState(func(st *domState) {
		for r, i := range got {
			st.RDAP[r] = i
		}
		for r := range st.RDAP {
			if !want[r] {
				delete(st.RDAP, r)
			}
		}
		for r, i := range st.RDAP {
			if i.Expires == 0 {
				continue
			}
			left := time.Until(time.Unix(i.Expires, 0))
			w := threshold(left, []int{30, 14, 7, 3, 1, 0})
			key := fmt.Sprintf("%d:%d", i.Expires, w)
			if w < 0 || st.Alerted["rdap:"+r] == key {
				continue
			}
			st.Alerted["rdap:"+r] = key
			if left <= 0 {
				s.St.Log("tls", "the domain %s has expired at its registrar", r)
			} else {
				s.St.Log("tls", "the domain %s expires at its registrar in %d days; renew it there", r, int(left.Hours()/24))
			}
		}
	})
}

func (s *Server) lookupRDAP(domain string) *rdapInfo {
	base := s.dom.rdapBase
	if base == "" {
		base = "https://rdap.org"
	}
	info := &rdapInfo{At: time.Now().Unix()}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(base, "/")+"/domain/"+domain, nil)
	req.Header.Set("Accept", "application/rdap+json, application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		info.Error = "unreachable"
		return info
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		info.Error = fmt.Sprintf("HTTP %d", resp.StatusCode) // the registry has no RDAP (404) or refused
		return info
	}
	var r struct {
		Events []struct {
			Action string `json:"eventAction"`
			Date   string `json:"eventDate"`
		} `json:"events"`
		Entities []struct {
			Roles []string `json:"roles"`
			VCard []any    `json:"vcardArray"`
		} `json:"entities"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		info.Error = "unreadable"
		return info
	}
	for _, e := range r.Events {
		if e.Action == "expiration" {
			if t, err := time.Parse(time.RFC3339, e.Date); err == nil {
				info.Expires = t.Unix()
			}
		}
	}
	for _, e := range r.Entities {
		for _, role := range e.Roles {
			if role == "registrar" {
				info.Registrar = vcardName(e.VCard)
			}
		}
	}
	return info
}

// vcardName is the "fn" of a jCard.
func vcardName(v []any) string {
	if len(v) < 2 {
		return ""
	}
	props, _ := v[1].([]any)
	for _, p := range props {
		f, _ := p.([]any)
		if len(f) >= 4 && f[0] == "fn" {
			s, _ := f[3].(string)
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------- the view and handlers

type certView struct {
	Mode       string       `json:"mode"` // acme, custom or self
	CA         string       `json:"ca"`   // letsencrypt, staging or a directory
	Email      string       `json:"email"`
	File       string       `json:"file"`
	KeyFile    string       `json:"keyFile"`
	Names      []string     `json:"names"` // what it should cover
	IP         string       `json:"ip,omitempty"`
	Have       *tlsmgr.Info `json:"have,omitempty"`
	Due        bool         `json:"due"`
	Missing    []string     `json:"missing"`
	RenewAt    int64        `json:"renewAt,omitempty"` // when it is renewed by itself
	Running    bool         `json:"running"`
	Started    int64        `json:"started,omitempty"`
	Working    []string     `json:"working,omitempty"` // the names being validated
	Last       *certRun     `json:"last,omitempty"`
	RetryAt    int64        `json:"retryAt,omitempty"`
	Port80     string       `json:"port80,omitempty"` // what holds port 80 when HTTP validation cannot use it
	SelfSigned bool         `json:"selfSigned"`
}

func (s *Server) certView(p certPlan, st *domState) certView {
	v := certView{Mode: p.Mode, CA: s.St.GetSetting("acme_ca", "letsencrypt"), Email: s.St.GetSetting("acme_email", ""),
		File: s.St.GetSetting("cert_file", ""), KeyFile: s.St.GetSetting("key_file", ""), Names: p.Names, IP: p.IP,
		Have: p.Have, Due: p.Due, Missing: p.Missing, Last: st.Last, RetryAt: st.RetryAt}
	if v.CA == "" {
		v.CA = "letsencrypt"
	}
	if v.Names == nil {
		v.Names = []string{}
	}
	if v.Missing == nil {
		v.Missing = []string{}
	}
	env := s.Env()
	v.SelfSigned = env.SelfSigned
	if p.Have == nil && env.CertFile != "" {
		v.Have = s.certInfo(env.CertFile) // the self-signed one in use
	}
	if p.Have != nil && p.Mode == "acme" {
		v.RenewAt = p.Have.NotAfter.Add(-renewWindow(p.Have)).Unix()
	}
	j := &s.dom.job
	j.mu.Lock()
	v.Running, v.Started, v.Working = j.running, j.started, j.names
	j.mu.Unlock()
	if p.Mode == "acme" && s.St.GetSetting("panel_port", "") != "80" {
		if ln, err := net.Listen("tcp", ":80"); err != nil {
			v.Port80 = s.portOwner(80)
		} else {
			ln.Close()
		}
	}
	return v
}

// portOwner names the protocol on a port of this server ("" = another program).
func (s *Server) portOwner(port int) string {
	ins, _ := s.St.Inbounds()
	for _, in := range ins {
		if in.NodeID == 0 && in.Port == port && in.Enabled {
			if in.Remark != "" {
				return in.Remark
			}
			return in.Preset
		}
	}
	if !util.FreePort(port) {
		return "?"
	}
	return ""
}

func (s *Server) getCert(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.certView(s.certPlan(), s.state()))
}

// postCert asks for the certificate now, whatever the waits after failures say.
func (s *Server) postCert(w http.ResponseWriter, r *http.Request) {
	plan := s.certPlan()
	if plan.Mode != "acme" && plan.IP == "" {
		if plan.Mode == "custom" {
			fail(w, 400, "the certificate is your own files: switch to Let's Encrypt first")
			return
		}
		// self-signed so far: the first name makes it Let's Encrypt's
		s.St.SetSetting("cert_mode", "acme")
		plan = s.certPlan()
	}
	if len(plan.Names) == 0 && plan.IP == "" {
		fail(w, 400, "add a domain (with its certificate on) first, or turn on the certificate for the server's IP")
		return
	}
	s.startCert(true)
	s.St.Log("tls", "%s asked for a certificate", adminOf(r).Username)
	writeJSON(w, 200, s.certView(s.certPlan(), s.state()))
}

// obtainCert is the older "get a certificate" of Settings: it now asks for
// the certificate of every name in the list, and waits for it.
func (s *Server) obtainCert(w http.ResponseWriter, r *http.Request) {
	l := domains.Load(s.St)
	main := l.Main()
	if main == nil {
		fail(w, 400, "set a domain first")
		return
	}
	if !main.Cert {
		main.Cert = true
		if _, err := domains.Save(s.St, l); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	if s.St.GetSetting("cert_mode", "self") != "custom" {
		s.St.SetSetting("cert_mode", "acme")
	}
	plan := s.certPlan()
	o, ok := plan.order(s.state(), true)
	if !ok {
		fail(w, 400, "nothing to certify")
		return
	}
	run := s.issueCert(o)
	if !run.OK {
		msg := run.Error
		if f := run.Failed[main.Name]; f != nil {
			msg = f.Detail
		}
		fail(w, 400, msg)
		return
	}
	if s.Mgr != nil {
		s.Mgr.Apply()
	}
	cert, key := s.acmeCertPaths()
	writeJSON(w, 200, map[string]string{"cert": cert, "key": key})
}
