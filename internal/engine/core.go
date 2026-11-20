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
)

// Core is one supervised proxy process.
type Core struct {
	Name       string // "xray" or "sing-box"
	BinDir     string
	ConfigPath string
	APIPort    int

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
	out, err := exec.Command(c.Bin(), "version").CombinedOutput()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(out), "\r", ""), "\n")
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
	if c.Name == "xray" {
		cmd = exec.Command(c.Bin(), "run", "-test", "-c", tmp)
	} else {
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
	if err := c.Test(cfg); err != nil {
		c.mu.Lock()
		c.lastErr = err.Error()
		c.mu.Unlock()
		return err
	}
	old, _ := os.ReadFile(c.ConfigPath)
	if bytes.Equal(old, cfg) && c.Running() {
		return nil
	}
	if err := os.WriteFile(c.ConfigPath, cfg, 0o600); err != nil {
		return err
	}
	c.stop()
	return c.start()
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
		line := sc.Text()
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
