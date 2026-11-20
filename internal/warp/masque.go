package warp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// Transports a device can use.
const (
	WireGuard = "wireguard"
	MASQUE    = "masque" // HTTP/3 (QUIC) on port 443, run by usque
)

// Masque reports whether the device uses MASQUE.
func (a *Account) Masque() bool { return a.Transport == MASQUE }

// EnrollMasque switches the device to MASQUE with a new P-256 key. Cloudflare
// stopped answering WARP's WireGuard from many networks, data centres among
// them, while MASQUE (HTTP/3 on port 443) keeps working.
func (c *Client) EnrollMasque(ctx context.Context, a *Account) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	pub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return err
	}
	priv, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	var r struct {
		regReply
		WarpEnabled bool `json:"warp_enabled"`
	}
	err = c.call(ctx, "PATCH", "/reg/"+a.ID, a.Token, map[string]string{
		"key": base64.StdEncoding.EncodeToString(pub), "key_type": "secp256r1", "tunnel_type": MASQUE,
	}, &r)
	if err != nil {
		return err
	}
	if len(r.Config.Peers) == 0 || !strings.Contains(r.Config.Peers[0].PublicKey, "BEGIN PUBLIC KEY") {
		return errors.New("Cloudflare sent no MASQUE endpoint")
	}
	p := r.Config.Peers[0]
	v4, v6 := hostOnly(p.Endpoint.V4), hostOnly(p.Endpoint.V6)
	if v4 == "" && v6 == "" {
		return errors.New("Cloudflare sent no MASQUE endpoint address")
	}
	a.Transport, a.MasqueKey, a.MasquePeer, a.MasqueV4, a.MasqueV6 = MASQUE, base64.StdEncoding.EncodeToString(priv), p.PublicKey, v4, v6
	if ad := r.Config.Interface.Addresses; ad.V4 != "" {
		a.AddressV4, a.AddressV6 = ad.V4, ad.V6
	}
	if !r.WarpEnabled {
		// the 1.1.1.1 app switches this on when it connects
		c.call(ctx, "PATCH", "/reg/"+a.ID, a.Token, map[string]bool{"warp_enabled": true}, nil)
	}
	return nil
}

// UsqueConfig is the device as usque's config.json.
func (a *Account) UsqueConfig() []byte {
	b, _ := json.MarshalIndent(map[string]string{
		"private_key": a.MasqueKey, "endpoint_v4": a.MasqueV4, "endpoint_v6": a.MasqueV6,
		"endpoint_h2_v4": "", "endpoint_h2_v6": "", "endpoint_pub_key": a.MasquePeer,
		"id": a.ID, "access_token": a.Token, "ipv4": a.AddressV4, "ipv6": a.AddressV6,
	}, "", "  ")
	return b
}
