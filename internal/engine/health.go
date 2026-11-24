package engine

import (
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// HealthName is the user of the config health check: the panel tests every
// config with it, from the server, with real client cores. A real user's
// name cannot hold "~", so the two never mix. It is not in the database: it
// counts no traffic, takes no seat and never shows in the user list.
const HealthName = "~health"

// HealthKeySetting holds the secret the check's user is made from. The panel
// creates it; nodes receive it with their settings, so the same user exists
// on every server.
const HealthKeySetting = "health_key"

// healthWGIndex is the check's WireGuard address, 10.66.255.254: the last one
// the address plan gives out, far past any real user.
const healthWGIndex = 254*256 - 1

// EnsureHealthKey creates the secret once (on the panel) and returns it.
func EnsureHealthKey(st *store.Store) string {
	if k := st.GetSetting(HealthKeySetting, ""); k != "" {
		return k
	}
	k := hex.EncodeToString(util.RandomBytes(32))
	st.SetSetting(HealthKeySetting, k)
	return k
}

// HealthUser is the check's user, the same on every server holding the key;
// nil while this server has none (a node before its first sync).
func HealthUser(st *store.Store) *store.User {
	key := st.GetSetting(HealthKeySetting, "")
	if key == "" {
		return nil
	}
	derive := func(label string) []byte {
		h := sha256.Sum256([]byte(key + ":" + label))
		return h[:]
	}
	id := derive("uuid")[:16]
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	wg := derive("wireguard")
	wg[0] &= 248
	wg[31] &= 127
	wg[31] |= 64
	priv, err := ecdh.X25519().NewPrivateKey(wg)
	if err != nil {
		return nil
	}
	return &store.User{
		Name:      HealthName,
		UUID:      fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16]),
		Password:  hex.EncodeToString(derive("password"))[:24],
		Enabled:   true,
		WGPrivate: base64.StdEncoding.EncodeToString(wg),
		WGPublic:  base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()),
		WGIndex:   healthWGIndex,
	}
}

// HealthServed says whether the check's user is added to an inbound of this
// preset: the protocols the check has a client for (SSH: sing-box). The others (Mieru,
// TrustTunnel, AmneziaWG, Slipstream, the Telegram proxy) are left as they are.
func HealthServed(p *protocol.Preset) bool {
	switch p.Engine {
	case "xray", "singbox", "dnstt", "ssh":
		return true
	}
	return false
}
