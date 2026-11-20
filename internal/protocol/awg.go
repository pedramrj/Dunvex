package protocol

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// AmneziaWG: WireGuard with junk packets, padded messages, random message
// headers and (3.x) header protection, so DPI cannot spot the handshake.
// Server and clients must share S1–S4, H1–H4 and the header protection key;
// junk (Jc, Jmin, Jmax) only matters on the client.

const awgSubnet = "10.67"

// AWGServerAddress is the server's side of the tunnel.
func AWGServerAddress() string { return awgSubnet + ".0.1/16" }

// AWGAddress is a user's address in the tunnel.
func AWGAddress(u *store.User) string {
	i := u.WGIndex
	if i < 2 {
		i = 2
	}
	return fmt.Sprintf("%s.%d.%d", awgSubnet, (i/254)%256, i%254+1)
}

// awgDefaults fills the obfuscation parameters for the chosen version. They
// are made again when the version changes (the two need different values).
func awgDefaults(params map[string]any) {
	ver := str(params["version"])
	if ver != "2.0" {
		ver = "3.1"
		params["version"] = ver
	}
	if s, _ := params["privateKey"].(string); s == "" {
		priv, pub := util.WireGuardKeys()
		params["privateKey"], params["publicKey"] = priv, pub
	} else if pub, err := util.WireGuardPublic(s); err == nil {
		params["publicKey"] = pub
	}
	if n := num(params["mtu"]); n < 576 || n > 1500 {
		params["mtu"] = 1280
	} else {
		params["mtu"] = n
	}
	if str(params["dns"]) == "" {
		params["dns"] = "1.1.1.1, 8.8.8.8"
	}
	if str(params["madeFor"]) == ver {
		return
	}
	r := func(a, b int) int { return a + rand.IntN(b-a+1) }
	params["jc"], params["jmin"], params["jmax"] = r(4, 8), 50, 600
	if ver == "3.1" {
		// one padding for all four (sizes are random anyway with trailers);
		// header protection needs at least 12
		s := r(16, 48)
		params["s1"], params["s2"], params["s3"], params["s4"] = s, s, s, s
		params["hpk"] = base64.StdEncoding.EncodeToString(util.RandomBytes(32))
	} else {
		s1 := r(15, 150)
		s2 := r(15, 150)
		for s1+56 == s2 { // initiation and response must not end up the same size
			s2 = r(15, 150)
		}
		params["s1"], params["s2"], params["s3"], params["s4"] = s1, s2, r(15, 150), r(0, 32)
		delete(params, "hpk")
	}
	// four separate header ranges, each in its own quarter of the space above
	// WireGuard's own message types
	const lo, span = 0x10000000, 0x3C000000
	for i := 0; i < 4; i++ {
		start := lo + i*span + rand.IntN(span-(1<<20))
		params["h"+strconv.Itoa(i+1)] = fmt.Sprintf("%d-%d", start, start+(1<<16)+rand.IntN(1<<19))
	}
	params["madeFor"] = ver
}

// num reads a number however it arrived (Go value, JSON number or text).
func num(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	}
	return 0
}

// AWGDevice is the server's UAPI device settings for an inbound.
func AWGDevice(in *store.Inbound) map[string]string {
	d := map[string]string{}
	for _, k := range []string{"jc", "jmin", "jmax", "s1", "s2", "s3", "s4", "h1", "h2", "h3", "h4"} {
		d[k] = in.Str(k)
	}
	if in.Str("version") != "2.0" {
		if k, err := base64.StdEncoding.DecodeString(in.Str("hpk")); err == nil && len(k) == 32 {
			d["header_protection_key"] = hex.EncodeToString(k)
		}
		d["random_trailers"] = "true"
	}
	return d
}

// AWGConfig is a user's .conf file, as AmneziaWG and AmneziaVPN import it.
func AWGConfig(in *store.Inbound, u *store.User, env Env) string {
	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s = %s\n", k, v)
		}
	}
	b.WriteString("[Interface]\n")
	line("PrivateKey", u.WGPrivate)
	line("Address", AWGAddress(u)+"/32")
	line("DNS", in.Str("dns"))
	line("MTU", in.Str("mtu"))
	for _, k := range []string{"Jc", "Jmin", "Jmax", "S1", "S2", "S3", "S4", "H1", "H2", "H3", "H4"} {
		line(k, in.Str(strings.ToLower(k)))
	}
	if in.Str("version") != "2.0" {
		line("HeaderProtectionKey", in.Str("hpk"))
		line("RandomTrailers", "true")
	}
	line("I1", in.Str("i1"))
	b.WriteString("\n[Peer]\n")
	line("PublicKey", in.Str("publicKey"))
	line("Endpoint", hostPort(LinkHost(in, env), in.Port))
	line("AllowedIPs", "0.0.0.0/0, ::/0")
	line("PersistentKeepalive", "25")
	return b.String()
}
