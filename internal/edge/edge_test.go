package edge

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// clientHello captures the first TLS record a Go client sends for this server name.
func clientHello(t *testing.T, name string) []byte {
	a, b := net.Pipe()
	go func() {
		c := tls.Client(a, &tls.Config{ServerName: name, InsecureSkipVerify: true})
		c.SetDeadline(time.Now().Add(2 * time.Second))
		c.Handshake()
		a.Close()
	}()
	b.SetReadDeadline(time.Now().Add(2 * time.Second))
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(b, hdr); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, int(hdr[3])<<8|int(hdr[4]))
	if _, err := io.ReadFull(b, body); err != nil {
		t.Fatal(err)
	}
	b.Close()
	return append(hdr, body...)
}

func TestServerName(t *testing.T) {
	rec := clientHello(t, "www.microsoft.com")
	if got := ServerName(rec[5:]); got != "www.microsoft.com" {
		t.Fatalf("sni: %q", got)
	}
	if ServerName([]byte{1, 2, 3}) != "" || ServerName(nil) != "" {
		t.Fatal("garbage should give no name")
	}
}

func TestRouting(t *testing.T) {
	r, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go r.Serve()

	// a protocol backend that records what it receives
	back, _ := net.Listen("tcp", "127.0.0.1:0")
	defer back.Close()
	got := make(chan string, 1)
	go func() {
		c, err := back.Accept()
		if err != nil {
			return
		}
		br := bufio.NewReader(c)
		line, _ := br.ReadString('\n')
		first, _ := br.Peek(1)
		got <- line + string(first)
		c.Close()
	}()
	r.SetRoutes(map[string]Target{"WWW.Microsoft.com": {Addr: back.Addr().String(), ProxyHeader: true}})

	// the panel: plain HTTP and HTTPS on the same in-memory listener
	panel := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "panel") })}
	go panel.Serve(r.PanelListener())
	defer panel.Close()

	// 1) a Reality client (SNI of the camouflage site) goes to the protocol, with a PROXY header
	c, err := net.Dial("tcp", r.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Write(clientHello(t, "www.microsoft.com"))
	select {
	case s := <-got:
		if !strings.HasPrefix(s, "PROXY TCP4 127.0.0.1 127.0.0.1 ") || !strings.HasSuffix(s, "\x16") {
			t.Fatalf("backend got %q", s)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the protocol backend got nothing")
	}
	c.Close()

	// 2) plain HTTP goes to the panel
	res, err := http.Get("http://" + r.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "panel" {
		t.Fatalf("http: %q", body)
	}
}

func TestPanelTLS(t *testing.T) {
	r, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go r.Serve()
	r.SetRoutes(map[string]Target{"www.microsoft.com": {Addr: "127.0.0.1:1"}})

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "panel.test"}, DNSNames: []string{"panel.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	panel := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "secure panel") }),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
	go panel.ServeTLS(r.PanelListener(), "", "")
	defer panel.Close()

	cl := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{ServerName: "panel.test", InsecureSkipVerify: true}}, Timeout: 5 * time.Second}
	res, err := cl.Get("https://" + r.Addr().String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "secure panel" {
		t.Fatalf("https: %q", body)
	}
}

// sing-box routes get no PROXY header; the router remembers who is behind the loopback port.
func TestRawRouteAndClientOf(t *testing.T) {
	r, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go r.Serve()
	back, _ := net.Listen("tcp", "127.0.0.1:0")
	defer back.Close()
	type seen struct {
		first byte
		from  string
	}
	got := make(chan seen, 1)
	go func() {
		c, err := back.Accept()
		if err != nil {
			return
		}
		b := make([]byte, 1)
		io.ReadFull(c, b)
		got <- seen{b[0], c.RemoteAddr().String()}
		time.Sleep(200 * time.Millisecond)
		c.Close()
	}()
	r.SetRoutes(map[string]Target{"dl.google.com": {Addr: back.Addr().String()}})
	c, err := net.Dial("tcp", r.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write(clientHello(t, "dl.google.com"))
	select {
	case s := <-got:
		if s.first != 0x16 {
			t.Fatalf("backend should get the ClientHello first, got %#x", s.first)
		}
		if ip := r.ClientOf(s.from); ip != "127.0.0.1" {
			t.Fatalf("ClientOf(%s) = %q", s.from, ip)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the backend got nothing")
	}
	if r.ClientOf("127.0.0.1:1") != "" {
		t.Fatal("unknown address should give no client")
	}
}

// TLS for an unknown name goes to the fallback (the camouflage Reality inbound)
// while the panel speaks plain HTTP, and to the panel once it speaks HTTPS.
func TestFallback(t *testing.T) {
	for _, panelTLS := range []bool{false, true} {
		r, err := Listen("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		r.PanelTLS = panelTLS
		go r.Serve()
		back, _ := net.Listen("tcp", "127.0.0.1:0")
		hit := make(chan bool, 1)
		go func() {
			c, err := back.Accept()
			if err == nil {
				hit <- true
				c.Close()
			}
		}()
		r.SetRoutes(map[string]Target{"www.microsoft.com": {Addr: "127.0.0.1:1"}, Fallback: {Addr: back.Addr().String()}})
		panelGot := make(chan bool, 1)
		go func() {
			c, err := r.PanelListener().Accept()
			if err == nil {
				panelGot <- true
				c.Close()
			}
		}()
		c, err := net.Dial("tcp", r.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		c.Write(clientHello(t, "random-probe.example"))
		select {
		case <-hit:
			if panelTLS {
				t.Error("with an HTTPS panel, unknown names belong to the panel")
			}
		case <-panelGot:
			if !panelTLS {
				t.Error("with a plain-HTTP panel, unknown TLS should reach the fallback")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("nobody got the connection")
		}
		c.Close()
		back.Close()
		r.Close()
	}
}

// fragmentHello splits a ClientHello record into handshake records of size
// bytes each, as Xray's TLS fragment ("tlshello") sends it.
func fragmentHello(rec []byte, size int) []byte {
	body := rec[5:]
	var out []byte
	for len(body) > 0 {
		n := min(size, len(body))
		out = append(out, 0x16, rec[1], rec[2], byte(n>>8), byte(n))
		out = append(out, body[:n]...)
		body = body[n:]
	}
	return out
}

// A ClientHello in many small records (TLS fragment on the client) still
// finds its protocol by name, and reaches it byte for byte.
func TestFragmentedHello(t *testing.T) {
	r, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	go r.Serve()
	type delivery struct {
		to string
		b  []byte
	}
	got := make(chan delivery, 2)
	backend := func(name string) string {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		go func() {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.SetReadDeadline(time.Now().Add(3 * time.Second))
			b, _ := io.ReadAll(c)
			got <- delivery{name, b}
			c.Close()
			l.Close()
		}()
		return l.Addr().String()
	}
	right, wrong := backend("right"), backend("wrong")
	r.SetRoutes(map[string]Target{"www.microsoft.com": {Addr: right}, "www.speedtest.net": {Addr: wrong}, Fallback: {Addr: wrong}})

	for _, size := range []int{7, 100, 150} {
		frag := fragmentHello(clientHello(t, "www.microsoft.com"), size)
		br := bufio.NewReaderSize(strings.NewReader(string(frag)), 16<<10)
		if sni := peekServerName(br); sni != "www.microsoft.com" {
			t.Fatalf("pieces of %d bytes: %q", size, sni)
		}
		if b, _ := br.Peek(len(frag)); string(b) != string(frag) {
			t.Fatal("bytes were consumed")
		}
	}

	// through the router, the pieces sent with pauses between them
	frag := fragmentHello(clientHello(t, "www.microsoft.com"), 100)
	c, err := net.Dial("tcp", r.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(frag); i += 105 {
		c.Write(frag[i:min(i+105, len(frag))])
		time.Sleep(5 * time.Millisecond)
	}
	c.(*net.TCPConn).CloseWrite()
	select {
	case d := <-got:
		if d.to != "right" || string(d.b) != string(frag) {
			t.Fatalf("the %s backend got %d bytes, want the right one with %d", d.to, len(d.b), len(frag))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing reached a backend")
	}
	c.Close()

	// a broken second record: no name, so the fallback answers
	bad := fragmentHello(clientHello(t, "www.microsoft.com"), 100)
	bad[105] = 0x17
	if sni := peekServerName(bufio.NewReaderSize(strings.NewReader(string(bad)), 16<<10)); sni != "" {
		t.Fatalf("a broken hello gave %q", sni)
	}
}
