package protocol

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// Xray 26.11 apps refuse a VLESS link to a public address with neither TLS
// nor encryption, so the tunnels without TLS carry VLESS Encryption: the
// server decrypts with the private key, the link encrypts with its pair.
func TestVLESSEncryptedTunnels(t *testing.T) {
	u := &store.User{Name: "sara", UUID: "11111111-1111-1111-1111-111111111111"}
	env := Env{Host: "203.0.113.10"}
	for _, id := range []string{"vless-xicmp", "vless-xdns", "vless-enc-xhttp"} {
		p := Get(id)
		if !VLESSEncrypted(id) {
			t.Fatalf("%s: not encrypted", id)
		}
		in := &store.Inbound{ID: 7, Preset: id, Port: 5353, Enabled: true, Params: Defaults(p, nil, "")}
		if id == "vless-xdns" {
			in.Params["xdnsDomain"] = "t.example.com"
		}
		dec, enc := in.Str("decryption"), in.Str("encryption")
		if !strings.HasPrefix(dec, "mlkem768x25519plus.native.600s.") || !strings.HasPrefix(enc, "mlkem768x25519plus.native.0rtt.") {
			t.Fatalf("%s keys: %q %q", id, dec, enc)
		}
		pub, err := util.X25519Public(strings.TrimPrefix(dec, "mlkem768x25519plus.native.600s."))
		if err != nil || pub != strings.TrimPrefix(enc, "mlkem768x25519plus.native.0rtt.") {
			t.Fatalf("%s: the link's key is not the server key's pair (%v)", id, err)
		}

		// the keys are made once: a second Defaults keeps them
		again := Defaults(p, in.Params, "")
		if again["decryption"] != dec || again["encryption"] != enc {
			t.Fatalf("%s: keys changed on a second Defaults", id)
		}

		ib, err := XrayInbound(p, in, []*store.User{u}, env)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got := ib["settings"].(obj)["decryption"]; got != dec {
			t.Fatalf("%s server decryption: %v", id, got)
		}
		link, err := url.Parse(Link(p, in, u, env, "x"))
		if err != nil {
			t.Fatal(err)
		}
		if got := link.Query().Get("encryption"); got != enc {
			t.Fatalf("%s link encryption: %q", id, got)
		}
		// the panel's own link reader (config health, Xray JSON) keeps it
		out, err := XrayOutbound(link.String(), "proxy")
		if err != nil {
			t.Fatalf("%s outbound: %v", id, err)
		}
		if !strings.Contains(toJSON(out), `"encryption":"`+enc+`"`) {
			t.Fatalf("%s outbound lost the encryption: %s", id, toJSON(out))
		}
		// sing-box has no VLESS Encryption
		if kind, items := SingboxOutbound(p, in, u, env, "x"); kind != "" || items != nil {
			t.Errorf("%s sing-box outbound: %s %v", id, kind, items)
		}
	}

	// an inbound saved before v1.31.1 (no keys) still builds, without encryption
	p := Get("vless-xicmp")
	old := &store.Inbound{ID: 8, Preset: p.ID, Enabled: true, Params: map[string]any{"icmpDgram": true}}
	ib, err := XrayInbound(p, old, []*store.User{u}, env)
	if err != nil || ib["settings"].(obj)["decryption"] != "none" {
		t.Fatalf("old inbound: %v %v", err, ib["settings"])
	}
	if q, _ := url.Parse(Link(p, old, u, env, "x")); q.Query().Get("encryption") != "none" {
		t.Fatalf("old link: %s", q)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
