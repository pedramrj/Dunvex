package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/node"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/tlsmgr"
	"github.com/pedramrj/dunvex/internal/util"
)

// issueFor writes a 90-day certificate for domain where Let's Encrypt's goes.
func issueFor(certDir, domain string) (string, string, bool, error) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		return "", "", false, err
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	cert, key := tlsmgr.Paths(certDir, domain)
	os.MkdirAll(filepath.Dir(cert), 0o700)
	os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return cert, key, true, nil
}

func TestNodeDomainAndUpdate(t *testing.T) {
	// a node whose certificate is issued once the test lets it
	ndir := t.TempDir()
	nst, err := store.Open(filepath.Join(ndir, "node.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer nst.Close()
	certDir := filepath.Join(ndir, "certs")
	exe := filepath.Join(ndir, "dunvex")
	os.WriteFile(exe, []byte("Dunvex v1.0.0"), 0o755)
	var restarts atomic.Int32
	issue := make(chan struct{})
	agent := &node.Agent{Version: "v1.0.0", St: nst, Sys: sysinfo.NewSampler(), CertDir: certDir, Executable: exe,
		Env: func() protocol.Env { return node.EnvFor(nst, certDir, "127.0.0.1", "") },
		Obtain: func(dir, domain, email string) (string, string, bool, error) {
			<-issue
			return issueFor(dir, domain)
		},
		CheckBinary: func(p string) (string, error) {
			b, _ := os.ReadFile(p)
			return strings.TrimPrefix(string(b), "Dunvex "), nil
		},
		Restart: func() { restarts.Add(1) }}
	agent.Mgr = engine.NewManager(nst, filepath.Join(ndir, "bin"), filepath.Join(ndir, "run"), agent.Env)
	ncert, _, _ := node.LoadOrCreateCert(certDir, "agent", "dunvex-node")
	nts := httptest.NewUnstartedServer(agent.Handler())
	nts.TLS = agent.TLSConfig(ncert)
	nts.StartTLS()
	defer nts.Close()
	tok, err := node.Init(nst, certDir, "127.0.0.1", nts.Listener.Addr().(*net.TCPAddr).Port, false)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	st.SetSetting("host", "127.0.0.1")
	st.SetSetting("acme_email", "owner@example.com")
	srv := &Server{Home: dir, Version: "v1.1.0", St: st, Sys: sysinfo.NewSampler(),
		SelfBinary: func() ([]byte, error) { return []byte("Dunvex v1.1.0"), nil }}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	code, out := owner.do("POST", "/nodes", map[string]any{"name": "Frankfurt", "token": tok.String()})
	if code != 200 || out["paired"] != true {
		t.Fatalf("add node: %d %v", code, out)
	}
	id := itoa(int64(out["id"].(float64)))
	if u, _ := out["update"].(map[string]any); u == nil || u["via"] != "panel" || u["panel"] != "v1.1.0" {
		t.Fatalf("update offered: %v", out["update"])
	}

	// a TLS protocol needs the node's domain
	if code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "vless-ws-tls", "nodeId": out["id"]}); code != 400 || !strings.Contains(out["error"].(string), "domain") {
		t.Fatalf("TLS without a domain: %d %v", code, out)
	}
	if code, _ := owner.do("PUT", "/nodes/"+id, map[string]any{"domain": "not a domain"}); code != 400 {
		t.Fatal("a bad domain was accepted")
	}
	if code, out := owner.do("PUT", "/nodes/"+id, map[string]any{"domain": "Node.Example.com."}); code != 200 || out["domain"] != "node.example.com" {
		t.Fatalf("set domain: %d %v", code, out)
	}
	nodeID, _ := out["id"].(float64)
	code, out = owner.do("POST", "/inbounds", map[string]any{"preset": "vless-ws-tls", "nodeId": nodeID})
	if code != 200 {
		t.Fatalf("TLS with a domain: %d %v", code, out)
	}
	ins, _ := st.Inbounds()
	if ins[0].Str("sni") != "node.example.com" {
		t.Fatalf("sni: %v", ins[0].Params)
	}
	st.CreateUser(&store.User{Name: "sara", UUID: util.UUID(), Password: "sara-pass-0123456789", SubToken: "sara-token-0123456789", Enabled: true, ResetPeriod: "none"})
	link := func() string {
		res, err := http.Get(ts.URL + "/sub/sara-token-0123456789")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		d, _ := base64.StdEncoding.DecodeString(string(b))
		for _, l := range strings.Fields(string(d)) {
			if strings.Contains(l, "type=ws") {
				return l
			}
		}
		return ""
	}

	// until Let's Encrypt answers, the node's own certificate is pinned
	owner.do("POST", "/nodes/"+id+"/sync", nil)
	if l := link(); !strings.Contains(l, "sni=node.example.com") || !strings.Contains(l, "pcs=") || !strings.Contains(l, "@127.0.0.1:") {
		t.Fatalf("before the certificate: %s", l)
	}
	close(issue)
	for i := 0; i < 100 && !agent.Status().RealCert; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	code, out = owner.do("POST", "/nodes/"+id+"/sync", nil)
	s, _ := out["status"].(map[string]any)
	if code != 200 || s["realCert"] != true || s["domain"] != "node.example.com" || s["certExpiry"] == nil || out["realCert"] != true {
		t.Fatalf("after the certificate: %d %v", code, out)
	}
	if l := link(); !strings.Contains(l, "sni=node.example.com") || strings.Contains(l, "pcs=") {
		t.Fatalf("with a real certificate the link pins nothing: %s", l)
	}

	// a new domain: the protocol follows, the old certificate is not trusted
	if code, out := owner.do("PUT", "/nodes/"+id, map[string]any{"domain": "vpn.example.com"}); code != 200 || out["realCert"] != false {
		t.Fatalf("new domain: %d %v", code, out)
	}
	if ins, _ := st.Inbounds(); ins[0].Str("sni") != "vpn.example.com" {
		t.Fatalf("sni after the move: %v", ins[0].Params)
	}
	if code, out := owner.do("PUT", "/nodes/"+id, map[string]any{"domain": ""}); code != 400 || !strings.Contains(out["error"].(string), "needs the domain") {
		t.Fatalf("domain removed under a TLS protocol: %d %v", code, out)
	}

	// one-click update: the panel sends its own binary, the node restarts
	code, out = owner.do("POST", "/nodes/"+id+"/update", nil)
	if code != 200 || out["version"] != "v1.1.0" || out["from"] != "v1.0.0" {
		t.Fatalf("update: %d %v", code, out)
	}
	if b, _ := os.ReadFile(exe); string(b) != "Dunvex v1.1.0" {
		t.Fatalf("binary: %q", b)
	}
	for i := 0; i < 100 && restarts.Load() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if restarts.Load() != 1 {
		t.Fatal("the node did not restart")
	}
	// the node now says the panel's version: nothing more to update
	agent.Version = "v1.1.0"
	owner.do("POST", "/nodes/"+id+"/sync", nil)
	if code, out := owner.do("POST", "/nodes/"+id+"/update", nil); code != 400 || !strings.Contains(out["error"].(string), "already runs") {
		t.Fatalf("update again: %d %v", code, out)
	}
	if code, out := owner.do("POST", "/nodes/update", nil); code != 200 || len(out["results"].([]any)) != 0 {
		t.Fatalf("update all: %d %v", code, out)
	}
	// a node newer than the panel is not downgraded
	agent.Version = "v2.0.0"
	owner.do("POST", "/nodes/"+id+"/sync", nil)
	if code, out := owner.do("POST", "/nodes/"+id+"/update", nil); code != 400 || !strings.Contains(out["error"].(string), "newer") {
		t.Fatalf("downgrade: %d %v", code, out)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"v1.17.0", "v1.18.0", -1}, {"v1.18.0", "v1.17.9", 1}, {"v1.10.0", "v1.9.0", 1}, {"v2.0.0", "v2.0.0", 0}, {"dev", "v1.0.0", -1}, {"v1.0.0", "dev", 1}, {"v1.2.3-rc1", "v1.2.3", 0}} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("%s vs %s: %d", c.a, c.b, got)
		}
	}
}
