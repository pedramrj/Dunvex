package warp

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// fakeAPI answers like Cloudflare's client API.
const masquePeer = "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEIaHO\n-----END PUBLIC KEY-----\n"

func fakeAPI(t *testing.T) (*httptest.Server, *map[string]any) {
	var lastReg map[string]any
	enabled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Client-Version") == "" || !strings.HasPrefix(r.Header.Get("User-Agent"), "1.1.1.1/") {
			http.Error(w, `{"errors":[{"message":"unknown client"}]}`, 403)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/reg":
			json.NewDecoder(r.Body).Decode(&lastReg)
			w.Write([]byte(`{"id":"dev-1","token":"tok-1","account":{"license":"L1","account_type":"free"},
			"config":{"client_id":"AQID","peers":[{"public_key":"bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=",
			"endpoint":{"host":"engage.cloudflareclient.com:2408","v4":"162.159.192.1:0","v6":"[2606:4700:d0::a29f:c001]:0","ports":[2408,500]}}],
			"interface":{"addresses":{"v4":"172.16.0.2","v6":"2606:4700:110:8a36::1"}}}}`))
		case r.Method == "PATCH" && r.URL.Path == "/reg/dev-1":
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			if in["warp_enabled"] == true {
				enabled = true
				w.Write([]byte(`{"warp_enabled":true}`))
				return
			}
			der, _ := base64.StdEncoding.DecodeString(in["key"].(string))
			if _, err := x509.ParsePKIXPublicKey(der); err != nil || in["key_type"] != "secp256r1" || in["tunnel_type"] != "masque" {
				http.Error(w, `{"errors":[{"message":"bad key"}]}`, 400)
				return
			}
			b, _ := json.Marshal(map[string]any{"warp_enabled": enabled, "config": map[string]any{
				"peers":     []map[string]any{{"public_key": masquePeer, "endpoint": map[string]any{"v4": "162.159.198.2:0", "v6": "[2606:4700:103::2]:0"}}},
				"interface": map[string]any{"addresses": map[string]any{"v4": "172.16.0.2", "v6": "2606:4700:110:8a36::1"}}}})
			w.Write(b)
		case r.URL.Path == "/reg/dev-1/account" && r.Header.Get("Authorization") != "Bearer tok-1":
			http.Error(w, `{"errors":[{"message":"Unauthorized"}]}`, 401)
		case r.Method == "PUT" && r.URL.Path == "/reg/dev-1/account":
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if in["license"] != "ab12-cd34" {
				http.Error(w, `{"errors":[{"message":"Invalid license"}]}`, 400)
				return
			}
			w.WriteHeader(204)
		case r.Method == "GET" && r.URL.Path == "/reg/dev-1/account":
			w.Write([]byte(`{"license":"ab12-cd34","account_type":"limited"}`))
		case r.Method == "DELETE" && r.URL.Path == "/reg/dev-1":
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &lastReg
}

func TestRegister(t *testing.T) {
	srv, reg := fakeAPI(t)
	c := &Client{Base: srv.URL, HTTP: srv.Client()}
	a, err := c.Register(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if (*reg)["key_type"] != "curve25519" || (*reg)["tunnel_type"] != "wireguard" || len((*reg)["key"].(string)) != 44 {
		t.Errorf("request: %v", *reg)
	}
	if a.ID != "dev-1" || a.Token != "tok-1" || a.Type != "free" || a.AddressV4 != "172.16.0.2" || a.Created == 0 {
		t.Errorf("account: %+v", a)
	}
	// registered devices use MASQUE (HTTP/3), which usque carries
	if !a.Masque() || a.MasqueV4 != "162.159.198.2" || a.MasqueV6 != "2606:4700:103::2" || a.MasquePeer != masquePeer {
		t.Errorf("MASQUE: %+v", a)
	}
	der, _ := base64.StdEncoding.DecodeString(a.MasqueKey)
	if _, err := x509.ParseECPrivateKey(der); err != nil {
		t.Errorf("MASQUE key: %v", err)
	}
	var u map[string]string
	if err := json.Unmarshal(a.UsqueConfig(), &u); err != nil || u["private_key"] != a.MasqueKey || u["endpoint_v4"] != "162.159.198.2" ||
		u["endpoint_pub_key"] != masquePeer || u["access_token"] != "tok-1" || u["id"] != "dev-1" || u["ipv4"] != "172.16.0.2" {
		t.Errorf("usque config: %v %v", err, u)
	}
	// an IP endpoint (no DNS needed inside the cores) on the first offered port
	if a.Endpoint != "162.159.192.1:2408" || a.Host() != "162.159.192.1" || a.Port() != 2408 {
		t.Errorf("endpoint %q", a.Endpoint)
	}
	if !reflect.DeepEqual(a.Reserved, []int{1, 2, 3}) {
		t.Errorf("reserved %v (client_id AQID = 1,2,3)", a.Reserved)
	}
	if len(a.PrivateKey) != 44 {
		t.Errorf("private key %q", a.PrivateKey)
	}
	if !reflect.DeepEqual(a.Addresses(), []string{"172.16.0.2/32", "2606:4700:110:8a36::1/128"}) {
		t.Errorf("addresses %v", a.Addresses())
	}

	x := a.XrayOutbound("warp")
	xs := x["settings"].(map[string]any)
	if x["protocol"] != "wireguard" || xs["secretKey"] != a.PrivateKey || xs["noKernelTun"] != true ||
		xs["peers"].([]map[string]any)[0]["endpoint"] != "162.159.192.1:2408" {
		t.Errorf("xray: %v", x)
	}
	s := a.SingboxEndpoint("warp")
	sp := s["peers"].([]map[string]any)[0]
	if s["type"] != "wireguard" || s["private_key"] != a.PrivateKey || sp["address"] != "162.159.192.1" || sp["port"] != 2408 {
		t.Errorf("sing-box: %v", s)
	}

	if err := c.SetLicense(context.Background(), a, "wrong"); err == nil || !strings.Contains(err.Error(), "Invalid license") {
		t.Errorf("a wrong key: %v", err)
	}
	if err := c.SetLicense(context.Background(), a, " ab12-cd34 "); err != nil || a.Type != "limited" || a.License != "ab12-cd34" {
		t.Errorf("license: %v %+v", err, a)
	}
	if err := c.Delete(context.Background(), a); err != nil {
		t.Error(err)
	}
}

func TestIncompleteRegistration(t *testing.T) {
	for _, body := range []string{`{"id":"x"}`, `{"id":"x","token":"t","config":{"peers":[],"interface":{"addresses":{"v4":"172.16.0.2"}}}}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		if _, err := (&Client{Base: srv.URL, HTTP: srv.Client()}).Register(context.Background()); err == nil {
			t.Errorf("accepted %s", body)
		}
		srv.Close()
	}
}
