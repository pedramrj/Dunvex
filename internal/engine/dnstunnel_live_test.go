//go:build linux

package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/dnstt/dnsttclient"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// socks5 signs in on a stream with a user name and password and asks for
// host:port. It reports the server's answer code (0: granted).
func socks5(c net.Conn, user, pass, host string, port int) (byte, error) {
	c.SetDeadline(time.Now().Add(40 * time.Second))
	if _, err := c.Write([]byte{5, 1, 2}); err != nil {
		return 0, err
	}
	r := bufio.NewReader(c)
	m := make([]byte, 2)
	if _, err := io.ReadFull(r, m); err != nil || m[1] != 2 {
		return 0xff, fmt.Errorf("method: %x %v", m, err)
	}
	a := append([]byte{1, byte(len(user))}, user...)
	a = append(append(a, byte(len(pass))), pass...)
	c.Write(a)
	if _, err := io.ReadFull(r, m); err != nil {
		return 0xff, err
	}
	if m[1] != 0 {
		return 0xfe, nil // wrong name or password
	}
	q := append([]byte{5, 1, 0, 3, byte(len(host))}, host...)
	q = append(q, byte(port>>8), byte(port))
	c.Write(q)
	h := make([]byte, 10)
	if _, err := io.ReadFull(r, h); err != nil {
		return 0xff, err
	}
	return h[1], nil
}

// TestDNSTunnelsOnRealXray: a real dnstt client signs in to Xray's SOCKS with
// a user's password through the in-process tunnel and reaches the internet;
// a wrong password is refused; the usage lands on that user. A real Xray XDNS
// client works on the same port, behind the shared DNS port. Needs
// DUNVEX_CORE_DIR (a folder with xray) and internet access.
func TestDNSTunnelsOnRealXray(t *testing.T) {
	cores := os.Getenv("DUNVEX_CORE_DIR")
	if cores == "" {
		t.Skip("needs DUNVEX_CORE_DIR")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	copyCores(t, cores, bin)
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sara := &store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(20), SubToken: util.RandomString(20), Enabled: true}
	st.CreateUser(sara)

	port := freeUDP(45400)
	dp, xp := protocol.Get("dnstt"), protocol.Get("vless-xdns")
	dt := &store.Inbound{Preset: dp.ID, Port: port, Enabled: true, Params: protocol.Defaults(dp, map[string]any{"xdnsDomain": "d.dunvex.test", "resolver": "direct"}, "")}
	st.CreateInbound(dt)
	xd := &store.Inbound{Preset: xp.ID, Port: port, Enabled: true, Params: protocol.Defaults(xp, map[string]any{"xdnsDomain": "x.dunvex.test", "resolver": "direct"}, "")}
	st.CreateInbound(xd)

	env := protocol.Env{Host: "127.0.0.1", Listen: "127.0.0.1"}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.Xray.APIPort, m.SingBox.APIPort, m.TT.APIPort = nextFree(45500), nextFree(45510), nextFree(45520)
	m.RouterPort, m.WarpTestPort, m.Usque.APIPort = nextFree(45530), nextFree(45540), nextFree(45550)
	defer m.Xray.Stop()
	defer m.stopDNS()
	if err := m.Apply(); err != nil {
		t.Fatalf("apply: %v\n%s", err, strings.Join(m.Xray.Logs(), "\n"))
	}
	if ie, _ := m.Errors(); ie[dt.ID] != "" || ie[xd.ID] != "" {
		t.Fatalf("inbound errors: %v", ie)
	}

	// dnstt, as SlipNet would: the link's domain, key and address
	link := protocol.Link(dp, dt, sara, env, "dnstt")
	if !strings.HasPrefix(link, "slipnet://") {
		t.Fatal(link)
	}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	c, err := dnsttclient.Dial(addr, "d.dunvex.test", dt.Str("publicKey"))
	if err != nil {
		t.Fatalf("dnstt session: %v", err)
	}
	defer c.Close()
	bad, _ := c.Open()
	if code, err := socks5(bad, "sara", "wrong", "cp.cloudflare.com", 80); code != 0xfe {
		t.Fatalf("a wrong password got %x %v", code, err)
	}
	bad.Close()
	s, err := c.Open()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if code, err := socks5(s, "sara", sara.Password, "cp.cloudflare.com", 80); code != 0 || err != nil {
		t.Fatalf("SOCKS through dnstt: %x %v\n%s", code, err, strings.Join(m.Xray.Logs(), "\n"))
	}
	fmt.Fprintf(s, "GET /generate_204 HTTP/1.1\r\nHost: cp.cloudflare.com\r\nConnection: close\r\n\r\n")
	res, err := http.ReadResponse(bufio.NewReader(s), nil)
	if err != nil || res.StatusCode != 204 {
		t.Fatalf("through dnstt: %v %v", res, err)
	}
	s.Close()
	t.Logf("dnstt: a request through Xray's SOCKS took %v", time.Since(start).Round(time.Millisecond))

	// the usage is sara's and the dnstt inbound's
	time.Sleep(1500 * time.Millisecond)
	m.collect()
	u, _ := st.UserByName("sara")
	ins, _ := st.Inbounds()
	var inUsed int64
	for _, in := range ins {
		if in.ID == dt.ID {
			inUsed = in.Up + in.Down
		}
	}
	if u.UsedUp+u.UsedDown == 0 || inUsed == 0 {
		t.Fatalf("usage not counted: user %d/%d, inbound %d", u.UsedUp, u.UsedDown, inUsed)
	}
	t.Logf("counted for sara: %d up, %d down; for the inbound: %d", u.UsedUp, u.UsedDown, inUsed)

	// XDNS on the same port, behind the shared DNS port
	xl, _ := url.Parse(protocol.Link(xp, xd, sara, env, "XDNS"))
	var fm any
	json.Unmarshal([]byte(xl.Query().Get("fm")), &fm)
	host, lport, _ := net.SplitHostPort(xl.Host)
	lp, _ := strconv.Atoi(lport)
	if lp != port {
		t.Fatalf("XDNS link port %d, want the shared %d", lp, port)
	}
	socks := nextFree(45560)
	client, _ := json.Marshal(map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
		"outbounds": []any{map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{
			"address": host, "port": lp, "users": []any{map[string]any{"id": sara.UUID, "encryption": "none"}}}}},
			"streamSettings": map[string]any{"network": "kcp", "security": "none", "finalmask": fm}}},
	})
	cpath := filepath.Join(dir, "client.json")
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
	hc := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	start = time.Now()
	r2, err := hc.Get("http://cp.cloudflare.com/generate_204")
	if err != nil {
		t.Fatalf("XDNS behind the shared port: %v\nserver: %s\nclient: %s", err, strings.Join(m.Xray.Logs(), "\n"), clog.String())
	}
	r2.Body.Close()
	if r2.StatusCode != 204 {
		t.Fatalf("XDNS status %d", r2.StatusCode)
	}
	t.Logf("XDNS behind the shared port: a request took %v", time.Since(start).Round(time.Millisecond))
	z := m.DNSPorts()[port]
	t.Logf("shared port %d: %+v", port, z)
	if len(z) != 2 {
		t.Fatalf("zones: %+v", z)
	}
}
