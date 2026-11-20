package protocol

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/store"
)

// TrustTunnel (AdGuard): a VPN over HTTP/2 or HTTP/3 that looks like an HTTPS
// site. Its server, trusttunnel_endpoint, reads its users once at start and
// serves them all on one TCP + UDP port, so a server runs one TrustTunnel.
// TLS: the panel's real certificate when the hostname is the panel's domain,
// otherwise a certificate made for the hostname and carried in the link.

// TTProtocols are the transports the apps can use.
var TTProtocols = []string{"http2", "http3"}

func ttDefaults(params map[string]any, domain string) {
	host := strings.ToLower(strings.TrimSpace(str(params["sni"])))
	if host == "" {
		host = strings.ToLower(strings.TrimSpace(domain))
	}
	if host == "" || net.ParseIP(host) != nil {
		host = DefaultReality // TLS sends no server name for an IP address
	}
	params["sni"] = host
	if p := str(params["protocol"]); p != "http2" && p != "http3" {
		params["protocol"] = "http2"
	}
	if str(params["ttCertFor"]) != host || str(params["ttCert"]) == "" || str(params["ttKey"]) == "" {
		if c, k, err := ttSelfSigned(host); err == nil {
			params["ttCert"], params["ttKey"], params["ttCertFor"] = c, k, host
		}
	}
}

// ttSelfSigned makes a certificate for host (ECDSA P-256, 10 years), PEM.
func ttSelfSigned(host string) (certPEM, keyPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	// like the endpoint's own setup wizard (rcgen defaults): no basic
	// constraints or key usage, so the apps' OpenSSL accepts the pinned
	// certificate as its own issuer ("CA:FALSE" makes it refuse)
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return
	}
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb})), nil
}

// ttRealCert reports whether the inbound uses the panel's own certificate:
// its hostname is the panel's domain and that certificate is a real one.
func ttRealCert(in *store.Inbound, env Env) bool {
	return env.Domain != "" && strings.EqualFold(in.Str("sni"), env.Domain) && env.CertFile != "" && !env.SelfSigned
}

// TTServer is everything trusttunnel_endpoint needs: the files to write in
// dir and the main settings (which carry a digest of the other files, so any
// change starts it again).
type TTServer struct {
	Main  []byte
	Files map[string][]byte // name in dir → content
}

// TTHostsFile and the other names are the endpoint's files in its folder.
const (
	TTHostsFile = "tt-hosts.toml"
	ttCredsFile = "tt-credentials.toml"
	ttCertFile  = "tt-cert.pem"
	ttKeyFile   = "tt-key.pem"
)

func tomlStr(s string) string { b, _ := json.Marshal(s); return string(b) } // a JSON string is a TOML basic string

// TrustTunnelServer builds the endpoint's files for an inbound and its users.
// It returns nil when nobody may use it: without users the endpoint lets
// anyone in, so it must not run.
// forward, when set, is a local SOCKS5 address all traffic leaves through
// (Xray's routing inbound), so the server's routing rules apply to it too.
func TrustTunnelServer(in *store.Inbound, users []*store.User, env Env, dir string, metricsPort int, forward string) *TTServer {
	var creds strings.Builder
	n := 0
	for _, u := range users {
		if u.Name == "" || u.Password == "" {
			continue
		}
		fmt.Fprintf(&creds, "[[client]]\nusername = %s\npassword = %s\n\n", tomlStr(u.Name), tomlStr(u.Password))
		n++
	}
	if n == 0 {
		return nil
	}
	files := map[string][]byte{ttCredsFile: []byte(creds.String())}
	cert, key := in.Str("ttCert"), in.Str("ttKey")
	certPath, keyPath := dir+"/"+ttCertFile, dir+"/"+ttKeyFile
	if ttRealCert(in, env) {
		certPath, keyPath = env.CertFile, env.KeyFile
	} else {
		files[ttCertFile], files[ttKeyFile] = []byte(cert), []byte(key)
	}
	files[TTHostsFile] = []byte(fmt.Sprintf("[[main_hosts]]\nhostname = %s\ncert_chain_path = %s\nprivate_key_path = %s\n",
		tomlStr(in.Str("sni")), tomlStr(certPath), tomlStr(keyPath)))

	h := sha256.New()
	for _, name := range []string{ttCredsFile, TTHostsFile, ttCertFile, ttKeyFile} {
		h.Write(files[name])
	}
	if ttRealCert(in, env) {
		// a renewed certificate on disk needs a restart too
		h.Write([]byte(fileDigest(env.CertFile)))
	}
	listen := net.JoinHostPort(or(env.Listen, "0.0.0.0"), strconv.Itoa(in.Port))
	fwd := "[forward_protocol]\ndirect = {}"
	if forward != "" {
		fwd = "[forward_protocol.socks5]\naddress = " + tomlStr(forward) + "\nextended_auth = false"
	}
	main := fmt.Sprintf(`# written by Dunvex; users and certificate: %s
listen_address = %s
ipv6_available = false
allow_private_network_connections = false
credentials_file = %s

[listen_protocols.http1]
[listen_protocols.http2]
[listen_protocols.quic]

%s

[metrics]
address = %s
request_timeout_secs = 3
per_client_metrics = true
`, hex.EncodeToString(h.Sum(nil))[:16], tomlStr(listen), tomlStr(dir+"/"+ttCredsFile), fwd, tomlStr("127.0.0.1:"+strconv.Itoa(metricsPort)))
	return &TTServer{Main: []byte(main), Files: files}
}

// fileDigest identifies a file's content (empty when unreadable).
func fileDigest(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// ttLink is the tt:// deep link the TrustTunnel apps import (format version 2).
func ttLink(in *store.Inbound, u *store.User, env Env, remark string) string {
	var b []byte
	tlv := func(tag byte, v []byte) {
		b = append(b, tag)
		b = quicVarint(b, uint64(len(v)))
		b = append(b, v...)
	}
	tlv(0x00, quicVarint(nil, 2)) // version
	tlv(0x01, []byte(in.Str("sni")))
	tlv(0x02, []byte(hostPort(LinkHost(in, env), in.Port)))
	tlv(0x04, []byte{0}) // no IPv6 through the tunnel (the endpoint routes IPv4)
	tlv(0x05, []byte(u.Name))
	tlv(0x06, []byte(u.Password))
	if !ttRealCert(in, env) {
		if blk, _ := pem.Decode([]byte(in.Str("ttCert"))); blk != nil {
			tlv(0x08, blk.Bytes)
		}
	}
	proto := byte(1)
	if in.Str("protocol") == "http3" {
		proto = 2
	}
	tlv(0x09, []byte{proto})
	if in.Bool("antiDpi") {
		tlv(0x0A, []byte{1})
	}
	tlv(0x0C, []byte(remark))
	var dns []byte
	for _, d := range strings.Split(in.Str("dns"), ",") {
		if d = strings.TrimSpace(d); d != "" {
			dns = quicVarint(dns, uint64(len(d)))
			dns = append(dns, d...)
		}
	}
	if len(dns) > 0 {
		tlv(0x0D, dns)
	}
	return "tt://?" + base64.RawURLEncoding.EncodeToString(b)
}

// quicVarint appends v in the variable-length form of RFC 9000 §16.
func quicVarint(b []byte, v uint64) []byte {
	switch {
	case v < 1<<6:
		return append(b, byte(v))
	case v < 1<<14:
		return append(b, byte(v>>8)|0x40, byte(v))
	case v < 1<<30:
		return append(b, byte(v>>24)|0x80, byte(v>>16), byte(v>>8), byte(v))
	}
	return append(b, byte(v>>56)|0xC0, byte(v>>48), byte(v>>40), byte(v>>32), byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
