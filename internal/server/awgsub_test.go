package server

import (
	"encoding/base64"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestAmneziaWGSubscription: the .conf is on the user page and downloadable,
// but stays out of the link lists that VPN apps import.
func TestAmneziaWGSubscription(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return protocol.Env{Host: "203.0.113.10"} })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	priv, pub := util.WireGuardKeys()
	sara := &store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(16), SubToken: "sara-token-0123456789",
		Enabled: true, ResetPeriod: "none", WGPrivate: priv, WGPublic: pub, WGIndex: 2}
	st.CreateUser(sara)
	for _, id := range []string{"amneziawg", "vless-reality-vision"} {
		p := protocol.Get(id)
		st.CreateInbound(&store.Inbound{Preset: id, Port: p.DefaultPort, Enabled: true, Params: protocol.Defaults(p, nil, "")})
	}
	ins, _ := st.Inbounds()
	awgID := ins[0].ID

	get := func(path string, hdr ...string) (int, http.Header, string) {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header, string(b)
	}

	// plain links: Reality only, no .conf text
	_, _, body := get("/sub/sara-token-0123456789")
	links, _ := base64.StdEncoding.DecodeString(body)
	if !strings.Contains(string(links), "vless://") || strings.Contains(string(links), "[Interface]") {
		t.Fatalf("links:\n%s", links)
	}

	// the file itself
	code, h, conf := get("/sub/sara-token-0123456789/conf?i=" + itoa(awgID))
	if code != 200 || !strings.HasPrefix(conf, "[Interface]\nPrivateKey = "+priv) || !strings.Contains(conf, "Endpoint = 127.0.0.1:51821") {
		t.Fatalf("conf %d:\n%s", code, conf)
	}
	if cd := h.Get("Content-Disposition"); cd != `attachment; filename="dunvex-`+itoa(awgID)+`.conf"` {
		t.Errorf("file name: %q", cd)
	}

	// the user page shows it with a download button
	_, _, page := get("/sub/sara-token-0123456789", "Accept", "text/html", "User-Agent", "Mozilla/5.0")
	if !strings.Contains(page, `download="dunvex-`+itoa(awgID)+`.conf"`) || !strings.Contains(page, "<textarea") || !strings.Contains(html.UnescapeString(page), "PrivateKey = "+priv) {
		t.Fatal("user page has no .conf block")
	}

	// a user who is switched off gets no file
	sara.Enabled = false
	st.UpdateUser(sara)
	if code, _, _ := get("/sub/sara-token-0123456789/conf"); code != 403 {
		t.Errorf("disabled user got the file: %d", code)
	}
}
