//go:build linux

package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestXICMPOnRealXray runs Xray's ping tunnel under Dunvex and a real Xray
// client set up from the panel's link, in both of the apps' ping modes
// (unprivileged "dgram" and raw), and reaches the internet through it. The
// usage is the user's, and the tunnel's stand-in addresses are no devices.
// Needs root and DUNVEX_CORE_DIR (xray), and internet access.
func TestXICMPOnRealXray(t *testing.T) {
	cores := os.Getenv("DUNVEX_CORE_DIR")
	if cores == "" || os.Geteuid() != 0 {
		t.Skip("needs root and DUNVEX_CORE_DIR")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	copyCores(t, cores, bin)
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sara := &store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(20), SubToken: util.RandomString(20), Enabled: true, IPLimit: 1}
	st.CreateUser(sara)
	p := protocol.Get("vless-xicmp")
	in := &store.Inbound{Preset: p.ID, Port: 31999, Enabled: true, Params: protocol.Defaults(p, nil, "")}
	st.CreateInbound(in)

	env := protocol.Env{Host: "127.0.0.1"}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.Xray.APIPort, m.SingBox.APIPort, m.TT.APIPort = nextFree(45800), nextFree(45810), nextFree(45820)
	m.RouterPort, m.WarpTestPort, m.Usque.APIPort = nextFree(45830), nextFree(45840), nextFree(45850)
	defer m.Xray.Stop()
	if err := m.Apply(); err != nil {
		t.Fatalf("apply: %v\n%s", err, strings.Join(m.Xray.Logs(), "\n"))
	}
	if ie, _ := m.Errors(); ie[in.ID] != "" {
		t.Fatalf("inbound error: %s", ie[in.ID])
	}
	if !m.Xray.Running() {
		t.Fatalf("Xray is not running: %s", strings.Join(m.Xray.Logs(), "\n"))
	}

	run := func(dgram bool) {
		t.Helper()
		in.Params["icmpDgram"] = dgram
		link, _ := url.Parse(protocol.Link(p, in, sara, env, "ICMP"))
		var fm any
		if err := json.Unmarshal([]byte(link.Query().Get("fm")), &fm); err != nil {
			t.Fatal(err)
		}
		host, port, _ := net.SplitHostPort(link.Host)
		var lp int
		fmt.Sscan(port, &lp)
		socks := nextFree(45860)
		client, _ := json.Marshal(map[string]any{
			"log":      map[string]any{"loglevel": "warning"},
			"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
			"outbounds": []any{map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{
				"address": host, "port": lp, "users": []any{map[string]any{"id": sara.UUID, "encryption": link.Query().Get("encryption")}}}}},
				"streamSettings": map[string]any{"network": "kcp", "security": "none", "finalmask": fm}}},
		})
		cpath := filepath.Join(dir, fmt.Sprintf("client-%v.json", dgram))
		os.WriteFile(cpath, client, 0o600)
		cl := exec.Command(filepath.Join(bin, "xray"), "run", "-c", cpath)
		var clog strings.Builder
		cl.Stdout, cl.Stderr = &clog, &clog
		if err := cl.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { cl.Process.Kill(); cl.Wait() }()
		waitPort(socks)
		proxy, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", socks))
		hc := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
		start := time.Now()
		res, err := hc.Get("http://cp.cloudflare.com/generate_204")
		if err != nil {
			t.Fatalf("dgram=%v: %v\nserver: %s\nclient: %s", dgram, err, lastLines(strings.Join(m.Xray.Logs(), "\n"), 10), clog.String())
		}
		res.Body.Close()
		if res.StatusCode != 204 {
			t.Fatalf("status %d", res.StatusCode)
		}
		t.Logf("xICMP (dgram=%v): a request took %v", dgram, time.Since(start).Round(time.Millisecond))
		// several connections: each is a new tunnel session with its own stand-in address
		start = time.Now()
		res, err = hc.Get("http://speed.cloudflare.com/__down?bytes=1000000")
		if err == nil {
			n, _ := io.Copy(io.Discard, res.Body)
			res.Body.Close()
			d := time.Since(start)
			t.Logf("xICMP (dgram=%v): %d bytes in %v, %.0f KB/s", dgram, n, d.Round(time.Millisecond), float64(n)/d.Seconds()/1024)
		}
	}
	run(true)
	run(false)

	time.Sleep(1500 * time.Millisecond)
	m.collect()
	u, _ := st.UserByName("sara")
	if u.UsedUp+u.UsedDown == 0 {
		t.Fatal("usage not counted for sara")
	}
	t.Logf("counted for sara: %d up, %d down", u.UsedUp, u.UsedDown)
	if n := m.DeviceCounts()["sara"]; n > 1 || u.PausedUntil != 0 {
		t.Fatalf("tunnel sessions counted as devices: %d %+v", n, m.Devices("sara"))
	}
}
