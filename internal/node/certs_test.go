package node

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/tlsmgr"
)

// fakeIssue writes a certificate for domain where Let's Encrypt's would go.
func fakeIssue(t *testing.T, certDir, domain string, days int) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Duration(days) * 24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	cert, key := tlsmgr.Paths(certDir, domain)
	os.MkdirAll(filepath.Dir(cert), 0o700)
	os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestNodeCertificate(t *testing.T) {
	a, _, dir := startNode(t)
	certDir := filepath.Join(dir, "certs")
	a.CertDir = certDir
	a.Env = func() protocol.Env { return EnvFor(a.St, certDir, "203.0.113.9", "") }
	var calls atomic.Int32
	var mu sync.Mutex
	fail := errors.New("Let's Encrypt could not reach port 80")
	a.Obtain = func(dir, domain, email string) (string, string, bool, error) {
		calls.Add(1)
		mu.Lock()
		defer mu.Unlock()
		if fail != nil {
			return "", "", false, fail
		}
		fakeIssue(t, dir, domain, 90)
		c, k := tlsmgr.Paths(dir, domain)
		return c, k, true, nil
	}

	// no domain: the self-signed certificate, pinned in links
	if e := a.Env(); !e.SelfSigned || e.CertSHA256 == "" || e.Domain != "" {
		t.Fatalf("no domain: %+v", e)
	}
	// a domain whose certificate fails: reported, and not tried again at once
	if err := a.Apply(State{Domain: "node.example.com", ACMEEmail: "me@example.com"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the failure", func() bool { return a.Status().CertError != "" && !a.Status().CertBusy })
	st := a.Status()
	if st.Domain != "node.example.com" || st.RealCert || !strings.Contains(st.CertError, "port 80") || a.Env().Domain != "node.example.com" || !a.Env().SelfSigned {
		t.Fatalf("after a failure: %+v", st)
	}
	a.Apply(State{Domain: "node.example.com", ACMEEmail: "me@example.com"})
	time.Sleep(100 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("tried again within the hour: %d", calls.Load())
	}
	// the issue is fixed; a new domain is tried at once and its certificate used
	mu.Lock()
	fail = nil
	mu.Unlock()
	a.Apply(State{Domain: "vpn.example.com", ACMEEmail: "me@example.com"})
	waitFor(t, "the certificate", func() bool { return a.Status().RealCert })
	st, e := a.Status(), a.Env()
	if st.CertError != "" || st.CertExpiry < time.Now().Add(80*24*time.Hour).Unix() || e.SelfSigned || e.CertSHA256 != "" || e.Domain != "vpn.example.com" || st.CertSHA256 != "" {
		t.Fatalf("with a certificate: %+v %+v", st, e)
	}
	// valid for long enough: nothing to do
	n := calls.Load()
	a.ensureCert(false)
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != n {
		t.Fatal("renewed a fresh certificate")
	}
	// close to expiry: renewed
	fakeIssue(t, certDir, "vpn.example.com", 10)
	a.ensureCert(false)
	waitFor(t, "the renewal", func() bool { return calls.Load() == n+1 && !a.Status().CertBusy })
	if ok, _ := tlsmgr.Valid(filepath.Join(certDir, "vpn.example.com", "fullchain.pem"), "vpn.example.com", tlsmgr.RenewBefore); !ok {
		t.Fatal("not renewed")
	}
	// the domain removed: back to the self-signed certificate
	a.Apply(State{})
	if e := a.Env(); !e.SelfSigned || e.Domain != "" || a.Status().RealCert {
		t.Fatalf("domain removed: %+v", e)
	}
}

func TestNodeUpdate(t *testing.T) {
	a, tok, dir := startNode(t)
	ctx := context.Background()
	panel, _, _ := LoadOrCreateCert(filepath.Join(dir, "panel"), "panel-client", "dunvex-panel")
	c := NewClient(tok.Addr, tok.Fingerprint, panel)
	if err := c.Pair(ctx, tok.Secret); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "dunvex")
	os.WriteFile(exe, []byte("Dunvex v1.0.0"), 0o755)
	a.Version, a.Executable = "v1.0.0", exe
	// the stand-in "runs" a binary by reading the version written in it
	a.CheckBinary = func(p string) (string, error) {
		b, _ := os.ReadFile(p)
		if m := versionRe.FindStringSubmatch(string(b)); m != nil {
			return m[1], nil
		}
		return "", errors.New("the new binary does not run on this server")
	}
	var restarts atomic.Int32
	a.Restart = func() { restarts.Add(1) }
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	content := func() string { b, _ := os.ReadFile(exe); return string(b) }

	bin := []byte("Dunvex v1.2.0")
	if _, err := c.Update(ctx, bin, sum([]byte("something else")), "v1.2.0"); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("a damaged binary: %v", err)
	}
	if _, err := c.Update(ctx, []byte("ELF junk"), sum([]byte("ELF junk")), "v1.2.0"); err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("a binary that does not run: %v", err)
	}
	if _, err := c.Update(ctx, bin, sum(bin), "v1.3.0"); err == nil || !strings.Contains(err.Error(), "not v1.3.0") {
		t.Fatalf("another version: %v", err)
	}
	if content() != "Dunvex v1.0.0" || restarts.Load() != 0 {
		t.Fatal("a refused update changed the node")
	}
	got, err := c.Update(ctx, bin, sum(bin), "v1.2.0")
	if err != nil || got != "v1.2.0" || content() != "Dunvex v1.2.0" {
		t.Fatalf("update: %q %v %q", got, err, content())
	}
	waitFor(t, "the restart", func() bool { return restarts.Load() == 1 })
	if _, err := os.Stat(exe + ".update"); err == nil {
		t.Fatal("the temporary file was left")
	}

	// from GitHub: the node downloads the release the panel names
	a.Download = func(v string) ([]byte, error) {
		if v != "v1.4.0" {
			return nil, errors.New("no such release")
		}
		return []byte("Dunvex v1.4.0"), nil
	}
	if _, err := c.Update(ctx, nil, "", "v9.9.9"); err == nil || !strings.Contains(err.Error(), "no such release") {
		t.Fatalf("a missing release: %v", err)
	}
	if got, err := c.Update(ctx, nil, "", "v1.4.0"); err != nil || got != "v1.4.0" || content() != "Dunvex v1.4.0" {
		t.Fatalf("from GitHub: %q %v", got, err)
	}
	// another panel cannot update this node
	other, _, _ := LoadOrCreateCert(filepath.Join(dir, "other"), "panel-client", "dunvex-panel")
	if _, err := NewClient(tok.Addr, tok.Fingerprint, other).Update(ctx, bin, sum(bin), "v1.2.0"); err == nil {
		t.Fatal("an unpaired panel updated the node")
	}
}
