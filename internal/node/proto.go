// Package node lets one Dunvex panel run protocols on other servers.
//
// A node is the same dunvex binary started as `dunvex node serve`. It keeps a
// small database of its own and runs the usual engine (Xray and sing-box); the
// panel pushes it the protocols that belong to it and the users who may
// connect, and pulls back traffic and device addresses.
//
// The two talk over HTTPS with mutual TLS and both certificates pinned: the
// node's token (made at install) carries its address, its certificate's
// fingerprint and a one-time secret. The first call from the panel presents
// the secret together with the panel's client certificate; from then on the
// node answers only that certificate.
package node

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/relay"
	"github.com/pedramrj/dunvex/internal/store"
)

// DefaultPort is where a node's agent listens.
const DefaultPort = 62050

const tokenPrefix = "dvxnode1."

// Token is what the panel needs to reach and pair with a node.
type Token struct {
	Addr        string `json:"a"` // host:port of the agent
	Fingerprint string `json:"f"` // SHA-256 of the agent certificate, hex
	Secret      string `json:"s"` // one-time pairing secret
}

// String encodes the token for copying.
func (t Token) String() string {
	b, _ := json.Marshal(t)
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// ParseToken reads a token pasted into the panel.
func ParseToken(s string) (Token, error) {
	var t Token
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, tokenPrefix) {
		return t, errors.New("this is not a Dunvex node token (it starts with dvxnode1.)")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, tokenPrefix))
	if err != nil || json.Unmarshal(raw, &t) != nil {
		return t, errors.New("the node token is damaged; copy it again")
	}
	host, port, err := net.SplitHostPort(t.Addr)
	if err != nil || host == "" {
		return t, errors.New("the node token has no address")
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return t, errors.New("the node token has a bad port")
	}
	if b, err := hex.DecodeString(t.Fingerprint); err != nil || len(b) != sha256.Size {
		return t, errors.New("the node token has a bad fingerprint")
	}
	if len(t.Secret) < 16 {
		return t, errors.New("the node token has no pairing secret")
	}
	return t, nil
}

// Fingerprint is the hex SHA-256 of a DER certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// LoadOrCreateCert returns the certificate kept in dir/name.crt and .key,
// making a self-signed ECDSA one the first time.
func LoadOrCreateCert(dir, name, cn string) (tls.Certificate, string, error) {
	crt, key := filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
	if c, err := tls.LoadX509KeyPair(crt, key); err == nil {
		return c, Fingerprint(c.Certificate[0]), nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, "", err
	}
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn, Organization: []string{"Dunvex"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{cn},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &pk.PublicKey, pk)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	kb, err := x509.MarshalECPrivateKey(pk)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(crt, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, "", err
	}
	c, err := tls.LoadX509KeyPair(crt, key)
	return c, Fingerprint(der), err
}

// ---------------------------------------------------------------- messages

// User is a user as a node needs it: who may connect, never their limits.
type User struct {
	Name       string  `json:"name"`
	UUID       string  `json:"uuid"`
	Password   string  `json:"password"`
	InboundIDs []int64 `json:"inboundIds"`
	WGPrivate  string  `json:"wgPrivate,omitempty"`
	WGPublic   string  `json:"wgPublic,omitempty"`
	WGIndex    int     `json:"wgIndex,omitempty"`
}

// State is everything the node should serve. A relay serves no protocols:
// it forwards ports to another server.
type State struct {
	Inbounds []*store.Inbound     `json:"inbounds"`
	Users    []User               `json:"users"`
	Settings map[string]string    `json:"settings"`
	Forwards []relay.Forward      `json:"forwards,omitempty"`
	Tunnel   *relay.TunnelServer  `json:"tunnel,omitempty"`  // a reverse relay's tunnel port
	Tunnels  []relay.TunnelClient `json:"tunnels,omitempty"` // reverse relays this server keeps links to
}

// Status is the node's health, as the panel shows it.
type Status struct {
	Version       string             `json:"version"`
	Epoch         string             `json:"epoch"`
	Hostname      string             `json:"hostname"`
	PublicIP      string             `json:"publicIp"`
	CertSHA256    string             `json:"certSha256"` // of the protocols' TLS certificate
	Cores         []engine.Status    `json:"cores"`
	CoreErrors    map[string]string  `json:"coreErrors"`
	InboundErrors map[int64]string   `json:"inboundErrors"`
	CPU           float64            `json:"cpu"`
	MemUsed       uint64             `json:"memUsed"`
	MemTotal      uint64             `json:"memTotal"`
	DiskUsed      uint64             `json:"diskUsed"`
	DiskTotal     uint64             `json:"diskTotal"`
	Uptime        uint64             `json:"uptime"`
	NetUp         uint64             `json:"netUp"`
	NetDown       uint64             `json:"netDown"`
	Users         int                `json:"users"`
	Inbounds      int                `json:"inbounds"`
	Forwards      []relay.Stat       `json:"forwards,omitempty"`    // a relay's ports
	Tunnel        *relay.TunnelStat  `json:"tunnel,omitempty"`      // a reverse relay's tunnel
	TunnelLinks   []relay.ClientStat `json:"tunnelLinks,omitempty"` // this server's links to reverse relays
	Time          int64              `json:"time"`
}

// Batch is the traffic since the last acknowledged batch.
type Batch struct {
	Epoch    string                   `json:"epoch"`
	Seq      int64                    `json:"seq"`
	Users    map[string]store.Traffic `json:"users"`
	Inbounds map[int64]store.Traffic  `json:"inbounds"`
	Devices  []engine.Seen            `json:"devices"`
}
