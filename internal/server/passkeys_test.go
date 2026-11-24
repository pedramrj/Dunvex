package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
)

var b64 = base64.RawURLEncoding

// softKey is a passkey held in software, as a phone or a security key holds one.
type softKey struct {
	id      []byte
	key     *ecdsa.PrivateKey
	rpID    string
	origin  string
	handle  []byte
	counter uint32
}

func newSoftKey(t *testing.T, rpID, origin string) *softKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	rand.Read(id)
	return &softKey{id: id, key: k, rpID: rpID, origin: origin}
}

func (k *softKey) clientData(typ, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

func (k *softKey) authData(flags byte, extra []byte) []byte {
	h := sha256.Sum256([]byte(k.rpID))
	out := append(h[:], flags)
	out = binary.BigEndian.AppendUint32(out, k.counter)
	return append(out, extra...)
}

// create answers a registration's options: none attestation, a P-256 key.
func (k *softKey) create(t *testing.T, options map[string]any) map[string]any {
	pk := options["publicKey"].(map[string]any)
	k.handle, _ = b64.DecodeString(pk["user"].(map[string]any)["id"].(string))
	x, y := k.key.PublicKey.X.FillBytes(make([]byte, 32)), k.key.PublicKey.Y.FillBytes(make([]byte, 32))
	cose, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	if err != nil {
		t.Fatal(err)
	}
	attested := make([]byte, 16) // AAGUID
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(k.id)))
	attested = append(append(attested, k.id...), cose...)
	att, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": k.authData(0x45, attested)})
	cd := k.clientData("webauthn.create", pk["challenge"].(string), k.origin)
	return map[string]any{"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response":               map[string]any{"clientDataJSON": b64.EncodeToString(cd), "attestationObject": b64.EncodeToString(att), "transports": []string{"internal"}},
		"clientExtensionResults": map[string]any{}}
}

// get answers a sign-in's options for origin.
func (k *softKey) get(t *testing.T, options map[string]any, origin string) map[string]any {
	pk := options["publicKey"].(map[string]any)
	ad := k.authData(0x05, nil) // user present and verified
	cd := k.clientData("webauthn.get", pk["challenge"].(string), origin)
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte(nil), ad...), h[:]...)) // ES256 signs SHA-256(authData || SHA-256(clientData))
	sig, err := ecdsa.SignASN1(rand.Reader, k.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64.EncodeToString(cd), "authenticatorData": b64.EncodeToString(ad),
			"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(k.handle)},
		"clientExtensionResults": map[string]any{}}
}

func TestPasskeyRP(t *testing.T) {
	for host, want := range map[string][3]string{
		"vpn.esfvip.shop":          {"https", "esfvip.shop", "https://vpn.esfvip.shop"},
		"vpn.esfvip.shop:443":      {"https", "esfvip.shop", "https://vpn.esfvip.shop"},
		"panel.example.co.uk:8443": {"https", "example.co.uk", "https://panel.example.co.uk:8443"},
		"localhost:18543":          {"http", "localhost", "http://localhost:18543"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = host
		if want[0] == "https" {
			r.TLS = &tls.ConnectionState{}
		}
		rp, origin, err := passkeyRP(r)
		if err != nil || rp != want[1] || origin != want[2] {
			t.Errorf("%s: %q %q %v", host, rp, origin, err)
		}
	}
	for host, want := range map[string]error{"57.131.56.65:443": errPasskeyIP, "[2001:db8::1]:443": errPasskeyIP, "vpn.esfvip.shop": errPasskeyHTTP} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = host
		if host != "vpn.esfvip.shop" {
			r.TLS = &tls.ConnectionState{}
		}
		if _, _, err := passkeyRP(r); err != want {
			t.Errorf("%s: %v, want %v", host, err, want)
		}
	}
}

func TestPasskeys(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	// a passkey needs a name, not an address: localhost stands in for the domain
	base := strings.Replace(ts.URL, "127.0.0.1", "localhost", 1)
	origin := base

	owner := newClient(t, base)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatalf("login: %d", code)
	}
	if code, out := owner.do("GET", "/account/passkeys", nil); code != 200 || out["supported"] != true || out["rpId"] != "localhost" {
		t.Fatalf("list: %d %v", code, out)
	}
	if code, _ := owner.do("POST", "/account/passkeys/begin", map[string]string{"password": "wrong"}); code != 400 {
		t.Fatalf("begin with a wrong password: %d", code)
	}
	code, begin := owner.do("POST", "/account/passkeys/begin", map[string]string{"password": "owner-pass-1"})
	if code != 200 {
		t.Fatalf("begin: %d %v", code, begin)
	}
	sel := begin["options"].(map[string]any)["publicKey"].(map[string]any)["authenticatorSelection"].(map[string]any)
	if sel["residentKey"] != "required" || sel["userVerification"] != "required" {
		t.Fatalf("selection: %v", sel)
	}
	key := newSoftKey(t, "localhost", origin)
	cred := key.create(t, begin["options"].(map[string]any))
	if code, out := owner.do("POST", "/account/passkeys/finish", map[string]any{"token": begin["token"], "name": "  My   phone ", "credential": cred}); code != 200 || out["name"] != "My phone" {
		t.Fatalf("finish: %d %v", code, out)
	}
	if code, _ := owner.do("POST", "/account/passkeys/finish", map[string]any{"token": begin["token"], "credential": cred}); code != 400 {
		t.Fatalf("a token used twice: %d", code)
	}
	_, list := owner.do("GET", "/account/passkeys", nil)
	pks := list["passkeys"].([]any)
	if len(pks) != 1 {
		t.Fatalf("passkeys: %v", list)
	}
	pkID := int64(pks[0].(map[string]any)["id"].(float64))

	// sign in with it: no username, password or code
	signIn := func(c *client, origin string) int {
		code, b := c.do("POST", "/login/passkey/begin", map[string]any{})
		if code != 200 {
			t.Fatalf("login begin: %d %v", code, b)
		}
		code, _ = c.do("POST", "/login/passkey/finish", map[string]any{"token": b["token"], "credential": key.get(t, b["options"].(map[string]any), origin)})
		return code
	}
	phone := newClient(t, base)
	key.counter = 5
	if code := signIn(phone, origin); code != 200 {
		t.Fatalf("passkey sign-in: %d %+v", code, st.Events(3))
	}
	if code, out := phone.do("GET", "/me", nil); code != 200 || out["admin"].(map[string]any)["username"] != "boss" {
		t.Fatalf("session after passkey: %d %v", code, out)
	}

	// another site's page asking for it, a counter that went back, a removed passkey
	key.counter = 6
	if code := signIn(newClient(t, base), "https://evil.example"); code != 401 {
		t.Fatalf("wrong origin: %d", code)
	}
	key.counter = 3
	if code := signIn(newClient(t, base), origin); code != 401 {
		t.Fatalf("counter went back: %d", code)
	}
	if code, _ := owner.do("DELETE", "/account/passkeys/"+itoa(pkID), nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	key.counter = 10
	if code := signIn(newClient(t, base), origin); code != 401 {
		t.Fatalf("removed passkey signed in: %d", code)
	}

	// by an IP address: explained, not offered
	ipClient := newClient(t, ts.URL)
	ipClient.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"})
	if code, out := ipClient.do("GET", "/account/passkeys", nil); code != 200 || out["supported"] != false || !strings.Contains(out["reason"].(string), "IP address") {
		t.Fatalf("by IP: %d %v", code, out)
	}
	if code, _ := ipClient.do("POST", "/login/passkey/begin", map[string]any{}); code != 400 {
		t.Fatalf("sign-in by IP: %d", code)
	}
}
