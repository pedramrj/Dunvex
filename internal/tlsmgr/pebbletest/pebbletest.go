// Package pebbletest runs Pebble, Let's Encrypt's test certificate
// authority, with its DNS test server answering for every name, for live
// ACME tests. They run only with
//
//	DUNVEX_PEBBLE=<dir with bin/pebble[.exe], bin/pebble-challtestsrv[.exe], certs/pebble.minica.pem and certs/localhost/{cert,key}.pem>
//
// (go install github.com/letsencrypt/pebble/v2/cmd/...@latest; the certs
// are in its test/certs). Never point tests at Let's Encrypt's production.
package pebbletest

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Pebble is a running Pebble with its DNS test server.
type Pebble struct {
	Dir    string // ACME directory URL
	HTTP   *http.Client
	Mgmt   string // the DNS test server's management address
	HTTP01 string // where Pebble sends HTTP validations
}

// Start starts Pebble for a test (skips without DUNVEX_PEBBLE).
func Start(t testing.TB) *Pebble {
	root := os.Getenv("DUNVEX_PEBBLE")
	if root == "" {
		t.Skip("DUNVEX_PEBBLE is not set (see the pebbletest package)")
	}
	exe := func(n string) string {
		if runtime.GOOS == "windows" {
			n += ".exe"
		}
		return filepath.Join(root, "bin", n)
	}
	free := func() int {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		return ln.Addr().(*net.TCPAddr).Port
	}
	acmePort, dnsPort, mgmtPort, httpPort, pmgmt := free(), free(), free(), free(), free()
	dir := t.TempDir()
	cfg := map[string]any{"pebble": map[string]any{
		"listenAddress": fmt.Sprintf("127.0.0.1:%d", acmePort), "managementListenAddress": fmt.Sprintf("127.0.0.1:%d", pmgmt),
		"certificate": filepath.Join(root, "certs", "localhost", "cert.pem"), "privateKey": filepath.Join(root, "certs", "localhost", "key.pem"),
		"httpPort": httpPort, "tlsPort": free(), "externalAccountBindingRequired": false,
		"domainBlocklist": []string{"blocked.dunvex.test"},
		"retryAfter":      map[string]int{"authz": 1, "order": 1},
		"profiles": map[string]any{
			"default":    map[string]any{"description": "default", "validityPeriod": 7776000},
			"shortlived": map[string]any{"description": "short", "validityPeriod": 518400},
		},
	}}
	b, _ := json.Marshal(cfg)
	cfgPath := filepath.Join(dir, "pebble.json")
	os.WriteFile(cfgPath, b, 0o600)

	cts := exec.Command(exe("pebble-challtestsrv"), "-http01", "", "-https01", "", "-doh", "", "-tlsalpn01", "",
		"-dnsserver", fmt.Sprintf("127.0.0.1:%d", dnsPort), "-management", fmt.Sprintf("127.0.0.1:%d", mgmtPort), "-defaultIPv4", "127.0.0.1", "-defaultIPv6", "")
	peb := exec.Command(exe("pebble"), "-config", cfgPath, "-dnsserver", fmt.Sprintf("127.0.0.1:%d", dnsPort), "-strict=false")
	peb.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0", "PEBBLE_AUTHZREUSE=100")
	var logs bytes.Buffer
	cts.Stdout, cts.Stderr, peb.Stdout, peb.Stderr = &logs, &logs, &logs, &logs
	for _, c := range []*exec.Cmd{cts, peb} {
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		c := c
		t.Cleanup(func() { c.Process.Kill(); c.Wait() })
	}
	pool := x509.NewCertPool()
	ca, err := os.ReadFile(filepath.Join(root, "certs", "pebble.minica.pem"))
	if err != nil {
		t.Fatal(err)
	}
	pool.AppendCertsFromPEM(ca)
	hc := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	p := &Pebble{Dir: fmt.Sprintf("https://127.0.0.1:%d/dir", acmePort), HTTP: hc, Mgmt: fmt.Sprintf("http://127.0.0.1:%d", mgmtPort),
		HTTP01: fmt.Sprintf(":%d", httpPort)}
	// localhost's certificate names localhost: the directory is fetched by that name
	p.Dir = fmt.Sprintf("https://localhost:%d/dir", acmePort)
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := hc.Get(p.Dir)
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pebble did not start: %v\n%s", err, logs.String())
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("pebble:\n%s", logs.String())
		}
	})
	return p
}

// Do calls the DNS test server's management API.
func (p *Pebble) Do(path string, body map[string]any) error {
	b, _ := json.Marshal(body)
	resp, err := http.Post(p.Mgmt+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	return nil
}

// DNS answers dns-01 challenges through the DNS test server.
type DNS struct {
	P      *Pebble
	Refuse map[string]bool // names whose records cannot be made
}

func (d DNS) Present(_ context.Context, fqdn, value string) error {
	if d.Refuse[strings.TrimPrefix(fqdn, "_acme-challenge.")] {
		return errors.New("refused by the test")
	}
	return d.P.Do("/set-txt", map[string]any{"host": fqdn + ".", "value": value})
}

func (d DNS) CleanUp(_ context.Context, fqdn, _ string) error {
	return d.P.Do("/clear-txt", map[string]any{"host": fqdn + "."})
}
