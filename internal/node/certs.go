package node

import (
	"log"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/tlsmgr"
	"github.com/pedramrj/dunvex/internal/util"
)

// what a node keeps about its own certificate
const (
	keyDomain  = "node_domain"     // the domain the panel gave this node
	keyACME    = "node_acme_email" // the panel's Let's Encrypt email
	keyCertErr = "node_cert_error" // why the last issuance failed
)

// retryAfter keeps failed issuances from running into Let's Encrypt's limit
// on failed validations (a few per hour).
const retryAfter = time.Hour

// EnvFor is what a node's protocols run with: the real certificate of its
// domain once Let's Encrypt gave one, otherwise a self-signed certificate
// that links pin.
func EnvFor(st *store.Store, certDir, host, listen string) protocol.Env {
	e := protocol.Env{Host: host, Listen: listen, Domain: st.GetSetting(keyDomain, "")}
	if e.Domain != "" {
		cert, key := tlsmgr.Paths(certDir, e.Domain)
		if ok, _ := tlsmgr.Valid(cert, e.Domain, 0); ok && util.FileExists(key) {
			e.CertFile, e.KeyFile = cert, key
			return e
		}
	}
	if c, k, err := util.SelfSignedCert(certDir, host); err == nil {
		e.CertFile, e.KeyFile, e.SelfSigned = c, k, true
		e.CertSHA256 = util.CertSHA256(c)
	}
	return e
}

// certJob is the background issuance of the node's certificate.
type certJob struct {
	mu      sync.Mutex
	busy    bool
	tried   time.Time
	domain  string
	renewed func() // tests watch for issuances
}

// setDomain saves the domain and email the panel sent and gets a certificate
// when the node has none for it.
func (a *Agent) setDomain(domain, email string) {
	if a.St.GetSetting(keyDomain, "") != domain || a.St.GetSetting(keyACME, "") != email {
		a.St.SetSetting(keyDomain, domain)
		a.St.SetSetting(keyACME, email)
		a.St.SetSetting(keyCertErr, "")
	}
	a.ensureCert(false)
}

// ensureCert gets or renews the certificate in the background. A failure is
// tried again after an hour (or at once for another domain).
func (a *Agent) ensureCert(force bool) {
	domain, email := a.St.GetSetting(keyDomain, ""), a.St.GetSetting(keyACME, "")
	if domain == "" || a.CertDir == "" {
		return
	}
	cert, _ := tlsmgr.Paths(a.CertDir, domain)
	if ok, _ := tlsmgr.Valid(cert, domain, tlsmgr.RenewBefore); ok && !force {
		return
	}
	j := &a.cert
	j.mu.Lock()
	if j.busy || (!force && j.domain == domain && a.St.GetSetting(keyCertErr, "") != "" && time.Since(j.tried) < retryAfter) {
		j.mu.Unlock()
		return
	}
	j.busy, j.tried, j.domain = true, time.Now(), domain
	j.mu.Unlock()
	go func() {
		defer func() {
			j.mu.Lock()
			j.busy = false
			j.mu.Unlock()
		}()
		obtain := a.Obtain
		if obtain == nil {
			obtain = tlsmgr.Renew
		}
		_, _, renewed, err := obtain(a.CertDir, domain, email)
		if err != nil {
			msg := tlsmgr.Explain(err)
			if a.St.GetSetting(keyCertErr, "") != msg {
				a.St.Log("tls", "no certificate for %s yet: %s", domain, msg)
			}
			a.St.SetSetting(keyCertErr, msg)
			log.Printf("node: certificate for %s: %v", domain, err)
			return
		}
		a.St.SetSetting(keyCertErr, "")
		if renewed {
			log.Printf("node: certificate for %s issued", domain)
			a.St.Log("tls", "certificate issued for %s", domain)
			a.Mgr.CertsRenewed()
		}
		if j.renewed != nil {
			j.renewed()
		}
	}()
}

// RunCerts checks the certificate twice a day and renews it a month before
// it expires.
func (a *Agent) RunCerts(stop <-chan struct{}) {
	t := time.NewTicker(12 * time.Hour)
	defer t.Stop()
	for {
		a.ensureCert(false)
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

// certStatus fills the certificate part of the status.
func (a *Agent) certStatus(st *Status, env protocol.Env) {
	st.Domain = a.St.GetSetting(keyDomain, "")
	st.RealCert = !env.SelfSigned && env.CertFile != ""
	st.CertError = a.St.GetSetting(keyCertErr, "")
	if st.RealCert {
		if t, err := tlsmgr.Expiry(env.CertFile); err == nil {
			st.CertExpiry = t.Unix()
		}
	}
	a.cert.mu.Lock()
	st.CertBusy = a.cert.busy
	a.cert.mu.Unlock()
}
