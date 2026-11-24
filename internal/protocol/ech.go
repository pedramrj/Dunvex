package protocol

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/cryptobyte"
)

// ECH (Encrypted Client Hello) encrypts the start of the TLS handshake, the
// server name included: a filter sees only the ECH config's public name. Two
// kinds reach the apps:
//
//   - this server's own ECH, on its TLS protocols over TCP: the server holds
//     the keys (Xray's echServerKeys, sing-box's tls.ech.key) and the apps get
//     the config;
//   - a CDN's ECH (Cloudflare's, public name cloudflare-ech.com) on the
//     configs that go through it: the CDN holds the keys.
//
// Xray apps read the config from the link ("ech"), sing-box apps from
// tls.ech.config, Clash from ech-opts. An app without ECH sends the real name
// as before, and the server answers it as before.
type ECH struct {
	Config string // base64 ECHConfigList the apps get
	Keys   string // base64 server keys; "" for a CDN's ECH (the CDN holds them)
}

// NewECHKeys makes this server's ECH: an X25519 key and its config for
// publicName, in the form Xray and sing-box both read (each server key is the
// 2-byte-length private key, then the 2-byte-length ECHConfig).
func NewECHKeys(publicName string) (*ECH, error) {
	if publicName == "" || len(publicName) > 255 {
		return nil, fmt.Errorf("the public name must be a hostname")
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	var id [1]byte
	rand.Read(id[:])
	cfg, err := marshalECHConfig(id[0], key.PublicKey().Bytes(), publicName)
	if err != nil {
		return nil, err
	}
	list := cryptobyte.NewBuilder(nil)
	list.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(cfg) })
	lb, err := list.Bytes()
	if err != nil {
		return nil, err
	}
	keys := cryptobyte.NewBuilder(nil)
	keys.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(key.Bytes()) })
	keys.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(cfg) })
	kb, err := keys.Bytes()
	if err != nil {
		return nil, err
	}
	return &ECH{Config: base64.StdEncoding.EncodeToString(lb), Keys: base64.StdEncoding.EncodeToString(kb)}, nil
}

// marshalECHConfig is one ECHConfig (draft-ietf-tls-esni, version 0xfe0d):
// DHKEM(X25519, HKDF-SHA256), HKDF-SHA256 with AES-128-GCM, AES-256-GCM or
// ChaCha20-Poly1305, no extensions.
func marshalECHConfig(id uint8, pub []byte, publicName string) ([]byte, error) {
	b := cryptobyte.NewBuilder(nil)
	b.AddUint16(0xfe0d)
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint8(id)
		b.AddUint16(0x0020) // DHKEM(X25519, HKDF-SHA256)
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(pub) })
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			for _, aead := range []uint16{0x0001, 0x0002, 0x0003} {
				b.AddUint16(0x0001) // HKDF-SHA256
				b.AddUint16(aead)
			}
		})
		b.AddUint8(0) // maximum_name_length: let the client pad as it likes
		b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes([]byte(publicName)) })
		b.AddUint16(0) // extensions
	})
	return b.Bytes()
}

// ECHPublicName is the public name of the first config in a base64
// ECHConfigList: what a filter sees as the server name.
func ECHPublicName(config string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(config)
	if err != nil {
		return "", fmt.Errorf("the ECH config is not base64")
	}
	s := cryptobyte.String(raw)
	var list cryptobyte.String
	if !s.ReadUint16LengthPrefixed(&list) || !s.Empty() {
		return "", errors.New("not an ECH config list")
	}
	for !list.Empty() {
		var version uint16
		var body cryptobyte.String
		if !list.ReadUint16(&version) || !list.ReadUint16LengthPrefixed(&body) {
			return "", errors.New("not an ECH config list")
		}
		if version != 0xfe0d {
			continue // a version this code does not know: the apps skip it too
		}
		var id uint8
		var kem uint16
		var pub, suites, name cryptobyte.String
		var maxLen uint8
		if !body.ReadUint8(&id) || !body.ReadUint16(&kem) || !body.ReadUint16LengthPrefixed(&pub) ||
			!body.ReadUint16LengthPrefixed(&suites) || !body.ReadUint8(&maxLen) || !body.ReadUint8LengthPrefixed(&name) {
			return "", errors.New("a broken ECH config")
		}
		return string(name), nil
	}
	return "", errors.New("no ECH config this code knows")
}

// ECHable: TLS over TCP that both ends can do ECH on: Xray's VLESS, VMess and
// Trojan with TLS, and sing-box's AnyTLS. QUIC (Hysteria2, TUIC), Reality
// (which has its own camouflage) and the rest are left alone.
func ECHable(p *Preset) bool {
	if p.Security != "tls" || p.Network != "tcp" {
		return false
	}
	switch p.proto {
	case "vless", "vmess", "trojan":
		return p.Engine == "xray" && p.ID != "vless-enc-xhttp"
	case "anytls":
		return true
	}
	return false
}

// echFor is the ECH config p's configs carry in env ("" = none).
func echFor(p *Preset, env Env) string {
	if env.ECH == nil || !ECHable(p) {
		return ""
	}
	return env.ECH.Config
}

// echServerKeys is the server keys p's inbound holds ("" = none): this
// server's own ECH only.
func echServerKeys(p *Preset, env Env) string {
	if env.ECH == nil || !ECHable(p) {
		return ""
	}
	return env.ECH.Keys
}

// echSingbox is a sing-box client's tls.ech, the config in PEM lines.
func echSingbox(config string) obj {
	return obj{"enabled": true, "config": []string{"-----BEGIN ECH CONFIGS-----", config, "-----END ECH CONFIGS-----"}}
}

// echSingboxServer is a sing-box inbound's tls.ech, the keys in PEM lines.
func echSingboxServer(keys string) obj {
	return obj{"enabled": true, "key": []string{"-----BEGIN ECH KEYS-----", keys, "-----END ECH KEYS-----"}}
}
