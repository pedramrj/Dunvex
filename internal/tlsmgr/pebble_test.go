package tlsmgr

// Live ACME tests against Pebble (see the pebbletest package); skipped
// without DUNVEX_PEBBLE.

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/tlsmgr/pebbletest"
)

func TestPebbleIssue(t *testing.T) {
	p := pebbletest.Start(t)
	dir := t.TempDir()
	is := &Issuer{Directory: p.Dir, HTTPClient: p.HTTP, KeyFile: filepath.Join(dir, "account.pem"), HTTP01: &HTTP01{Addr: p.HTTP01},
		DNS01: pebbletest.DNS{P: p, Refuse: map[string]bool{"refused.dunvex.test": true}}, Timeout: 40 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// two names over HTTP and one through DNS, in one certificate
	got, err := is.Issue(ctx, Order{Names: []string{"vpn.dunvex.test", "vpn2.dunvex.test", "dns.dunvex.test"}, DNS: map[string]bool{"dns.dunvex.test": true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Failed) != 0 || strings.Join(got.Names, ",") != "vpn.dunvex.test,vpn2.dunvex.test,dns.dunvex.test" {
		t.Fatalf("issued: %+v", got)
	}
	cert, key := filepath.Join(dir, "c", "fullchain.pem"), filepath.Join(dir, "c", "privkey.pem")
	if err := WriteFiles(cert, key, got.CertPEM, got.KeyPEM); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
		t.Fatal(err)
	}
	info, err := Read(cert)
	if err != nil || !info.Covers("vpn2.dunvex.test") || !info.Covers("dns.dunvex.test") || info.SelfSigned { // (Pebble picks a random profile when none is asked for)
		t.Fatalf("certificate: %+v %v", info, err)
	}

	// one name that fails (its DNS record cannot be made) and one whose DNS
	// does not answer: left out, the others are issued
	if err := p.Do("/set-servfail", map[string]any{"host": "broken.dunvex.test."}); err != nil {
		t.Fatal(err)
	}
	got, err = is.Issue(ctx, Order{Names: []string{"vpn.dunvex.test", "refused.dunvex.test", "broken.dunvex.test"}, DNS: map[string]bool{"refused.dunvex.test": true}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Names, ",") != "vpn.dunvex.test" || got.Failed["refused.dunvex.test"] == nil || got.Failed["refused.dunvex.test"].Code != "nodns" ||
		got.Failed["broken.dunvex.test"] == nil {
		t.Fatalf("partial: names %v failed %+v", got.Names, got.Failed)
	}
	t.Logf("broken: %+v", got.Failed["broken.dunvex.test"])

	// a name the authority refuses outright: left out, the rest asked for again
	got, err = is.Issue(ctx, Order{Names: []string{"vpn.dunvex.test", "blocked.dunvex.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Names, ",") != "vpn.dunvex.test" || got.Failed["blocked.dunvex.test"] == nil || got.Failed["blocked.dunvex.test"].Code != "rejected" {
		t.Fatalf("refused: %v %+v", got.Names, got.Failed)
	}
	_, err = is.Issue(ctx, Order{Names: []string{"blocked.dunvex.test"}})
	var ie0 *IssueError
	if !errors.As(err, &ie0) || ie0.Failed["blocked.dunvex.test"] == nil {
		t.Fatalf("only a refused name: %v", err)
	}

	// a required name that fails: nothing is issued
	_, err = is.Issue(ctx, Order{Names: []string{"vpn.dunvex.test", "broken.dunvex.test"}, Require: []string{"broken.dunvex.test"}})
	var ie *IssueError
	if !errors.As(err, &ie) || ie.Failed["broken.dunvex.test"] == nil {
		t.Fatalf("required: %v", err)
	}

	// the IP address, with the short-lived profile
	got, err = is.Issue(ctx, Order{IPs: []string{"127.0.0.1"}, Profile: ShortLived})
	if err != nil {
		t.Fatal(err)
	}
	WriteFiles(cert, key, got.CertPEM, got.KeyPEM)
	info, _ = Read(cert)
	if !info.Covers("127.0.0.1") || len(info.Names) != 0 || info.NotAfter.Sub(info.NotBefore) > 7*24*time.Hour {
		t.Fatalf("IP certificate: %+v", info)
	}

	// port 80 (here Pebble's port) busy: the HTTP names fail with the reason, DNS ones still pass
	ln, err := net.Listen("tcp", p.HTTP01)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got, err = is.Issue(ctx, Order{Names: []string{"vpn3.dunvex.test", "dns2.dunvex.test"}, DNS: map[string]bool{"dns2.dunvex.test": true}})
	if err != nil {
		t.Fatal(err)
	}
	if f := got.Failed["vpn3.dunvex.test"]; f == nil || f.Code != "port80" || strings.Join(got.Names, ",") != "dns2.dunvex.test" {
		t.Fatalf("busy port: %v %+v", got.Names, got.Failed)
	}
}
