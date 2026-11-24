package tlsmgr

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RenewBefore is how long before expiry a certificate is renewed.
const RenewBefore = 30 * 24 * time.Hour

// Valid reports whether the certificate in certPath is for domain and stays
// valid for at least d more, and returns its expiry.
func Valid(certPath, domain string, d time.Duration) (bool, time.Time) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return false, time.Time{}
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return false, time.Time{}
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, time.Time{}
	}
	if c.VerifyHostname(domain) != nil {
		return false, c.NotAfter
	}
	return time.Now().Add(d).Before(c.NotAfter), c.NotAfter
}

// Renew keeps the certificate of domain fresh: it does nothing while the one
// on disk stays valid for RenewBefore, and otherwise obtains a new one
// (Let's Encrypt, port 80). renewed reports whether a new one was written.
func Renew(certDir, domain, email string) (cert, key string, renewed bool, err error) {
	cert, key = Paths(certDir, domain)
	if ok, _ := Valid(cert, domain, RenewBefore); ok {
		if _, err := os.Stat(key); err == nil {
			return cert, key, false, nil
		}
	}
	// autocert keeps a still-valid certificate in its cache and would hand it
	// back: drop it so a new one is issued (the account key stays)
	cache := filepath.Join(certDir, ".acme")
	for _, name := range []string{domain, domain + "+rsa"} {
		os.Remove(filepath.Join(cache, name))
	}
	cert, key, err = Obtain(certDir, domain, email)
	return cert, key, err == nil, err
}

// Reloader serves a certificate from files and reads them again when they
// change, so a renewed certificate is used without a restart.
type Reloader struct {
	Cert, Key string
	// Paths, when set, says where the files are now (the Domains page may
	// move the certificate); Cert and Key are then ignored.
	Paths func() (cert, key string)

	mu   sync.Mutex
	mod  time.Time
	from string
	have *tls.Certificate
}

// GetCertificate is for tls.Config.
func (r *Reloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	certPath, keyPath := r.Cert, r.Key
	if r.Paths != nil {
		if c, k := r.Paths(); c != "" && k != "" {
			certPath, keyPath = c, k
		}
	}
	fi, err := os.Stat(certPath)
	if err != nil {
		if r.have != nil {
			return r.have, nil
		}
		return nil, err
	}
	if r.have == nil || !fi.ModTime().Equal(r.mod) || r.from != certPath {
		c, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			if r.have != nil {
				return r.have, nil // a half-written renewal: keep serving the old one
			}
			return nil, err
		}
		r.have, r.mod, r.from = &c, fi.ModTime(), certPath
	}
	return r.have, nil
}

// Explain turns an issuance error into a hint for the owner.
func Explain(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "port 80"):
		return s
	case strings.Contains(s, "does not resolve"):
		return s
	case strings.Contains(s, "rateLimited"), strings.Contains(s, "too many certificates"):
		return "Let's Encrypt's rate limit was reached for this domain; it is tried again later: " + s
	case strings.Contains(s, "unauthorized"), strings.Contains(s, "Invalid response"), strings.Contains(s, "Timeout during connect"):
		return "Let's Encrypt could not reach this server on port 80 at the domain's address (check the A record and the firewall): " + s
	}
	return s
}
