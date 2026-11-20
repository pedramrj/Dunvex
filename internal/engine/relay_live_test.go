//go:build linux

package engine

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/relay"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestRelayWithRealCores runs VLESS Reality (TCP) and Hysteria2 (UDP) in a
// real Xray, puts a relay in front of them on shifted ports, and connects a
// real sing-box client built from the relayed configs users get. Needs root
// and DUNVEX_CORE_DIR (a directory with xray, sing-box and the geo files).
func TestRelayWithRealCores(t *testing.T) {
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
	u := &store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(20), SubToken: util.RandomString(20), Enabled: true, ResetPeriod: "none"}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	// a local TLS 1.3 site stands in for Reality's camouflage target
	cover := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "cover") }))
	defer cover.Close()
	cert, key, err := util.SelfSignedCert(filepath.Join(dir, "certs"), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	env := protocol.Env{Host: "127.0.0.1", CertFile: cert, KeyFile: key, SelfSigned: true, CertSHA256: util.CertSHA256(cert)}
	rp, hp := nextFree(43100), freeUDP(43200)
	reality, hy := protocol.Get("vless-reality-vision"), protocol.Get("hysteria2")
	for _, x := range []struct {
		p    *protocol.Preset
		port int
		ext  map[string]any
	}{{reality, rp, map[string]any{"sni": "www.example.com", "target": cover.Listener.Addr().String()}}, {hy, hp, nil}} {
		if err := st.CreateInbound(&store.Inbound{Preset: x.p.ID, Port: x.port, Enabled: true, Params: protocol.Defaults(x.p, x.ext, "")}); err != nil {
			t.Fatal(err)
		}
	}
	ins, _ := st.Inbounds()
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.Xray.APIPort, m.SingBox.APIPort = nextFree(43300), nextFree(43310)
	m.RouterPort, m.WarpTestPort, m.Usque.APIPort = nextFree(43320), nextFree(43330), nextFree(43340)
	defer m.Xray.Stop()
	if err := m.Apply(); err != nil {
		t.Fatalf("apply: %v %v", err, m.Xray.Logs())
	}

	// the relay listens on each port +1000, as a relay with that shift does
	const shift = 1000
	rl := relay.New(nil)
	defer rl.Close()
	var fwds []relay.Forward
	for _, in := range ins {
		p := protocol.Get(in.Preset)
		fwds = append(fwds, relay.Forward{Name: p.Name, Listen: in.Port + shift, Network: protocol.Networks(p), Target: "127.0.0.1:" + strconv.Itoa(in.Port)})
	}
	rl.Apply(fwds)
	for _, s := range rl.Stats() {
		if !s.Listening {
			t.Fatalf("relay: %+v", s)
		}
	}

	for _, in := range ins {
		p := protocol.Get(in.Preset)
		// the config users get through the relay: the relay's address and port
		via := *in
		via.Port = in.Port + shift
		via.Params = map[string]any{}
		for k, v := range in.Params {
			via.Params[k] = v
		}
		via.Params["address"] = "127.0.0.1"
		mixed := nextFree(43400)
		cfile := filepath.Join(dir, "client-"+in.Preset+".json")
		var client *exec.Cmd
		if p.Security == "reality" {
			// sing-box apps get no Xray Reality configs: a real Xray client, as users run
			cfg, _ := json.Marshal(map[string]any{
				"log":      map[string]any{"loglevel": "warning"},
				"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": mixed, "protocol": "socks", "settings": map[string]any{"udp": false}}},
				"outbounds": []any{map[string]any{"protocol": "vless", "settings": map[string]any{"vnext": []any{map[string]any{
					"address": protocol.LinkHost(&via, env), "port": via.Port, "users": []any{map[string]any{"id": u.UUID, "encryption": "none", "flow": "xtls-rprx-vision"}}}}},
					"streamSettings": map[string]any{"network": "raw", "security": "reality", "realitySettings": map[string]any{
						"serverName": via.Str("sni"), "fingerprint": "chrome", "password": via.Str("publicKey"), "shortId": via.Str("shortId")}}}},
			})
			os.WriteFile(cfile, cfg, 0o600)
			client = exec.Command(filepath.Join(bin, "xray"), "run", "-c", cfile)
			client.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+bin)
		} else {
			_, outs := protocol.SingboxOutbound(p, &via, u, env, "relay")
			if len(outs) == 0 {
				t.Fatalf("%s: no outbound", p.Name)
			}
			cfg, _ := json.MarshalIndent(map[string]any{
				"log":       map[string]any{"level": "warn"},
				"inbounds":  []any{map[string]any{"type": "mixed", "listen": "127.0.0.1", "listen_port": mixed}},
				"outbounds": outs,
				"route":     map[string]any{"final": "relay"},
			}, "", "  ")
			os.WriteFile(cfile, cfg, 0o600)
			client = exec.Command(filepath.Join(bin, "sing-box"), "run", "-c", cfile)
		}
		before := rl.Stats()
		logf, _ := os.Create(cfile + ".log")
		client.Stdout, client.Stderr = logf, logf
		if err := client.Start(); err != nil {
			t.Fatal(err)
		}
		waitPort(mixed)
		var exit string
		for i := 0; i < 5 && exit == ""; i++ {
			_, body := fetch(mixed, "https://1.1.1.1/cdn-cgi/trace")
			exit = traceField(body, "ip")
			if exit == "" {
				time.Sleep(time.Second)
			}
		}
		client.Process.Kill()
		client.Wait()
		if exit == "" {
			b, _ := os.ReadFile(cfile + ".log")
			t.Errorf("%s through the relay: no internet\nclient: %s\nserver: %v", p.Name, lastLines(string(b), 6), lastLines(fmt.Sprint(m.Xray.Logs()), 6))
			continue
		}
		var moved int64
		for i, s := range rl.Stats() {
			if s.Listen == in.Port+shift {
				moved = s.Up + s.Down - before[i].Up - before[i].Down
			}
		}
		if moved < 1000 {
			t.Errorf("%s: the traffic did not go through the relay (%d bytes)", p.Name, moved)
		}
		t.Logf("%s through the relay (%s): leaves as %s, %d bytes relayed", p.Name, protocol.Networks(p), exit, moved)
	}
}
