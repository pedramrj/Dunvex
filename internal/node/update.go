package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/coremgr"
)

// MaxBinary caps an uploaded Dunvex binary.
const MaxBinary = 200 << 20

var versionRe = regexp.MustCompile(`Dunvex (v?[0-9][0-9A-Za-z.\-+]*|dev)`)

// binaryVersion runs a candidate binary's "version" command and reads the
// version it prints: a file that does not run on this server never replaces
// the running one.
func binaryVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("the new binary does not run on this server: %v %s", err, strings.TrimSpace(string(out)))
	}
	m := versionRe.FindStringSubmatch(string(out))
	if m == nil {
		return "", fmt.Errorf("the new binary is not Dunvex (%q)", strings.TrimSpace(string(out)))
	}
	return m[1], nil
}

// update replaces this node's Dunvex with the panel's: either the binary the
// panel sends (when it runs on the same system), or the release the panel
// names, from GitHub. The node checks that the new binary runs and is the
// version asked for, swaps it in, and restarts.
func (a *Agent) update(w http.ResponseWriter, r *http.Request) {
	a.upd.Lock()
	defer a.upd.Unlock()
	want := strings.TrimSpace(r.Header.Get("X-Dunvex-Version"))
	var data []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/octet-stream") {
		b, err := io.ReadAll(io.LimitReader(r.Body, MaxBinary+1))
		if err != nil {
			reply(w, http.StatusBadRequest, map[string]string{"error": "the binary did not arrive: " + err.Error()})
			return
		}
		if len(b) > MaxBinary {
			reply(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "the binary is too large"})
			return
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, r.Header.Get("X-Dunvex-SHA256")) {
			reply(w, http.StatusBadRequest, map[string]string{"error": "the binary was damaged on the way (checksum mismatch)"})
			return
		}
		data = b
	} else {
		var in struct {
			Version string `json:"version"`
		}
		json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in)
		want = strings.TrimSpace(in.Version)
		if !strings.HasPrefix(want, "v") {
			reply(w, http.StatusBadRequest, map[string]string{"error": "no release version to download"})
			return
		}
		download := a.Download
		if download == nil {
			download = coremgr.DunvexBinary
		}
		b, err := download(want)
		if err != nil {
			reply(w, http.StatusBadGateway, map[string]string{"error": "could not download Dunvex " + want + ": " + err.Error()})
			return
		}
		data = b
	}
	got, err := a.install(data, want)
	if err != nil {
		reply(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("node: updated from %s to %s, restarting", a.Version, got)
	a.St.Log("update", "Dunvex updated from %s to %s by the panel", a.Version, got)
	reply(w, http.StatusOK, map[string]string{"version": got, "from": a.Version})
	if a.Restart != nil {
		go func() { time.Sleep(time.Second); a.Restart() }()
	}
}

// install writes the binary next to the running one, checks it and swaps it in.
func (a *Agent) install(data []byte, want string) (string, error) {
	exe := a.Executable
	if exe == "" {
		p, err := os.Executable()
		if err != nil {
			return "", err
		}
		if p, err = filepath.EvalSymlinks(p); err != nil {
			return "", err
		}
		exe = p
	}
	tmp := exe + ".update"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return "", fmt.Errorf("cannot write the new binary: %w", err)
	}
	check := a.CheckBinary
	if check == nil {
		check = binaryVersion
	}
	got, err := check(tmp)
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if want != "" && got != want {
		os.Remove(tmp)
		return "", fmt.Errorf("the new binary is %s, not %s", got, want)
	}
	if runtime.GOOS == "windows" {
		// a running program cannot be replaced there, but it can be moved aside
		os.Remove(exe + ".old")
		os.Rename(exe, exe+".old")
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("cannot replace the running binary: %w", err)
	}
	return got, nil
}

// ErrNoRelease: the panel is not a release build, so nodes on another
// system cannot fetch its version from GitHub.
var ErrNoRelease = errors.New("this panel is a development build: nodes on another system cannot download it")
