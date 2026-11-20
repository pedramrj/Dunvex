package awg

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func key(b byte) string {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return base64.StdEncoding.EncodeToString(k)
}

func hexKey(b byte) string {
	k, _ := base64.StdEncoding.DecodeString(key(b))
	return hex.EncodeToString(k)
}

func cfg(peers ...Peer) *Config {
	return &Config{PrivateKey: key(1), Port: 51821, Address: "10.67.0.1/16", MTU: 1420,
		Device: map[string]string{"s1": "20", "jc": "5", "h1": "300000000-300100000", "header_protection_key": hexKey(9), "random_trailers": "true"},
		Peers:  peers}
}

func TestFullSet(t *testing.T) {
	c := cfg(Peer{User: "sara", PublicKey: key(2), Address: "10.67.0.3/32"}, Peer{User: "bad", PublicKey: "not-a-key", Address: "10.67.0.4/32"})
	got := strings.Join(fullSet(c), "\n")
	want := strings.Join([]string{
		"private_key=" + hexKey(1), "listen_port=51821",
		"h1=300000000-300100000", "header_protection_key=" + hexKey(9), "jc=5", "random_trailers=true", "s1=20",
		"replace_peers=true",
		"public_key=" + hexKey(2), "replace_allowed_ips=true", "allowed_ip=10.67.0.3/32",
	}, "\n")
	if got != want {
		t.Fatalf("fullSet:\n%s\nwant:\n%s", got, want)
	}
}

func TestPeerDiff(t *testing.T) {
	sara := Peer{User: "sara", PublicKey: key(2), Address: "10.67.0.3/32"}
	reza := Peer{User: "reza", PublicKey: key(3), Address: "10.67.0.4/32"}
	nima := Peer{User: "nima", PublicKey: key(4), Address: "10.67.0.5/32"}
	moved := Peer{User: "reza", PublicKey: key(3), Address: "10.67.0.9/32"}

	if d := peerDiff(cfg(sara, reza), cfg(sara, reza)); len(d) != 0 {
		t.Errorf("no change should send nothing: %v", d)
	}
	// nima joins, reza leaves; sara is left alone (stays connected)
	d := strings.Join(peerDiff(cfg(sara, reza), cfg(sara, nima)), "\n")
	if strings.Contains(d, hexKey(2)) {
		t.Errorf("sara was touched:\n%s", d)
	}
	if !strings.Contains(d, "public_key="+hexKey(4)+"\nreplace_allowed_ips=true\nallowed_ip=10.67.0.5/32") {
		t.Errorf("nima not added:\n%s", d)
	}
	if !strings.Contains(d, "public_key="+hexKey(3)+"\nremove=true") {
		t.Errorf("reza not removed:\n%s", d)
	}
	// a new address is set again, not removed
	d = strings.Join(peerDiff(cfg(reza), cfg(moved)), "\n")
	if d != "public_key="+hexKey(3)+"\nreplace_allowed_ips=true\nallowed_ip=10.67.0.9/32" {
		t.Errorf("moved peer:\n%s", d)
	}

	if !sameDevice(cfg(sara), cfg(nima)) {
		t.Error("peers alone should not count as a new device")
	}
	other := cfg()
	other.Device["s1"] = "21"
	if sameDevice(cfg(), other) {
		t.Error("a new padding must restart the interface")
	}
}

func TestParseStats(t *testing.T) {
	ans := strings.Join([]string{
		"private_key=" + hexKey(1), "listen_port=51821", "jc=5",
		"public_key=" + hexKey(2), "endpoint=198.51.100.7:40211", "last_handshake_time_sec=1763650000",
		"last_handshake_time_nsec=0", "rx_bytes=1234", "tx_bytes=56789", "persistent_keepalive_interval=0", "allowed_ip=10.67.0.3/32",
		"public_key=" + hexKey(3), "rx_bytes=0", "tx_bytes=0", "allowed_ip=10.67.0.4/32",
		"errno=0", "",
	}, "\n")
	st, err := parseStats(strings.NewReader(ans))
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 {
		t.Fatalf("peers: %v", st)
	}
	s := st[key(2)]
	if s.RX != 1234 || s.TX != 56789 || s.LastHandshake != 1763650000 || s.Endpoint != "198.51.100.7:40211" {
		t.Errorf("sara: %+v", s)
	}
	if r := st[key(3)]; r.RX != 0 || r.Endpoint != "" || r.LastHandshake != 0 {
		t.Errorf("reza: %+v", r)
	}
	if _, err := parseStats(strings.NewReader("errno=5\n\n")); err == nil {
		t.Error("an error answer must fail")
	}
}

func TestDescribeHidesPrivateKey(t *testing.T) {
	c := cfg(Peer{User: "sara", PublicKey: key(2), Address: "10.67.0.3/32"})
	text := c.Describe("dvxawg0")
	if strings.Contains(text, key(1)) || strings.Contains(text, hexKey(1)) {
		t.Fatalf("private key shown:\n%s", text)
	}
	if !strings.Contains(text, "ListenPort = 51821") || !strings.Contains(text, "# sara") {
		t.Errorf("describe:\n%s", text)
	}
}
