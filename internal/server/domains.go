package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/cdn"
	"github.com/pedramrj/dunvex/internal/domains"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/tlsmgr"
)

// The Cloudflare token kept for DNS validation and one-click records, when
// the owner chooses to keep it (it is never sent back in full).
const cfTokenSetting = "cf_token"

// domainRuntime is what the Domains page needs while the panel runs.
type domainRuntime struct {
	// tests point these elsewhere
	acmeDir  string       // the ACME directory ("" = the acme_ca setting)
	acmeHTTP *http.Client // the ACME API's client
	httpAddr string       // where HTTP validation listens ("" = :80)
	resolver domains.Resolver
	rdapBase string // "" = https://rdap.org
	dnsWait  bool   // wait for dns-01 records on Cloudflare's name servers (on for the real API)
	noKick   bool   // saving does not start an issuance by itself

	stMu   sync.Mutex // the state setting
	once   sync.Once
	http01 *tlsmgr.HTTP01
	job    certJob

	certMu  sync.Mutex
	certAt  time.Time
	certFor string
	cert    *tlsmgr.Info
}

func (s *Server) http01() *tlsmgr.HTTP01 {
	s.dom.once.Do(func() {
		s.dom.http01 = &tlsmgr.HTTP01{Addr: s.dom.httpAddr, Shared: func() bool {
			// the panel answers on port 80 itself (plain HTTP goes to it)
			return s.St.GetSetting("panel_port", "2096") == "80" && s.St.GetSetting("panel_tls", "false") != "true"
		}}
		s.dom.job.kick = make(chan struct{}, 1)
	})
	return s.dom.http01
}

func (s *Server) resolver() domains.Resolver {
	if s.dom.resolver != nil {
		return s.dom.resolver
	}
	return domains.PublicResolver()
}

// certInfo reads a certificate file, again only when it changed.
func (s *Server) certInfo(path string) *tlsmgr.Info {
	if path == "" {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	s.dom.certMu.Lock()
	defer s.dom.certMu.Unlock()
	if s.dom.certFor == path && s.dom.certAt.Equal(fi.ModTime()) {
		return s.dom.cert
	}
	info, err := tlsmgr.Read(path)
	if err != nil {
		return nil
	}
	s.dom.certFor, s.dom.certAt, s.dom.cert = path, fi.ModTime(), info
	return info
}

// InitDomains moves the older domain settings into the list (once) and
// takes over ones changed outside the panel. Run it before the first apply.
func (s *Server) InitDomains() {
	msg, err := domains.Migrate(s.St)
	if err != nil {
		s.St.Log("tls", "the domains could not be moved to the Domains page: %v", err)
		return
	}
	if msg != "" && len(domains.Load(s.St).Domains) > 0 {
		s.St.Log("settings", "%s", msg)
	}
}

// afterDomains is what follows a change of the domains: the cores (the main
// name is in their TLS settings), the certificate, and the config check.
func (s *Server) afterDomains() {
	if s.Mgr != nil {
		s.Mgr.RequestApply()
	}
	s.kickCert()
	if s.Mgr != nil && engine.HealthUser(s.St) != nil {
		s.CheckConfigs()
	}
}

// ---------------------------------------------------------------- state

// domState is what the checks and the certificate found, kept across restarts.
type domState struct {
	Names   map[string]*nameState     `json:"names"`             // per name (and IP) in the certificate
	DNS     map[string]*domains.Check `json:"dns"`               // the last DNS check of each name
	RDAP    map[string]*rdapInfo      `json:"rdap"`              // registration of each registered domain
	Alerted map[string]string         `json:"alerted"`           // what was last warned about, so a warning is sent once
	Last    *certRun                  `json:"last,omitempty"`    // the last issuance
	RetryAt int64                     `json:"retryAt,omitempty"` // no automatic try before (rate limits)
	DNSAt   int64                     `json:"dnsAt,omitempty"`   // the last automatic DNS check
}

// nameState is how one name did at the certificate authority.
type nameState struct {
	Code     string `json:"code,omitempty"`
	Detail   string `json:"detail,omitempty"`
	FailedAt int64  `json:"failedAt,omitempty"`
	Fails    int    `json:"fails,omitempty"`
	OKAt     int64  `json:"okAt,omitempty"`
}

// waitUntil is when a failed name is tried again by itself (1 h, doubling to a day).
func (n *nameState) waitUntil() int64 {
	if n == nil || n.Fails == 0 {
		return 0
	}
	d := time.Hour << min(n.Fails-1, 5)
	if d > 24*time.Hour {
		d = 24 * time.Hour
	}
	return n.FailedAt + int64(d/time.Second)
}

func (s *Server) loadState() *domState {
	st := &domState{}
	json.Unmarshal([]byte(s.St.GetSetting(domains.StateSetting, "")), st)
	if st.Names == nil {
		st.Names = map[string]*nameState{}
	}
	if st.DNS == nil {
		st.DNS = map[string]*domains.Check{}
	}
	if st.RDAP == nil {
		st.RDAP = map[string]*rdapInfo{}
	}
	if st.Alerted == nil {
		st.Alerted = map[string]string{}
	}
	return st
}

// editState changes the saved state under its lock.
func (s *Server) editState(f func(st *domState)) {
	s.dom.stMu.Lock()
	defer s.dom.stMu.Unlock()
	st := s.loadState()
	f(st)
	b, _ := json.Marshal(st)
	s.St.SetSetting(domains.StateSetting, string(b))
}

func (s *Server) state() *domState {
	s.dom.stMu.Lock()
	defer s.dom.stMu.Unlock()
	return s.loadState()
}

// ---------------------------------------------------------------- view

type domainUse struct {
	Kind  string   `json:"kind"` // tls, links, panel, sub, cdn, copies, protocol
	Count int      `json:"count,omitempty"`
	Names []string `json:"names,omitempty"`
}

type domainCert struct {
	Wanted   bool   `json:"wanted"`  // the name should be in the certificate
	Covered  bool   `json:"covered"` // the current certificate is for it
	Code     string `json:"code,omitempty"`
	Detail   string `json:"detail,omitempty"`
	FailedAt int64  `json:"failedAt,omitempty"`
	RetryAt  int64  `json:"retryAt,omitempty"`
}

type domainRow struct {
	domains.Domain
	Main        bool           `json:"main"`
	Check       *domains.Check `json:"check,omitempty"`
	CertState   domainCert     `json:"certState"`
	Uses        []domainUse    `json:"uses"`
	ConfigCount int            `json:"configCount"` // configs a user with every protocol gets through it
	Registered  string         `json:"registered,omitempty"`
}

type nodeDomain struct {
	ID         int64          `json:"id"`
	Name       string         `json:"name"`
	Domain     string         `json:"domain"`
	Address    string         `json:"address"`
	RealCert   bool           `json:"realCert"`
	CertExpiry int64          `json:"certExpiry,omitempty"`
	Online     bool           `json:"online"`
	Check      *domains.Check `json:"check,omitempty"`
}

type domainsView struct {
	Domains   []domainRow          `json:"domains"`
	CDNDirect bool                 `json:"cdnDirect"`
	IPCert    bool                 `json:"ipCert"`
	Server    serverInfo           `json:"server"`
	Cert      certView             `json:"cert"`
	CF        cfView               `json:"cloudflare"`
	Nodes     []nodeDomain         `json:"nodes"`
	RDAP      map[string]*rdapInfo `json:"rdap"`
	Problems  int                  `json:"problems"` // names whose DNS or certificate needs the owner
}

type serverInfo struct {
	IPv4      []string `json:"ipv4"`
	IPv6      []string `json:"ipv6"`
	PublicIP  string   `json:"publicIp"`
	Host      string   `json:"host"`      // the address set for links ("" = the main domain, else the IP)
	LinkHost  string   `json:"linkHost"`  // the address links use
	PanelPort int      `json:"panelPort"` // the panel's port
	PanelTLS  bool     `json:"panelTls"`
	SubBase   string   `json:"subBase"` // the subscription links' base
}

type cfView struct {
	Saved bool   `json:"saved"`
	Hint  string `json:"hint,omitempty"` // the start and end of the kept token
}

func (s *Server) serverAddrs() domains.Server {
	extra := []string{}
	if s.Sys != nil {
		extra = append(extra, s.Sys.PublicIP())
	}
	if h := strings.TrimSpace(s.St.GetSetting("host", "")); net.ParseIP(h) != nil {
		extra = append(extra, h)
	}
	return domains.LocalAddresses(extra...)
}

func (s *Server) domainsState() domainsView {
	l := domains.Load(s.St)
	st := s.state()
	env := s.Env()
	plan := s.certPlan()
	addrs := s.serverAddrs()
	port, _ := strconv.Atoi(s.St.GetSetting("panel_port", "2096"))
	v := domainsView{Domains: []domainRow{}, CDNDirect: l.CDNDirect, IPCert: l.IPCert, Nodes: []nodeDomain{}, RDAP: st.RDAP,
		Server: serverInfo{IPv4: or2(addrs.IPv4), IPv6: or2(addrs.IPv6), Host: strings.TrimSpace(s.St.GetSetting("host", "")),
			LinkHost: env.Host, PanelPort: port, PanelTLS: s.St.GetSetting("panel_tls", "false") == "true", SubBase: s.PublicBase(nil)},
		Cert: s.certView(plan, st)}
	if s.Sys != nil {
		v.Server.PublicIP = s.Sys.PublicIP()
	}
	if t := s.St.GetSetting(cfTokenSetting, ""); t != "" {
		v.CF = cfView{Saved: true, Hint: maskToken(t)}
	}
	uses := s.domainUses(l)
	main := l.MainName()
	for _, d := range l.Domains {
		row := domainRow{Domain: d, Main: d.Name == main, Check: st.DNS[d.Name], Uses: uses[d.Name].uses, ConfigCount: uses[d.Name].configs}
		if row.Uses == nil {
			row.Uses = []domainUse{}
		}
		if row.Addresses == nil {
			row.Addresses = []cdn.Address{}
		}
		row.CertState = domainCert{Wanted: d.Cert && plan.Mode == "acme"}
		if plan.Have != nil {
			row.CertState.Covered = plan.Have.Covers(d.Name)
		}
		if ns := st.Names[d.Name]; ns != nil && ns.Fails > 0 && !row.CertState.Covered {
			row.CertState.Code, row.CertState.Detail, row.CertState.FailedAt, row.CertState.RetryAt = ns.Code, ns.Detail, ns.FailedAt, ns.waitUntil()
		}
		row.Registered = registered(d.Name)
		if (row.Check != nil && row.Check.Status == "bad") || (row.CertState.Wanted && !row.CertState.Covered) {
			v.Problems++
		}
		v.Domains = append(v.Domains, row)
	}
	nodes, _ := s.St.Nodes()
	for _, n := range nodes {
		if n.Domain == "" {
			continue
		}
		nv := s.viewNode(n, nil)
		nd := nodeDomain{ID: n.ID, Name: n.Name, Domain: n.Domain, Address: n.Address, RealCert: n.RealCert, Online: nv.Online, Check: st.DNS["node:"+n.Domain]}
		if nv.Status != nil {
			nd.CertExpiry = nv.Status.CertExpiry
		}
		v.Nodes = append(v.Nodes, nd)
	}
	return v
}

func or2(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func maskToken(t string) string {
	if len(t) <= 10 {
		return strings.Repeat("•", 8)
	}
	return t[:4] + strings.Repeat("•", 8) + t[len(t)-4:]
}

type nameUses struct {
	uses    []domainUse
	configs int
}

// domainUses says what uses each name: which protocols, links and pages.
func (s *Server) domainUses(l domains.List) map[string]nameUses {
	out := map[string]nameUses{}
	add := func(name string, u domainUse) {
		x := out[name]
		x.uses = append(x.uses, u)
		out[name] = x
	}
	ins, _ := s.St.Inbounds()
	main := l.MainName()
	host := strings.TrimSpace(s.St.GetSetting("host", ""))
	var tlsNames, all, cdnNames, copyTLS, copyAll []string
	for _, in := range ins {
		p := protocol.Get(in.Preset)
		if p == nil || in.NodeID != 0 || !in.Enabled {
			continue
		}
		name := in.Remark
		if name == "" {
			name = p.Name
		}
		own := strings.TrimSpace(in.Str("address")) != ""
		direct := p.Group != "cdn" || len(l.CDNs()) == 0 || l.CDNDirect // users get the config straight to the server
		if !own && direct {
			all = append(all, name)
		}
		sni := domains.Normalize(in.Str("sni"))
		if p.Security == "tls" && (sni == "" || sni == main) && direct {
			tlsNames = append(tlsNames, name)
		}
		if p.Group == "cdn" {
			cdnNames = append(cdnNames, name)
		}
		if !own && direct {
			copyAll = append(copyAll, name)
			if p.Security == "tls" {
				copyTLS = append(copyTLS, name)
			}
		}
		// a protocol that names the domain itself
		for _, k := range []string{"address", "sni", "host"} {
			if v := domains.Normalize(in.Str(k)); v != "" && l.Find(v) != nil && !(k == "sni" && v == main && p.Security == "tls") {
				add(v, domainUse{Kind: "protocol", Names: []string{name + " · " + k}})
			}
		}
	}
	if main != "" {
		if len(tlsNames) > 0 {
			add(main, domainUse{Kind: "tls", Count: len(tlsNames), Names: tlsNames})
		}
		if host == "" || domains.Normalize(host) == main {
			add(main, domainUse{Kind: "links", Count: len(all), Names: all})
			x := out[main]
			x.configs = len(all)
			out[main] = x
		}
		if s.St.GetSetting("panel_tls", "false") == "true" {
			add(main, domainUse{Kind: "panel"})
		}
	}
	for i, d := range l.CDNs() {
		if len(cdnNames) == 0 {
			continue
		}
		add(d.Name, domainUse{Kind: "cdn", Count: len(cdnNames) * (1 + len(d.Addresses)), Names: cdnNames})
		x := out[d.Name]
		x.configs = len(cdnNames) * (1 + len(d.Addresses))
		out[d.Name] = x
		_ = i
	}
	for _, d := range l.Mirrors() {
		list := copyTLS
		if d.All {
			list = copyAll
		}
		if len(list) == 0 {
			continue
		}
		add(d.Name, domainUse{Kind: "copies", Count: len(list), Names: list})
		x := out[d.Name]
		x.configs = len(list)
		out[d.Name] = x
	}
	if sub := l.SubName(); sub != "" && s.St.GetSetting("sub_base_url", "") == "" {
		add(sub, domainUse{Kind: "sub"})
	}
	return out
}

// followMain moves the TLS protocols of this server that use the old main
// domain as their SNI to the new one (a protocol with a name of its own keeps it).
func (s *Server) followMain(from, to string) {
	if from == "" || to == "" || from == to {
		return
	}
	ins, _ := s.St.Inbounds()
	n := 0
	for _, in := range ins {
		p := protocol.Get(in.Preset)
		if p == nil || in.NodeID != 0 || p.Security != "tls" || domains.Normalize(in.Str("sni")) != from {
			continue
		}
		in.Params["sni"] = to
		if err := s.St.UpdateInbound(in); err == nil {
			n++
		}
	}
	if n > 0 {
		s.St.Log("inbound", "%d TLS protocol(s) now use %s as their name", n, to)
	}
}

// ---------------------------------------------------------------- handlers

func (s *Server) getDomains(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.domainsState())
}

type domainsIn struct {
	Domains   []domains.Domain `json:"domains"`
	CDNDirect bool             `json:"cdnDirect"`
	IPCert    bool             `json:"ipCert"`
	CertMode  string           `json:"certMode,omitempty"` // acme, custom or self ("" = unchanged)
	Email     *string          `json:"email,omitempty"`
	CA        *string          `json:"ca,omitempty"` // letsencrypt, staging or a directory URL
	CertFile  *string          `json:"certFile,omitempty"`
	KeyFile   *string          `json:"keyFile,omitempty"`
	Host      *string          `json:"host,omitempty"` // the address in links
}

func (s *Server) putDomains(w http.ResponseWriter, r *http.Request) {
	var in domainsIn
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	old := domains.Load(s.St)
	l := old
	l.Domains, l.CDNDirect, l.IPCert = in.Domains, in.CDNDirect, in.IPCert
	if err := l.Clean(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	for _, d := range l.Domains {
		if d.DNS01 && s.St.GetSetting(cfTokenSetting, "") == "" {
			fail(w, 400, d.Name+": DNS validation needs a Cloudflare token kept on this page")
			return
		}
	}
	switch in.CertMode {
	case "", "acme", "self":
	case "custom":
		cf, kf := s.St.GetSetting("cert_file", ""), s.St.GetSetting("key_file", "")
		if in.CertFile != nil {
			cf = strings.TrimSpace(*in.CertFile)
		}
		if in.KeyFile != nil {
			kf = strings.TrimSpace(*in.KeyFile)
		}
		if _, err := os.Stat(cf); err != nil {
			fail(w, 400, "file not found: "+cf)
			return
		}
		if _, err := os.Stat(kf); err != nil {
			fail(w, 400, "file not found: "+kf)
			return
		}
		s.St.SetSetting("cert_file", cf)
		s.St.SetSetting("key_file", kf)
	default:
		fail(w, 400, "unknown certificate mode")
		return
	}
	if in.CA != nil {
		ca := strings.TrimSpace(*in.CA)
		if ca != "" && ca != "letsencrypt" && ca != "staging" && !strings.HasPrefix(ca, "https://") {
			fail(w, 400, "the certificate authority is letsencrypt, staging or an https:// ACME directory")
			return
		}
		s.St.SetSetting("acme_ca", ca)
	}
	if in.Email != nil {
		s.St.SetSetting("acme_email", strings.TrimSpace(*in.Email))
	}
	if in.Host != nil {
		s.St.SetSetting("host", strings.TrimSpace(*in.Host))
	}
	if in.CertMode != "" {
		if in.CertMode != "custom" && s.St.GetSetting("cert_mode", "self") == "custom" {
			// back from the owner's own files: the panel's certificate is used again
			cf, kf := s.acmeCertPaths()
			if in.CertMode == "self" {
				cf, kf = "", ""
			}
			s.St.SetSetting("cert_file", cf)
			s.St.SetSetting("key_file", kf)
		}
		if in.CertMode == "self" {
			s.St.SetSetting("cert_file", "")
			s.St.SetSetting("key_file", "")
		}
		s.St.SetSetting("cert_mode", in.CertMode)
	}
	if old.IPCert && !l.IPCert && len(l.CertNames()) == 0 && s.St.GetSetting("cert_mode", "self") == "acme" {
		// the IP's certificate is no longer renewed: back to the pinned self-signed one now
		s.St.SetSetting("cert_mode", "self")
		s.St.SetSetting("cert_file", "")
		s.St.SetSetting("key_file", "")
	}
	if _, err := domains.Save(s.St, l); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.St.Log("settings", "%s changed the domains (%d)", adminOf(r).Username, len(l.Domains))
	if old.MainName() != l.MainName() {
		s.followMain(old.MainName(), l.MainName())
	}
	s.afterDomains()
	writeJSON(w, 200, s.domainsState())
}

// checkDomains looks every name (or the ones asked for) up now.
func (s *Server) checkDomains(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Names []string `json:"names"`
	}
	if r.ContentLength > 0 {
		readJSON(r, &in)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	checks := s.runDNSChecks(ctx, in.Names)
	writeJSON(w, 200, map[string]any{"checks": checks, "state": s.domainsState()})
}

// runDNSChecks checks names (nil = every name and every node's domain) and
// keeps the results.
func (s *Server) runDNSChecks(ctx context.Context, names []string) map[string]*domains.Check {
	l := domains.Load(s.St)
	type job struct{ key, name, role string }
	var jobs []job
	want := map[string]bool{}
	for _, n := range names {
		want[domains.Normalize(n)] = true
	}
	for _, d := range l.Domains {
		if len(want) == 0 || want[d.Name] {
			jobs = append(jobs, job{d.Name, d.Name, d.Role})
		}
	}
	if len(want) == 0 {
		nodes, _ := s.St.Nodes()
		for _, n := range nodes {
			if n.Domain != "" {
				jobs = append(jobs, job{"node:" + n.Domain, n.Domain, "node"})
			}
		}
	} else {
		for n := range want {
			if l.Find(n) == nil {
				jobs = append(jobs, job{n, n, ""}) // a name not in the list yet (the add dialog)
			}
		}
	}
	me := s.serverAddrs()
	nodeAddrs := map[string]domains.Server{}
	nodes, _ := s.St.Nodes()
	for _, n := range nodes {
		nodeAddrs[n.Domain] = domains.Server{IPv4: []string{n.Address}}
	}
	res := s.resolver()
	out := map[string]*domains.Check{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	limit := make(chan struct{}, 6)
	for _, j := range jobs {
		j := j
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			addrs, role := me, j.role
			if j.role == "node" {
				addrs, role = nodeAddrs[j.name], domains.Direct
				if ips, err := net.DefaultResolver.LookupIPAddr(ctx, nodeAddrs[j.name].IPv4[0]); err == nil {
					for _, ip := range ips {
						addrs.IPv4 = append(addrs.IPv4, ip.IP.String())
					}
				}
			}
			c := domains.Lookup(ctx, res, j.name, role, addrs)
			mu.Lock()
			out[j.key] = &c
			mu.Unlock()
		}()
	}
	wg.Wait()
	s.editState(func(st *domState) {
		for k, c := range out {
			if l.Find(k) != nil || strings.HasPrefix(k, "node:") {
				st.DNS[k] = c
			}
		}
		// forget names no longer in the list
		for k := range st.DNS {
			if !strings.HasPrefix(k, "node:") && l.Find(k) == nil {
				delete(st.DNS, k)
			}
		}
	})
	return out
}

// putCloudflare keeps (or forgets) the Cloudflare token after checking it.
func (s *Server) putCloudflare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	in.Token = strings.TrimSpace(in.Token)
	if in.Token == "" {
		s.St.SetSetting(cfTokenSetting, "")
		// names that were validated through DNS go back to HTTP
		l := domains.Load(s.St)
		changed := false
		for i := range l.Domains {
			if l.Domains[i].DNS01 {
				l.Domains[i].DNS01, changed = false, true
			}
		}
		if changed {
			domains.Save(s.St, l)
		}
		s.St.Log("settings", "%s removed the kept Cloudflare token", adminOf(r).Username)
		writeJSON(w, 200, s.domainsState())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	cf := &cdn.Cloudflare{Base: s.CloudflareAPI, Token: in.Token}
	if err := cf.Verify(ctx); err != nil {
		fail(w, 400, err.Error())
		return
	}
	zones, err := cf.Zones(ctx)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.St.SetSetting(cfTokenSetting, in.Token)
	s.St.Log("settings", "%s kept a Cloudflare token for the domains", adminOf(r).Username)
	names := []string{}
	for _, z := range zones {
		names = append(names, z.Name)
	}
	sort.Strings(names)
	writeJSON(w, 200, map[string]any{"zones": names, "state": s.domainsState()})
}

// cfToken is the token to use: the one sent, else the kept one.
func (s *Server) cfToken(sent string) string {
	if t := strings.TrimSpace(sent); t != "" && !strings.Contains(t, "•") {
		return t
	}
	return s.St.GetSetting(cfTokenSetting, "")
}

// pointDomain makes a name's DNS record point at this server through
// Cloudflare: proxied for a CDN name, DNS only otherwise. Records that point
// elsewhere change only with "replace" (the panel asks first).
func (s *Server) pointDomain(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		Token   string `json:"token"`
		Replace bool   `json:"replace"`
		IPv6    bool   `json:"ipv6"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	l := domains.Load(s.St)
	d := l.Find(in.Name)
	if d == nil {
		fail(w, 404, "that domain is not in the list")
		return
	}
	token := s.cfToken(in.Token)
	if token == "" {
		fail(w, 400, "paste your Cloudflare API token, or keep one on this page")
		return
	}
	v := s.cdnStateFor("")
	if a := net.ParseIP(v.ServerIP); a == nil || a.To4() == nil {
		fail(w, 502, "this server's public IPv4 address is not known yet")
		return
	}
	o := cdn.PointOptions{Domain: d.Name, IPv4: v.ServerIP, Proxied: d.Role == domains.CDN, Replace: in.Replace}
	if in.IPv6 {
		if a := s.serverAddrs(); len(a.IPv6) > 0 {
			o.IPv6 = a.IPv6[0]
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	cf := &cdn.Cloudflare{Base: s.CloudflareAPI, Token: token}
	res, err := cf.Point(ctx, o)
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	if res.Done {
		s.St.Log("settings", "%s pointed %s at this server through Cloudflare", adminOf(r).Username, d.Name)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			time.Sleep(3 * time.Second) // let the record reach the resolvers
			s.runDNSChecks(ctx, []string{d.Name})
			s.kickCert()
		}()
	}
	writeJSON(w, 200, map[string]any{"result": res, "state": s.domainsState()})
}
