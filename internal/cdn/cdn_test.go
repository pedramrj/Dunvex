package cdn

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestClean(t *testing.T) {
	s := Settings{Domain: " CDN.Example.com. ", Addresses: []Address{
		{Address: "104.16.1.1", Name: "  MCI  ok "}, {Address: "[2606:4700::1]"}, {Address: "Speed.Cloudflare.com"}, {Address: "104.16.1.1"}, {Address: ""},
	}}
	if err := s.Clean(); err != nil {
		t.Fatal(err)
	}
	if s.Domain != "cdn.example.com" || len(s.Addresses) != 3 || s.Addresses[0].Name != "MCI ok" || s.Addresses[1].Address != "2606:4700::1" || s.Addresses[2].Address != "speed.cloudflare.com" {
		t.Fatalf("%+v", s)
	}
	for _, bad := range []Settings{{Domain: "not a domain"}, {Addresses: []Address{{Address: "1.2.3"}}}, {Addresses: []Address{{Address: "a b.com"}}}} {
		if err := bad.Clean(); err == nil {
			t.Errorf("%+v passed", bad)
		}
	}
	if (Settings{}).DomainFor("Panel.example.com") != "panel.example.com" || (Settings{Domain: "cdn.x.com"}).DomainFor("p.x.com") != "cdn.x.com" {
		t.Fatal("domain")
	}
}

func TestCloudflareRangesAndPorts(t *testing.T) {
	for ip, want := range map[string]bool{"104.16.132.229": true, "172.67.1.1": true, "2606:4700::6810:84e5": true, "8.8.8.8": false, "91.107.138.242": false} {
		if IsCloudflare(net.ParseIP(ip)) != want {
			t.Errorf("%s: want %v", ip, want)
		}
	}
	if !ProxiedPort(2053, true) || ProxiedPort(2052, true) || !ProxiedPort(8080, false) || ProxiedPort(8443, false) {
		t.Fatal("ports")
	}
}

func dialOf(ts *httptest.Server) string {
	return strings.TrimPrefix(strings.TrimPrefix(ts.URL, "https://"), "http://")
}

func TestProbe(t *testing.T) {
	var mu sync.Mutex
	var seen []*http.Request
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r)
		mu.Unlock()
		switch {
		case r.URL.Path == "/ws" && r.Header.Get("Upgrade") == "websocket":
			w.WriteHeader(http.StatusSwitchingProtocols)
		case strings.HasPrefix(r.URL.Path, "/xh/") && len(r.URL.Query().Get("x_padding")) >= 100:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done() // a download streams until the client leaves
		case r.URL.Path == "/svc/Tun" && r.Header.Get("Content-Type") == "application/grpc":
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Grpc-Status", "12")
			w.WriteHeader(200)
		case r.URL.Path == "/down":
			w.Header().Set("Server", "cloudflare")
			w.Header().Set("CF-Ray", "8f1e2d3c4b5a6978-FRA")
			w.WriteHeader(522)
			io.WriteString(w, "<html>... error code: 522 ...</html>")
		default:
			http.NotFound(w, r)
		}
	})
	tlsSrv := httptest.NewUnstartedServer(h)
	tlsSrv.EnableHTTP2 = true
	tlsSrv.StartTLS()
	defer tlsSrv.Close()
	plain := httptest.NewServer(h)
	defer plain.Close()
	ctx := context.Background()

	for _, c := range []struct {
		name string
		t    Target
		code string
	}{
		{"ws over TLS", Target{Transport: "ws", TLS: true, Dial: dialOf(tlsSrv), Host: "cdn.example.com", Path: "/ws", Insecure: true}, "ok"},
		{"httpupgrade", Target{Transport: "httpupgrade", Dial: dialOf(plain), Host: "cdn.example.com", Path: "ws"}, "ok"},
		{"xhttp", Target{Transport: "xhttp", TLS: true, Dial: dialOf(tlsSrv), Host: "cdn.example.com", Path: "/xh", Insecure: true}, "ok"},
		{"grpc", Target{Transport: "grpc", TLS: true, Dial: dialOf(tlsSrv), Host: "cdn.example.com", Service: "svc", Insecure: true}, "ok"},
		{"wrong path", Target{Transport: "ws", TLS: true, Dial: dialOf(tlsSrv), Host: "cdn.example.com", Path: "/nope", Insecure: true}, "status"},
		{"certificate checked", Target{Transport: "ws", TLS: true, Dial: dialOf(tlsSrv), Host: "cdn.example.com", Path: "/ws"}, "cert"},
		{"grpc needs h2", Target{Transport: "grpc", TLS: false, Dial: dialOf(plain), Host: "x.com", Service: "svc"}, "h2"},
		{"nothing listening", Target{Transport: "ws", Dial: "127.0.0.1:1", Host: "x.com", Path: "/"}, "refused"},
	} {
		r := Probe(ctx, c.t)
		if r.Code != c.code || r.OK != (c.code == "ok") {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
	r := Probe(ctx, Target{Transport: "ws", Dial: dialOf(plain), Host: "cdn.example.com", Path: "/down"})
	if r.Status != 522 || r.CFCode != 522 || r.Server != "cloudflare" || r.Colo != "FRA" {
		t.Fatalf("cloudflare error: %+v", r)
	}
	for _, rq := range seen {
		if rq.Host != "cdn.example.com" && rq.Host != "x.com" {
			t.Errorf("Host %q", rq.Host)
		}
	}
}

// fakeCF is a small stand-in for Cloudflare's API.
type fakeCF struct {
	mu       sync.Mutex
	records  []Record
	ssl, ws  string
	calls    []string
	nextID   int
	zoneName string
	status   string
}

func (f *fakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	ok := func(v any) {
		json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": v})
	}
	if r.Header.Get("Authorization") != "Bearer good-token" {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"code": 9109, "message": "Invalid access token"}}})
		return
	}
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	p := strings.TrimPrefix(r.URL.Path, "/client/v4")
	switch {
	case p == "/zones":
		if r.URL.Query().Get("name") == f.zoneName {
			ok([]Zone{{ID: "z1", Name: f.zoneName, Status: f.status}})
		} else {
			ok([]Zone{})
		}
	case p == "/zones/z1/dns_records" && r.Method == "GET":
		var out []Record
		for _, rec := range f.records {
			if rec.Name == r.URL.Query().Get("name") {
				out = append(out, rec)
			}
		}
		ok(out)
	case p == "/zones/z1/dns_records" && r.Method == "POST":
		f.nextID++
		rec := Record{ID: "r" + string(rune('0'+f.nextID)), Type: body["type"].(string), Name: body["name"].(string), Content: body["content"].(string), Proxied: body["proxied"].(bool)}
		f.records = append(f.records, rec)
		ok(rec)
	case strings.HasPrefix(p, "/zones/z1/dns_records/"):
		id := strings.TrimPrefix(p, "/zones/z1/dns_records/")
		for i := range f.records {
			if f.records[i].ID != id {
				continue
			}
			if r.Method == "DELETE" {
				f.records = append(f.records[:i], f.records[i+1:]...)
			} else {
				if c, ok := body["content"].(string); ok {
					f.records[i].Content = c
				}
				if pr, ok := body["proxied"].(bool); ok {
					f.records[i].Proxied = pr
				}
			}
			ok(map[string]string{"id": id})
			return
		}
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"code": 81044, "message": "Record not found"}}})
	case p == "/zones/z1/settings/ssl" || p == "/zones/z1/settings/websockets":
		v := &f.ssl
		if strings.HasSuffix(p, "websockets") {
			v = &f.ws
		}
		if r.Method == "PATCH" {
			*v = body["value"].(string)
		}
		ok(map[string]string{"value": *v})
	default:
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"code": 7003, "message": "No route"}}})
	}
}

func TestCloudflareSetup(t *testing.T) {
	f := &fakeCF{zoneName: "example.com", status: "active", ssl: "strict", ws: "off",
		records: []Record{{ID: "a1", Type: "A", Name: "cdn.example.com", Content: "5.6.7.8"}, {ID: "t1", Type: "TXT", Name: "cdn.example.com", Content: "hello"}, {ID: "w1", Type: "A", Name: "www.example.com", Content: "9.9.9.9"}}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	ctx := context.Background()
	o := SetupOptions{Domain: "cdn.example.com", IP: "1.2.3.4", SSL: true, SelfSigned: true, WebSockets: true}

	if _, err := (&Cloudflare{Base: srv.URL + "/client/v4", Token: "bad"}).Setup(ctx, o, false); err == nil || !strings.Contains(err.Error(), "Invalid access token") {
		t.Fatalf("bad token: %v", err)
	}
	cf := &Cloudflare{Base: srv.URL + "/client/v4", Token: "good-token"}
	// an A record that points elsewhere: reported, nothing changed
	res, err := cf.Setup(ctx, o, false)
	if err != nil || res.Done || len(res.Conflicts) != 1 || res.Conflicts[0].Content != "5.6.7.8" || f.records[0].Content != "5.6.7.8" {
		t.Fatalf("conflict: %+v %v", res, err)
	}
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET") {
			t.Fatalf("changed something without Replace: %v", f.calls)
		}
	}
	// replaced: the record is repointed and proxied, Full replaces Full (strict), WebSockets on
	o.Replace = true
	if res, err = cf.Setup(ctx, o, false); err != nil || !res.Done || res.Zone != "example.com" {
		t.Fatalf("setup: %+v %v", res, err)
	}
	if r := f.records[0]; r.Content != "1.2.3.4" || !r.Proxied || len(f.records) != 3 || f.ssl != "full" || f.ws != "on" {
		t.Fatalf("after setup: %+v ssl=%s ws=%s", f.records, f.ssl, f.ws)
	}
	// run again: nothing more to change
	f.calls = nil
	if res, err = cf.Setup(ctx, o, false); err != nil || !res.Done {
		t.Fatal(res, err)
	}
	for _, s := range res.Steps {
		if s.Changed || !s.OK {
			t.Errorf("second run: %+v", s)
		}
	}
	// a new name in a zone found from a deeper name; Flexible on 443 is reported, not changed
	f.ssl = "flexible"
	o2 := SetupOptions{Domain: "a.b.example.com", IP: "1.2.3.4", SSL: true}
	if res, err = cf.Setup(ctx, o2, true); err != nil || !res.Done || f.ssl != "flexible" {
		t.Fatal(res, err)
	}
	var created bool
	for _, r := range f.records {
		created = created || (r.Name == "a.b.example.com" && r.Type == "A" && r.Proxied && r.Content == "1.2.3.4")
	}
	if !created || res.Steps[2].Warn != "flexible443" {
		t.Fatalf("new record: %+v %+v", f.records, res.Steps)
	}
	// a name outside the account
	if _, err := cf.Setup(ctx, SetupOptions{Domain: "cdn.other.org", IP: "1.2.3.4"}, false); err == nil {
		t.Fatal("unknown zone")
	}
	// a CNAME and an AAAA of the name are removed when replacing
	f.records = append(f.records, Record{ID: "c9", Type: "CNAME", Name: "x.example.com", Content: "elsewhere.net"}, Record{ID: "q9", Type: "AAAA", Name: "x.example.com", Content: "2001:db8::1"})
	if res, err = cf.Setup(ctx, SetupOptions{Domain: "x.example.com", IP: "1.2.3.4", Replace: true}, false); err != nil || !res.Done {
		t.Fatal(res, err)
	}
	var left []string
	for _, r := range f.records {
		if r.Name == "x.example.com" {
			left = append(left, r.Type+" "+r.Content)
		}
	}
	if len(left) != 1 || left[0] != "A 1.2.3.4" {
		t.Fatalf("x.example.com: %v", left)
	}
}
