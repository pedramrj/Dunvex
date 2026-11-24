package tlsmgr

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
)

// ACME directories.
const (
	LetsEncrypt        = "https://acme-v02.api.letsencrypt.org/directory"
	LetsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"
	// ShortLived is Let's Encrypt's profile for certificates of about six
	// days; IP addresses are only certified with it.
	ShortLived = "shortlived"
)

// Issuer gets certificates over ACME (RFC 8555): several names in one
// certificate, each validated over HTTP (port 80) or through DNS, and IP
// addresses with the short-lived profile. A name that fails is left out and
// reported, so one broken record never blocks the others.
type Issuer struct {
	Directory  string       // ACME directory ("" = Let's Encrypt)
	HTTPClient *http.Client // nil = the default client
	Email      string       // contact for the account (optional)
	KeyFile    string       // the account key (made on first use)
	HTTP01     *HTTP01      // answers http-01 challenges
	DNS01      DNSProvider  // sets dns-01 records (nil = no DNS validation)
	Timeout    time.Duration
}

// DNSProvider sets and removes the TXT records of dns-01 challenges.
type DNSProvider interface {
	Present(ctx context.Context, fqdn, value string) error
	CleanUp(ctx context.Context, fqdn, value string) error
}

// DNSWaiter is a DNSProvider that can wait until its record is visible.
type DNSWaiter interface {
	Wait(ctx context.Context, fqdn, value string) error
}

// Order is what one certificate should cover.
type Order struct {
	Names   []string        // DNS names; the first is the certificate's subject
	IPs     []string        // IP addresses (need the ShortLived profile at Let's Encrypt)
	DNS     map[string]bool // names validated through DNS (else over HTTP)
	Profile string          // ACME profile ("" = the CA's default)
	// Require: when one of these fails, nothing is issued (a renewal must not
	// drop a name the current certificate covers).
	Require []string
	// Need: when none of these passes, nothing is issued (a certificate asked
	// for to add names is not made again without them).
	Need []string
}

// Issued is a new certificate.
type Issued struct {
	CertPEM  []byte
	KeyPEM   []byte
	Names    []string
	IPs      []string
	NotAfter time.Time
	Failed   map[string]*NameError // names (and IPs) left out, and why
}

// NameError is why a name could not be validated. Code: port80, connection,
// dns, unauthorized, caa, rejected, rateLimited, timeout, nodns or other.
type NameError struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func (e *NameError) Error() string { return e.Detail }

// IssueError is a failed issuance with what failed for each name.
type IssueError struct {
	Msg         string
	Failed      map[string]*NameError
	RateLimited bool
	RetryAfter  time.Duration
	problem     *acme.Error
}

func (e *IssueError) Error() string {
	if len(e.Failed) == 0 {
		return e.Msg
	}
	keys := make([]string, 0, len(e.Failed))
	for k := range e.Failed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		parts = append(parts, k+": "+e.Failed[k].Detail)
	}
	return e.Msg + " (" + strings.Join(parts, "; ") + ")"
}

func (is *Issuer) client(ctx context.Context) (*acme.Client, error) {
	key, err := is.accountKey()
	if err != nil {
		return nil, err
	}
	dir := is.Directory
	if dir == "" {
		dir = LetsEncrypt
	}
	cl := &acme.Client{Key: key, DirectoryURL: dir, HTTPClient: is.HTTPClient, UserAgent: "dunvex"}
	acct := &acme.Account{}
	if is.Email != "" {
		acct.Contact = []string{"mailto:" + is.Email}
	}
	if _, err := cl.Register(ctx, acct, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, fmt.Errorf("could not open an account at %s: %w", dir, err)
	}
	return cl, nil
}

func (is *Issuer) accountKey() (crypto.Signer, error) {
	if b, err := os.ReadFile(is.KeyFile); err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			if k, err := x509.ParseECPrivateKey(blk.Bytes); err == nil {
				return k, nil
			}
		}
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, _ := x509.MarshalECPrivateKey(k)
	if err := os.MkdirAll(filepath.Dir(is.KeyFile), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(is.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

// Issue validates every name of the order and gets one certificate for those
// that passed. It fails only when none passed, or a required one did not.
func (is *Issuer) Issue(ctx context.Context, o Order) (*Issued, error) {
	if len(o.Names)+len(o.IPs) == 0 {
		return nil, &IssueError{Msg: "nothing to certify"}
	}
	cl, err := is.client(ctx)
	if err != nil {
		return nil, &IssueError{Msg: err.Error()}
	}
	var opts []acme.OrderOption
	if o.Profile != "" {
		opts = append(opts, acme.WithOrderIssuanceProfile(acme.ProfileName(o.Profile)))
	}
	failed := map[string]*NameError{}
	order, err := cl.AuthorizeOrder(ctx, append(acme.DomainIDs(o.Names...), acme.IPIDs(o.IPs...)...), opts...)
	if err != nil {
		// names the CA refuses outright are left out, and the rest asked for again
		ie, _ := refused(orderFailure(err), o).(*IssueError)
		if ie == nil || len(ie.Failed) == 0 {
			return nil, refused(orderFailure(err), o)
		}
		rest := Order{DNS: o.DNS}
		for _, n := range o.Names {
			if ie.Failed[n] == nil {
				rest.Names = append(rest.Names, n)
			}
		}
		for _, ip := range o.IPs {
			if ie.Failed[ip] == nil {
				rest.IPs = append(rest.IPs, ip)
			}
		}
		for _, r := range o.Require {
			if ie.Failed[r] != nil {
				return nil, ie
			}
		}
		if len(rest.Names)+len(rest.IPs) == 0 {
			return nil, ie
		}
		order, err = cl.AuthorizeOrder(ctx, append(acme.DomainIDs(rest.Names...), acme.IPIDs(rest.IPs...)...), opts...)
		if err != nil {
			return nil, refused(orderFailure(err), rest)
		}
		failed = ie.Failed
	}

	// http-01 needs port 80 for as long as the validations run
	needHTTP := len(o.IPs) > 0
	for _, n := range o.Names {
		needHTTP = needHTTP || !o.DNS[n]
	}
	var httpErr error
	if needHTTP {
		if is.HTTP01 == nil {
			httpErr = errors.New("this server cannot answer over HTTP")
		} else if httpErr = is.HTTP01.start(); httpErr == nil {
			defer is.HTTP01.stop()
		}
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	limit := make(chan struct{}, 4)
	for _, u := range order.AuthzURLs {
		u := u
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			id, err := is.authorize(ctx, cl, u, o, httpErr)
			if err != nil {
				mu.Lock()
				failed[id] = asNameError(err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	var names, ips []string
	for _, n := range o.Names {
		if failed[n] == nil {
			names = append(names, n)
		}
	}
	for _, ip := range o.IPs {
		if failed[ip] == nil {
			ips = append(ips, ip)
		}
	}
	for _, r := range o.Require {
		if failed[r] != nil {
			return nil, &IssueError{Msg: "a name the current certificate covers did not pass, so it is kept", Failed: failed}
		}
	}
	if len(o.Need) > 0 {
		passed := false
		for _, n := range o.Need {
			passed = passed || failed[n] == nil
		}
		if !passed {
			return nil, &IssueError{Msg: "no new name passed validation, so the certificate is kept", Failed: failed}
		}
	}
	if len(names)+len(ips) == 0 {
		e := &IssueError{Msg: "no name passed validation", Failed: failed}
		for _, f := range failed {
			e.RateLimited = e.RateLimited || f.Code == "rateLimited"
		}
		return nil, e
	}
	if len(failed) > 0 {
		// the first order is spoiled by the failures: a new one with the names
		// that passed reuses their valid authorizations
		order, err = cl.AuthorizeOrder(ctx, append(acme.DomainIDs(names...), acme.IPIDs(ips...)...), opts...)
		if err != nil {
			return nil, orderFailure(err)
		}
	}
	if order.Status != acme.StatusReady {
		wctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		order, err = cl.WaitOrder(wctx, order.URI)
		cancel()
		if err != nil {
			return nil, orderFailure(err)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, &IssueError{Msg: err.Error()}
	}
	tmpl := &x509.CertificateRequest{DNSNames: names}
	if len(names) > 0 && len(names[0]) <= 64 {
		tmpl.Subject = pkix.Name{CommonName: names[0]}
	}
	for _, ip := range ips {
		tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP(ip))
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, &IssueError{Msg: err.Error()}
	}
	der, _, err := cl.CreateCertFromOrder(ctx, order, csr, true)
	if err != nil {
		return nil, orderFailure(err)
	}
	out := &Issued{Names: names, IPs: ips, Failed: failed}
	for _, b := range der {
		out.CertPEM = append(out.CertPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b})...)
	}
	if leaf, err := x509.ParseCertificate(der[0]); err == nil {
		out.NotAfter = leaf.NotAfter
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	out.KeyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	return out, nil
}

// authorize completes one authorization and returns its identifier.
func (is *Issuer) authorize(ctx context.Context, cl *acme.Client, url string, o Order, httpErr error) (string, error) {
	a, err := cl.GetAuthorization(ctx, url)
	if err != nil {
		return url, err
	}
	id := a.Identifier.Value
	switch a.Status {
	case acme.StatusValid:
		return id, nil
	case acme.StatusPending:
	default:
		return id, fmt.Errorf("the authorization is %s", a.Status)
	}
	want := "http-01"
	if a.Identifier.Type == "dns" && o.DNS[id] {
		want = "dns-01"
	}
	var ch *acme.Challenge
	for _, c := range a.Challenges {
		if c.Type == want {
			ch = c
		}
	}
	if ch == nil {
		return id, &NameError{Code: "rejected", Detail: "the certificate authority offers no " + want + " validation for " + id}
	}
	timeout := is.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch want {
	case "http-01":
		if httpErr != nil {
			return id, &NameError{Code: "port80", Detail: httpErr.Error()}
		}
		resp, err := cl.HTTP01ChallengeResponse(ch.Token)
		if err != nil {
			return id, err
		}
		is.HTTP01.set(ch.Token, resp)
		defer is.HTTP01.del(ch.Token)
	case "dns-01":
		if is.DNS01 == nil {
			return id, &NameError{Code: "nodns", Detail: "DNS validation needs a saved Cloudflare token"}
		}
		val, err := cl.DNS01ChallengeRecord(ch.Token)
		if err != nil {
			return id, err
		}
		fqdn := "_acme-challenge." + id
		if err := is.DNS01.Present(ctx, fqdn, val); err != nil {
			return id, &NameError{Code: "nodns", Detail: "the DNS record could not be made: " + err.Error()}
		}
		defer func() {
			cctx, ccancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer ccancel()
			is.DNS01.CleanUp(cctx, fqdn, val)
		}()
		if w, ok := is.DNS01.(DNSWaiter); ok {
			if err := w.Wait(ctx, fqdn, val); err != nil {
				return id, &NameError{Code: "timeout", Detail: "the DNS record did not show up in time: " + err.Error()}
			}
		}
	}
	if _, err := cl.Accept(ctx, ch); err != nil {
		return id, err
	}
	if _, err := cl.WaitAuthorization(ctx, a.URI); err != nil {
		return id, err
	}
	return id, nil
}

// asNameError says in plain words why a validation failed.
func asNameError(err error) *NameError {
	var ne *NameError
	if errors.As(err, &ne) {
		return ne
	}
	var ae *acme.AuthorizationError
	if errors.As(err, &ae) {
		for _, e := range ae.Errors {
			var x *acme.Error
			if errors.As(e, &x) {
				return problem(x)
			}
		}
		return &NameError{Code: "other", Detail: ae.Error()}
	}
	var x *acme.Error
	if errors.As(err, &x) {
		return problem(x)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &NameError{Code: "timeout", Detail: "the certificate authority did not finish the validation in time"}
	}
	return &NameError{Code: "other", Detail: err.Error()}
}

func problem(x *acme.Error) *NameError {
	t := x.ProblemType[strings.LastIndexByte(x.ProblemType, ':')+1:]
	code := map[string]string{
		"connection": "connection", "dns": "dns", "unauthorized": "unauthorized", "incorrectResponse": "unauthorized",
		"caa": "caa", "rejectedIdentifier": "rejected", "unsupportedIdentifier": "rejected", "rateLimited": "rateLimited",
		"tls": "connection",
	}[t]
	if code == "" {
		code = "other"
	}
	return &NameError{Code: code, Detail: x.Detail}
}

// refused puts a refused order on the names it names (the CA lists them as
// subproblems), or on every name when it refused them all at once.
func refused(err error, o Order) error {
	e, ok := err.(*IssueError)
	if !ok || e.problem == nil {
		return err
	}
	x := e.problem
	e.Failed = map[string]*NameError{}
	for _, sp := range x.Subproblems {
		if sp.Identifier != nil && sp.Identifier.Value != "" {
			e.Failed[sp.Identifier.Value] = problem(&acme.Error{ProblemType: sp.Type, Detail: sp.Detail})
		}
	}
	if len(e.Failed) == 0 && strings.HasSuffix(x.ProblemType, ":rejectedIdentifier") {
		for _, n := range append(append([]string{}, o.Names...), o.IPs...) {
			e.Failed[n] = problem(x)
		}
	}
	if len(e.Failed) == 0 {
		e.Failed = nil
	}
	return e
}

// orderFailure turns an order error into an IssueError.
func orderFailure(err error) error {
	e := &IssueError{Msg: err.Error()}
	var x *acme.Error
	if errors.As(err, &x) {
		e.Msg, e.problem = x.Detail, x
		if d, ok := acme.RateLimit(err); ok {
			e.RateLimited, e.RetryAfter = true, d
		}
	}
	var oe *acme.OrderError
	if errors.As(err, &oe) && oe.Problem != nil {
		e.Msg = oe.Problem.Detail
	}
	return e
}

// ---------------------------------------------------------------- http-01

// HTTP01 answers http-01 challenges: on its own listener while validations
// run, and through ServeHTTP for a server that already holds port 80.
type HTTP01 struct {
	Addr string // where its own listener opens (":80")
	// Shared: something else already answers on port 80 through ServeHTTP,
	// so a busy port is no failure.
	Shared func() bool

	mu     sync.Mutex
	tokens map[string]string
	users  int
	srv    *http.Server
}

// Prefix is the path challenges are fetched from.
const Prefix = "/.well-known/acme-challenge/"

// Answer answers a challenge; false when the path is not one.
func (h *HTTP01) Answer(w http.ResponseWriter, r *http.Request) bool {
	tok, ok := strings.CutPrefix(r.URL.Path, Prefix)
	if !ok {
		return false
	}
	h.mu.Lock()
	v, ok := h.tokens[tok]
	h.mu.Unlock()
	if !ok {
		return false
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(v))
	return true
}

func (h *HTTP01) set(tok, v string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.tokens == nil {
		h.tokens = map[string]string{}
	}
	h.tokens[tok] = v
}

func (h *HTTP01) del(tok string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.tokens, tok)
}

func (h *HTTP01) start() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.users++
	if h.srv != nil {
		return nil
	}
	addr := h.Addr
	if addr == "" {
		addr = ":80"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if h.Shared != nil && h.Shared() {
			return nil
		}
		h.users--
		return fmt.Errorf("port %s is busy (another program or protocol holds it); it must be free for the HTTP validation, or use Cloudflare DNS validation", strings.TrimPrefix(addr, ":"))
	}
	h.srv = &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.Answer(w, r) {
			http.NotFound(w, r)
		}
	})}
	go h.srv.Serve(ln)
	return nil
}

func (h *HTTP01) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.users--; h.users > 0 || h.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	h.srv.Shutdown(ctx)
	h.srv = nil
}

// ---------------------------------------------------------------- reading certificates

// Info is what a certificate file says.
type Info struct {
	Names      []string  `json:"names"`
	IPs        []string  `json:"ips"`
	Issuer     string    `json:"issuer"`
	NotBefore  time.Time `json:"notBefore"`
	NotAfter   time.Time `json:"notAfter"`
	SelfSigned bool      `json:"selfSigned"`
	Key        string    `json:"key"`
}

// Read reads the first certificate of a PEM file.
func Read(path string) (*Info, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no certificate in " + path)
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	in := &Info{Names: append([]string{}, c.DNSNames...), IPs: []string{}, NotBefore: c.NotBefore, NotAfter: c.NotAfter}
	for _, ip := range c.IPAddresses {
		in.IPs = append(in.IPs, ip.String())
	}
	if len(c.Issuer.Organization) > 0 {
		in.Issuer = c.Issuer.Organization[0]
	} else {
		in.Issuer = c.Issuer.CommonName
	}
	// signed by its own key (CheckSignatureFrom would also want a CA certificate)
	in.SelfSigned = c.Issuer.String() == c.Subject.String() && c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil
	switch k := c.PublicKey.(type) {
	case *ecdsa.PublicKey:
		in.Key = "ECDSA " + k.Curve.Params().Name
	default:
		in.Key = c.PublicKeyAlgorithm.String()
	}
	if len(in.Names) == 0 && c.Subject.CommonName != "" && net.ParseIP(c.Subject.CommonName) == nil {
		in.Names = []string{c.Subject.CommonName}
	}
	return in, nil
}

// Covers reports whether the certificate is for name.
func (in *Info) Covers(name string) bool {
	for _, n := range in.Names {
		if strings.EqualFold(n, name) {
			return true
		}
		if strings.HasPrefix(n, "*.") && strings.Count(name, ".") >= 2 && strings.EqualFold(n[2:], name[strings.IndexByte(name, '.')+1:]) {
			return true
		}
	}
	for _, ip := range in.IPs {
		if ip == name {
			return true
		}
	}
	return false
}

// WriteFiles writes a certificate and its key, the key first and both
// through a temporary file, so a reader never sees half of one.
func WriteFiles(certPath, keyPath string, cert, key []byte) error {
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return err
	}
	write := func(p string, b []byte, mode os.FileMode) error {
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, b, mode); err != nil {
			return err
		}
		return os.Rename(tmp, p)
	}
	if err := write(keyPath, key, 0o600); err != nil {
		return err
	}
	return write(certPath, cert, 0o644)
}
