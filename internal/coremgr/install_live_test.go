//go:build linux

package coremgr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallSidecars downloads mita, amneziawg-go and the TrustTunnel endpoint from the real releases
// into a temporary folder (checksums checked by download). Needs network and
// DUNVEX_NET_TEST=1.
func TestInstallSidecars(t *testing.T) {
	if os.Getenv("DUNVEX_NET_TEST") != "1" {
		t.Skip("DUNVEX_NET_TEST not set")
	}
	dir := t.TempDir()
	v, err := InstallMita(dir)
	if err != nil {
		t.Fatalf("mita: %v", err)
	}
	out, err := exec.Command(filepath.Join(dir, "mita"), "version").CombinedOutput()
	if err != nil || !strings.Contains(string(out), v) {
		t.Errorf("mita %s runs as %q (%v)", v, out, err)
	}
	t.Logf("mita %s", v)
	a, err := InstallAWG(dir)
	if err != nil {
		t.Fatalf("amneziawg-go: %v", err)
	}
	if !strings.HasPrefix(a, "v3.") {
		t.Errorf("amneziawg-go version %q", a)
	}
	t.Logf("amneziawg-go %s", a)
	tt, err := InstallTrustTunnel(dir)
	if err != nil {
		t.Fatalf("trusttunnel: %v", err)
	}
	out, err = exec.Command(filepath.Join(dir, "trusttunnel"), "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != tt {
		t.Errorf("trusttunnel %s runs as %q (%v)", tt, out, err)
	}
	t.Logf("trusttunnel %s", tt)
}
