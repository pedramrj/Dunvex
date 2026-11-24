package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

func cfgWith(port int, clients ...map[string]any) []byte {
	cl := []any{}
	for _, c := range clients {
		cl = append(cl, c)
	}
	b, _ := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"tag": "in-1", "port": port, "protocol": "vless",
			"settings": map[string]any{"clients": cl, "decryption": "none"}}},
	})
	return b
}

func TestXrayUserDiff(t *testing.T) {
	a := map[string]any{"id": "a", "email": "alice"}
	b := map[string]any{"id": "b", "email": "bob"}
	b2 := map[string]any{"id": "b2", "email": "bob"}

	ops, gone, ok := xrayUserDiff(cfgWith(1, a), cfgWith(1, a, b))
	if !ok || len(ops) != 1 || ops[0].add == nil || len(gone) != 0 {
		t.Fatalf("add: ok=%v ops=%+v gone=%v", ok, ops, gone)
	}
	ops, gone, ok = xrayUserDiff(cfgWith(1, a, b), cfgWith(1, b))
	if !ok || len(ops) != 1 || ops[0].remove != "alice" || len(gone) != 1 || gone[0] != "alice" {
		t.Fatalf("remove: ok=%v ops=%+v gone=%v", ok, ops, gone)
	}
	ops, _, ok = xrayUserDiff(cfgWith(1, a, b), cfgWith(1, a, b2))
	if !ok || len(ops) != 2 {
		t.Fatalf("change: ok=%v ops=%+v", ok, ops)
	}
	if _, _, ok = xrayUserDiff(cfgWith(1, a), cfgWith(2, a)); ok {
		t.Fatal("a port change must need a restart")
	}
	if ops, _, ok = xrayUserDiff(cfgWith(1, a), cfgWith(1, a)); !ok || len(ops) != 0 {
		t.Fatal("identical configs need nothing")
	}
}

func inboundUsers(t *testing.T, addr, tag string) map[string]bool {
	resp, err := invoke(addr, "/xray.app.proxyman.command.HandlerService/GetInboundUsers", appendStr(nil, 1, tag))
	if err != nil {
		t.Fatalf("GetInboundUsers %s: %v", tag, err)
	}
	out := map[string]bool{}
	for len(resp) > 0 {
		num, typ, n := protowire.ConsumeTag(resp)
		resp = resp[n:]
		if num == 1 && typ == protowire.BytesType {
			u, m := protowire.ConsumeBytes(resp)
			resp = resp[m:]
			for len(u) > 0 {
				fn, ft, k := protowire.ConsumeTag(u)
				u = u[k:]
				if fn == 2 && ft == protowire.BytesType {
					s, k := protowire.ConsumeString(u)
					out[s] = true
					u = u[k:]
					continue
				}
				u = u[protowire.ConsumeFieldValue(fn, ft, u):]
			}
			continue
		}
		resp = resp[protowire.ConsumeFieldValue(num, typ, resp):]
	}
	return out
}

// TestLiveUsersOnRealXray checks that user changes reach a running Xray
// without a restart, for every protocol that supports it, and that a new user
// can actually pass traffic. Needs DUNVEX_TEST_BIN (folder with xray).
func TestLiveUsersOnRealXray(t *testing.T) {
	src := os.Getenv("DUNVEX_TEST_BIN")
	if src == "" {
		t.Skip("DUNVEX_TEST_BIN not set")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	exe := "xray"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	raw, err := os.ReadFile(filepath.Join(src, exe))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(bin, exe), raw, 0o755) // no site lists: the routing works without them

	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mk := func(name string) *store.User {
		u := &store.User{Name: name, UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20), Enabled: true}
		if err := st.CreateUser(u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	alice := mk("alice")
	presets := []string{"vless-reality-vision", "vless-ws-tls", "vmess-ws", "trojan-tls", "shadowsocks-2022", "shadowsocks", "hysteria2"}
	port := nextFree(31000)
	ports := map[string]int{}
	for _, id := range presets {
		p := protocol.Get(id)
		in := &store.Inbound{Preset: id, Port: port, Enabled: true, Params: protocol.Defaults(p, map[string]any{}, "example.com")}
		if err := st.CreateInbound(in); err != nil {
			t.Fatal(err)
		}
		ports[id] = port
		port = nextFree(port + 1)
	}
	cert, key, _ := util.SelfSignedCert(filepath.Join(dir, "certs"), "example.com")
	env := protocol.Env{Host: "127.0.0.1", Domain: "example.com", CertFile: cert, KeyFile: key, SelfSigned: true, Listen: "127.0.0.1"}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.Xray.APIPort = nextFree(port + 1)
	defer m.Xray.Stop()
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	pid := func() int { m.Xray.mu.Lock(); defer m.Xray.mu.Unlock(); return m.Xray.cmd.Process.Pid }
	first := pid()
	inbounds, _ := st.Inbounds()

	bob := mk("bob")
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	if pid() != first {
		t.Fatal("adding a user restarted Xray")
	}
	for _, in := range inbounds {
		if us := inboundUsers(t, m.xrayAPI(), in.Tag()); !us["bob"] || !us["alice"] {
			t.Errorf("%s: users after add = %v", in.Preset, us)
		}
	}

	// bob passes traffic through VMess right away
	socks := nextFree(m.Xray.APIPort + 1)
	vm := protocol.Get("vmess-ws")
	var vmIn *store.Inbound
	for _, in := range inbounds {
		if in.Preset == "vmess-ws" {
			vmIn = in
		}
	}
	client, _ := json.Marshal(map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
		"outbounds": []any{map[string]any{"protocol": "vmess", "settings": map[string]any{"vnext": []any{map[string]any{
			"address": "127.0.0.1", "port": vmIn.Port, "users": []any{map[string]any{"id": bob.UUID, "security": "aes-128-gcm"}}}}},
			"streamSettings": map[string]any{"network": "ws", "wsSettings": map[string]any{"path": vmIn.Str("path")}}}},
	})
	_ = vm
	cpath := filepath.Join(dir, "client.json")
	os.WriteFile(cpath, client, 0o600)
	cl := exec.Command(filepath.Join(bin, exe), "run", "-c", cpath)
	var clog strings.Builder
	cl.Stdout, cl.Stderr = &clog, &clog
	if err := cl.Start(); err != nil {
		t.Fatal(err)
	}
	defer cl.Process.Kill()
	time.Sleep(1500 * time.Millisecond)
	proxy, _ := url.Parse(fmt.Sprintf("socks5://127.0.0.1:%d", socks))
	hc := &http.Client{Timeout: 12 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	// Xray refuses private destinations, so ask a public 204 endpoint
	res, err := hc.Get("http://cp.cloudflare.com/generate_204")
	if err != nil {
		t.Fatalf("bob through vmess: %v | server: %v | client: %s", err, m.Xray.Logs(), clog.String())
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("unexpected status %d", res.StatusCode)
	}

	// disabling alice removes her live
	alice.Enabled = false
	st.UpdateUser(alice)
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	if pid() != first {
		t.Fatal("disabling a user restarted Xray")
	}
	for _, in := range inbounds {
		if us := inboundUsers(t, m.xrayAPI(), in.Tag()); us["alice"] || !us["bob"] {
			t.Errorf("%s: users after disable = %v", in.Preset, us)
		}
	}
	m.mu.Lock()
	_, tracked := m.liveGone["alice"]
	m.mu.Unlock()
	if !tracked {
		t.Error("alice should be tracked for lingering connections")
	}

	// an inbound change still restarts
	in := inbounds[0]
	in.Port = nextFree(socks + 1)
	st.UpdateInbound(in)
	if err := m.Apply(); err != nil {
		t.Fatal(err)
	}
	if pid() == first {
		t.Fatal("a port change should restart Xray")
	}
}

func nextFree(from int) int {
	for p := from; p < 65000; p++ {
		if util.FreePort(p) {
			return p
		}
	}
	return 0
}
