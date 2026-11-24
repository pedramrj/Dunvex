package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// xICMP and xDNS protocols saved by an older Dunvex get VLESS Encryption keys
// on start; ones that have them, and every other protocol, stay as they are.
func TestUpgradeInboundsAddsVLESSKeys(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	oldICMP := &store.Inbound{Preset: "vless-xicmp", Remark: "xICMP", Enabled: true, Params: map[string]any{"icmpDgram": false}}
	oldDNS := &store.Inbound{Preset: "vless-xdns", Remark: "XDNS", Port: 53, Enabled: true, Params: map[string]any{"xdnsDomain": "t.example.com", "resolver": "1.1.1.1"}}
	p := protocol.Get("vless-xicmp")
	fresh := &store.Inbound{Preset: p.ID, Remark: "new", Enabled: true, Params: protocol.Defaults(p, nil, "")}
	reality := &store.Inbound{Preset: "vless-reality-universal", Remark: "Reality", Port: 443, Enabled: true, Params: map[string]any{"sni": "www.speedtest.net"}}
	for _, in := range []*store.Inbound{oldICMP, oldDNS, fresh, reality} {
		if err := st.CreateInbound(in); err != nil {
			t.Fatal(err)
		}
	}
	freshKey := fresh.Str("decryption")

	if n := upgradeInbounds(st); n != 2 {
		t.Fatalf("changed %d protocols, want 2", n)
	}
	if n := upgradeInbounds(st); n != 0 {
		t.Fatalf("second start changed %d protocols", n)
	}
	ins, _ := st.Inbounds()
	by := map[string]*store.Inbound{}
	for _, in := range ins {
		by[in.Remark] = in
	}
	for _, r := range []string{"xICMP", "XDNS"} {
		if !strings.HasPrefix(by[r].Str("decryption"), "mlkem768x25519plus.native.600s.") || !strings.HasPrefix(by[r].Str("encryption"), "mlkem768x25519plus.native.0rtt.") {
			t.Fatalf("%s keys: %v", r, by[r].Params)
		}
	}
	// the owner's own choices survive
	if by["xICMP"].Bool("icmpDgram") || by["XDNS"].Str("resolver") != "1.1.1.1" || by["XDNS"].Str("xdnsDomain") != "t.example.com" {
		t.Fatalf("settings changed: %v %v", by["xICMP"].Params, by["XDNS"].Params)
	}
	if by["new"].Str("decryption") != freshKey {
		t.Fatal("keys of an up-to-date protocol were replaced")
	}
	if _, ok := by["Reality"].Params["decryption"]; ok {
		t.Fatalf("Reality got VLESS Encryption keys: %v", by["Reality"].Params)
	}
}
