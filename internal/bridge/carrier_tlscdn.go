package bridge

// The TLS-over-CDN carrier: a WebSocket inside TLS, optionally with Encrypted
// Client Hello so the filter sees only the CDN's public name. The relay abroad
// of the border... no — in the Bridge the relay inside Iran is the listener and
// the target abroad dials it, here over wss to the relay's own domain (which
// may sit behind a CDN); ECH hides the real name. Research found CDN-with-ECH
// reached tens of MB/s on some routes while plain TLS died after the handshake
// (net4people #676), but it was throttled to a crawl on at least one phone ISP,
// so it is one carrier among several, not the whole plan.
//
// Frames ride as binary WebSocket messages; the stream is reliable, so kcp
// above sees no loss on this path.

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/websocket"
)

// TLSCDNDialer is the target-abroad side: it dials wss to the relay.
type TLSCDNDialer struct {
	url        string
	origin     string
	serverName string
	echList    []byte // decoded ECH config list ("" = none)
	pinSHA     string // hex SHA-256 of the relay cert to pin ("" = verify with system roots, for a real CDN)
	recv       func(frame []byte, src any)
	ws         atomic.Pointer[websocket.Conn]
	up         atomic.Bool
	closed     chan struct{}
	once       sync.Once
}

// NewTLSCDNClient dials wssURL (wss://host/path). echB64 is the CDN's ECH
// config list from its DNS HTTPS record (""=none); serverName is the real
// (inner) name; pinSHA pins the relay's certificate when there is no real CDN
// in front (the namespace test), else leave it "" to verify normally.
func NewTLSCDNClient(wssURL, serverName, echB64, pinSHA string) (*TLSCDNDialer, error) {
	u := wssURL
	host := serverName
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if j := strings.IndexAny(rest, "/"); j >= 0 {
			host = rest[:j]
		} else {
			host = rest
		}
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
	}
	d := &TLSCDNDialer{url: u, origin: "https://" + host, serverName: serverName, pinSHA: strings.ToLower(pinSHA), closed: make(chan struct{})}
	if d.serverName == "" {
		d.serverName = host
	}
	if echB64 != "" {
		raw, err := base64.StdEncoding.DecodeString(echB64)
		if err != nil {
			return nil, errors.New("bridge: bad ECH config for the CDN carrier")
		}
		d.echList = raw
	}
	return d, nil
}

func (d *TLSCDNDialer) Name() string { return "tls-cdn" }
func (d *TLSCDNDialer) Up() bool     { return d.up.Load() }

// SetPeer is a no-op: the client dialled a fixed, certificate-pinned server.
func (d *TLSCDNDialer) SetPeer(any) {}

func (d *TLSCDNDialer) Start(recv func(frame []byte, src any)) error {
	d.recv = recv
	go d.keep()
	return nil
}

func (d *TLSCDNDialer) tlsConfig() *tls.Config {
	c := &tls.Config{ServerName: d.serverName, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	if len(d.echList) > 0 {
		c.EncryptedClientHelloConfigList = d.echList
	}
	if d.pinSHA != "" {
		c.InsecureSkipVerify = true
		c.VerifyPeerCertificate = func(certs [][]byte, _ [][]*x509.Certificate) error {
			if len(certs) == 0 {
				return errors.New("the relay sent no certificate")
			}
			sum := sha256.Sum256(certs[0])
			if !strings.EqualFold(hex.EncodeToString(sum[:]), d.pinSHA) {
				return errors.New("the relay's certificate does not match")
			}
			return nil
		}
	}
	return c
}

func (d *TLSCDNDialer) keep() {
	wait := time.Second
	for {
		select {
		case <-d.closed:
			return
		default:
		}
		cfg, err := websocket.NewConfig(d.url, d.origin)
		if err == nil {
			cfg.TlsConfig = d.tlsConfig()
			var ws *websocket.Conn
			ws, err = websocket.DialConfig(cfg)
			if err == nil {
				d.ws.Store(ws)
				d.up.Store(true)
				wait = time.Second
				d.readLoop(ws)
				d.up.Store(false)
				d.ws.Store(nil)
			}
		}
		select {
		case <-d.closed:
			return
		case <-time.After(wait):
		}
		if wait < 20*time.Second {
			wait *= 2
		}
	}
}

func (d *TLSCDNDialer) readLoop(ws *websocket.Conn) {
	for {
		var b []byte
		if err := websocket.Message.Receive(ws, &b); err != nil {
			ws.Close()
			return
		}
		if d.recv != nil && len(b) > 0 {
			d.recv(b, ws)
		}
	}
}

func (d *TLSCDNDialer) Send(frame []byte) error {
	ws := d.ws.Load()
	if ws == nil {
		return net.ErrClosed
	}
	return websocket.Message.Send(ws, frame)
}

func (d *TLSCDNDialer) Close() error {
	d.once.Do(func() { d.up.Store(false); close(d.closed) })
	if ws := d.ws.Load(); ws != nil {
		ws.Close()
	}
	return nil
}

// TLSCDNListener is the Iran-relay side: an HTTPS server with a WebSocket
// endpoint the target dials.
type TLSCDNListener struct {
	bind string
	path string
	cert tls.Certificate
	recv func(frame []byte, src any)
	srv  *http.Server
	ln   net.Listener
	ws   atomic.Pointer[websocket.Conn] // the active link, set only once a frame on it authenticates
	up   atomic.Bool
	once sync.Once
}

// NewTLSCDNServer binds an HTTPS/WSS listener on bind at path, serving cert.
func NewTLSCDNServer(bind, path string, cert tls.Certificate) *TLSCDNListener {
	if path == "" {
		path = "/dvxb"
	}
	return &TLSCDNListener{bind: bind, path: path, cert: cert}
}

func (l *TLSCDNListener) Name() string { return "tls-cdn" }
func (l *TLSCDNListener) Up() bool     { return l.up.Load() }

// SetPeer makes a websocket the active link once a frame on it authenticates.
func (l *TLSCDNListener) SetPeer(src any) {
	if ws, ok := src.(*websocket.Conn); ok && ws != nil {
		l.ws.Store(ws)
	}
}

const cdnAuthTimeout = 10 * time.Second

func (l *TLSCDNListener) Start(recv func(frame []byte, src any)) error {
	l.recv = recv
	ln, err := tls.Listen("tcp", l.bind, &tls.Config{Certificates: []tls.Certificate{l.cert}, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	if err != nil {
		return err
	}
	l.ln = ln
	mux := http.NewServeMux()
	mux.Handle(l.path, websocket.Handler(func(ws *websocket.Conn) {
		// A new connection is NOT the active link until a frame on it opens under
		// the session key (SetPeer). If none does within the timeout, drop it
		// silently — a prober that merely completes TLS gets nothing.
		done := make(chan struct{})
		defer close(done)
		go func() {
			select {
			case <-time.After(cdnAuthTimeout):
				if l.ws.Load() != ws {
					ws.Close()
				}
			case <-done:
			}
		}()
		for {
			var b []byte
			if err := websocket.Message.Receive(ws, &b); err != nil {
				return
			}
			if l.recv != nil && len(b) > 0 {
				l.recv(b, ws)
			}
		}
	}))
	// Any other path answers like an ordinary nginx site, not Go's default 404.
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { writeNginx404(w) })
	l.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 15 * time.Second}
	l.up.Store(true)
	go l.srv.Serve(ln)
	return nil
}

// writeNginx404 mimics nginx's default 404, so a wrong path looks like a normal
// web server rather than a Go service.
func writeNginx404(w http.ResponseWriter) {
	w.Header().Set("Server", "nginx")
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusNotFound)
	io.WriteString(w, "<html>\r\n<head><title>404 Not Found</title></head>\r\n<body>\r\n<center><h1>404 Not Found</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n")
}

func (l *TLSCDNListener) Send(frame []byte) error {
	ws := l.ws.Load()
	if ws == nil {
		return errNoPeer
	}
	return websocket.Message.Send(ws, frame)
}

func (l *TLSCDNListener) Close() error {
	l.once.Do(func() { l.up.Store(false) })
	if l.srv != nil {
		l.srv.Close()
	}
	if l.ln != nil {
		l.ln.Close()
	}
	return nil
}
