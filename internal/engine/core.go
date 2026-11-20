// Package engine supervises the proxy cores (Xray, sing-box), builds their
// configs from the database and collects per-user traffic.
package engine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"github.com/pedramrj/dunvex/internal/protocol"
)

// Core is one supervised proxy process.
type Core struct {
	Name       string // "xray" or "sing-box"
	BinDir     string
	ConfigPath string
	APIPort    int
	// Live, when set, tries to apply a changed config to the running process
	// without a restart and reports whether that worked.
	Live func(old, cfg []byte) bool
	// OnLine sees every log line; returning true keeps the line out of Logs().
	OnLine func(line string) bool

	opMu      sync.Mutex // serialises Apply/Restart/Stop so only one process ever runs
	mu        sync.Mutex
	cmd       *exec.Cmd
	wanted    bool // should be running
	startedAt time.Time
	lastErr   string
	restarts  int
	logs      []string
	version   string
	tags      []string
	done      chan struct{}
}

// Bin returns the core executable path.
func (c *Core) Bin() string {
	name := c.Name
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(c.BinDir, name)
}

// Installed reports whether the binary exists.
func (c *Core) Installed() bool {
	st, err := os.Stat(c.Bin())
	return err == nil && !st.IsDir()
}

// Version returns the core version (cached).
func (c *Core) Version() string {
	c.mu.Lock()
	v := c.version
	c.mu.Unlock()
	if v != "" || !c.Installed() {
		return v
	}
	args := []string{"version"}
	if c.Name == "mtg" || c.Name == "trusttunnel" || c.Name == "slipstream-server" {
		args = []string{"--version"}
	}
	if c.Name == "usque" {
		args = []string{"-c", c.ConfigPath, "version"}
	}
	out, err := exec.Command(c.Bin(), args...).CombinedOutput()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n")
	if c.Name == "usque" {
		for _, l := range lines {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "usque version: "); ok {
				lines = []string{"usque version " + strings.TrimPrefix(v, "v")}
			}
		}
	}
	var tags []string
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "Tags:") {
			for _, t := range strings.Split(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "Tags:")), ",") {
				tags = append(tags, strings.TrimSpace(t))
			}
		}
	}
	f := strings.Fields(lines[0])
	switch {
	case c.Name == "mtg" && len(f) >= 1:
		v = f[0] // "2.3.0 (go1.x, …)"
	case c.Name == "trusttunnel" && len(f) >= 1:
		v = f[len(f)-1] // "1.3.1"
	case c.Name == "mita" && len(f) >= 1:
		v = f[len(f)-1] // a log line ending in "3.39.0"
	case c.Name == "xray" && len(f) >= 2:
		v = f[1]
	case len(f) >= 3:
		v = f[2]
	case len(f) > 0:
		v = f[len(f)-1]
	}
	c.mu.Lock()
	c.version, c.tags = v, tags
	c.mu.Unlock()
	return v
}

// HasTag reports whether a sing-box build tag is present.
func (c *Core) HasTag(tag string) bool {
	c.Version()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range c.tags {
		if t == tag {
			return true
		}
	}
	return false
}

// ResetVersion clears the cached version (after an update).
func (c *Core) ResetVersion() {
	c.mu.Lock()
	c.version, c.tags = "", nil
	c.mu.Unlock()
}

func (c *Core) env() []string {
	env := os.Environ()
	if c.Name == "xray" {
		env = append(env, "XRAY_LOCATION_ASSET="+c.BinDir)
	}
	if c.Name == "sing-box" {
		env = append(env, "ENABLE_DEPRECATED_LEGACY_DNS_SERVERS=true")
	}
	if c.Name == "mita" {
		// mita reads its JSON from our file and is controlled through its own
		// socket, without the "mita" system user its package would create
		env = append(env, "MITA_CONFIG_JSON_FILE="+c.ConfigPath, "MITA_UDS_PATH="+c.Socket(),
			"MITA_INSECURE_UDS=1", "MITA_LOG_NO_TIMESTAMP=1")
	}
	return env
}

// Test validates a config without applying it.
func (c *Core) Test(cfg []byte) error {
	if !c.Installed() {
		return fmt.Errorf("%s is not installed", c.Name)
	}
	tmp := c.ConfigPath + ".test.json"
	if err := os.WriteFile(tmp, cfg, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	var cmd *exec.Cmd
	switch c.Name {
	case "xray":
		cmd = exec.Command(c.Bin(), "run", "-test", "-c", tmp)
	case "mtg", "mita", "trusttunnel", "usque", "slipstream-server":
		return nil // no check command; Dunvex writes these small configs itself
	default:
		cmd = exec.Command(c.Bin(), "check", "-c", tmp)
	}
	cmd.Env = c.env()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = ctx
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s rejected the config: %s", c.Name, lastLines(string(out), 6))
	}
	return nil
}

// Apply tests a config, writes it and (re)starts the core. An empty cfg stops it.
func (c *Core) Apply(cfg []byte) error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if cfg == nil {
		c.stop()
		return nil
	}
	old, _ := os.ReadFile(c.ConfigPath)
	if bytes.Equal(old, cfg) && c.Running() {
		return nil
	}
	if err := c.Test(cfg); err != nil {
		c.mu.Lock()
		c.lastErr = err.Error()
		c.mu.Unlock()
		return err
	}
	if c.Live != nil && old != nil && c.Running() && c.Live(old, cfg) {
		return os.WriteFile(c.ConfigPath, cfg, 0o600)
	}
	if err := os.WriteFile(c.ConfigPath, cfg, 0o600); err != nil {
		return err
	}
	c.stop()
	return c.start()
}

// MitaMetricsDump is where mita keeps its counters between starts.
var MitaMetricsDump = "/var/lib/mita/metrics.pb"

// Socket is the control socket of a core that has one (mita).
func (c *Core) Socket() string { return filepath.Join(filepath.Dir(c.ConfigPath), c.Name+".sock") }

// StartedAt is when the current process started (zero when stopped).
func (c *Core) StartedAt() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil {
		return time.Time{}
	}
	return c.startedAt
}

// Running reports whether the process is alive.
func (c *Core) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cmd != nil && c.cmd.ProcessState == nil
}

// Restart stops and starts the core with the current config file.
func (c *Core) Restart() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.stop()
	if _, err := os.Stat(c.ConfigPath); err != nil {
		return nil // nothing to run yet
	}
	return c.start()
}

func (c *Core) start() error {
	if !c.Installed() {
		return fmt.Errorf("%s is not installed", c.Name)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killStale()
	cmd := exec.Command(c.Bin(), "run", "-c", c.ConfigPath)
	switch c.Name {
	case "mtg":
		cmd = exec.Command(c.Bin(), "run", c.ConfigPath)
	case "trusttunnel":
		cmd = exec.Command(c.Bin(), c.ConfigPath, filepath.Join(filepath.Dir(c.ConfigPath), protocol.TTHostsFile))
	case "usque":
		// WARP over MASQUE as a SOCKS proxy that only this server can reach
		// --always-reconnect keeps the tunnel up: otherwise usque connects only
		// on the first traffic, and that first connection fails
		cmd = exec.Command(c.Bin(), "-c", c.ConfigPath, "socks", "-b", "127.0.0.1", "-p", strconv.Itoa(c.APIPort), "--always-reconnect")
	case "slipstream-server":
		// no config file: its flags, one per line, written by Apply
		cmd = exec.Command(c.Bin(), slipArgs(c.ConfigPath)...)
	case "mita":
		// mita keeps per-user totals in one file and reloads them at start; a
		// crash can bring back older totals, so each start counts from zero
		os.Remove(MitaMetricsDump)
		cmd = exec.Command(c.Bin(), "run")
	}
	cmd.Env = c.env()
	cmd.Dir = c.BinDir
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		c.lastErr = err.Error()
		return err
	}
	c.cmd, c.wanted, c.startedAt, c.lastErr = cmd, true, time.Now(), ""
	os.WriteFile(c.ConfigPath+".pid", []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	done := make(chan struct{})
	c.done = done
	go c.readLogs(pr)
	go func() {
		err := cmd.Wait()
		pw.Close()
		close(done)
		c.mu.Lock()
		unexpected := c.wanted && c.cmd == cmd
		if unexpected {
			c.lastErr = fmt.Sprintf("exited: %v", err)
			c.restarts++
		}
		c.mu.Unlock()
		if unexpected {
			time.Sleep(3 * time.Second)
			c.opMu.Lock()
			c.mu.Lock()
			still := c.wanted && c.cmd == cmd
			c.mu.Unlock()
			if still {
				c.start()
			}
			c.opMu.Unlock()
		}
	}()
	return nil
}

// Stop terminates the core and waits for it to exit.
func (c *Core) Stop() {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	c.stop()
}

func (c *Core) stop() {
	c.mu.Lock()
	cmd, done := c.cmd, c.done
	c.wanted = false
	c.cmd = nil
	c.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		cmd.Process.Kill()
	} else {
		cmd.Process.Signal(os.Interrupt)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		<-done
	}
}

func (c *Core) readLogs(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := ansiRe.ReplaceAllString(sc.Text(), "")
		if c.OnLine != nil && c.OnLine(line) {
			continue
		}
		c.mu.Lock()
		c.logs = append(c.logs, line)
		if len(c.logs) > 400 {
			c.logs = c.logs[len(c.logs)-400:]
		}
		c.mu.Unlock()
	}
}

// Logs returns the last log lines.
func (c *Core) Logs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.logs...)
}

// Status is a JSON-friendly snapshot.
type Status struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version"`
	Uptime    int64  `json:"uptime"`
	Restarts  int    `json:"restarts"`
	Error     string `json:"error"`
	Idle      bool   `json:"idle,omitempty"`    // nothing to serve, so it is stopped on purpose
	BuiltIn   bool   `json:"builtIn,omitempty"` // part of Dunvex itself (nothing to install)
}

// Status returns the current state.
func (c *Core) Status() Status {
	v := c.Version()
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Status{Name: c.Name, Installed: c.Installed(), Version: v, Restarts: c.restarts, Error: c.lastErr}
	if c.cmd != nil && c.cmd.ProcessState == nil {
		s.Running = true
		s.Uptime = int64(time.Since(c.startedAt).Seconds())
	}
	s.Idle = !s.Running && !c.wanted && c.lastErr == ""
	return s
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(s, "\r", "")), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

var errNoCore = errors.New("core not running")

// killStale stops a core left running by a previous Dunvex process (crash or
// hard kill), but only when that PID still runs our own core binary.
func (c *Core) killStale() {
	b, err := os.ReadFile(c.ConfigPath + ".pid")
	if err != nil {
		return
	}
	os.Remove(c.ConfigPath + ".pid")
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return
	}
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return
	}
	exe, err := p.Exe()
	if err != nil {
		return
	}
	want, _ := filepath.Abs(c.Bin())
	if strings.EqualFold(filepath.Clean(exe), filepath.Clean(want)) {
		p.Kill()
		time.Sleep(300 * time.Millisecond)
	}
}

var bindRe = regexp.MustCompile(`(?i)listen (?:tcp|udp)[46]? [^ ]*:(\d+).*(?:bind|address already in use|only one usage)`)

// BindFailure waits briefly after a start and, when the process died because a
// port was busy, returns that port.
func (c *Core) BindFailure(started bool) int {
	if !started {
		return 0
	}
	for i := 0; i < 15; i++ {
		time.Sleep(100 * time.Millisecond)
		if !c.Running() {
			break
		}
	}
	if c.Running() {
		return 0
	}
	logs := c.Logs()
	for i := len(logs) - 1; i >= 0 && i >= len(logs)-20; i-- {
		if m := bindRe.FindStringSubmatch(logs[i]); m != nil {
			p, _ := strconv.Atoi(m[1])
			return p
		}
	}
	return 0
}
