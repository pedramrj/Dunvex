package domains

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/cdn"
)

// Resolver looks names up. The real one asks public resolvers, so a record
// just changed shows the same way it does for users.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupNS(ctx context.Context, name string) ([]*net.NS, error)
}

// PublicResolver asks Cloudflare's and Google's resolvers (not the server's
// own cache), and the system's resolver when they cannot be reached.
func PublicResolver() Resolver {
	servers := []string{"1.1.1.1:53", "8.8.8.8:53"}
	var n int
	var mu sync.Mutex
	pub := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		mu.Lock()
		addr := servers[n%len(servers)]
		n++
		mu.Unlock()
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, addr)
	}}
	return fallback{pub, net.DefaultResolver}
}

// fallback asks a, and b when a cannot answer (not when a says "no such name").
type fallback struct{ a, b Resolver }

func unreachable(err error) bool {
	var de *net.DNSError
	return err != nil && !(errors.As(err, &de) && de.IsNotFound)
}

func (f fallback) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	r, err := f.a.LookupIPAddr(c, host)
	cancel()
	if unreachable(err) && ctx.Err() == nil {
		return f.b.LookupIPAddr(ctx, host)
	}
	return r, err
}

func (f fallback) LookupCNAME(ctx context.Context, host string) (string, error) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	r, err := f.a.LookupCNAME(c, host)
	cancel()
	if unreachable(err) && ctx.Err() == nil {
		return f.b.LookupCNAME(ctx, host)
	}
	return r, err
}

func (f fallback) LookupNS(ctx context.Context, name string) ([]*net.NS, error) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	r, err := f.a.LookupNS(c, name)
	cancel()
	if unreachable(err) && ctx.Err() == nil {
		return f.b.LookupNS(ctx, name)
	}
	return r, err
}

// Problems a check can find (the panel says them in the owner's language).
const (
	ProbNotFound      = "notFound"      // the name has no A or AAAA record
	ProbNotHere       = "notHere"       // a direct name points elsewhere
	ProbProxied       = "proxied"       // a direct name is proxied by Cloudflare (orange cloud)
	ProbNotProxied    = "notProxied"    // a CDN name points straight at this server
	ProbNotCDN        = "notCDN"        // a CDN name points neither at Cloudflare nor here
	ProbSomeElsewhere = "someElsewhere" // some of its addresses are not this server (some users miss it)
	ProbV6Elsewhere   = "v6Elsewhere"   // its AAAA record is not this server (IPv6 users miss it)
	ProbLookup        = "lookup"        // the lookup itself failed (timeout, server failure)
)

// Addr is one address a name points to.
type Addr struct {
	IP         string `json:"ip"`
	Here       bool   `json:"here"`       // one of this server's addresses
	Cloudflare bool   `json:"cloudflare"` // in Cloudflare's ranges
}

// Check is what a name's DNS says.
type Check struct {
	Name     string   `json:"name"`
	A        []Addr   `json:"a"`
	AAAA     []Addr   `json:"aaaa"`
	CNAME    string   `json:"cname,omitempty"` // the name it is an alias of
	NS       []string `json:"ns,omitempty"`    // the name servers of its zone
	Zone     string   `json:"zone,omitempty"`  // the zone those name servers answer for
	OnCF     bool     `json:"onCloudflare"`    // the zone's DNS is at Cloudflare
	Points   string   `json:"points"`          // here, cloudflare, elsewhere, mixed or none
	Status   string   `json:"status"`          // ok, warn or bad
	Problems []string `json:"problems"`
	Error    string   `json:"error,omitempty"`
	At       int64    `json:"at"`
}

// Server is the addresses that count as this server.
type Server struct {
	IPv4 []string
	IPv6 []string
}

func (s Server) has(ip net.IP) bool {
	for _, l := range [][]string{s.IPv4, s.IPv6} {
		for _, a := range l {
			if x := net.ParseIP(a); x != nil && x.Equal(ip) {
				return true
			}
		}
	}
	return false
}

// LocalAddresses are this machine's public addresses (multi-IP servers have
// several), plus the given ones.
func LocalAddresses(extra ...string) Server {
	var s Server
	seen := map[string]bool{}
	add := func(ip net.IP) {
		if ip == nil || seen[ip.String()] || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			return
		}
		seen[ip.String()] = true
		if ip.To4() != nil {
			s.IPv4 = append(s.IPv4, ip.String())
		} else {
			s.IPv6 = append(s.IPv6, ip.String())
		}
	}
	for _, e := range extra {
		add(net.ParseIP(strings.TrimSpace(e)))
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				add(n.IP)
			}
		}
	}
	return s
}

// Lookup checks one name for a role.
func Lookup(ctx context.Context, r Resolver, name, role string, me Server) Check {
	c := Check{Name: name, A: []Addr{}, AAAA: []Addr{}, Problems: []string{}, At: time.Now().Unix()}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var ips []net.IPAddr
	var ipErr error
	wg.Add(3)
	go func() { defer wg.Done(); ips, ipErr = r.LookupIPAddr(ctx, name) }()
	go func() {
		defer wg.Done()
		if cn, err := r.LookupCNAME(ctx, name); err == nil {
			cn = strings.TrimSuffix(strings.ToLower(cn), ".")
			if cn != "" && cn != name {
				c.CNAME = cn
			}
		}
	}()
	go func() { defer wg.Done(); c.Zone, c.NS = zoneNS(ctx, r, name) }()
	wg.Wait()
	for _, ns := range c.NS {
		if strings.HasSuffix(ns, ".ns.cloudflare.com") {
			c.OnCF = true
		}
	}
	for _, a := range ips {
		x := Addr{IP: a.IP.String(), Here: me.has(a.IP), Cloudflare: cdn.IsCloudflare(a.IP)}
		if a.IP.To4() != nil {
			c.A = append(c.A, x)
		} else {
			c.AAAA = append(c.AAAA, x)
		}
	}
	sort.Slice(c.A, func(i, j int) bool { return c.A[i].IP < c.A[j].IP })
	sort.Slice(c.AAAA, func(i, j int) bool { return c.AAAA[i].IP < c.AAAA[j].IP })
	if ipErr != nil && len(ips) == 0 {
		var de *net.DNSError
		if errors.As(ipErr, &de) && (de.IsNotFound || strings.Contains(de.Err, "no such host")) {
			c.Points, c.Status = "none", "bad"
			c.Problems = append(c.Problems, ProbNotFound)
			return c
		}
		c.Points, c.Status, c.Error = "none", "bad", ipErr.Error()
		c.Problems = append(c.Problems, ProbLookup)
		return c
	}
	judge(&c, role)
	return c
}

// judge sets where the name points and what is wrong with that for its role.
func judge(c *Check, role string) {
	all := append(append([]Addr{}, c.A...), c.AAAA...)
	here, cf, other := 0, 0, 0
	for _, a := range all {
		switch {
		case a.Here:
			here++
		case a.Cloudflare:
			cf++
		default:
			other++
		}
	}
	switch {
	case len(all) == 0:
		c.Points = "none"
	case here == len(all):
		c.Points = "here"
	case cf == len(all):
		c.Points = "cloudflare"
	case here == 0 && cf == 0:
		c.Points = "elsewhere"
	default:
		c.Points = "mixed"
	}
	add := func(p string) { c.Problems = append(c.Problems, p) }
	bad := false
	switch {
	case len(all) == 0:
		add(ProbNotFound)
		bad = true
	case role == Direct:
		switch {
		case here == 0 && cf > 0 && other == 0:
			add(ProbProxied)
			bad = true
		case here == 0:
			add(ProbNotHere)
			bad = true
		case hasOther(c.A) || hasCF(c.A):
			add(ProbSomeElsewhere)
		case hasOther(c.AAAA) || hasCF(c.AAAA):
			add(ProbV6Elsewhere)
		}
	case role == CDN:
		switch {
		case cf == 0 && here > 0:
			add(ProbNotProxied)
			bad = true
		case cf == 0:
			add(ProbNotCDN)
			bad = true
		case here > 0 || other > 0:
			add(ProbSomeElsewhere)
		}
	case role == Sub:
		if here == 0 && cf == 0 {
			add(ProbNotHere)
			bad = true
		}
	}
	switch {
	case bad:
		c.Status = "bad"
	case len(c.Problems) > 0:
		c.Status = "warn"
	default:
		c.Status = "ok"
	}
}

func hasOther(l []Addr) bool {
	for _, a := range l {
		if !a.Here && !a.Cloudflare {
			return true
		}
	}
	return false
}

func hasCF(l []Addr) bool {
	for _, a := range l {
		if a.Cloudflare && !a.Here {
			return true
		}
	}
	return false
}

// zoneNS finds the zone a name belongs to (the nearest name with NS records)
// and its name servers.
func zoneNS(ctx context.Context, r Resolver, name string) (string, []string) {
	labels := strings.Split(name, ".")
	for i := 0; i+2 <= len(labels); i++ {
		z := strings.Join(labels[i:], ".")
		ns, err := r.LookupNS(ctx, z)
		if err != nil || len(ns) == 0 {
			if ctx.Err() != nil {
				return "", nil
			}
			continue
		}
		var out []string
		for _, n := range ns {
			out = append(out, strings.TrimSuffix(strings.ToLower(n.Host), "."))
		}
		sort.Strings(out)
		return z, out
	}
	return "", nil
}
