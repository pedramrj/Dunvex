// Package domains keeps every name of the panel in one ordered list: the names
// that point straight at this server, the names a CDN proxies to it, and the
// names that only carry subscription links. The first direct name is the main
// domain (the one the TLS protocols, the certificate and the panel use).
//
// Older settings stay in step with the list ("domain" is the main name and
// "cdn" the first CDN name), so code that reads them, and an older Dunvex
// after a downgrade, see the same setup.
package domains

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/cdn"
	"github.com/pedramrj/dunvex/internal/store"
)

// Roles: how a name reaches this server.
const (
	Direct = "direct" // the name points straight at this server (DNS only)
	CDN    = "cdn"    // a CDN (Cloudflare) proxies the name to this server
	Sub    = "sub"    // only subscription links and the user page use it
)

// Setting keys.
const (
	Setting      = "domains"       // the list and its options (JSON)
	StateSetting = "domains_state" // what the checks and the certificate found (JSON)
)

// Limits.
const (
	MaxDomains = 50
	MaxLabel   = 32
	MaxNote    = 200
)

// Domain is one name of the panel.
type Domain struct {
	Name  string `json:"name"`
	Role  string `json:"role"`
	Label string `json:"label,omitempty"` // how configs through it are named ("" = the name)
	// Configs: users get configs through it. A direct name gives copies of
	// this server's TLS protocols (every protocol with All); a CDN name gives
	// the CDN protocols. The main domain always carries the configs.
	Configs bool `json:"configs"`
	All     bool `json:"all,omitempty"`
	// Cert: the name is in this server's Let's Encrypt certificate; DNS01:
	// it is validated through Cloudflare's DNS (needs the saved token), else
	// over HTTP on port 80.
	Cert      bool          `json:"cert"`
	DNS01     bool          `json:"dns01,omitempty"`
	Addresses []cdn.Address `json:"addresses,omitempty"` // CDN: clean addresses, one more config each
	Note      string        `json:"note,omitempty"`
	Added     int64         `json:"added,omitempty"`
}

// Title is how configs through the name are labelled.
func (d Domain) Title() string {
	if d.Label != "" {
		return d.Label
	}
	return d.Name
}

// List is the saved setup.
type List struct {
	Domains []Domain `json:"domains"`
	// CDNDirect: the CDN protocols also give the config straight to the server.
	CDNDirect bool `json:"cdnDirect"`
	// IPCert: with no name in the certificate, Let's Encrypt certifies the
	// server's IP address instead (short-lived, renewed every few days).
	IPCert bool `json:"ipCert"`
	// Mirror is what the older settings were last set to; a difference means
	// something else (the command line, an older version) changed them.
	Mirror Mirror `json:"mirror"`
}

// Mirror holds the older settings as the list last wrote them.
type Mirror struct {
	Domain string `json:"domain"`
	CDN    string `json:"cdn"`
}

// Main is the main domain: the first direct name, else the first CDN name
// (an older setup put the panel's own domain behind the CDN); nil when none.
func (l List) Main() *Domain {
	for _, role := range []string{Direct, CDN} {
		for i := range l.Domains {
			if l.Domains[i].Role == role {
				return &l.Domains[i]
			}
		}
	}
	return nil
}

// MainName is the main domain's name ("" = none).
func (l List) MainName() string {
	if m := l.Main(); m != nil {
		return m.Name
	}
	return ""
}

// Find returns the domain with that name.
func (l List) Find(name string) *Domain {
	name = Normalize(name)
	for i := range l.Domains {
		if l.Domains[i].Name == name {
			return &l.Domains[i]
		}
	}
	return nil
}

// CDNs are the CDN names that carry configs, in order.
func (l List) CDNs() []Domain {
	var out []Domain
	for _, d := range l.Domains {
		if d.Role == CDN && d.Configs {
			out = append(out, d)
		}
	}
	return out
}

// FirstCDN is the first CDN name (with configs or not); nil when none.
func (l List) FirstCDN() *Domain {
	for i := range l.Domains {
		if l.Domains[i].Role == CDN {
			return &l.Domains[i]
		}
	}
	return nil
}

// Mirrors are the direct names besides the main one that carry configs.
func (l List) Mirrors() []Domain {
	main := l.MainName()
	var out []Domain
	for _, d := range l.Domains {
		if d.Role == Direct && d.Configs && d.Name != main {
			out = append(out, d)
		}
	}
	return out
}

// SubName is the first subscription name ("" = none).
func (l List) SubName() string {
	for _, d := range l.Domains {
		if d.Role == Sub {
			return d.Name
		}
	}
	return ""
}

// CertNames are the names this server's certificate should cover, the main
// domain first.
func (l List) CertNames() []string {
	var out []string
	seen := map[string]bool{}
	add := func(d Domain) {
		if d.Cert && !seen[d.Name] {
			seen[d.Name] = true
			out = append(out, d.Name)
		}
	}
	if m := l.Main(); m != nil {
		add(*m)
	}
	for _, d := range l.Domains {
		add(d)
	}
	return out
}

// DNS01 reports whether name is validated through DNS.
func (l List) DNS01(name string) bool {
	d := l.Find(name)
	return d != nil && d.DNS01
}

// ---------------------------------------------------------------- cleaning

// Normalize lower-cases a name and drops a scheme, a path, a port and the final dot.
func Normalize(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, p := range []string{"https://", "http://"} {
		v = strings.TrimPrefix(v, p)
	}
	if i := strings.IndexAny(v, "/?#"); i >= 0 {
		v = v[:i]
	}
	if h, p, ok := strings.Cut(v, ":"); ok && p != "" && strings.Trim(p, "0123456789") == "" {
		v = h
	}
	return strings.TrimSuffix(v, ".")
}

// Clean normalizes the list and reports the first invalid value.
func (l *List) Clean() error {
	if len(l.Domains) > MaxDomains {
		return fmt.Errorf("at most %d domains", MaxDomains)
	}
	seen := map[string]bool{}
	now := time.Now().Unix()
	out := make([]Domain, 0, len(l.Domains))
	for _, d := range l.Domains {
		d.Name = Normalize(d.Name)
		if d.Name == "" {
			return fmt.Errorf("a domain has no name")
		}
		if _, err := netip.ParseAddr(d.Name); err == nil {
			return fmt.Errorf("%s is an IP address: add a domain name (the IP is used without one)", d.Name)
		}
		if strings.HasPrefix(d.Name, "*.") {
			return fmt.Errorf("%s: wildcard names are not supported; add each name", d.Name)
		}
		if !cdn.IsHostname(d.Name) {
			return fmt.Errorf("%q is not a domain name", d.Name)
		}
		if seen[d.Name] {
			return fmt.Errorf("%s is in the list twice", d.Name)
		}
		seen[d.Name] = true
		switch d.Role {
		case Direct, CDN, Sub:
		case "":
			d.Role = Direct
		default:
			return fmt.Errorf("%s: unknown role %q", d.Name, d.Role)
		}
		d.Label = strings.Join(strings.Fields(d.Label), " ")
		if len([]rune(d.Label)) > MaxLabel {
			return fmt.Errorf("%s: the label is too long (%d characters at most)", d.Name, MaxLabel)
		}
		d.Note = strings.TrimSpace(d.Note)
		if len([]rune(d.Note)) > MaxNote {
			return fmt.Errorf("%s: the note is too long", d.Name)
		}
		if d.Role != Direct {
			d.All = false
		}
		if d.Role == Sub {
			d.Configs = false
		}
		if d.Role == CDN {
			c := cdn.Settings{Addresses: d.Addresses}
			if err := c.Clean(); err != nil {
				return fmt.Errorf("%s: %w", d.Name, err)
			}
			d.Addresses = c.Addresses
		} else {
			d.Addresses = nil
		}
		if len(d.Addresses) == 0 {
			d.Addresses = nil
		}
		if d.Added == 0 {
			d.Added = now
		}
		out = append(out, d)
	}
	l.Domains = out
	return nil
}

// ---------------------------------------------------------------- storage

// Load reads the list. Before the first save it is made from the older
// settings, and older settings changed elsewhere since are taken over.
func Load(st *store.Store) List {
	l, _ := load(st)
	return l
}

func load(st *store.Store) (List, bool) {
	raw := st.GetSetting(Setting, "")
	if raw == "" {
		return FromLegacy(st), true
	}
	var l List
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return FromLegacy(st), true
	}
	return l, l.reconcile(st)
}

// Saved reports whether the list was ever saved (else it is still made from
// the older settings on every read).
func Saved(st *store.Store) bool { return st.GetSetting(Setting, "") != "" }

// Save cleans and stores the list, and sets the older settings to match.
func Save(st *store.Store, l List) (List, error) {
	if err := l.Clean(); err != nil {
		return l, err
	}
	l.Mirror = Mirror{Domain: l.MainName(), CDN: string(legacyCDN(l))}
	b, err := json.Marshal(l)
	if err != nil {
		return l, err
	}
	if err := st.SetSetting(Setting, string(b)); err != nil {
		return l, err
	}
	st.SetSetting("domain", l.Mirror.Domain)
	st.SetSetting(cdn.Setting, l.Mirror.CDN)
	return l, nil
}

// Migrate saves the list made from the older settings once, and saves it
// again when older settings were changed elsewhere (the command line, an
// older version). It reports what it did ("" = nothing).
func Migrate(st *store.Store) (string, error) {
	first := !Saved(st)
	l, changed := load(st)
	if !changed {
		return "", nil
	}
	if _, err := Save(st, l); err != nil {
		return "", err
	}
	if first {
		return fmt.Sprintf("%d domain(s) moved to the Domains page", len(l.Domains)), nil
	}
	return "the domains follow settings changed outside the panel", nil
}

// FromLegacy makes the list from the older settings: the panel's domain and
// the CDN settings.
func FromLegacy(st *store.Store) List {
	var l List
	if d := Normalize(st.GetSetting("domain", "")); d != "" {
		l.Domains = append(l.Domains, Domain{Name: d, Role: Direct, Configs: true, Cert: st.GetSetting("cert_mode", "self") == "acme"})
	}
	l.TakeCDN(cdn.Load(st))
	l.Clean() // a broken old value is dropped below, not kept
	l.Domains = valid(l.Domains)
	return l
}

// valid keeps the domains that pass Clean one by one (an old setting may hold
// a name Clean refuses).
func valid(ds []Domain) []Domain {
	out := []Domain{}
	for _, d := range ds {
		one := List{Domains: []Domain{d}}
		if one.Clean() == nil {
			out = append(out, one.Domains[0])
		}
	}
	// names may repeat across roles in old settings: keep the first
	seen := map[string]bool{}
	keep := out[:0]
	for _, d := range out {
		if !seen[d.Name] {
			seen[d.Name] = true
			keep = append(keep, d)
		}
	}
	return keep
}

// TakeCDN puts CDN settings of the older kind into the list: the name becomes the
// first CDN name (an empty name meant the panel's own domain).
func (l *List) TakeCDN(c cdn.Settings) {
	l.CDNDirect = c.Direct
	name := Normalize(c.Domain)
	if name == "" {
		if !c.Enabled {
			return
		}
		// the panel's own domain was behind the CDN
		if m := l.Main(); m != nil {
			m.Role, m.Configs, m.Addresses = CDN, true, c.Addresses
		}
		return
	}
	if d := l.Find(name); d != nil {
		if d.Role == CDN || l.MainName() == name {
			d.Role, d.Configs, d.Addresses = CDN, c.Enabled || d.Configs, c.Addresses
		}
		return
	}
	if f := l.FirstCDN(); f != nil {
		f.Name, f.Configs, f.Addresses = name, c.Enabled, c.Addresses
		return
	}
	l.Domains = append(l.Domains, Domain{Name: name, Role: CDN, Configs: c.Enabled, Addresses: c.Addresses})
}

// legacyCDN is the older CDN setting that matches the list.
func legacyCDN(l List) []byte {
	c := cdn.Settings{Direct: l.CDNDirect, Addresses: []cdn.Address{}}
	if f := l.FirstCDN(); f != nil {
		c.Enabled, c.Domain = f.Configs, f.Name
		if f.Addresses != nil {
			c.Addresses = f.Addresses
		}
	}
	b, _ := json.Marshal(c)
	return b
}

// reconcile takes over older settings changed elsewhere since the last
// save, and reports whether it changed anything.
func (l *List) reconcile(st *store.Store) bool {
	changed := false
	if d := Normalize(st.GetSetting("domain", "")); d != Normalize(l.Mirror.Domain) {
		l.SetMain(d)
		changed = true
	}
	if c := st.GetSetting(cdn.Setting, ""); c != l.Mirror.CDN && c != "" {
		l.TakeCDN(cdn.Load(st))
		changed = true
	}
	if changed {
		l.Domains = valid(l.Domains)
	}
	return changed
}

// SetMain makes name the main domain, as setting the panel's domain did:
// an existing name moves to the top (a subscription name becomes direct), a
// new one is added there, and "" removes the main domain.
func (l *List) SetMain(name string) {
	name = Normalize(name)
	cur := l.Main()
	if name == "" {
		if cur != nil {
			l.remove(cur.Name)
		}
		return
	}
	if cur != nil && cur.Name == name {
		return
	}
	d := Domain{Name: name, Role: Direct, Configs: true, Cert: true}
	if f := l.Find(name); f != nil {
		d = *f
		if d.Role == Sub {
			d.Role = Direct
		}
		d.Configs = true
		l.remove(name)
	}
	l.Domains = append([]Domain{d}, l.Domains...)
}

func (l *List) remove(name string) {
	out := l.Domains[:0]
	for _, d := range l.Domains {
		if d.Name != name {
			out = append(out, d)
		}
	}
	l.Domains = out
}
