package engine

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/routing"
	"github.com/pedramrj/dunvex/internal/store"
)

const loopbackSecret = "only-this-machine-may-read-this"

// loopbackTargets are a page and a UDP echo that only this machine should
// reach, on 127.0.0.1 and (when the system has it) ::1, with the same ports.
type loopbackTargets struct {
	tcp, udp int
	v6       bool
	pageHits atomic.Int32
	echoHits atomic.Int32
}

func newLoopbackTargets(t *testing.T) *loopbackTargets {
	t.Helper()
	lt := &loopbackTargets{}
	page := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		lt.pageHits.Add(1)
		io.WriteString(w, loopbackSecret)
	})
	l4, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lt.tcp = l4.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: page}
	t.Cleanup(func() { srv.Close() })
	go srv.Serve(l4)
	if l6, err := net.Listen("tcp", "[::1]:"+strconv.Itoa(lt.tcp)); err == nil {
		lt.v6 = true
		go srv.Serve(l6)
	}
	echo := func(c net.PacketConn) {
		t.Cleanup(func() { c.Close() })
		go func() {
			b := make([]byte, 2048)
			for {
				n, from, err := c.ReadFrom(b)
				if err != nil {
					return
				}
				lt.echoHits.Add(1)
				c.WriteTo(b[:n], from)
			}
		}()
	}
	u4, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lt.udp = u4.LocalAddr().(*net.UDPAddr).Port
	echo(u4)
	if u6, err := net.ListenPacket("udp", "[::1]:"+strconv.Itoa(lt.udp)); err == nil {
		echo(u6)
	}
	return lt
}

// getVia loads a URL through a local SOCKS5 port (names are looked up by the proxy).
func getVia(socks int, target string, timeout time.Duration) (int, string, error) {
	proxy, _ := url.Parse("socks5://127.0.0.1:" + strconv.Itoa(socks))
	hc := &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
	res, err := hc.Get(target)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	return res.StatusCode, string(b), nil
}

// socksUDP sends payload to host:port through a SOCKS5 UDP association and
// reports whether it came back within wait.
func socksUDP(socks int, host string, port int, payload []byte, wait time.Duration) (bool, error) {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(socks), 3*time.Second)
	if err != nil {
		return false, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	r := make([]byte, 16)
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		return false, err
	}
	if _, err := io.ReadFull(c, r[:2]); err != nil || r[1] != 0 {
		return false, fmt.Errorf("greeting: %v %x", err, r[:2])
	}
	if _, err := c.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return false, err
	}
	if _, err := io.ReadFull(c, r[:4]); err != nil || r[1] != 0 {
		return false, fmt.Errorf("UDP associate: %v %x", err, r[:4])
	}
	n := map[byte]int{1: 4, 4: 16}[r[3]]
	if n == 0 {
		return false, fmt.Errorf("relay address type %d", r[3])
	}
	if _, err := io.ReadFull(c, r[:n+2]); err != nil {
		return false, err
	}
	relay := &net.UDPAddr{IP: append(net.IP{}, r[:n]...), Port: int(binary.BigEndian.Uint16(r[n : n+2]))}
	if relay.IP.IsUnspecified() {
		relay.IP = net.IPv4(127, 0, 0, 1)
	}
	c.SetDeadline(time.Time{})
	u, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		return false, err
	}
	defer u.Close()
	pkt := []byte{0, 0, 0}
	if ip := net.ParseIP(host); ip == nil {
		pkt = append(append(pkt, 3, byte(len(host))), host...)
	} else if ip4 := ip.To4(); ip4 != nil {
		pkt = append(append(pkt, 1), ip4...)
	} else {
		pkt = append(append(pkt, 4), ip.To16()...)
	}
	pkt = append(binary.BigEndian.AppendUint16(pkt, uint16(port)), payload...)
	b := make([]byte, 2048)
	for end := time.Now().Add(wait); time.Now().Before(end); {
		u.Write(pkt)
		u.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
		if k, err := u.Read(b); err == nil && bytes.Contains(b[:k], payload) {
			return true, nil
		}
	}
	return false, nil
}

// TestPrivateNamesRefusedOnRealCores puts a SOCKS port in front of the routing
// Dunvex builds for real Xray and sing-box, then asks for a name that resolves
// to this machine (localtest.me is 127.0.0.1 and ::1) and for loopback
// addresses: neither a page nor a UDP echo on loopback may answer, in any
// routing mode (WARP-all included, where "direct" is not the first way out),
// while normal sites still load. Needs DUNVEX_TEST_BIN (xray, sing-box,
// geosite.dat, geoip.dat) and the internet.
func TestPrivateNamesRefusedOnRealCores(t *testing.T) {
	src := os.Getenv("DUNVEX_TEST_BIN")
	if src == "" {
		t.Skip("DUNVEX_TEST_BIN not set")
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(context.Background(), "localtest.me")
	if err != nil || len(addrs) == 0 {
		t.Skipf("localtest.me does not resolve here: %v", err)
	}
	for _, a := range addrs {
		if !a.IP.IsLoopback() {
			t.Skipf("localtest.me is %s here, not loopback", a.IP)
		}
	}
	lt := newLoopbackTargets(t)
	exe := func(n string) string {
		if runtime.GOOS == "windows" {
			return n + ".exe"
		}
		return n
	}
	// through WARP, a name only leaves directly when it is on the direct list
	viaWarp := routing.Settings{Iran: routing.IranDirect, Warp: routing.WarpAll, DirectDomains: []string{"localtest.me", "cloudflare.com"}}
	for _, c := range []struct {
		name, core string
		geo, warp  bool
		set        routing.Settings
	}{
		{"xray/site lists", "xray", true, false, routing.Defaults()},
		{"xray/no site lists", "xray", false, false, routing.Defaults()},
		{"xray/everything through WARP", "xray", true, true, viaWarp},
		{"sing-box/site lists", "sing-box", true, false, routing.Defaults()},
		{"sing-box/everything through WARP", "sing-box", true, true, viaWarp},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			os.MkdirAll(bin, 0o755)
			files := []string{exe(c.core)}
			if c.geo {
				files = append(files, "geosite.dat", "geoip.dat")
			}
			for _, f := range files {
				b, err := os.ReadFile(filepath.Join(src, f))
				if err != nil {
					t.Fatal(err)
				}
				os.WriteFile(filepath.Join(bin, f), b, 0o755)
			}
			st, err := store.Open(filepath.Join(dir, "t.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if c.warp {
				routing.SaveAccount(st, fakeAccount()) // a made-up device: only the direct way is used
			}
			if _, err := routing.Save(st, c.set); err != nil {
				t.Fatal(err)
			}
			m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return protocol.Env{Host: "127.0.0.1"} })
			m.Xray.APIPort, m.SingBox.APIPort = nextFree(43100), nextFree(43110)
			m.RouterPort, m.WarpTestPort = nextFree(43120), nextFree(43130)
			rp := m.routePlan()
			if (rp.dat != "") != c.geo || rp.warp() != c.warp {
				t.Fatalf("plan: dat %q warp %v", rp.dat, rp.warp())
			}
			socks := nextFree(43140)
			core := m.Xray
			var cfg obj
			if c.core == "xray" {
				cfg = m.xrayConfig([]obj{{"tag": "in-test", "listen": "127.0.0.1", "port": socks, "protocol": "socks",
					"settings": obj{"auth": "noauth", "udp": true, "ip": "127.0.0.1"},
					"sniffing": obj{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}}}, rp, false)
			} else {
				core = m.SingBox
				cfg = m.singboxConfig([]obj{{"type": "mixed", "tag": "in-test", "listen": "127.0.0.1", "listen_port": socks}}, nil, nil, rp)
				delete(cfg, "experimental")
			}
			b, _ := json.MarshalIndent(cfg, "", "  ")
			if err := core.Apply(b); err != nil {
				t.Fatalf("%v\n%s", err, b)
			}
			defer core.Stop()
			up := false
			for i := 0; i < 80 && !up; i++ {
				if cn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(socks)); err == nil {
					cn.Close()
					up = true
				} else {
					time.Sleep(100 * time.Millisecond)
				}
			}
			if !up {
				t.Fatalf("%s did not start: %v", c.core, core.Logs())
			}
			lt.pageHits.Store(0)
			lt.echoHits.Store(0)

			// normal sites still load
			for _, p := range []struct {
				url  string
				code int
			}{{"http://cp.cloudflare.com/generate_204", 204}, {"https://www.cloudflare.com/cdn-cgi/trace", 200}} {
				code, _, err := getVia(socks, p.url, 15*time.Second)
				if code != p.code {
					code, _, err = getVia(socks, p.url, 15*time.Second)
				}
				if code != p.code {
					t.Errorf("%s did not load (%d, %v)", p.url, code, err)
				}
			}

			// the loopback page and echo never answer (a refused connection is
			// held open for a while, so these wait in parallel)
			hosts := []string{"localtest.me", "127.0.0.1"}
			if lt.v6 {
				hosts = append(hosts, "[::1]")
			}
			var wg sync.WaitGroup
			for _, h := range hosts {
				wg.Add(1)
				go func() {
					defer wg.Done()
					target := "http://" + h + ":" + strconv.Itoa(lt.tcp) + "/"
					if _, body, _ := getVia(socks, target, 4*time.Second); body == loopbackSecret {
						t.Errorf("%s reached this machine's own page", target)
					}
				}()
			}
			for _, h := range []string{"localtest.me", "127.0.0.1"} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					got, err := socksUDP(socks, h, lt.udp, []byte("echo-"+h), 3*time.Second)
					if err != nil {
						t.Errorf("UDP to %s: %v", h, err)
					} else if got {
						t.Errorf("UDP to %s:%d reached this machine's own echo", h, lt.udp)
					}
				}()
			}
			wg.Wait()
			if n, e := lt.pageHits.Load(), lt.echoHits.Load(); n != 0 || e != 0 {
				t.Errorf("loopback services were reached: page %d time(s), echo %d packet(s)", n, e)
			}
			if t.Failed() {
				t.Logf("%s log: %v", c.core, core.Logs())
			}
		})
	}
}
