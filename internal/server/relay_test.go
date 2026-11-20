package server

import (
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/node"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/relay"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// standIn listens like a core on a port: TCP echoes, UDP answers "pong:".
func standIn(t *testing.T, tcp, udp int) {
	t.Helper()
	if tcp > 0 {
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(tcp))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go func() { defer c.Close(); b, _ := io.ReadAll(c); c.Write(append([]byte("echo:"), b...)) }()
			}
		}()
	}
	if udp > 0 {
		u, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: udp})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { u.Close() })
		go func() {
			buf := make([]byte, 2048)
			for {
				n, from, err := u.ReadFromUDP(buf)
				if err != nil {
					return
				}
				u.WriteToUDP(append([]byte("pong:"), buf[:n]...), from)
			}
		}()
	}
}

func freeBoth(t *testing.T, from int) int {
	for p := from; p < from+500; p++ {
		l, err := net.Listen("tcp", ":"+strconv.Itoa(p))
		if err != nil {
			continue
		}
		u, err := net.ListenUDP("udp", &net.UDPAddr{Port: p})
		l.Close()
		if err != nil {
			continue
		}
		u.Close()
		if l2, err := net.Listen("tcp", ":"+strconv.Itoa(p+7)); err == nil {
			l2.Close()
			if u2, err := net.ListenUDP("udp", &net.UDPAddr{Port: p + 7}); err == nil {
				u2.Close()
				return p
			}
		}
	}
	t.Fatal("no free port")
	return 0
}

func TestPanelWithRelay(t *testing.T) {
	// the relay: a node agent that forwards
	ndir := t.TempDir()
	nst, err := store.Open(filepath.Join(ndir, "node.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer nst.Close()
	nenv := protocol.Env{Host: "127.0.0.1"}
	agent := &node.Agent{Version: "test", St: nst, Sys: sysinfo.NewSampler(), Env: func() protocol.Env { return nenv }}
	agent.Mgr = engine.NewManager(nst, filepath.Join(ndir, "bin"), filepath.Join(ndir, "run"), agent.Env)
	ncert, _, _ := node.LoadOrCreateCert(filepath.Join(ndir, "certs"), "agent", "dunvex-node")
	nts := httptest.NewUnstartedServer(agent.Handler())
	nts.TLS = agent.TLSConfig(ncert)
	nts.StartTLS()
	defer nts.Close()
	nport := nts.Listener.Addr().(*net.TCPAddr).Port
	agent.Relay = relay.New(map[int]string{nport: "the Dunvex agent"})
	defer agent.Relay.Close()
	tok, err := node.Init(nst, filepath.Join(ndir, "certs"), "127.0.0.1", nport, false)
	if err != nil {
		t.Fatal(err)
	}

	// the panel, whose protocols the relay carries
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	st.SetSetting("host", "127.0.0.1")
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	reality, hy := freeBoth(t, 31500), 0
	hy = freeBoth(t, reality+20)
	mk := func(preset string, port int, extra map[string]any, enabled bool) {
		p := protocol.Get(preset)
		if err := st.CreateInbound(&store.Inbound{Preset: preset, Port: port, Enabled: enabled, Params: protocol.Defaults(p, extra, "")}); err != nil {
			t.Fatal(err)
		}
	}
	mk("vless-reality-vision", reality, nil, true)
	mk("hysteria2", hy, nil, true)
	mk("naive", hy+3, nil, true)                                                 // dialled by its domain: not relayed
	mk("vless-ws-tls", hy+4, map[string]any{"address": "cdn.example.com"}, true) // behind a CDN: not relayed
	mk("vless-reality-vision", hy+5, nil, false)                                 // off
	st.CreateUser(&store.User{Name: "sara", UUID: util.UUID(), Password: "sara-pass-0123456789", SubToken: "sara-token-0123456789", Enabled: true, ResetPeriod: "none"})
	standIn(t, reality, hy)

	// a relay must point at a server that runs protocols
	if code, _ := owner.do("POST", "/nodes", map[string]any{"name": "Tehran", "token": tok.String(), "role": "relay", "relayTarget": 99}); code != 400 {
		t.Fatal("a relay to a missing server was accepted")
	}
	code, out := owner.do("POST", "/nodes", map[string]any{"name": "Tehran", "token": tok.String(), "role": "relay", "relayTarget": 0, "relayShift": 7})
	if code != 200 || out["paired"] != true || out["role"] != "relay" {
		t.Fatalf("add relay: %d %v", code, out)
	}
	relayID := int64(out["id"].(float64))
	if code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "vless-reality-vision", "port": 8443, "nodeId": relayID}); code != 400 || !strings.Contains(out["error"].(string), "relay") {
		t.Errorf("a protocol on the relay: %d %v", code, out)
	}

	// the relay listens on each port +7 and carries TCP and UDP to the panel's server
	fw := agent.Relay.Stats()
	if len(fw) != 2 || fw[0].Listen != reality+7 || fw[0].Network != "tcp" || fw[0].Target != "127.0.0.1:"+strconv.Itoa(reality) ||
		fw[1].Listen != hy+7 || fw[1].Network != "udp" || !fw[0].Listening || !fw[1].Listening {
		t.Fatalf("forwards: %+v", fw)
	}
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(reality+7))
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("hello"))
	c.(*net.TCPConn).CloseWrite()
	got, _ := io.ReadAll(c)
	c.Close()
	if string(got) != "echo:hello" {
		t.Errorf("TCP through the relay: %q", got)
	}
	u, _ := net.Dial("udp", "127.0.0.1:"+strconv.Itoa(hy+7))
	u.SetDeadline(time.Now().Add(3 * time.Second))
	u.Write([]byte("quic"))
	buf := make([]byte, 64)
	n, err := u.Read(buf)
	u.Close()
	if err != nil || string(buf[:n]) != "pong:quic" {
		t.Errorf("UDP through the relay: %q %v", buf[:n], err)
	}
	// the panel shows the relay's ports
	srv.hub().sync(mustNode(t, st, relayID))
	code, list := owner.do("GET", "/nodes", nil)
	r0, _ := list["list"].([]any)[0].(map[string]any)
	if code != 200 || r0["status"] == nil || len(r0["status"].(map[string]any)["forwards"].([]any)) != 2 {
		t.Errorf("node list: %v", list)
	}

	// subscriptions: each relayed protocol also through the relay
	links := func() string {
		res, err := http.Get(ts.URL + "/sub/sara-token-0123456789")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		d, _ := base64.StdEncoding.DecodeString(string(b))
		return string(d)
	}
	all := links()
	for _, want := range []string{"@127.0.0.1:" + strconv.Itoa(reality) + "?", "@127.0.0.1:" + strconv.Itoa(reality+7) + "?",
		"@127.0.0.1:" + strconv.Itoa(hy+7) + "/", "Tehran"} {
		if !strings.Contains(all, want) {
			t.Errorf("links lack %q:\n%s", want, all)
		}
	}
	if strings.Count(all, "Tehran") != 2 {
		t.Errorf("relayed links: %d\n%s", strings.Count(all, "Tehran"), all)
	}
	// relay only: the direct configs of relayed protocols go away
	if code, out := owner.do("PUT", "/nodes/"+itoa(relayID), map[string]any{"relayOnly": true}); code != 200 || out["relayOnly"] != true {
		t.Fatalf("relay only: %d %v", code, out)
	}
	only := links()
	if strings.Contains(only, "@127.0.0.1:"+strconv.Itoa(reality)+"?") || !strings.Contains(only, "@127.0.0.1:"+strconv.Itoa(reality+7)+"?") ||
		!strings.Contains(only, ":"+strconv.Itoa(hy+3)) {
		t.Errorf("relay-only links:\n%s", only)
	}
	// a relay cannot point at another relay
	if code, _ := owner.do("PUT", "/nodes/"+itoa(relayID), map[string]any{"relayTarget": relayID}); code != 400 {
		t.Error("a relay to itself was accepted")
	}
}

func mustNode(t *testing.T, st *store.Store, id int64) *store.Node {
	n, err := st.NodeByID(id)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
