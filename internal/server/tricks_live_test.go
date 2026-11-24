package server

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/health"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
)

// Live tests with the real cores: set DUNVEX_TEST_BIN to a folder with xray
// and sing-box (and geoip.dat, geosite.dat). They need the internet: Reality
// borrows www.speedtest.net, and the check loads a public page.

var nextLive atomic.Int32

// livePort is a port free for TCP and UDP (Hysteria2, TUIC, WireGuard and
// mKCP use UDP), below the range the system hands out to outgoing connections.
func livePort(t *testing.T) int {
	nextLive.CompareAndSwap(0, 23000)
	for {
		p := int(nextLive.Add(1))
		if p > 30000 {
			t.Fatal("no free port")
		}
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err != nil {
			continue
		}
		l.Close()
		if u, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(p)); err == nil {
			u.Close()
			return p
		}
	}
}

// ipProbe is a 204 page at an address, not a name. Xray's WireGuard looks
// names up through the tunnel with the system's DNS servers, which an IPv4
// tunnel cannot reach when they are IPv6 (as on the Windows test machine);
// the address is looked up here, outside the tunnel.
func ipProbe(t *testing.T) []string {
	ips, err := net.DefaultResolver.LookupIP(context.Background(), "ip4", "connectivitycheck.gstatic.com")
	if err != nil || len(ips) == 0 {
		t.Fatalf("no address for the probe: %v", err)
	}
	return []string{"http://" + ips[0].String() + "/generate_204"}
}

// healthBusy reports whether a config check is running.
func (s *Server) healthBusy() bool {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	return s.healthRunning
}

// liveBin copies the cores into a folder of the test's own.
func liveBin(t *testing.T) string {
	src := os.Getenv("DUNVEX_TEST_BIN")
	if src == "" {
		t.Skip("DUNVEX_TEST_BIN not set")
	}
	bin := filepath.Join(t.TempDir(), "bin")
	os.MkdirAll(bin, 0o755)
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	for _, f := range []string{"xray" + ext, "sing-box" + ext, "geoip.dat", "geosite.dat"} {
		raw, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(bin, f), raw, 0o755)
	}
	return bin
}

// Every anti-censorship option on at once, through the settings API: this
// server's cores take their side of it (ECH keys, sing-box's multiplex), and
// the config check, with the real Xray and sing-box as the apps, loads a page
// through every config built with them (links read the way an Xray app reads
// them plus the Xray JSON's Mux.Cool, sing-box profiles as they are).
func TestTricksOnRealCores(t *testing.T) {
	bin := liveBin(t)
	t.Setenv("DUNVEX_LISTEN", "127.0.0.1")
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	st.SetSetting("host", "127.0.0.1")
	st.SetSetting("domain", "vpn.dunvex.test")
	engine.EnsureHealthKey(st)
	presets := []string{"vless-reality-vision", "vless-reality-universal", "vless-reality-xhttp", "vless-reality-grpc", "trojan-reality",
		"vless-xhttp-tls", "vless-ws-tls", "vless-httpupgrade-tls", "vless-grpc-tls", "trojan-ws-tls", "vmess-ws-tls", "vmess-ws",
		"trojan-tls", "hysteria2", "tuic", "hysteria2-sb", "wireguard", "anytls", "shadowtls", "shadowsocks-2022", "shadowsocks", "vmess-kcp"}
	for _, id := range presets {
		p := protocol.Get(id)
		if err := st.CreateInbound(&store.Inbound{Preset: id, Port: livePort(t), Enabled: true, Params: protocol.Defaults(p, nil, "vpn.dunvex.test")}); err != nil {
			t.Fatal(err)
		}
	}
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler(),
		HealthRunner: &health.Runner{BinDir: bin, Parallel: 4, Timeout: 30 * time.Second}}
	srv.Mgr = engine.NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	srv.Mgr.Xray.APIPort, srv.Mgr.SingBox.APIPort = livePort(t), livePort(t)
	defer srv.Mgr.Xray.Stop()
	defer srv.Mgr.SingBox.Stop()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	set := map[string]string{"fragment": "true", "ech_direct": "true", "ech_name": "www.speedtest.net",
		"mux": "true", "mux_protocol": "smux", "mux_streams": "4", "mux_brutal": "true", "wg_noise": "true", "health_hours": "0"}
	// DUNVEX_TEST_SETTINGS (JSON) changes some of them, to compare a run with an option off
	if v := os.Getenv("DUNVEX_TEST_SETTINGS"); v != "" {
		if err := json.Unmarshal([]byte(v), &set); err != nil {
			t.Fatal(err)
		}
	}
	tricksOn := set["fragment"] == "true" && set["ech_direct"] == "true" && set["mux"] == "true" && set["wg_noise"] == "true"
	if code, out := owner.do("PUT", "/settings", set); code != 200 {
		t.Fatalf("settings: %d %v", code, out)
	}
	time.Sleep(300 * time.Millisecond) // the check the save started sees the old cores: let it go
	for srv.healthBusy() {
		time.Sleep(200 * time.Millisecond)
	}
	if err := srv.Mgr.Apply(); err != nil {
		t.Fatal(err)
	}
	if errs, core := srv.Mgr.Errors(); len(errs) > 0 || len(core) > 0 {
		t.Fatalf("inbounds: %v, cores: %v", errs, core)
	}
	xcfg, _ := os.ReadFile(srv.Mgr.Xray.ConfigPath)
	scfg, _ := os.ReadFile(srv.Mgr.SingBox.ConfigPath)
	if tricksOn && (!strings.Contains(string(xcfg), `"echServerKeys"`) || !strings.Contains(string(scfg), `"multiplex"`) || !strings.Contains(string(scfg), "BEGIN ECH KEYS")) {
		t.Fatalf("the servers' side is missing:\nxray: %.300s\nsing-box: %.300s", xcfg, scfg)
	}
	if strings.Contains(string(scfg), `"brutal"`) && runtime.GOOS != "linux" {
		t.Fatal("Brutal on a server that cannot do it")
	}
	time.Sleep(2 * time.Second) // the cores open their ports

	hu := engine.HealthUser(st)
	targets := srv.healthTargets(hu)
	var wg []health.Target // tested with a page at an address (see ipProbe)
	for i := 0; i < len(targets); i++ {
		if targets[i].Preset == "wireguard" && targets[i].Xray != nil {
			wg = append(wg, targets[i])
			targets = append(targets[:i], targets[i+1:]...)
			i--
		}
	}
	// what sing-box apps get for the Xray protocols too (their ECH and fragment)
	n := len(targets) + len(wg) // the configs of the check, WireGuard included
	for _, it := range srv.items(hu) {
		if it.p.Engine != "xray" {
			continue
		}
		kind, items := protocol.SingboxOutbound(it.p, it.in, hu, it.env, "proxy")
		if len(items) == 0 || kind != "outbound" {
			continue
		}
		targets = append(targets, health.Target{Config: health.Config{Preset: it.p.ID + " (sing-box)", Name: it.name}, Kind: health.KindSingbox, Singbox: items})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res := srv.healthRunner().Run(ctx, targets)
	if len(wg) > 0 {
		r := *srv.healthRunner()
		r.Probes = ipProbe(t)
		targets = append(targets, wg...)
		res = append(res, r.Run(ctx, wg)...)
	}
	ok := 0
	for i, r := range res {
		js, _ := json.Marshal(targets[i].Xray)
		if targets[i].Singbox != nil {
			js, _ = json.Marshal(targets[i].Singbox)
		}
		switch r.Status {
		case health.StatusOK:
			ok++
			t.Logf("ok   %-34s %5d ms", r.Preset, r.MS)
		case health.StatusSkip:
			t.Logf("skip %-34s %s", r.Preset, r.Error)
		default:
			t.Errorf("FAIL %-34s %s\n  client: %.600s", r.Preset, r.Error, js)
		}
	}
	t.Logf("%d of %d passed (%d configs of the check, %d sing-box profiles of Xray protocols)", ok, len(targets), n, len(targets)-n)
	// what the apps got really had the options in it
	all := ""
	for _, tg := range targets {
		a, _ := json.Marshal(tg.Xray)
		b, _ := json.Marshal(tg.Singbox)
		all += string(a) + string(b)
	}
	for _, want := range []string{`"echConfigList"`, `"mux":{`, `"type":"noise"`, `"type":"fragment"`, `"multiplex":{`, `"record_fragment":true`, `BEGIN ECH CONFIGS`} {
		if tricksOn && !strings.Contains(all, want) {
			t.Errorf("no config carried %s", want)
		}
	}
}

// The WireGuard noise on the real Xray: the app's junk packets reach the
// server before its handshake (a UDP relay in front counts them), the server
// ignores them, and the page comes back.
func TestWireGuardNoiseOnRealXray(t *testing.T) {
	bin := liveBin(t)
	t.Setenv("DUNVEX_LISTEN", "127.0.0.1")
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetSetting("host", "127.0.0.1")
	engine.EnsureHealthKey(st)
	st.SetSetting(noiseSetting, "true")
	st.SetSetting(noiseCountSetting, "6")
	p := protocol.Get("wireguard")
	in := &store.Inbound{Preset: p.ID, Port: livePort(t), Enabled: true, Params: protocol.Defaults(p, nil, "")}
	if err := st.CreateInbound(in); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler(), HealthRunner: &health.Runner{BinDir: bin, Timeout: 30 * time.Second}}
	srv.Mgr = engine.NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	srv.Mgr.Xray.APIPort = livePort(t)
	defer srv.Mgr.Xray.Stop()
	if err := srv.Mgr.Apply(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)

	// the relay: everything to the server, back to the app; junk is what is no WireGuard message
	relay, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	server, _ := net.ResolveUDPAddr("udp", "127.0.0.1:"+strconv.Itoa(in.Port))
	up, err := net.DialUDP("udp", nil, server)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	var junk, beforeHandshake atomic.Int32
	var seen atomic.Bool
	var app atomic.Value
	go func() {
		b := make([]byte, 2048)
		for {
			n, from, err := relay.ReadFrom(b)
			if err != nil {
				return
			}
			app.Store(from)
			wg := n >= 4 && b[0] >= 1 && b[0] <= 4 && b[1] == 0 && b[2] == 0 && b[3] == 0
			if !wg {
				junk.Add(1)
			}
			if b[0] == 1 && wg && seen.CompareAndSwap(false, true) {
				beforeHandshake.Store(junk.Load())
			}
			up.Write(b[:n])
		}
	}()
	go func() {
		b := make([]byte, 2048)
		for {
			n, err := up.Read(b)
			if err != nil {
				return
			}
			if a, ok := app.Load().(net.Addr); ok {
				relay.WriteTo(b[:n], a)
			}
		}
	}()
	c := *in
	c.Params = map[string]any{}
	for k, v := range in.Params {
		c.Params[k] = v
	}
	h, port, _ := net.SplitHostPort(relay.LocalAddr().String())
	c.Params["address"] = h
	c.Port, _ = strconv.Atoi(port)
	hu := engine.HealthUser(st)
	link := protocol.Link(p, &c, hu, srv.Env(), "wg")
	o, err := protocol.XrayOutbound(link, "proxy")
	if err != nil {
		t.Fatal(err)
	}
	r := *srv.healthRunner()
	r.Probes = ipProbe(t)
	res := r.Run(context.Background(), []health.Target{{Config: health.Config{Preset: p.ID, Name: "wg"}, Kind: health.KindXray, Xray: o}})
	if res[0].Status != health.StatusOK {
		t.Fatalf("%s\n%s", res[0].Error, link)
	}
	if beforeHandshake.Load() < 6 {
		t.Fatalf("%d junk packets before the handshake (%d in all), want 6", beforeHandshake.Load(), junk.Load())
	}
	t.Logf("page in %d ms; %d junk packets before WireGuard's handshake", res[0].MS, beforeHandshake.Load())
}

// counter forwards TCP to target and counts the connections: a stand-in for
// the CDN (and for the straight way), to see which way each half takes.
func counter(t *testing.T, target string) (addr string, n *atomic.Int32) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	n = &atomic.Int32{}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			go func() {
				defer c.Close()
				up, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer up.Close()
				go io.Copy(up, c)
				io.Copy(c, up)
			}()
		}
	}()
	return l.Addr().String(), n
}

// XHTTP that sends its downloads another way, on the real Xray: uploads reach
// the inbound through one forwarder (the CDN), downloads through another (the
// straight way), and the page comes back.
func TestXHTTPSplitOnRealXray(t *testing.T) {
	bin := liveBin(t)
	t.Setenv("DUNVEX_LISTEN", "127.0.0.1")
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetSetting("host", "127.0.0.1")
	st.SetSetting("domain", "vpn.dunvex.test")
	engine.EnsureHealthKey(st)
	p := protocol.Get("vless-xhttp-tls")
	in := &store.Inbound{Preset: p.ID, Port: livePort(t), Enabled: true, Params: protocol.Defaults(p, nil, "vpn.dunvex.test")}
	if err := st.CreateInbound(in); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler(), HealthRunner: &health.Runner{BinDir: bin, Timeout: 30 * time.Second}}
	srv.Mgr = engine.NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	srv.Mgr.Xray.APIPort = livePort(t)
	defer srv.Mgr.Xray.Stop()
	if err := srv.Mgr.Apply(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	target := "127.0.0.1:" + strconv.Itoa(in.Port)
	upAddr, ups := counter(t, target)
	downAddr, downs := counter(t, target)
	at := func(addr string) (*store.Inbound, protocol.Env) {
		h, port, _ := net.SplitHostPort(addr)
		c := *in
		c.Params = map[string]any{}
		for k, v := range in.Params {
			c.Params[k] = v
		}
		c.Params["address"] = h
		c.Port, _ = strconv.Atoi(port)
		return &c, srv.Env()
	}
	upIn, upEnv := at(upAddr)
	downIn, downEnv := at(downAddr)
	for _, mode := range []string{"auto", "packet-up", "stream-up"} {
		upIn.Params["mode"], downIn.Params["mode"] = mode, mode
		upEnv.Download = &protocol.Route{In: downIn, Env: downEnv}
		hu := engine.HealthUser(st)
		link := protocol.Link(p, upIn, hu, upEnv, "split")
		o, err := protocol.XrayOutbound(link, "proxy")
		if err != nil {
			t.Fatal(err)
		}
		u0, d0 := ups.Load(), downs.Load()
		res := srv.healthRunner().Run(context.Background(), []health.Target{{Config: health.Config{Preset: p.ID, Name: mode}, Kind: health.KindXray, Xray: o}})
		if res[0].Status != health.StatusOK {
			t.Fatalf("%s: %s\n%s", mode, res[0].Error, link)
		}
		if ups.Load() == u0 || downs.Load() == d0 {
			t.Fatalf("%s: uploads took the upload way %d times, downloads the download way %d times", mode, ups.Load()-u0, downs.Load()-d0)
		}
		t.Logf("%s: page in %d ms, %d upload and %d download connections", mode, res[0].MS, ups.Load()-u0, downs.Load()-d0)
	}
}

// The CDN's ECH as the server looks it up: Cloudflare's, and a real TLS
// handshake to Cloudflare with it hides the name.
func TestCloudflareECHLookup(t *testing.T) {
	if os.Getenv("DUNVEX_TEST_BIN") == "" {
		t.Skip("DUNVEX_TEST_BIN not set (the live tests need the internet)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg, err := echLookup(ctx, "crypto.cloudflare.com")
	if err != nil || cfg == "" {
		t.Fatalf("lookup: %q %v", cfg, err)
	}
	name, _ := protocol.ECHPublicName(cfg)
	list, _ := base64.StdEncoding.DecodeString(cfg)
	c, err := tls.Dial("tcp", "crypto.cloudflare.com:443", &tls.Config{ServerName: "crypto.cloudflare.com", EncryptedClientHelloConfigList: list})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "GET /cdn-cgi/trace HTTP/1.1\r\nHost: crypto.cloudflare.com\r\nConnection: close\r\n\r\n")
	b, _ := io.ReadAll(c)
	if !c.ConnectionState().ECHAccepted || !strings.Contains(string(b), "sni=encrypted") {
		t.Fatalf("ECH not taken (public name %s):\n%s", name, b)
	}
	t.Logf("Cloudflare's ECH, public name %s: the name went encrypted", name)
}
