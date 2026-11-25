package bridge

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// A wrong path on the CDN listener answers like nginx, not like a Go service,
// so a scanner sees an ordinary web server.
func TestNginx404(t *testing.T) {
	w := httptest.NewRecorder()
	writeNginx404(w)
	if w.Code != 404 {
		t.Fatalf("want 404, got %d", w.Code)
	}
	if got := w.Header().Get("Server"); got != "nginx" {
		t.Fatalf("want Server: nginx, got %q", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<center>nginx</center>") || !strings.Contains(body, "404 Not Found") {
		t.Fatalf("body does not look like nginx's 404: %q", body)
	}
	if strings.Contains(body, "page not found") {
		t.Fatal("body leaks Go's default 404 text")
	}
}

// The ICMP id and CDN path are derived from the PSK, so two different keys give
// different fingerprints and the same key is stable.
func TestFingerprintsFromPSK(t *testing.T) {
	a := []byte("psk-one-aaaaaaaaaaaaaaaaaaaaaaaa")
	b := []byte("psk-two-bbbbbbbbbbbbbbbbbbbbbbbb")
	if DeriveICMPID(a) == 0 {
		t.Fatal("ICMP id must not be 0")
	}
	if DeriveICMPID(a) != DeriveICMPID(a) {
		t.Fatal("ICMP id must be stable for one PSK")
	}
	if DeriveICMPID(a) == DeriveICMPID(b) {
		t.Fatal("two PSKs should give different ICMP ids")
	}
	if !strings.HasPrefix(DeriveCDNPath(a), "/") {
		t.Fatal("CDN path must start with /")
	}
	if DeriveCDNPath(a) == DeriveCDNPath(b) {
		t.Fatal("two PSKs should give different CDN paths")
	}
	if DeriveCDNPath(a) == "/dvxb" {
		t.Fatal("CDN path should not be the constant default")
	}
}
