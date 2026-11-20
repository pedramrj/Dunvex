//go:build linux

package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/routing"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
	"github.com/pedramrj/dunvex/internal/warp"
)

// copyCores copies Xray, sing-box and the site lists from dir into bin.
func copyCores(t *testing.T, dir, bin string) {
	t.Helper()
	os.MkdirAll(bin, 0o755)
	for _, name := range []string{"xray", "sing-box", "geosite.dat", "geoip.dat"} {
		in, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out, _ := os.OpenFile(filepath.Join(bin, name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		io.Copy(out, in)
		in.Close()
		out.Close()
	}
}

// fetch loads a URL through a local SOCKS proxy and returns the HTTP status
// ("000" when the connection was refused or dropped) and the body.
func fetch(socks int, url string) (string, string) {
	out, _ := exec.Command("curl", "-s", "-m", "12", "-w", "\n%{http_code}", "--socks5-hostname", fmt.Sprintf("127.0.0.1:%d", socks), url).CombinedOutput()
	s := strings.TrimRight(string(out), "\n")
	i := strings.LastIndex(s, "\n")
	if i < 0 {
		return strings.TrimSpace(s), ""
	}
	return strings.TrimSpace(s[i+1:]), s[:i]
}

func traceField(body, key string) string {
	for _, l := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(l, key+"="); ok {
			return v
		}
	}
	return ""
}

func waitPort(port int) bool {
	for i := 0; i < 80; i++ {
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			c.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// TestRoutingOnRealCores runs the routing rules in a real Xray and a real
// sing-box behind local SOCKS ports and checks what is blocked, what leaves
// directly and what leaves through WARP. Needs root and DUNVEX_CORE_DIR (a
// directory with xray, sing-box, geosite.dat and geoip.dat). With
// DUNVEX_WARP_ACCOUNT (a JSON file of a registered device) the WARP rules are
// tested too; without it the configs only have to pass the cores' own checks.
func TestRoutingOnRealCores(t *testing.T) {
	src := os.Getenv("DUNVEX_CORE_DIR")
	if src == "" || os.Geteuid() != 0 {
		t.Skip("needs root and DUNVEX_CORE_DIR")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("needs curl")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	copyCores(t, src, bin)
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var acct *warp.Account
	realWarp := false
	if f := os.Getenv("DUNVEX_WARP_ACCOUNT"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		acct = &warp.Account{}
		if err := json.Unmarshal(b, acct); err != nil {
			t.Fatal(err)
		}
		realWarp = true
	} else {
		// a made-up device: the configs must still pass the cores' checks
		priv, _ := util.WireGuardKeys()
		_, peer := util.WireGuardKeys()
		acct = &warp.Account{ID: "x", Token: "x", PrivateKey: priv, PeerKey: peer, AddressV4: "172.16.0.2",
			AddressV6: "2606:4700:110:8a36::1", Endpoint: "162.159.192.1:2408", Reserved: []int{1, 2, 3}, Type: "free"}
	}
	routing.SaveAccount(st, acct)
	set := routing.Settings{BlockAds: true, BlockDomains: []string{"example.org"}, Iran: routing.IranBlock, ClientIran: true,
		Warp: routing.WarpSites, WarpGroups: []string{"ai"}, WarpDomains: []string{"cloudflare.com"}}
	if _, err := routing.Save(st, set); err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return protocol.Env{Host: "127.0.0.1"} })
	m.Xray.APIPort, m.SingBox.APIPort = nextFree(42190), nextFree(42200)
	m.RouterPort, m.WarpTestPort = nextFree(42210), nextFree(42220)
	rp := m.routePlan()
	if !rp.warp() || !rp.useRouter() || rp.dat == "" {
		t.Fatalf("plan: warp %v router %v dat %q", rp.warp(), rp.useRouter(), rp.dat)
	}

	// Xray with a plain SOCKS port in front of the rules (plus the router and WARP check ports)
	xs := nextFree(42230)
	xcfg := m.xrayConfig([]obj{{"tag": "in-test", "listen": "127.0.0.1", "port": xs, "protocol": "socks",
		"settings": obj{"auth": "noauth", "udp": true},
		"sniffing": obj{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}}}, rp, true)
	xfile := filepath.Join(dir, "xray.json")
	b, _ := json.MarshalIndent(xcfg, "", "  ")
	os.WriteFile(xfile, b, 0o600)
	if out, err := exec.Command(filepath.Join(bin, "xray"), "run", "-test", "-c", xfile).CombinedOutput(); err != nil {
		t.Fatalf("xray refused the config: %v\n%s\n%s", err, lastLines(string(out), 12), b)
	}
	// sing-box with a mixed port in front of the same rules
	ss := nextFree(42240)
	scfg := m.singboxConfig([]obj{{"type": "mixed", "tag": "in-test", "listen": "127.0.0.1", "listen_port": ss}}, nil, nil, rp)
	delete(scfg, "experimental")
	scfg["log"] = obj{"level": "warn"}
	sfile := filepath.Join(dir, "sing-box.json")
	b, _ = json.MarshalIndent(scfg, "", "  ")
	os.WriteFile(sfile, b, 0o600)
	if out, err := exec.Command(filepath.Join(bin, "sing-box"), "check", "-c", sfile).CombinedOutput(); err != nil {
		t.Fatalf("sing-box refused the config: %v\n%s", err, lastLines(string(out), 12))
	}
	t.Log("both cores accept the routing configs")

	start := func(name string, args ...string) *exec.Cmd {
		c := exec.Command(filepath.Join(bin, name), args...)
		logf, _ := os.Create(filepath.Join(dir, name+".log"))
		c.Stdout, c.Stderr = logf, logf
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		return c
	}
	xr := start("xray", "run", "-c", xfile)
	defer func() { xr.Process.Kill(); xr.Wait() }()
	sb := start("sing-box", "run", "-c", sfile)
	defer func() { sb.Process.Kill(); sb.Wait() }()
	if !waitPort(xs) || !waitPort(ss) || !waitPort(m.RouterPort) {
		b1, _ := os.ReadFile(filepath.Join(dir, "xray.log"))
		b2, _ := os.ReadFile(filepath.Join(dir, "sing-box.log"))
		t.Fatalf("the cores did not start\nxray: %s\nsing-box: %s", lastLines(string(b1), 8), lastLines(string(b2), 8))
	}

	for _, c := range []struct {
		name string
		port int
	}{{"xray", xs}, {"xray router", m.RouterPort}, {"sing-box", ss}} {
		code, body := fetch(c.port, "https://1.1.1.1/cdn-cgi/trace")
		direct := traceField(body, "ip")
		if code != "200" || direct == "" {
			t.Errorf("%s: no direct internet (%s)", c.name, code)
			continue
		}
		if w := traceField(body, "warp"); w != "off" {
			t.Errorf("%s: an address with no name went through WARP (%s)", c.name, w)
		}
		for _, url := range []string{"https://example.org/", "https://www.irna.ir/", "https://www.digikala.com/", "https://googleads.g.doubleclick.net/"} {
			if code, _ := fetch(c.port, url); code != "000" {
				t.Errorf("%s: %s was not blocked (%s)", c.name, url, code)
			}
		}
		if realWarp {
			code, body := fetch(c.port, "https://www.cloudflare.com/cdn-cgi/trace")
			if w := traceField(body, "warp"); code != "200" || (w != "on" && w != "plus") {
				t.Errorf("%s: cloudflare.com did not leave through WARP (%s, warp=%s)", c.name, code, w)
			} else {
				t.Logf("%s: direct %s, WARP %s (%s)", c.name, direct, traceField(body, "ip"), traceField(body, "colo"))
			}
		}
	}
	if realWarp {
		code, body := fetch(m.WarpTestPort, "https://1.1.1.1/cdn-cgi/trace")
		if w := traceField(body, "warp"); code != "200" || (w != "on" && w != "plus") {
			t.Errorf("the WARP check port does not leave through WARP (%s, warp=%s)", code, w)
		}
	}
}
