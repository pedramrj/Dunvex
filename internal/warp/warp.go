// Package warp registers a free Cloudflare WARP device (the same API the
// 1.1.1.1 app and wgcf use) and describes it as a WireGuard outbound, so the
// server can send chosen traffic out through Cloudflare.
package warp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/util"
)

// API is Cloudflare's client API with the version the Android app 6.38.9 uses.
const API = "https://api.cloudflareclient.com/v0a5641"

// Account is one registered WARP device and its WireGuard settings.
type Account struct {
	ID         string `json:"id"`
	Token      string `json:"token"`
	PrivateKey string `json:"privateKey"`
	PeerKey    string `json:"peerKey"`
	AddressV4  string `json:"addressV4"`
	AddressV6  string `json:"addressV6"`
	Endpoint   string `json:"endpoint"` // host:port
	Reserved   []int  `json:"reserved"`
	License    string `json:"license,omitempty"`
	Type       string `json:"type"` // free, limited (WARP+), unlimited, team
	Created    int64  `json:"created"`
}

// Client talks to the WARP API.
type Client struct {
	Base string       // API (tests point it elsewhere)
	HTTP *http.Client // nil = the app's TLS 1.2 settings
}

func (c *Client) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return API
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"},
			CipherSuites: []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384},
		},
		ForceAttemptHTTP2:   false,
		TLSHandshakeTimeout: 10 * time.Second,
	}}
}

type regReply struct {
	ID      string `json:"id"`
	Token   string `json:"token"`
	Account struct {
		License string `json:"license"`
		Type    string `json:"account_type"`
	} `json:"account"`
	Config struct {
		ClientID string `json:"client_id"`
		Peers    []struct {
			PublicKey string `json:"public_key"`
			Endpoint  struct {
				Host  string `json:"host"`
				V4    string `json:"v4"`
				V6    string `json:"v6"`
				Ports []int  `json:"ports"`
			} `json:"endpoint"`
		} `json:"peers"`
		Interface struct {
			Addresses struct {
				V4 string `json:"v4"`
				V6 string `json:"v6"`
			} `json:"addresses"`
		} `json:"interface"`
	} `json:"config"`
}

func (c *Client) call(ctx context.Context, method, path, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "1.1.1.1/6.38.9-5641 (Android 16.0.0)")
	req.Header.Set("CF-Client-Version", "a-6.38.9-5641")
	req.Header.Set("Connection", "Keep-Alive")
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Cloudflare: %w", err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(data))
		var e struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if json.Unmarshal(data, &e) == nil && len(e.Errors) > 0 && e.Errors[0].Message != "" {
			msg = e.Errors[0].Message
		}
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("Cloudflare answered %d: %s", res.StatusCode, msg)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Register creates a new free WARP device.
func (c *Client) Register(ctx context.Context) (*Account, error) {
	priv, pub := util.WireGuardKeys()
	var r regReply
	err := c.call(ctx, "POST", "/reg", "", map[string]any{
		"key": pub, "install_id": "", "fcm_token": "", "tos": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"model": "PC", "serial_number": "", "os_version": "16.0.0", "key_type": "curve25519", "tunnel_type": "wireguard",
		"locale": "en_US",
	}, &r)
	if err != nil {
		return nil, err
	}
	a, err := fromReply(&r)
	if err != nil {
		return nil, err
	}
	a.PrivateKey, a.Created = priv, time.Now().Unix()
	return a, nil
}

// SetLicense binds a WARP+ key from the 1.1.1.1 app and refreshes the account type.
func (c *Client) SetLicense(ctx context.Context, a *Account, key string) error {
	key = strings.TrimSpace(key)
	if err := c.call(ctx, "PUT", "/reg/"+a.ID+"/account", a.Token, map[string]string{"license": key}, nil); err != nil {
		return err
	}
	var acc struct {
		License string `json:"license"`
		Type    string `json:"account_type"`
	}
	if err := c.call(ctx, "GET", "/reg/"+a.ID+"/account", a.Token, nil, &acc); err != nil {
		return err
	}
	a.License, a.Type = key, acc.Type
	return nil
}

// Delete removes the device from Cloudflare (best effort).
func (c *Client) Delete(ctx context.Context, a *Account) error {
	return c.call(ctx, "DELETE", "/reg/"+a.ID, a.Token, nil, nil)
}

func fromReply(r *regReply) (*Account, error) {
	if r.ID == "" || r.Token == "" || len(r.Config.Peers) == 0 || r.Config.Interface.Addresses.V4 == "" {
		return nil, errors.New("Cloudflare sent an incomplete registration")
	}
	p := r.Config.Peers[0]
	a := &Account{ID: r.ID, Token: r.Token, PeerKey: p.PublicKey, License: r.Account.License, Type: or(r.Account.Type, "free"),
		AddressV4: r.Config.Interface.Addresses.V4, AddressV6: r.Config.Interface.Addresses.V6}
	// an IP endpoint needs no DNS inside the cores; the port is the first one offered
	port := 2408
	if len(p.Endpoint.Ports) > 0 && p.Endpoint.Ports[0] > 0 {
		port = p.Endpoint.Ports[0]
	}
	host := hostOnly(p.Endpoint.V4)
	if host == "" {
		host = hostOnly(p.Endpoint.Host)
		if _, pt, err := net.SplitHostPort(p.Endpoint.Host); err == nil {
			if n, _ := strconv.Atoi(pt); n > 0 {
				port = n
			}
		}
	}
	if host == "" {
		host = hostOnly(p.Endpoint.V6)
	}
	if host == "" || a.PeerKey == "" {
		return nil, errors.New("Cloudflare sent no WireGuard peer")
	}
	a.Endpoint = net.JoinHostPort(host, strconv.Itoa(port))
	// the "reserved" bytes are the device's client id
	if id, err := base64.StdEncoding.DecodeString(r.Config.ClientID); err == nil && len(id) >= 3 {
		a.Reserved = []int{int(id[0]), int(id[1]), int(id[2])}
	} else {
		a.Reserved = []int{0, 0, 0}
	}
	return a, nil
}

func hostOnly(s string) string {
	s = strings.TrimSpace(s)
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return strings.Trim(s, "[]")
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Host is the endpoint's address and Port its port.
func (a *Account) Host() string { h, _, _ := net.SplitHostPort(a.Endpoint); return h }

// Port is the endpoint's port.
func (a *Account) Port() int {
	_, p, _ := net.SplitHostPort(a.Endpoint)
	n, _ := strconv.Atoi(p)
	return n
}

// Addresses are the tunnel's own addresses with their prefixes.
func (a *Account) Addresses() []string {
	out := []string{a.AddressV4 + "/32"}
	if a.AddressV6 != "" {
		out = append(out, a.AddressV6+"/128")
	}
	return out
}

// XrayOutbound is the account as an Xray WireGuard outbound.
func (a *Account) XrayOutbound(tag string) map[string]any {
	return map[string]any{"protocol": "wireguard", "tag": tag, "settings": map[string]any{
		"secretKey": a.PrivateKey, "address": a.Addresses(), "mtu": 1280, "reserved": a.Reserved,
		"noKernelTun": true, // user space: no interface or routing table on the server
		"peers":       []map[string]any{{"publicKey": a.PeerKey, "endpoint": a.Endpoint, "allowedIPs": []string{"0.0.0.0/0", "::/0"}}},
	}}
}

// SingboxEndpoint is the account as a sing-box WireGuard endpoint.
func (a *Account) SingboxEndpoint(tag string) map[string]any {
	return map[string]any{"type": "wireguard", "tag": tag, "system": false, "mtu": 1280, "address": a.Addresses(),
		"private_key": a.PrivateKey,
		"peers": []map[string]any{{"address": a.Host(), "port": a.Port(), "public_key": a.PeerKey,
			"allowed_ips": []string{"0.0.0.0/0", "::/0"}, "reserved": a.Reserved}},
	}
}
