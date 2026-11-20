package relay

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"io"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func selfCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "dunvex-node"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}, hex.EncodeToString(sum[:])
}

func waitLinks(t *testing.T, r *Relay, want int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if s := r.TunnelStat(); s != nil && s.Links == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("links: %+v, want %d", r.TunnelStat(), want)
}

func echoVia(t *testing.T, port int, msg string) string {
	t.Helper()
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return "dial: " + err.Error()
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte(msg))
	c.(*net.TCPConn).CloseWrite()
	b, _ := io.ReadAll(c)
	return string(b)
}

func TestReverseTunnel(t *testing.T) {
	target := echoServer(t)
	_, tport, _ := net.SplitHostPort(target)
	tp, _ := strconv.Atoi(tport)
	cert, fp := selfCert(t)
	r := New(nil)
	defer r.Close()
	r.SetCert(cert)
	tunnelPort, listen := freePort(t), freePort(t)
	r.ApplyTunnel(&TunnelServer{Port: tunnelPort, Secret: "s3cret-s3cret-s3cret"})
	r.Apply([]Forward{{Name: "Reality + Hysteria2", Listen: listen, Network: "tcp+udp", Target: "127.0.0.1:" + tport, Reverse: true}})
	if s := r.TunnelStat(); s == nil || !s.Listening || s.Links != 0 {
		t.Fatalf("tunnel: %+v", s)
	}
	// no server yet: the user's connection is closed at once
	if got := echoVia(t, listen, "early"); got != "" {
		t.Errorf("before the tunnel: %q", got)
	}

	tun := NewTunnels("")
	defer tun.Close()
	addr := "127.0.0.1:" + strconv.Itoa(tunnelPort)
	tun.Apply([]TunnelClient{{Name: "Tehran", Addr: addr, Fingerprint: fp, Secret: "s3cret-s3cret-s3cret", Ports: []int{tp}}})
	waitLinks(t, r, Links)
	if s := r.TunnelStat(); s.Peer != "127.0.0.1" || s.Since == 0 {
		t.Errorf("tunnel peer: %+v", s)
	}
	if got := echoVia(t, listen, strings.Repeat("y", 50000)); got != "echo:"+strings.Repeat("y", 50000) {
		t.Errorf("TCP through the tunnel: %d bytes", len(got))
	}
	u, _ := net.Dial("udp", "127.0.0.1:"+strconv.Itoa(listen))
	u.SetDeadline(time.Now().Add(3 * time.Second))
	u.Write([]byte("quic"))
	buf := make([]byte, 64)
	n, err := u.Read(buf)
	u.Close()
	if err != nil || string(buf[:n]) != "pong:quic" {
		t.Errorf("UDP through the tunnel: %q %v", buf[:n], err)
	}
	if cs := tun.Stats(); len(cs) != 1 || cs[0].Links != Links || cs[0].Streams < 2 {
		t.Errorf("client: %+v", cs)
	}

	// the relay comes back after a restart; the server's links follow
	r.ApplyTunnel(nil)
	r.ApplyTunnel(&TunnelServer{Port: tunnelPort, Secret: "s3cret-s3cret-s3cret"})
	waitLinksFor(t, r, Links, 8*time.Second)
	if got := echoVia(t, listen, "again"); got != "echo:again" {
		t.Errorf("after the relay restarted: %q", got)
	}
}

func waitLinksFor(t *testing.T, r *Relay, want int, d time.Duration) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if s := r.TunnelStat(); s != nil && s.Links == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("links: %+v, want %d", r.TunnelStat(), want)
}

func TestTunnelRefusals(t *testing.T) {
	target := echoServer(t)
	_, tport, _ := net.SplitHostPort(target)
	cert, fp := selfCert(t)
	_, otherFP := selfCert(t)
	r := New(nil)
	defer r.Close()
	r.SetCert(cert)
	tunnelPort, listen := freePort(t), freePort(t)
	r.ApplyTunnel(&TunnelServer{Port: tunnelPort, Secret: "right-secret-123456"})
	r.Apply([]Forward{{Name: "x", Listen: listen, Network: "tcp", Target: "127.0.0.1:" + tport, Reverse: true}})
	addr := "127.0.0.1:" + strconv.Itoa(tunnelPort)

	for _, c := range []struct {
		name string
		cl   TunnelClient
		err  string
	}{
		{"a wrong secret", TunnelClient{Addr: addr, Fingerprint: fp, Secret: "wrong-secret-123456"}, "refused"},
		{"another relay's certificate", TunnelClient{Addr: addr, Fingerprint: otherFP, Secret: "right-secret-123456"}, "does not match"},
	} {
		tun := NewTunnels("")
		tun.Apply([]TunnelClient{c.cl})
		var st []ClientStat
		for i := 0; i < 60; i++ {
			if st = tun.Stats(); len(st) == 1 && st[0].Error != "" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(st) != 1 || st[0].Links != 0 || !strings.Contains(st[0].Error, c.err) {
			t.Errorf("%s: %+v", c.name, st)
		}
		if s := r.TunnelStat(); s.Links != 0 {
			t.Errorf("%s: the relay took a link", c.name)
		}
		tun.Close()
	}

	// the server reaches only the ports the panel allowed
	tun := NewTunnels("")
	defer tun.Close()
	tun.Apply([]TunnelClient{{Addr: addr, Fingerprint: fp, Secret: "right-secret-123456", Ports: []int{9}}})
	waitLinks(t, r, Links)
	if got := echoVia(t, listen, "sneaky"); got != "" {
		t.Errorf("a port the server did not allow answered: %q", got)
	}
}
