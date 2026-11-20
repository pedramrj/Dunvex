package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

func TestPanelCDN(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	ahash, _ := HashPassword("admin-pass-1")
	st.CreateAdmin(&store.Admin{Username: "helper", PasswordHash: ahash, Role: store.RoleAdmin, Enabled: true})
	st.SetSetting("domain", "panel.dunvex.invalid")
	st.SetSetting("host", "203.0.113.7")

	// a stand-in for Cloudflare's API
	var mu sync.Mutex
	var records []map[string]any
	var auth []string
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		auth = append(auth, r.Header.Get("Authorization"))
		ok := func(v any) { json.NewEncoder(w).Encode(map[string]any{"success": true, "result": v}) }
		p := strings.TrimPrefix(r.URL.Path, "/client/v4")
		switch {
		case p == "/zones" && r.URL.Query().Get("name") == "dunvex.invalid":
			ok([]map[string]string{{"id": "z1", "name": "dunvex.invalid", "status": "active"}})
		case p == "/zones":
			ok([]any{})
		case p == "/zones/z1/dns_records" && r.Method == "GET":
			ok(records)
		case p == "/zones/z1/dns_records" && r.Method == "POST":
			var rec map[string]any
			json.NewDecoder(r.Body).Decode(&rec)
			rec["id"] = "r1"
			records = append(records, rec)
			ok(rec)
		case strings.HasPrefix(p, "/zones/z1/settings/"):
			ok(map[string]string{"value": map[string]string{"ssl": "full", "websockets": "on"}[strings.TrimPrefix(p, "/zones/z1/settings/")]})
		default:
			w.WriteHeader(404)
			io.WriteString(w, `{"success":false,"errors":[{"code":7003,"message":"No route"}]}`)
		}
	}))
	defer cf.Close()

	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler(), CloudflareAPI: cf.URL + "/client/v4"}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}
	helper := newClient(t, ts.URL)
	helper.do("POST", "/login", map[string]string{"username": "helper", "password": "admin-pass-1"})
	if code, _ := helper.do("GET", "/cdn", nil); code != 403 {
		t.Errorf("an admin read the CDN settings: %d", code)
	}

	// a WebSocket protocol on a free port, with a stand-in that answers its handshake
	wsPort := freeBoth(t, 33100)
	ws := protocol.Get("vless-ws-tls")
	if err := st.CreateInbound(&store.Inbound{Preset: ws.ID, Port: wsPort, Enabled: true, Params: protocol.Defaults(ws, nil, "panel.dunvex.invalid")}); err != nil {
		t.Fatal(err)
	}
	re := protocol.Get("vless-reality-vision")
	st.CreateInbound(&store.Inbound{Preset: re.ID, Port: wsPort + 1, Enabled: true, Params: protocol.Defaults(re, nil, "")})
	ins, _ := st.Inbounds()
	wsPath := ins[0].Str("path")
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(wsPort))
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wsPath && r.Header.Get("Upgrade") == "websocket" {
			w.WriteHeader(http.StatusSwitchingProtocols)
			return
		}
		http.NotFound(w, r)
	}))
	origin.Listener.Close()
	origin.Listener = ln
	origin.StartTLS()
	defer origin.Close()
	st.CreateUser(&store.User{Name: "sara", UUID: util.UUID(), Password: "sara-pass-0123456789", SubToken: "sara-token-0123456789", Enabled: true, ResetPeriod: "none"})

	code, out := owner.do("GET", "/cdn", nil)
	list, _ := out["inbounds"].([]any)
	if code != 200 || out["domain"] != "panel.dunvex.invalid" || out["serverIP"] != "203.0.113.7" || len(list) != 1 || out["settings"].(map[string]any)["enabled"] != false {
		t.Fatalf("first look: %d %v", code, out)
	}
	if iv := list[0].(map[string]any); iv["transport"] != "ws" || iv["tls"] != true || iv["portOK"] != false {
		t.Fatalf("inbound: %v", iv)
	}
	serverIP := out["serverIP"].(string)

	links := func() []string {
		res, err := http.Get(ts.URL + "/sub/sara-token-0123456789")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		d, _ := base64.StdEncoding.DecodeString(string(b))
		var out []string
		for _, x := range strings.Fields(string(d)) {
			if !strings.Contains(x, "00000000-0000-0000-0000-000000000000") { // the usage line
				out = append(out, x)
			}
		}
		return out
	}
	if l := links(); len(l) != 2 || !strings.Contains(l[0], "pcs=") {
		t.Fatalf("before: %v", l)
	}

	if code, _ := owner.do("PUT", "/cdn", map[string]any{"enabled": true, "addresses": []map[string]string{{"address": "1.2.3"}}}); code != 400 {
		t.Error("a bad clean address was accepted")
	}
	code, out = owner.do("PUT", "/cdn", map[string]any{"enabled": true, "domain": "CDN.dunvex.invalid", "addresses": []map[string]string{
		{"address": "127.0.0.1", "name": "MCI"}, {"address": "speed.cloudflare.com"}}})
	if code != 200 || out["domain"] != "cdn.dunvex.invalid" {
		t.Fatalf("save: %d %v", code, out)
	}
	// the WebSocket protocol now goes through the CDN: the proxied name, then
	// each clean address; the CDN's certificate is checked, not this server's
	l := links()
	var cdnLinks []string
	for _, x := range l {
		if strings.Contains(x, "type=ws") {
			cdnLinks = append(cdnLinks, x)
		}
	}
	if len(l) != 4 || len(cdnLinks) != 3 {
		t.Fatalf("with the CDN: %v", l)
	}
	for i, want := range []string{"@cdn.dunvex.invalid:", "@127.0.0.1:", "@speed.cloudflare.com:"} {
		x := cdnLinks[i]
		if !strings.Contains(x, want) || !strings.Contains(x, "sni=cdn.dunvex.invalid") || !strings.Contains(x, "host=cdn.dunvex.invalid") || strings.Contains(x, "pcs=") {
			t.Errorf("link %d: %s", i, x)
		}
	}
	if !strings.Contains(cdnLinks[0], "CDN") || !strings.Contains(cdnLinks[1], "MCI") {
		t.Errorf("names: %v", cdnLinks)
	}
	_, cfg := owner.do("GET", "/users/1/configs", nil)
	if cs := cfg["configs"].([]any); len(cs) != 4 || cs[0].(map[string]any)["via"] != "cdn" || cs[1].(map[string]any)["via"] != "127.0.0.1" || cs[3].(map[string]any)["via"] != nil {
		t.Errorf("user page: %v", cs)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/sub/sara-token-0123456789", nil)
	req.Header.Set("User-Agent", "clash.meta")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	clash, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(clash), "speed.cloudflare.com") {
		t.Errorf("clash: %s", clash)
	}
	// keeping the direct config too
	owner.do("PUT", "/cdn", map[string]any{"enabled": true, "domain": "cdn.dunvex.invalid", "direct": true, "addresses": []map[string]string{{"address": "127.0.0.1", "name": "MCI"}}})
	if l := links(); len(l) != 4 {
		t.Errorf("with the direct config: %v", l)
	}

	// the check: the name does not resolve; the server itself answers; the
	// "clean address" (this machine) serves no CDN certificate
	code, out = owner.do("POST", "/cdn/check", nil)
	if code != 200 || out["dnsError"] == nil {
		t.Fatalf("check: %d %v", code, out)
	}
	ic := out["inbounds"].([]any)[0].(map[string]any)
	if ic["origin"].(map[string]any)["ok"] != true || ic["cdn"].(map[string]any)["code"] != "dns" {
		t.Errorf("check of the protocol: %v", ic)
	}
	if ac := out["addresses"].([]any)[0].(map[string]any); ac["result"].(map[string]any)["code"] != "cert" {
		t.Errorf("check of the clean address: %v", ac)
	}

	// Cloudflare: the token is sent to Cloudflare only, never kept
	token := "cf-token-that-must-not-be-kept-0123456789"
	if code, out := owner.do("POST", "/cdn/cloudflare", map[string]any{"token": token, "domain": "cdn.other.invalid"}); code != 502 || !strings.Contains(out["error"].(string), "not in this Cloudflare account") {
		t.Errorf("unknown zone: %d %v", code, out)
	}
	code, out = owner.do("POST", "/cdn/cloudflare", map[string]any{"token": token, "domain": "cdn.dunvex.invalid", "ssl": true, "websockets": true})
	if code != 200 || out["result"].(map[string]any)["done"] != true {
		t.Fatalf("setup: %d %v", code, out)
	}
	if len(records) != 1 || records[0]["content"] != serverIP || records[0]["proxied"] != true || records[0]["name"] != "cdn.dunvex.invalid" {
		t.Fatalf("record: %v", records)
	}
	if s := out["state"].(map[string]any)["settings"].(map[string]any); s["enabled"] != true || s["domain"] != "cdn.dunvex.invalid" {
		t.Errorf("settings after setup: %v", s)
	}
	for _, a := range auth {
		if a != "Bearer "+token {
			t.Fatalf("auth header %q", a)
		}
	}
	st.Close()
	for _, f := range []string{"t.db", "t.db-wal"} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil && strings.Contains(string(b), token) {
			t.Fatalf("the token was stored in %s", f)
		}
	}
}
