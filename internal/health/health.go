// Package health tests configs the way a user's app uses them: a real client
// core (Xray or sing-box), or the built-in dnstt client, connects to the
// server with the config, and a web page has to come back through it.
package health

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"

	"github.com/pedramrj/dunvex/internal/dnstt/dnsttclient"
)

// Kinds of client.
const (
	KindXray    = "xray"
	KindSingbox = "sing-box"
	KindDNSTT   = "dnstt"
)

// Outcomes.
const (
	StatusOK   = "ok"   // a page came back through the config
	StatusFail = "fail" // it did not
	StatusSkip = "skip" // not tested (no client for it here, nobody may use it…)
)

// Config names one config: an inbound, as one entry of the users' subscriptions.
type Config struct {
	InboundID int64  `json:"inboundId"`
	Preset    string `json:"preset"`
	Name      string `json:"name"`             // as in the user's app
	Server    string `json:"server,omitempty"` // the node it runs on ("" = this server)
	Via       string `json:"via,omitempty"`    // "cdn" or "relay" for those copies of a config
}

// Target is a config and the client to test it with.
type Target struct {
	Config
	Kind      string           // KindXray, KindSingbox or KindDNSTT; "" = not tested
	Skip      string           // why it is not tested (Kind "")
	Broken    string           // the config cannot even be built: it fails untested
	Xray      map[string]any   // the Xray outbound
	Singbox   []map[string]any // sing-box outbounds; the first one carries the traffic
	Endpoints []map[string]any // sing-box endpoints (WireGuard)
	DNSTT     *DNSTT
	Slow      bool   // a DNS or ping tunnel: it gets more time
	Addr      string // the server's host:port when the config runs over TCP (see portCheck)
}

// DNSTT is a dnstt tunnel and the SOCKS account behind it.
type DNSTT struct {
	Resolver  string // host:port (UDP)
	Domain    string
	PublicKey string
	User      string // "" = the tunnel lets anyone in
	Pass      string
}

// Result is the outcome for one config.
type Result struct {
	Config
	Status string `json:"status"`
	MS     int    `json:"ms,omitempty"` // how long the page took, connection included
	Error  string `json:"error,omitempty"`
}

// Probes answer 204 from far away; one is enough. Plain HTTP: the request
// itself goes through the tunnel, and no certificate on the way can fail it.
var Probes = []string{"http://cp.cloudflare.com/generate_204", "http://www.gstatic.com/generate_204"}

// Runner runs the checks.
type Runner struct {
	BinDir   string        // where xray and sing-box are
	Dir      string        // for the client configs ("" = the system's temp folder)
	Probes   []string      // nil = Probes
	Parallel int           // configs at once (0 = 4)
	Timeout  time.Duration // per config (0 = 25s); slow ones get three times as long

	// Start runs a client core with cfg (its SOCKS port is in the config) and
	// returns how to stop it, its log, and a channel closed when it exits.
	// nil = the real xray or sing-box in BinDir; tests put a stand-in here.
	Start func(ctx context.Context, kind string, cfg []byte) (stop func(), log func() string, exited <-chan struct{}, err error)
}

// Run tests every target and returns the results in the same order.
func (r *Runner) Run(ctx context.Context, targets []Target) []Result {
	out := make([]Result, len(targets))
	n := r.Parallel
	if n <= 0 {
		n = 4
	}
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	for i, t := range targets {
		if t.Broken != "" {
			out[i] = Result{Config: t.Config, Status: StatusFail, Error: short(t.Broken)}
			continue
		}
		if t.Kind == "" {
			out[i] = Result{Config: t.Config, Status: StatusSkip, Error: t.Skip}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = r.one(ctx, t)
		}()
	}
	wg.Wait()
	return out
}

func (r *Runner) one(ctx context.Context, t Target) Result {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	if t.Slow {
		timeout *= 3
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var (
		ms  int
		err error
	)
	switch t.Kind {
	case KindXray, KindSingbox:
		ms, err = r.viaCore(ctx, t)
		if err != nil && strings.Contains(err.Error(), "not included in this build") {
			// the client core lacks it, not the server
			return Result{Config: t.Config, Status: StatusSkip, Error: "the " + t.Kind + " build on this server cannot be its client"}
		}
	case KindDNSTT:
		ms, err = r.viaDNSTT(ctx, t)
	default:
		err = fmt.Errorf("no client for %q", t.Kind)
	}
	if err != nil {
		if ctx.Err() != nil && !strings.Contains(err.Error(), "timed out") {
			err = fmt.Errorf("no answer within %s: %v", timeout, err)
		}
		if t.Addr != "" && t.Kind != KindDNSTT {
			err = portCheck(t.Addr, err)
		}
		return Result{Config: t.Config, Status: StatusFail, Error: short(err.Error())}
	}
	return Result{Config: t.Config, Status: StatusOK, MS: ms}
}

// portCheck tells the two kinds of failure apart: nothing answers on the
// server's port (down, blocked, a wrong address), or the port answers but the
// config is refused (wrong keys, a server that was changed). Clients often
// say neither: Xray just closes the connection.
func portCheck(addr string, err error) error {
	c, derr := net.DialTimeout("tcp", addr, 5*time.Second)
	if derr != nil {
		why := derr.Error()
		if i := strings.LastIndex(why, ": "); i >= 0 {
			why = why[i+2:]
		}
		return fmt.Errorf("nothing answers on %s (%s)", addr, why)
	}
	c.Close()
	msg := fmt.Sprintf("the server answers on %s, but no page came back through the config", addr)
	if i := strings.Index(err.Error(), " (client: "); i >= 0 {
		msg += err.Error()[i:] // what the client said about it
	}
	return errors.New(msg)
}

func (r *Runner) probes() []string {
	if len(r.Probes) > 0 {
		return r.Probes
	}
	return Probes
}

// ---------------------------------------------------------------- Xray and sing-box

// ClientConfig is the whole client config for a target: a SOCKS inbound on
// 127.0.0.1:port, and the target's outbound as the only way out.
func ClientConfig(t Target, port int) ([]byte, error) {
	switch t.Kind {
	case KindXray:
		if t.Xray == nil {
			return nil, errors.New("no Xray outbound")
		}
		return json.Marshal(map[string]any{
			"log":       map[string]any{"loglevel": "info"}, // Xray tells why an outbound failed at info
			"inbounds":  []any{map[string]any{"tag": "in", "listen": "127.0.0.1", "port": port, "protocol": "socks", "settings": map[string]any{"udp": false}}},
			"outbounds": []any{t.Xray},
		})
	case KindSingbox:
		if len(t.Singbox) == 0 && len(t.Endpoints) == 0 {
			return nil, errors.New("no sing-box outbound")
		}
		final := ""
		if len(t.Singbox) > 0 {
			final, _ = t.Singbox[0]["tag"].(string)
		} else {
			final, _ = t.Endpoints[0]["tag"].(string)
		}
		c := map[string]any{
			"log":       map[string]any{"level": "warn"},
			"dns":       map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local"}}},
			"inbounds":  []any{map[string]any{"type": "socks", "tag": "in", "listen": "127.0.0.1", "listen_port": port}},
			"outbounds": t.Singbox,
			"route":     map[string]any{"final": final, "default_domain_resolver": "local"},
		}
		if len(t.Endpoints) > 0 {
			c["endpoints"] = t.Endpoints
		}
		if t.Singbox == nil {
			c["outbounds"] = []any{}
		}
		return json.Marshal(c)
	}
	return nil, fmt.Errorf("no client config for %q", t.Kind)
}

func (r *Runner) viaCore(ctx context.Context, t Target) (int, error) {
	port, err := freePort()
	if err != nil {
		return 0, err
	}
	cfg, err := ClientConfig(t, port)
	if err != nil {
		return 0, err
	}
	start := r.Start
	if start == nil {
		start = r.exec
	}
	stop, log, exited, err := start(ctx, t.Kind, cfg)
	if err != nil {
		return 0, err
	}
	defer stop()
	addr := "127.0.0.1:" + strconv.Itoa(port)
	if err := waitListen(ctx, addr, exited); err != nil {
		return 0, withLog(fmt.Errorf("the %s client did not start: %v", t.Kind, err), log())
	}
	proxyURL, _ := url.Parse("socks5://" + addr)
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}}
	ms, err := r.fetch(ctx, func(u string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		return hc.Do(req)
	})
	if err != nil {
		select {
		case <-time.After(400 * time.Millisecond): // the client logs why a moment after it gives up
		case <-exited:
		}
		return 0, withLog(err, log())
	}
	return ms, nil
}

// exec runs the real core.
func (r *Runner) exec(ctx context.Context, kind string, cfg []byte) (func(), func() string, <-chan struct{}, error) {
	name := "xray"
	if kind == KindSingbox {
		name = "sing-box"
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(r.BinDir, name)
	if _, err := os.Stat(bin); err != nil {
		return nil, nil, nil, fmt.Errorf("%s is not installed", kind)
	}
	f, err := os.CreateTemp(r.Dir, "dunvex-health-*.json")
	if err != nil {
		return nil, nil, nil, err
	}
	f.Write(cfg)
	f.Close()
	cmd := exec.Command(bin, "run", "-c", f.Name())
	lb := &lineBuf{}
	cmd.Stdout, cmd.Stderr = lb, lb
	if err := cmd.Start(); err != nil {
		os.Remove(f.Name())
		return nil, nil, nil, err
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	stop := func() {
		cmd.Process.Kill()
		<-exited
		os.Remove(f.Name())
	}
	return stop, lb.String, exited, nil
}

// ---------------------------------------------------------------- dnstt

func (r *Runner) viaDNSTT(ctx context.Context, t Target) (int, error) {
	d := t.DNSTT
	if d == nil {
		return 0, errors.New("no dnstt details")
	}
	begin := time.Now()
	type dialed struct {
		c   *dnsttclient.Client
		err error
	}
	ch := make(chan dialed, 1)
	go func() {
		c, err := dnsttclient.Dial(d.Resolver, d.Domain, d.PublicKey)
		ch <- dialed{c, err}
	}()
	var c *dnsttclient.Client
	select {
	case got := <-ch:
		if got.err != nil {
			return 0, fmt.Errorf("the tunnel did not open: %v", got.err)
		}
		c = got.c
	case <-ctx.Done():
		go func() { // the handshake gives up on its own; close what it opens late
			if got := <-ch; got.c != nil {
				got.c.Close()
			}
		}()
		return 0, errors.New("the tunnel did not answer (check the domain's NS record and that UDP 53 reaches the server)")
	}
	defer c.Close()
	var auth *proxy.Auth
	if d.User != "" {
		auth = &proxy.Auth{User: d.User, Password: d.Pass}
	}
	ms, err := r.fetch(ctx, func(u string) (*http.Response, error) {
		stream, err := c.Open()
		if err != nil {
			return nil, err
		}
		if dl, ok := ctx.Deadline(); ok {
			stream.SetDeadline(dl)
		}
		dialer, err := proxy.SOCKS5("tcp", "tunnel", auth, once{stream})
		if err != nil {
			stream.Close()
			return nil, err
		}
		tr := &http.Transport{DisableKeepAlives: true, Dial: dialer.Dial}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		res, err := tr.RoundTrip(req)
		if err != nil {
			stream.Close()
		}
		return res, err
	})
	if err != nil {
		return 0, err
	}
	// the handshake is part of what a user waits for
	if total := int(time.Since(begin).Milliseconds()); total > ms {
		ms = total
	}
	return ms, nil
}

// once is a dialer that hands out one connection, the tunnel's stream.
type once struct{ c net.Conn }

func (o once) Dial(network, addr string) (net.Conn, error) { return o.c, nil }

// ---------------------------------------------------------------- shared

// fetch asks each probe until one answers 204, and returns how long it took.
func (r *Runner) fetch(ctx context.Context, get func(u string) (*http.Response, error)) (int, error) {
	var last error
	for _, u := range r.probes() {
		start := time.Now()
		res, err := get(u)
		if err != nil {
			last = err
			if ctx.Err() != nil {
				break
			}
			continue
		}
		bufio.NewReader(res.Body).Discard(4096)
		res.Body.Close()
		if res.StatusCode == http.StatusNoContent {
			return max(int(time.Since(start).Milliseconds()), 1), nil
		}
		last = fmt.Errorf("the page came back as %s instead of 204", res.Status)
	}
	return 0, friendly(last)
}

// friendly turns Go's request errors into what happened.
func friendly(err error) error {
	if err == nil {
		return errors.New("no answer")
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	m := err.Error()
	switch {
	case strings.Contains(m, "general SOCKS server failure"), strings.Contains(m, "connection refused") && strings.Contains(m, "socks"),
		strings.Contains(m, "EOF"):
		return errors.New("the connection through the config failed")
	case strings.Contains(m, "reset by peer"), strings.Contains(m, "broken pipe"):
		return errors.New("the connection through the config broke off before the page came back")
	case strings.Contains(m, "authentication failed"):
		return errors.New("the server refused the account (it has not taken the check's user yet)")
	case strings.Contains(m, "deadline exceeded"), strings.Contains(m, "Timeout"), strings.Contains(m, "timeout"):
		return errors.New("timed out")
	}
	return err
}

// withLog adds the client's last word on the matter, when it said something.
func withLog(err error, log string) error {
	if l := lastLogLine(log); l != "" {
		return fmt.Errorf("%v (client: %s)", err, reason(l))
	}
	return err
}

var bracketed = regexp.MustCompile(`\[([^\[\]]*(dial|timeout|refused|unreachable|reset|handshake|EOF)[^\[\]]*)\]`)

// reason is the part of a client's error line that says what went wrong:
// Xray chains every layer ("a > b > [dial tcp …: i/o timeout] > c"); the
// innermost network error, or else the deepest layer, is the one to read.
func reason(line string) string {
	if m := bracketed.FindStringSubmatch(line); m != nil {
		return strings.TrimSpace(m[1])
	}
	parts := strings.Split(line, " > ")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if p != "" && !strings.Contains(p, "all retry attempts failed") {
			return p
		}
	}
	return line
}

var logPrefix = regexp.MustCompile(`^(\+\d{4} )?\d{4}[-/]\d\d[-/]\d\d[ T][\d:.]+\s*(\S+\s+)?(\[[A-Za-z]+\]\s*)?(\[\d+[^\]]*\]\s*)?`)

// lastLogLine is the client's last warning or error, without its timestamp.
func lastLogLine(log string) string {
	lines := strings.Split(strings.TrimSpace(log), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		low := strings.ToLower(l)
		if strings.HasSuffix(low, " started") || strings.Contains(low, "deprecated") {
			continue // Xray logs its start as a warning; neither says what went wrong
		}
		if strings.Contains(low, "fail") || strings.Contains(low, "error") || strings.Contains(low, "warn") || strings.Contains(low, "fatal") || strings.Contains(low, "invalid") {
			l = strings.TrimSpace(logPrefix.ReplaceAllString(l, ""))
			return short(l)
		}
	}
	return ""
}

func short(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if r := []rune(s); len(r) > 300 {
		s = string(r[:297]) + "…"
	}
	return s
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitListen waits until the client accepts connections, or gives up when it exits.
func waitListen(ctx context.Context, addr string, exited <-chan struct{}) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		select {
		case <-exited:
			return errors.New("it stopped")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return errors.New("its SOCKS port never opened")
		}
	}
}

// lineBuf keeps the last 32 KB a client printed.
type lineBuf struct {
	mu sync.Mutex
	b  []byte
}

func (l *lineBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b = append(l.b, p...)
	if len(l.b) > 32<<10 {
		l.b = l.b[len(l.b)-32<<10:]
	}
	return len(p), nil
}

func (l *lineBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.b)
}
