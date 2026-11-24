package health

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/dnstt"
)

// serveSOCKS is a small SOCKS5 server: it connects to what it is asked, or
// refuses everything when broken (a tunnel whose server is down).
func serveSOCKS(l net.Listener, user, pass string, broken bool) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			h := make([]byte, 2)
			if _, err := io.ReadFull(c, h); err != nil || h[0] != 5 {
				return
			}
			io.ReadFull(c, make([]byte, h[1]))
			if user != "" {
				c.Write([]byte{5, 2})
				v := make([]byte, 2)
				io.ReadFull(c, v)
				u := make([]byte, v[1])
				io.ReadFull(c, u)
				pl := make([]byte, 1)
				io.ReadFull(c, pl)
				p := make([]byte, pl[0])
				io.ReadFull(c, p)
				if string(u) != user || string(p) != pass {
					c.Write([]byte{1, 1})
					return
				}
				c.Write([]byte{1, 0})
			} else {
				c.Write([]byte{5, 0})
			}
			req := make([]byte, 4)
			if _, err := io.ReadFull(c, req); err != nil {
				return
			}
			var host string
			switch req[3] {
			case 1:
				b := make([]byte, 4)
				io.ReadFull(c, b)
				host = net.IP(b).String()
			case 3:
				n := make([]byte, 1)
				io.ReadFull(c, n)
				b := make([]byte, n[0])
				io.ReadFull(c, b)
				host = string(b)
			case 4:
				b := make([]byte, 16)
				io.ReadFull(c, b)
				host = net.IP(b).String()
			}
			pb := make([]byte, 2)
			io.ReadFull(c, pb)
			addr := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb))))
			if broken {
				c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
				return
			}
			up, err := net.Dial("tcp", addr)
			if err != nil {
				c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
				return
			}
			defer up.Close()
			c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
			go io.Copy(up, c)
			io.Copy(c, up)
		}()
	}
}

// fakeCore stands in for Xray and sing-box: it reads the SOCKS port from the
// client config and serves it, working or broken as the outbound says.
func fakeCore(t *testing.T) func(ctx context.Context, kind string, cfg []byte) (func(), func() string, <-chan struct{}, error) {
	return func(ctx context.Context, kind string, cfg []byte) (func(), func() string, <-chan struct{}, error) {
		var c struct {
			Inbounds []struct {
				Port       int `json:"port"`
				ListenPort int `json:"listen_port"`
			} `json:"inbounds"`
		}
		if err := json.Unmarshal(cfg, &c); err != nil || len(c.Inbounds) != 1 {
			t.Fatalf("client config: %v %s", err, cfg)
		}
		exited := make(chan struct{})
		s := string(cfg)
		if strings.Contains(s, "will-not-start") {
			close(exited)
			return func() {}, func() string { return "2026/11/24 10:00:00 [Error] infra/conf: failed to build outbound > bad key\n" }, exited, nil
		}
		port := c.Inbounds[0].Port + c.Inbounds[0].ListenPort
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Fatal(err)
		}
		go serveSOCKS(l, "", "", strings.Contains(s, "server-down"))
		log := "2026/11/24 10:00:01.123456 [Warning] [1234] app/proxyman/outbound: failed to process outbound traffic > dial tcp 203.0.113.10:443: i/o timeout\n"
		return func() { l.Close() }, func() string { return log }, exited, nil
	}
}

func probeServer(t *testing.T, code int) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
	t.Cleanup(srv.Close)
	return srv.URL + "/generate_204"
}

func TestRunWithClientCores(t *testing.T) {
	r := &Runner{Probes: []string{probeServer(t, 204)}, Timeout: 5 * time.Second, Start: fakeCore(t)}
	xr := func(id int64, tag string) Target {
		return Target{Config: Config{InboundID: id, Preset: "vless-reality-vision", Name: "cfg " + tag}, Kind: KindXray,
			Xray: map[string]any{"protocol": "vless", "tag": tag}}
	}
	targets := []Target{
		xr(1, "proxy"),
		{Config: Config{InboundID: 2, Preset: "tuic"}, Kind: KindSingbox, Singbox: []map[string]any{{"type": "tuic", "tag": "t"}}},
		xr(3, "server-down"),
		xr(4, "will-not-start"),
		{Config: Config{InboundID: 5, Preset: "mieru"}, Skip: "no Mieru client on the server"},
		{Config: Config{InboundID: 6, Preset: "vless-reality-vision"}, Kind: KindXray, Broken: "a Reality link without its public key (pbk)"},
	}
	res := r.Run(context.Background(), targets)
	if res[0].Status != StatusOK || res[0].MS <= 0 || res[0].Name != "cfg proxy" {
		t.Errorf("working Xray config: %+v", res[0])
	}
	if res[1].Status != StatusOK {
		t.Errorf("working sing-box config: %+v", res[1])
	}
	if res[2].Status != StatusFail || res[2].Error != "the connection through the config failed (client: dial tcp 203.0.113.10:443: i/o timeout)" {
		t.Errorf("server down: %+v", res[2])
	}
	if res[3].Status != StatusFail || !strings.Contains(res[3].Error, "did not start") || !strings.Contains(res[3].Error, "bad key") {
		t.Errorf("client that does not start: %+v", res[3])
	}
	if res[4].Status != StatusSkip || res[4].Error != "no Mieru client on the server" {
		t.Errorf("skipped: %+v", res[4])
	}
	if res[5].Status != StatusFail || !strings.Contains(res[5].Error, "pbk") {
		t.Errorf("broken link: %+v", res[5])
	}

	// a page that does not answer 204 is not a working tunnel (a block page, a portal)
	r.Probes = []string{probeServer(t, 200)}
	if res := r.Run(context.Background(), targets[:1]); res[0].Status != StatusFail || !strings.Contains(res[0].Error, "instead of 204") {
		t.Errorf("200 instead of 204: %+v", res[0])
	}
	// the second probe stands in when the first is down
	r.Probes = []string{"http://127.0.0.1:1/generate_204", probeServer(t, 204)}
	if res := r.Run(context.Background(), targets[:1]); res[0].Status != StatusOK {
		t.Errorf("second probe: %+v", res[0])
	}
}

// A failed TCP config says whether its port answers at all.
func TestPortCheck(t *testing.T) {
	r := &Runner{Probes: []string{probeServer(t, 204)}, Timeout: 3 * time.Second, Start: fakeCore(t)}
	open, _ := net.Listen("tcp", "127.0.0.1:0")
	defer open.Close()
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	closedAddr := closed.Addr().String()
	closed.Close()
	down := func(addr string) Target {
		return Target{Config: Config{InboundID: 1}, Kind: KindXray, Xray: map[string]any{"tag": "server-down"}, Addr: addr}
	}
	res := r.Run(context.Background(), []Target{down(closedAddr), down(open.Addr().String())})
	if res[0].Status != StatusFail || !strings.HasPrefix(res[0].Error, "nothing answers on "+closedAddr+" (") {
		t.Errorf("closed port: %+v", res[0])
	}
	if res[1].Status != StatusFail || res[1].Error != "the server answers on "+open.Addr().String()+", but no page came back through the config (client: dial tcp 203.0.113.10:443: i/o timeout)" {
		t.Errorf("open port: %+v", res[1])
	}
}

func TestClientConfigs(t *testing.T) {
	b, err := ClientConfig(Target{Kind: KindXray, Xray: map[string]any{"protocol": "vless", "tag": "proxy"}}, 1234)
	if err != nil || !strings.Contains(string(b), `"port":1234`) || !strings.Contains(string(b), `"protocol":"socks"`) {
		t.Fatalf("xray: %v %s", err, b)
	}
	b, err = ClientConfig(Target{Kind: KindSingbox, Singbox: []map[string]any{{"type": "shadowsocks", "tag": "ss", "detour": "ss-stls"}, {"type": "shadowtls", "tag": "ss-stls"}}}, 1234)
	if err != nil || !strings.Contains(string(b), `"final":"ss"`) || !strings.Contains(string(b), `"listen_port":1234`) {
		t.Fatalf("sing-box: %v %s", err, b)
	}
	b, err = ClientConfig(Target{Kind: KindSingbox, Endpoints: []map[string]any{{"type": "wireguard", "tag": "wg"}}}, 1)
	if err != nil || !strings.Contains(string(b), `"final":"wg"`) || !strings.Contains(string(b), `"endpoints"`) {
		t.Fatalf("sing-box endpoint: %v %s", err, b)
	}
}

// dnstt: the real tunnel server and the built-in client, then SOCKS with the
// user's account behind it.
func TestRunDNSTT(t *testing.T) {
	priv, pub, err := dnstt.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	key, _ := dnstt.DecodeKey(priv)
	sl, _ := net.Listen("tcp", "127.0.0.1:0")
	defer sl.Close()
	go serveSOCKS(sl, "~health", "secret", false)
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go dnstt.Run(key, "t.example.com", sl.Addr().String(), pc)

	r := &Runner{Probes: []string{probeServer(t, 204)}, Timeout: 20 * time.Second}
	tg := func(pass string) Target {
		return Target{Config: Config{InboundID: 9, Preset: "dnstt"}, Kind: KindDNSTT, Slow: true,
			DNSTT: &DNSTT{Resolver: pc.LocalAddr().String(), Domain: "t.example.com", PublicKey: pub, User: "~health", Pass: pass}}
	}
	res := r.Run(context.Background(), []Target{tg("secret"), tg("wrong")})
	if res[0].Status != StatusOK || res[0].MS <= 0 {
		t.Fatalf("dnstt: %+v", res[0])
	}
	if res[1].Status != StatusFail {
		t.Fatalf("dnstt with a wrong password: %+v", res[1])
	}
	t.Logf("through dnstt in %d ms; wrong password: %s", res[0].MS, res[1].Error)
}

func TestReason(t *testing.T) {
	for in, want := range map[string]string{
		"app/proxyman/outbound: failed to process outbound traffic > proxy/shadowsocks: failed to find an available destination > common/retry: [dial tcp 198.51.100.1:8389: i/o timeout] > common/retry: all retry attempts failed": "dial tcp 198.51.100.1:8389: i/o timeout",
		"app/proxyman/outbound: failed to process outbound traffic > proxy/vless/outbound: failed to read response > REALITY: processed invalid connection":                                                                          "REALITY: processed invalid connection",
		"connection: open connection to cp.cloudflare.com:80 using outbound/tuic[proxy]: timeout: no recent network activity":                                                                                                        "connection: open connection to cp.cloudflare.com:80 using outbound/tuic[proxy]: timeout: no recent network activity",
	} {
		if got := reason(in); got != want {
			t.Errorf("%q\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestLastLogLine(t *testing.T) {
	log := "Xray 26.11.9 started\n2026/11/24 18:00:00.123456 [Warning] [3348811] app/proxyman/outbound: failed to process outbound traffic > common/retry: all retry attempts failed\n"
	if l := lastLogLine(log); l != "app/proxyman/outbound: failed to process outbound traffic > common/retry: all retry attempts failed" {
		t.Fatalf("%q", l)
	}
	sb := "+0000 2026-11-24 18:00:00 ERROR [1234 0ms] outbound/tuic[t]: connection refused\n"
	if l := lastLogLine(sb); !strings.HasPrefix(l, "[1234 0ms] outbound/tuic") && !strings.HasPrefix(l, "outbound/tuic") {
		t.Fatalf("sing-box line: %q", l)
	}
	if l := lastLogLine("2026/11/24 18:00:00.1 [Warning] core: Xray 26.11.9 started\n"); l != "" {
		t.Fatalf("the start banner taken for an error: %q", l)
	}
	if lastLogLine("all good\n") != "" {
		t.Fatal("a quiet log gave a line")
	}
}
