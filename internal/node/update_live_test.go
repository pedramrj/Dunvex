//go:build linux

package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestUpdateWithRealBinaries updates a node with a real Dunvex binary (it is
// run with "version" before it replaces the old one) and downloads a real
// release from GitHub. DUNVEX_NEW_BIN is a Dunvex binary for this system;
// DUNVEX_RELEASE a published release tag.
func TestUpdateWithRealBinaries(t *testing.T) {
	newBin, tag := os.Getenv("DUNVEX_NEW_BIN"), os.Getenv("DUNVEX_RELEASE")
	if newBin == "" || tag == "" {
		t.Skip("needs DUNVEX_NEW_BIN and DUNVEX_RELEASE")
	}
	a, tok, dir := startNode(t)
	ctx := context.Background()
	panel, _, _ := LoadOrCreateCert(filepath.Join(dir, "panel"), "panel-client", "dunvex-panel")
	c := NewClient(tok.Addr, tok.Fingerprint, panel)
	if err := c.Pair(ctx, tok.Secret); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "dunvex")
	os.WriteFile(exe, []byte("#!/bin/sh\necho Dunvex v0.0.1\n"), 0o755)
	a.Executable = exe
	var restarts atomic.Int32
	a.Restart = func() { restarts.Add(1) }

	bin, err := os.ReadFile(newBin)
	if err != nil {
		t.Fatal(err)
	}
	want, err := binaryVersion(newBin)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bin)
	// junk that does not run is refused, and the old binary stays
	junk := []byte("\x7fELF this is not a program")
	js := sha256.Sum256(junk)
	if _, err := c.Update(ctx, junk, hex.EncodeToString(js[:]), want); err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("junk: %v", err)
	}
	got, err := c.Update(ctx, bin, hex.EncodeToString(sum[:]), want)
	if err != nil || got != want {
		t.Fatalf("update: %q %v", got, err)
	}
	now, _ := os.ReadFile(exe)
	fi, _ := os.Stat(exe)
	if !bytes.Equal(now, bin) || fi.Mode()&0o111 == 0 {
		t.Fatal("the binary was not swapped in, or is not executable")
	}
	if v, err := binaryVersion(exe); err != nil || v != want {
		t.Fatalf("the new binary: %q %v", v, err)
	}
	t.Logf("updated to %s (%d MB) from the panel", want, len(bin)>>20)

	// from GitHub
	got, err = c.Update(ctx, nil, "", tag)
	if err != nil || got != tag {
		t.Fatalf("from GitHub: %q %v", got, err)
	}
	if v, _ := binaryVersion(exe); v != tag {
		t.Fatalf("after the download: %q", v)
	}
	t.Logf("downloaded %s from GitHub and swapped it in", tag)
	waitFor(t, "the restarts", func() bool { return restarts.Load() >= 2 })
}
