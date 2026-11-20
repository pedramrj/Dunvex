// Package tlsmgr obtains Let's Encrypt certificates (HTTP-01 on port 80).
package tlsmgr

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// Paths returns where the certificate of a domain is stored.
func Paths(certDir, domain string) (cert, key string) {
	d := filepath.Join(certDir, domain)
	return filepath.Join(d, "fullchain.pem"), filepath.Join(d, "privkey.pem")
}

// Obtain gets (or renews when close to expiry) a certificate for domain and
// writes fullchain.pem / privkey.pem. Port 80 must be free and reachable.
func Obtain(certDir, domain, email string) (certPath, keyPath string, err error) {
	if ips, e := net.LookupIP(domain); e != nil || len(ips) == 0 {
		return "", "", fmt.Errorf("domain %s does not resolve; add an A record pointing to this server first", domain)
	}
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domain),
		Cache:      autocert.DirCache(filepath.Join(certDir, ".acme")),
		Email:      email,
	}
	ln, err := net.Listen("tcp", ":80")
	if err != nil {
		return "", "", fmt.Errorf("port 80 is busy; it must be free for Let's Encrypt validation: %w", err)
	}
	srv := &http.Server{Handler: m.HTTPHandler(nil), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	hello := &tls.ClientHelloInfo{
		ServerName:       domain,
		SignatureSchemes: []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
		SupportedCurves:  []tls.CurveID{tls.CurveP256},
		CipherSuites:     []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
		SupportedVersions: []uint16{tls.VersionTLS13, tls.VersionTLS12},
	}
	done := make(chan struct{})
	var cert *tls.Certificate
	go func() {
		cert, err = m.GetCertificate(hello)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Minute):
		return "", "", errors.New("timed out waiting for Let's Encrypt")
	}
	if err != nil {
		return "", "", err
	}
	certPath, keyPath = Paths(certDir, domain)
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return "", "", err
	}
	var chain []byte
	for _, der := range cert.Certificate {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	var keyPEM []byte
	switch k := cert.PrivateKey.(type) {
	case *ecdsa.PrivateKey:
		b, e := x509.MarshalECPrivateKey(k)
		if e != nil {
			return "", "", e
		}
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: b})
	case *rsa.PrivateKey:
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	default:
		return "", "", errors.New("unsupported key type")
	}
	if err := os.WriteFile(certPath, chain, 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

// Expiry returns the NotAfter time of the first certificate in a PEM file.
func Expiry(certPath string) (time.Time, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return time.Time{}, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return time.Time{}, errors.New("no certificate")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return c.NotAfter, nil
}

// SNIResult is the outcome of testing a REALITY target.
type SNIResult struct {
	Domain    string `json:"domain"`
	OK        bool   `json:"ok"`
	TLS13     bool   `json:"tls13"`
	H2        bool   `json:"h2"`
	LatencyMs int64  `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
}

// TestSNI checks that a site supports TLS 1.3 + HTTP/2 (required for a good REALITY target).
func TestSNI(domain string) SNIResult {
	r := SNIResult{Domain: domain}
	start := time.Now()
	d := &net.Dialer{Timeout: 6 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", net.JoinHostPort(domain, "443"), &tls.Config{
		ServerName: domain, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{tls.X25519},
	})
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer conn.Close()
	r.LatencyMs = time.Since(start).Milliseconds()
	st := conn.ConnectionState()
	r.TLS13 = st.Version == tls.VersionTLS13
	r.H2 = st.NegotiatedProtocol == "h2"
	r.OK = r.TLS13 && r.H2
	if !r.OK {
		r.Error = "needs TLS 1.3 and HTTP/2"
	}
	return r
}
