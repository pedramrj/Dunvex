package sshproxy

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestThroughRealXray puts the SSH server in front of a real Xray SOCKS
// inbound, set up as Dunvex sets it up (protocol.SSHSocks), and sends TCP and
// UDP over TCP through it to local echo servers. Needs DUNVEX_TEST_BIN (xray).
func TestThroughRealXray(t *testing.T) {
	src := os.Getenv("DUNVEX_TEST_BIN")
	if src == "" {
		t.Skip("DUNVEX_TEST_BIN not set")
	}
	exe := "xray"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	socksPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	dir := t.TempDir()
	cfg, _ := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"tag": "in-1", "listen": "127.0.0.1", "port": socksPort, "protocol": "socks",
			"settings": map[string]any{"auth": "noauth", "udp": true, "ip": "127.0.0.1"},
			"sniffing": map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}}},
		"outbounds": []any{map[string]any{"protocol": "freedom", "tag": "direct"}},
	})
	cf := filepath.Join(dir, "xray.json")
	os.WriteFile(cf, cfg, 0o600)
	cmd := exec.Command(filepath.Join(src, exe), "run", "-c", cf)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort)); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// the echo servers are on loopback, which users may not reach: allow it here
	forbidden = func(string) bool { return false }
	defer func() { forbidden = Forbidden }()

	s, hk := startServer(t, fmt.Sprintf("127.0.0.1:%d", socksPort), map[string]string{"alice": "pw"})
	cl, err := dial(s.Addr().String(), "alice", "pw", hk)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	c, err := cl.Dial("tcp", echoTCP(t))
	if err != nil {
		t.Fatalf("TCP through Xray: %v | %s", err, out.String())
	}
	roundTrip(t, c, strings.Repeat("tcp through xray ", 5000))
	c.Close()

	uh, up, _ := net.SplitHostPort(echoUDP(t))
	var port int
	fmt.Sscan(up, &port)
	u, err := cl.Dial("tcp", uotMagic+":0")
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	u.SetDeadline(time.Now().Add(10 * time.Second))
	req, _ := socksAddr([]byte{0}, uh, port)
	u.Write(req)
	for i := 0; i < 3; i++ {
		u.Write(uotFrame(uh, port, []byte(fmt.Sprintf("datagram %d", i))))
	}
	br := bufio.NewReader(u)
	for i := 0; i < 3; i++ {
		h, p, err := readUoTAddr(br)
		if err != nil {
			t.Fatalf("UDP through Xray: %v | %s", err, out.String())
		}
		var l [2]byte
		io.ReadFull(br, l[:])
		body := make([]byte, binary.BigEndian.Uint16(l[:]))
		io.ReadFull(br, body)
		if h != uh || p != port || !strings.HasPrefix(string(body), "echo:datagram ") {
			t.Fatalf("answer %q from %s:%d", body, h, p)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if tr := s.Take()["alice"]; tr.Up < 85000 || tr.Down < 85000 {
		t.Errorf("counted %+v", tr)
	}
}
