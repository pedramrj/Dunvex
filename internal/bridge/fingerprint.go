package bridge

// Per-install fingerprints. Values that would otherwise be constant across every
// Dunvex server — the ICMP echo id, the CDN WebSocket path — are derived from
// the pre-shared key, so no two installs share them and a scanner cannot look
// for a known marker. Both ends compute the same value from the same PSK.

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
)

// DeriveICMPID is the echo id both sides use, derived from the PSK (never 0).
func DeriveICMPID(psk []byte) uint16 {
	b, err := hkdf.Key(sha256.New, psk, nil, "dvxb/v1/icmp-id", 2)
	if err != nil {
		return 0x4242
	}
	id := binary.BigEndian.Uint16(b)
	if id == 0 {
		id = 1
	}
	return id
}

// DeriveCDNPath is the WebSocket path on the CDN carrier, a random-looking path
// derived from the PSK (always begins with "/").
func DeriveCDNPath(psk []byte) string {
	b, err := hkdf.Key(sha256.New, psk, nil, "dvxb/v1/cdn-path", 12)
	if err != nil {
		return "/dvxb"
	}
	return "/" + base64.RawURLEncoding.EncodeToString(b)
}
