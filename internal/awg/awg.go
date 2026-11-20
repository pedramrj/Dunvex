// Package awg runs AmneziaWG (WireGuard with DPI obfuscation) for Dunvex.
//
// The server side is amneziawg-go (userspace, MIT), started in the foreground
// on one TUN interface. Dunvex talks to it only through its UAPI control
// socket (the WireGuard text protocol): it sets the device and peers, changes
// peers live (a diff, so nobody else is disconnected) and reads each peer's
// counters, handshake time and address for usage, online state and devices.
// NAT and forwarding are set with iptables (or nft) and removed on stop.
package awg

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SocketDir is where amneziawg-go puts its control sockets.
var SocketDir = "/var/run/amneziawg"

// Peer is one user.
type Peer struct {
	User      string
	PublicKey string // base64
	Address   string // "10.67.0.2/32"
}

// Config is everything the interface serves.
type Config struct {
	PrivateKey string // base64
	Port       int
	Address    string            // server side of the tunnel, "10.67.0.1/16"
	MTU        int               // of the interface
	Device     map[string]string // UAPI device keys: jc, s1…s4, h1…h4, header_protection_key (hex), random_trailers
	Peers      []Peer
}

// PeerStat is what the interface knows about a peer right now.
type PeerStat struct {
	RX, TX        int64 // bytes the server received from / sent to the peer
	LastHandshake int64 // unix seconds, 0 = never
	Endpoint      string
}

// Server supervises amneziawg-go on one interface.
type Server struct {
	Name   string // the core's name in the panel
	BinDir string
	Iface  string

	op      sync.Mutex // one Apply or Stop at a time
	mu      sync.Mutex
	cmd     *exec.Cmd
	exited  bool // the current process has ended
	starts  int  // how many times it was started: counters begin again at each
	done    chan struct{}
	applied *Config
	lastErr string
	logs    []string
	started time.Time
	version string
	nat     natRules
}

// New prepares a server for the interface (not started).
func New(binDir, iface string) *Server {
	return &Server{Name: "amneziawg", BinDir: binDir, Iface: iface}
}

// Bin is the amneziawg-go executable.
func (s *Server) Bin() string { return filepath.Join(s.BinDir, "amneziawg-go") }

// Installed reports whether amneziawg-go is present.
func (s *Server) Installed() bool {
	st, err := os.Stat(s.Bin())
	return err == nil && !st.IsDir()
}

// Running reports whether the process is alive.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil && !s.exited
}

// Starts counts the starts of amneziawg-go; a new value means its peer
// counters began again from zero.
func (s *Server) Starts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.starts
}

// Version of amneziawg-go ("v3.1.…"): the release tag kept next to the
// binary, else what --version prints (an older built-in number).
func (s *Server) Version() string {
	s.mu.Lock()
	v := s.version
	s.mu.Unlock()
	if v != "" || !s.Installed() {
		return v
	}
	if b, err := os.ReadFile(s.Bin() + ".version"); err == nil {
		v = strings.TrimSpace(string(b))
	} else {
		out, _ := exec.Command(s.Bin(), "--version").CombinedOutput()
		if f := strings.Fields(string(out)); len(f) >= 2 && f[0] == "amneziawg-go" {
			v = f[1]
		}
	}
	s.mu.Lock()
	s.version = v
	s.mu.Unlock()
	return v
}

// LastError is the last problem, "" when fine.
func (s *Server) LastError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// Uptime in seconds while running.
func (s *Server) Uptime() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.exited {
		return 0
	}
	return int64(time.Since(s.started).Seconds())
}

// Logs returns the last lines amneziawg-go printed.
func (s *Server) Logs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.logs...)
}

func (s *Server) fail(err error) error {
	s.mu.Lock()
	s.lastErr = err.Error()
	s.mu.Unlock()
	return err
}

// Apply makes the interface serve c; nil stops it. Only peers changed: they
// are added and removed live. Anything else: the interface starts again.
func (s *Server) Apply(c *Config) error {
	s.op.Lock()
	defer s.op.Unlock()
	if c == nil {
		s.stop()
		return nil
	}
	if !s.Installed() {
		return s.fail(errors.New("amneziawg-go is not installed"))
	}
	s.mu.Lock()
	prev := s.applied
	running := s.cmd != nil && !s.exited
	s.mu.Unlock()
	if running && prev != nil && sameDevice(prev, c) {
		if err := s.uapiSet(peerDiff(prev, c)); err != nil {
			return s.fail(fmt.Errorf("updating peers: %w", err))
		}
		s.mu.Lock()
		s.applied, s.lastErr = c, ""
		s.mu.Unlock()
		return nil
	}
	s.stop()
	if err := s.start(); err != nil {
		s.stop()
		return s.fail(err)
	}
	if err := s.uapiSet(fullSet(c)); err != nil {
		s.stop()
		return s.fail(fmt.Errorf("configuring the interface: %w", err))
	}
	if err := s.network(c); err != nil {
		s.stop()
		return s.fail(err)
	}
	s.mu.Lock()
	s.applied, s.lastErr = c, ""
	s.mu.Unlock()
	return nil
}

func sameDevice(a, b *Config) bool {
	if a.PrivateKey != b.PrivateKey || a.Port != b.Port || a.Address != b.Address || a.MTU != b.MTU || len(a.Device) != len(b.Device) {
		return false
	}
	for k, v := range a.Device {
		if b.Device[k] != v {
			return false
		}
	}
	return true
}

func (s *Server) start() error {
	os.MkdirAll(SocketDir, 0o755)
	os.Remove(filepath.Join(SocketDir, s.Iface+".sock"))
	exec.Command("ip", "link", "del", s.Iface).Run() // an interface left by a crash
	cmd := exec.Command(s.Bin(), "-f", s.Iface)
	cmd.Env = append(os.Environ(), "LOG_LEVEL=error")
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	s.mu.Lock()
	s.cmd, s.exited, s.done, s.started = cmd, false, done, time.Now()
	s.starts++
	s.mu.Unlock()
	go s.readLogs(pr)
	go func() {
		err := cmd.Wait()
		pw.Close()
		s.mu.Lock()
		if s.cmd == cmd {
			s.exited = true
			s.lastErr = fmt.Sprintf("amneziawg-go exited: %v", err)
			s.applied = nil // the next apply starts it again
		}
		s.mu.Unlock()
		close(done)
	}()
	sock := filepath.Join(SocketDir, s.Iface+".sock")
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sock); err == nil {
			return nil
		}
		select {
		case <-done:
			return fmt.Errorf("amneziawg-go stopped at start: %s", strings.Join(s.Logs(), " | "))
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("amneziawg-go did not open its control socket")
}

func (s *Server) readLogs(r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		s.mu.Lock()
		s.logs = append(s.logs, sc.Text())
		if len(s.logs) > 200 {
			s.logs = s.logs[len(s.logs)-200:]
		}
		s.mu.Unlock()
	}
}

// Stop removes the interface and its NAT rules.
func (s *Server) Stop() {
	s.op.Lock()
	defer s.op.Unlock()
	s.stop()
}

func (s *Server) stop() {
	s.mu.Lock()
	cmd, done, nat := s.cmd, s.done, s.nat
	s.cmd, s.applied, s.nat = nil, nil, natRules{}
	s.mu.Unlock()
	nat.remove()
	if cmd == nil || cmd.Process == nil {
		return
	}
	exec.Command("ip", "link", "del", s.Iface).Run() // makes amneziawg-go exit cleanly
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		<-done
	}
}

// ---------------------------------------------------------------- UAPI

func b64hex(k string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k))
	if err != nil || len(b) != 32 {
		return "", fmt.Errorf("bad key %q", k)
	}
	return hex.EncodeToString(b), nil
}

func fullSet(c *Config) []string {
	priv, _ := b64hex(c.PrivateKey)
	lines := []string{"private_key=" + priv, "listen_port=" + strconv.Itoa(c.Port)}
	keys := make([]string, 0, len(c.Device))
	for k := range c.Device {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := c.Device[k]; v != "" {
			lines = append(lines, k+"="+v)
		}
	}
	lines = append(lines, "replace_peers=true")
	for _, p := range c.Peers {
		lines = append(lines, peerLines(p)...)
	}
	return lines
}

func peerLines(p Peer) []string {
	pub, err := b64hex(p.PublicKey)
	if err != nil {
		return nil
	}
	return []string{"public_key=" + pub, "replace_allowed_ips=true", "allowed_ip=" + p.Address}
}

// peerDiff adds new peers, removes gone ones and leaves the rest connected.
func peerDiff(old, c *Config) []string {
	had := map[string]Peer{}
	for _, p := range old.Peers {
		had[p.PublicKey] = p
	}
	var lines []string
	keep := map[string]bool{}
	for _, p := range c.Peers {
		keep[p.PublicKey] = true
		if q, ok := had[p.PublicKey]; ok && q.Address == p.Address {
			continue
		}
		lines = append(lines, peerLines(p)...)
	}
	for k := range had {
		if !keep[k] {
			if pub, err := b64hex(k); err == nil {
				lines = append(lines, "public_key="+pub, "remove=true")
			}
		}
	}
	return lines
}

func (s *Server) dial() (net.Conn, error) {
	return net.DialTimeout("unix", filepath.Join(SocketDir, s.Iface+".sock"), 3*time.Second)
}

func (s *Server) uapiSet(lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	c, err := s.dial()
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, "set=1\n"+strings.Join(lines, "\n")+"\n\n"); err != nil {
		return err
	}
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "errno="); ok {
			if v != "0" {
				return fmt.Errorf("amneziawg-go refused the configuration (errno %s): %s", v, strings.Join(s.Logs(), " | "))
			}
			return nil
		}
	}
	return errors.New("no answer from amneziawg-go")
}

// Stats reads every peer's counters, keyed by base64 public key.
func (s *Server) Stats() (map[string]PeerStat, error) {
	c, err := s.dial()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, "get=1\n\n"); err != nil {
		return nil, err
	}
	return parseStats(c)
}

// parseStats reads a UAPI get=1 answer.
func parseStats(r io.Reader) (map[string]PeerStat, error) {
	out := map[string]PeerStat{}
	var cur string
	var st PeerStat
	flush := func() {
		if cur != "" {
			out[cur] = st
		}
	}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "public_key":
			flush()
			b, _ := hex.DecodeString(v)
			cur, st = base64.StdEncoding.EncodeToString(b), PeerStat{}
		case "rx_bytes":
			st.RX, _ = strconv.ParseInt(v, 10, 64)
		case "tx_bytes":
			st.TX, _ = strconv.ParseInt(v, 10, 64)
		case "last_handshake_time_sec":
			st.LastHandshake, _ = strconv.ParseInt(v, 10, 64)
		case "endpoint":
			st.Endpoint = v
		case "errno":
			flush()
			if v != "0" {
				return nil, fmt.Errorf("amneziawg-go errno %s", v)
			}
			return out, nil
		}
	}
	flush()
	return out, sc.Err()
}

// ResetVersion forgets the cached version (after an update).
func (s *Server) ResetVersion() {
	s.mu.Lock()
	s.version = ""
	s.mu.Unlock()
}

// Describe is the running setup as text for the panel, without the private key.
func (c *Config) Describe(iface string) string {
	if c == nil {
		return "# AmneziaWG is not running\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# interface %s (private key hidden)\n[Interface]\nListenPort = %d\nAddress = %s\nMTU = %d\n", iface, c.Port, c.Address, c.MTU)
	keys := make([]string, 0, len(c.Device))
	for k := range c.Device {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", k, c.Device[k])
	}
	for _, p := range c.Peers {
		fmt.Fprintf(&b, "\n# %s\n[Peer]\nPublicKey = %s\nAllowedIPs = %s\n", p.User, p.PublicKey, p.Address)
	}
	return b.String()
}
