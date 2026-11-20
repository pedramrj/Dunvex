// Package util holds small helpers: randomness, keys, certificates and formatting.
package util

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandomBytes returns n cryptographically random bytes.
func RandomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// RandomHex returns n random bytes as hex (2n chars).
func RandomHex(n int) string { return hex.EncodeToString(RandomBytes(n)) }

// RandomString returns a random alphanumeric string of length n.
func RandomString(n int) string {
	b := RandomBytes(n)
	out := make([]byte, n)
	for i := range b {
		out[i] = alnum[int(b[i])%len(alnum)]
	}
	return string(out)
}

// RandomLower returns a random lowercase string of length n.
func RandomLower(n int) string {
	b := RandomBytes(n)
	out := make([]byte, n)
	for i := range b {
		out[i] = alnum[int(b[i])%26]
	}
	return string(out)
}

// UUID returns a random RFC 4122 v4 UUID.
func UUID() string {
	b := RandomBytes(16)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// X25519 returns a clamped private key and its public key, both raw-url base64
// (the encoding Xray uses for REALITY and VLESS Encryption).
func X25519() (priv, pub string) {
	k := RandomBytes(32)
	k[0] &= 248
	k[31] &= 127
	k[31] |= 64
	key, err := ecdh.X25519().NewPrivateKey(k)
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(k), base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
}

// X25519Public derives the public key from a raw-url base64 private key.
func X25519Public(priv string) (string, error) {
	k, err := base64.RawURLEncoding.DecodeString(priv)
	if err != nil {
		return "", err
	}
	key, err := ecdh.X25519().NewPrivateKey(k)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

// WireGuardKeys returns a WireGuard key pair in standard base64.
func WireGuardKeys() (priv, pub string) {
	k := RandomBytes(32)
	k[0] &= 248
	k[31] &= 127
	k[31] |= 64
	key, err := ecdh.X25519().NewPrivateKey(k)
	if err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(k), base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
}

// SSKey returns a Shadowsocks-2022 key of n bytes in standard base64.
func SSKey(n int) string { return base64.StdEncoding.EncodeToString(RandomBytes(n)) }

// SSKeyFrom derives a deterministic Shadowsocks-2022 key of n bytes from a seed string,
// so a user's key stays stable without storing one key per method.
func SSKeyFrom(seed string, n int) string {
	h := sha256.Sum256([]byte("dunvex-ss2022:" + seed))
	return base64.StdEncoding.EncodeToString(h[:n])
}

// ShortID returns a random REALITY short id (even-length hex).
func ShortID(nBytes int) string { return RandomHex(nBytes) }

// SelfSignedCert writes a long-lived ECDSA certificate for host into dir and returns the paths.
func SelfSignedCert(dir, host string) (certPath, keyPath string, err error) {
	certPath = filepath.Join(dir, "self.crt")
	keyPath = filepath.Join(dir, "self.key")
	if fileExists(certPath) && fileExists(keyPath) {
		return certPath, keyPath, nil
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else if host != "" {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return
	}
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return
	}
	err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return
}

// CertSHA256 returns the hex SHA-256 of the first certificate in a PEM file (for pinning).
func CertSHA256(certPath string) string {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return ""
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return ""
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:])
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// FileExists reports whether a regular file exists.
func FileExists(p string) bool { return fileExists(p) }

// HumanBytes formats bytes (binary units).
func HumanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// SanitizeName keeps a user name safe for core configs (used as the stats e-mail tag).
func SanitizeName(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('_')
		}
	}
	return b.String()
}

// FreePort reports whether a TCP and UDP port can be bound on all interfaces.
func FreePort(port int) bool {
	// check the wildcard and the loopback address: on Windows a wildcard bind can
	// succeed while another program holds 127.0.0.1:port
	for _, host := range []string{"", "127.0.0.1"} {
		l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
		if err != nil {
			return false
		}
		l.Close()
		u, err := net.ListenPacket("udp", fmt.Sprintf("%s:%d", host, port))
		if err != nil {
			return false
		}
		u.Close()
	}
	return true
}

// FreeUDPPort reports whether a UDP port can be bound on all interfaces.
func FreeUDPPort(port int) bool {
	u, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	u.Close()
	return true
}
