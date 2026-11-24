package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/geoip"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestUserPageLanguage: Persian by default, English on request and remembered,
// compressed when the browser accepts it.
func TestUserPageLanguage(t *testing.T) {
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
	st.CreateUser(&store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(16), SubToken: "sara-token-0123456789",
		Enabled: true, ResetPeriod: "none", DataLimit: 10 << 30, ExpireAt: time.Now().Add(30 * 24 * time.Hour).Unix()})

	page := func(path string, cookies ...*http.Cookie) (*http.Response, string) {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("Accept", "text/html")
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set("Accept-Encoding", "gzip") // set by hand, so the client leaves the body compressed
		for _, c := range cookies {
			req.AddCookie(c)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("%s: not compressed", path)
		}
		zr, err := gzip.NewReader(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(zr)
		return res, string(b)
	}

	_, fa := page("/sub/sara-token-0123456789")
	if !strings.Contains(fa, `<html lang="fa" dir="rtl">`) || !strings.Contains(fa, "باقی‌مانده") || !strings.Contains(fa, "۱۰ گیگ") {
		t.Fatal("default page is not Persian")
	}
	res, en := page("/sub/sara-token-0123456789?lang=en")
	if !strings.Contains(en, `<html lang="en" dir="ltr">`) || !strings.Contains(en, "remaining") || !strings.Contains(en, "10 GB") || strings.Contains(en, "باقی‌مانده") {
		t.Fatal("?lang=en did not give the English page")
	}
	var kept *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == pageLangCookie {
			kept = c
		}
	}
	if kept == nil || kept.Value != "en" {
		t.Fatal("the choice is not remembered")
	}
	if _, again := page("/sub/sara-token-0123456789", kept); !strings.Contains(again, `lang="en"`) {
		t.Fatal("the remembered choice is not used")
	}
}

// TestPageMap: the map carries places rounded to the city and server names,
// never an address, and joins devices in one city on the same servers.
func TestPageMap(t *testing.T) {
	srv := &Server{srvCache: []mapServer{{ID: 0, Name: "Frankfurt", Place: &geoip.Place{Lat: 50.11, Lon: 8.68}, ip: "198.51.100.7"}}, srvAt: time.Now()}
	views := []deviceView{
		{Device: engine.Device{IP: "5.120.33.7", Via: []int64{0}}, where: where{Place: &geoip.Place{City: "Tehran", Lat: 35.6892, Lon: 51.389}}},
		{Device: engine.Device{IP: "5.120.33.8", Via: []int64{0}}, where: where{Place: &geoip.Place{City: "Tehran", Lat: 35.6892, Lon: 51.389}}},
		{Device: engine.Device{IP: "10.0.0.1"}}, // no place: not on the map
	}
	m := srv.pageMapFor(views)
	if m == nil || len(m.Devices) != 1 || len(m.Servers) != 1 {
		t.Fatalf("map: %+v", m)
	}
	if d := m.Devices[0]; d.Lat != 35.7 || d.Lon != 51.4 || d.Name != "Tehran" || len(d.Servers) != 1 {
		t.Errorf("device: %+v", d)
	}
	if s := m.Servers[0]; s.Name != "Frankfurt" || s.Lat != 50.1 {
		t.Errorf("server: %+v", s)
	}
	if srv.pageMapFor(views[2:]) != nil {
		t.Error("a map with no placed device")
	}
}
