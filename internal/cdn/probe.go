package cdn

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Target is one way to reach a CDN protocol: through the CDN (the domain or a
// clean address) or straight to the server.
type Target struct {
	Transport string // ws, httpupgrade, grpc or xhttp
	TLS       bool
	Dial      string // host:port to connect to
	Host      string // the name sent as SNI and Host
	Path      string // ws, httpupgrade and xhttp
	Service   string // gRPC service name
	Insecure  bool   // do not verify the certificate (the server's own one)
}

// Result is what a probe found. Code says how far it got: ok, dns, refused,
// timeout, connect, tls, cert, http (no HTTP answer), h2 (no HTTP/2 for gRPC)
// or status (an unexpected HTTP status, in Status, with Cloudflare's error
// code in CFCode when its page has one).
type Result struct {
	OK     bool   `json:"ok"`
	Code   string `json:"code"`
	Status int    `json:"status,omitempty"`
	CFCode int    `json:"cfCode,omitempty"`
	Server string `json:"server,omitempty"` // the Server header ("cloudflare" through Cloudflare)
	Colo   string `json:"colo,omitempty"`   // the Cloudflare data center that answered
	Ms     int64  `json:"ms"`               // until the answer (or the failure)
	Error  string `json:"error,omitempty"`
}

var cfCode = regexp.MustCompile(`error code: (\d{3,4})`)

// Probe opens the protocol's transport the way a client does, without sending
// any proxied traffic: a WebSocket or HTTPUpgrade handshake, an XHTTP
// download request, or an empty gRPC stream. Any answer from the protocol
// itself (101, 200, a gRPC response) is a success.
func Probe(ctx context.Context, t Target) (res Result) {
	start := time.Now()
	defer func() { res.Ms = time.Since(start).Milliseconds() }()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: 6 * time.Second}).DialContext(ctx, "tcp", t.Dial)
	if err != nil {
		return failure(dialCode(err), err)
	}
	defer conn.Close()
	if d, ok := ctx.Deadline(); ok {
		conn.SetDeadline(d)
	}
	var c net.Conn = conn
	if t.TLS {
		alpn := []string{"http/1.1"}
		if t.Transport == "grpc" {
			alpn = []string{"h2"}
		}
		tc := tls.Client(conn, &tls.Config{ServerName: t.Host, NextProtos: alpn, InsecureSkipVerify: t.Insecure, MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			var ce *tls.CertificateVerificationError
			if errors.As(err, &ce) {
				return failure("cert", err)
			}
			return failure(dialCode(err), err)
		}
		c = tc
	}
	if t.Transport == "grpc" {
		return probeGRPC(ctx, c, t)
	}
	path := "/" + strings.TrimPrefix(t.Path, "/")
	req := "GET " + path + " HTTP/1.1\r\nHost: " + t.Host + "\r\nUser-Agent: Mozilla/5.0\r\n"
	switch t.Transport {
	case "xhttp":
		// a download request of a new session, padded as Xray's clients do
		req = "GET " + strings.TrimSuffix(path, "/") + "/" + session() + "?x_padding=" + strings.Repeat("X", 200) + " HTTP/1.1\r\nHost: " + t.Host + "\r\nUser-Agent: Mozilla/5.0\r\n"
	default:
		key := make([]byte, 16)
		rand.Read(key)
		req += "Connection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key) + "\r\n"
	}
	if _, err := io.WriteString(c, req+"\r\n"); err != nil {
		return failure(dialCode(err), err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return failure(httpCode(err), err)
	}
	// the body is not closed: an XHTTP download streams until the deadline;
	// closing the connection ends it
	want := http.StatusSwitchingProtocols
	if t.Transport == "xhttp" {
		want = http.StatusOK
	}
	return answer(resp, want, "")
}

// probeGRPC sends an empty gRPC stream over HTTP/2: the server answers it at once.
func probeGRPC(ctx context.Context, c net.Conn, t Target) Result {
	if tc, ok := c.(*tls.Conn); !ok || tc.ConnectionState().NegotiatedProtocol != "h2" {
		return Result{Code: "h2", Error: "the server did not offer HTTP/2, which gRPC needs"}
	}
	used := false
	tr := &http.Transport{ForceAttemptHTTP2: true, DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
		if used {
			return nil, errors.New("one connection per probe")
		}
		used = true
		return c, nil
	}}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://"+t.Host+"/"+strings.Trim(t.Service, "/")+"/Tun", http.NoBody)
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	req.Header.Set("User-Agent", "grpc-go/1.70.0")
	resp, err := tr.RoundTrip(req)
	if err != nil {
		return failure(httpCode(err), err)
	}
	defer resp.Body.Close()
	return answer(resp, http.StatusOK, "application/grpc")
}

// answer judges the response: the wanted status (and content type) is the
// protocol itself; anything else is explained by its status and, from
// Cloudflare, the error code on its page.
func answer(resp *http.Response, want int, ctype string) Result {
	r := Result{Status: resp.StatusCode, Server: resp.Header.Get("Server")}
	if ray := resp.Header.Get("CF-Ray"); ray != "" {
		if i := strings.LastIndexByte(ray, '-'); i > 0 {
			r.Colo = ray[i+1:]
		}
	}
	if resp.StatusCode == want && (ctype == "" || strings.HasPrefix(resp.Header.Get("Content-Type"), ctype)) {
		r.OK, r.Code = true, "ok"
		return r
	}
	r.Code = "status"
	if resp.StatusCode != want {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if m := cfCode.FindSubmatch(body); m != nil {
			r.CFCode, _ = strconv.Atoi(string(m[1]))
		}
		r.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		if r.CFCode != 0 {
			r.Error += fmt.Sprintf(" (Cloudflare error %d)", r.CFCode)
		}
	} else {
		r.Error = "the answer was not gRPC (" + resp.Header.Get("Content-Type") + ")"
	}
	return r
}

func failure(code string, err error) Result {
	return Result{Code: code, Error: err.Error()}
}

func dialCode(err error) string {
	var dns *net.DNSError
	var ne net.Error
	switch {
	case errors.As(err, &dns):
		return "dns"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case strings.Contains(err.Error(), "refused"):
		return "refused"
	case strings.Contains(err.Error(), "tls:"), strings.Contains(err.Error(), "handshake"):
		return "tls"
	}
	return "connect"
}

func httpCode(err error) string {
	if c := dialCode(err); c != "connect" {
		return c
	}
	return "http"
}

// session is a random XHTTP session id (a UUID, as clients use).
func session() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
