package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestAllPresetsAcceptedByCores builds one inbound per preset and asks the real
// cores to validate the generated configs. Set DUNVEX_TEST_BIN to a folder that
// holds xray and sing-box binaries; the test is skipped otherwise.
func TestAllPresetsAcceptedByCores(t *testing.T) {
	bin := os.Getenv("DUNVEX_TEST_BIN")
	if bin == "" {
		t.Skip("DUNVEX_TEST_BIN not set")
	}
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i, name := range []string{"alice", "bob"} {
		priv, pub := util.WireGuardKeys()
		u := &store.User{Name: name, UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20),
			Enabled: true, WGPrivate: priv, WGPublic: pub, WGIndex: i + 2}
		if err := st.CreateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	port := 30000
	for _, p := range protocol.All() {
		params := map[string]any{}
		if p.ID == "hysteria2" || p.ID == "hysteria2-sb" {
			params["obfs"] = true
		}
		if p.ID == "vless-xdns" {
			params["xdnsDomain"] = "t.example.com"
		}
		in := &store.Inbound{Preset: p.ID, Port: port, Enabled: true, Params: protocol.Defaults(p, params, "example.com")}
		port++
		if err := st.CreateInbound(in); err != nil {
			t.Fatal(err)
		}
	}
	cert, key, err := util.SelfSignedCert(filepath.Join(dir, "certs"), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	env := protocol.Env{Host: "203.0.113.10", Domain: "example.com", CertFile: cert, KeyFile: key, SelfSigned: true}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	x, s, ierr, _, err := m.Configs()
	if err != nil {
		t.Fatal(err)
	}
	for id, e := range ierr {
		t.Errorf("inbound %d: %s", id, e)
	}
	os.WriteFile(filepath.Join(dir, "x.json"), x, 0o600)
	if err := m.Xray.Test(x); err != nil {
		t.Errorf("xray: %v", err)
	}
	if err := m.SingBox.Test(s); err != nil {
		t.Errorf("sing-box: %v", err)
	}
	if out := os.Getenv("DUNVEX_TEST_DUMP"); out != "" {
		os.WriteFile(filepath.Join(out, "xray.json"), x, 0o644)
		os.WriteFile(filepath.Join(out, "sing-box.json"), s, 0o644)
	}
}
