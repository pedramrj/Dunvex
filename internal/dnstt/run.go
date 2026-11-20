package dnstt

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"sync/atomic"

	"www.bamsoftware.com/git/dnstt.git/dns"
	"www.bamsoftware.com/git/dnstt.git/noise"
)

// Logf receives the tunnel's log lines; set it before the first Run (nil: dropped).
var Logf func(format string, v ...any)

// Sessions counts the tunnel sessions open now, across every Run.
var Sessions atomic.Int64

func logf(format string, v ...any) {
	if Logf != nil {
		Logf(format, v...)
	}
}

// guard keeps a panic inside the tunnel from taking the whole panel down.
func guard(what string) {
	if r := recover(); r != nil {
		logf("%s: internal error: %v", what, r)
	}
}

// Run serves the tunnel zone domain on conn: queries carry the tunnel's
// packets, and each stream the clients open is forwarded to upstream (a TCP
// address). It returns when conn is closed.
func Run(privkey []byte, domain, upstream string, conn net.PacketConn) error {
	name, err := dns.ParseName(domain)
	if err != nil {
		conn.Close()
		return fmt.Errorf("invalid domain %q: %v", domain, err)
	}
	return run(privkey, name, upstream, conn)
}

// NewKey makes a server key pair (hex, as the clients take the public key).
func NewKey() (priv, pub string, err error) {
	k, err := noise.GeneratePrivkey()
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(k), hex.EncodeToString(noise.PubkeyFromPrivkey(k)), nil
}

// PublicKey is the public key of a hex private key ("" when it is not one).
func PublicKey(priv string) string {
	k, err := DecodeKey(priv)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(noise.PubkeyFromPrivkey(k))
}

// DecodeKey reads a hex key.
func DecodeKey(s string) ([]byte, error) {
	return noise.DecodeKey(strings.TrimSpace(s))
}
