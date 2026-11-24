package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/cdn"
	"github.com/pedramrj/dunvex/internal/protocol"
)

// The CDN's ECH: configs through the CDN carry the ECH config the CDN
// publishes in the DNS HTTPS record of the CDN name (Cloudflare's, public name
// cloudflare-ech.com: a filter sees that name instead of yours). The server
// asks public DNS over HTTPS for it, from outside the filtered network, so the
// apps need no DNS lookup of their own. A name on Cloudflare without a record
// of its own (ECH off in its zone) gets Cloudflare's shared one, as Hiddify
// does. The config check then shows whether the CDN takes it.

// cloudflareECHName publishes Cloudflare's shared ECH config.
const cloudflareECHName = "cloudflare-ech.com"

// How long a looked-up config is used before it is looked up again: the CDN
// changes its keys now and then, and the apps take the new one with their next
// subscription update.
const (
	echFresh = 30 * time.Minute
	echRetry = 5 * time.Minute // after a failed lookup
)

// dohServers answer DNS questions over HTTPS in JSON.
var dohServers = []string{"https://cloudflare-dns.com/dns-query", "https://dns.google/resolve"}

// echLookup finds the ECH config (base64) in name's HTTPS record; tests replace it.
var echLookup = func(ctx context.Context, name string) (string, error) {
	var last error = errors.New("no DNS server answered")
	for _, srv := range dohServers {
		ech, err := dohECH(ctx, srv, name)
		if err == nil {
			return ech, nil
		}
		last = err
	}
	return "", last
}

// onCloudflare reports whether name points to Cloudflare; tests replace it.
var onCloudflare = func(ctx context.Context, name string) bool {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if cdn.IsCloudflare(ip.IP) {
			return true
		}
	}
	return false
}

// dohECH asks one DNS-over-HTTPS server for name's HTTPS record. It returns
// "" without an error when the name has no ECH in it.
func dohECH(ctx context.Context, server, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", server+"?name="+url.QueryEscape(name)+"&type=HTTPS", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/dns-json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", errors.New(res.Status)
	}
	var out struct {
		Status int `json:"Status"`
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Status != 0 && out.Status != 3 { // 3: no such name
		return "", errors.New("the DNS server failed to answer")
	}
	for _, a := range out.Answer {
		if a.Type == 65 {
			if ech := echParam(a.Data); ech != "" {
				return ech, nil
			}
		}
	}
	return "", nil
}

// echParam is the "ech=" value of an HTTPS record in its text form
// (`1 . alpn=h3,h2 ipv4hint=104.18.10.118 ech=AEX+DQ...`), "" when it has none
// or it is not an ECH config list.
func echParam(data string) string {
	for _, f := range strings.Fields(data) {
		if v, ok := strings.CutPrefix(f, "ech="); ok {
			v = strings.Trim(v, `"`)
			if _, err := protocol.ECHPublicName(v); err == nil {
				return v
			}
		}
	}
	return ""
}

type cdnECHEntry struct {
	at   time.Time
	ech  *protocol.ECH
	from string // the name whose record it came from
	busy bool   // a refresh is running
}

var (
	cdnECHMu    sync.Mutex
	cdnECHCache = map[string]*cdnECHEntry{}
)

// cdnECH is the CDN's ECH for the CDN name (nil when there is none) and the
// name it was published under. The first call waits for the lookup; later
// ones get the kept one at once and refresh it behind the scenes when it is old.
func cdnECH(domain string) (*protocol.ECH, string) {
	cdnECHMu.Lock()
	e := cdnECHCache[domain]
	if e != nil {
		stale := time.Since(e.at) > echFresh || (e.ech == nil && time.Since(e.at) > echRetry)
		if stale && !e.busy {
			e.busy = true
			go refreshCDNECH(domain)
		}
		ech, from := e.ech, e.from
		cdnECHMu.Unlock()
		return ech, from
	}
	cdnECHMu.Unlock()
	refreshCDNECH(domain)
	cdnECHMu.Lock()
	defer cdnECHMu.Unlock()
	if e := cdnECHCache[domain]; e != nil {
		return e.ech, e.from
	}
	return nil, ""
}

// refreshCDNECH looks the config up again.
func refreshCDNECH(domain string) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	from := domain
	cfg, err := echLookup(ctx, domain)
	if err == nil && cfg == "" && onCloudflare(ctx, domain) {
		from = cloudflareECHName
		cfg, err = echLookup(ctx, cloudflareECHName)
	}
	cdnECHMu.Lock()
	defer cdnECHMu.Unlock()
	old := cdnECHCache[domain]
	e := &cdnECHEntry{at: time.Now()}
	switch {
	case err == nil && cfg != "":
		e.ech, e.from = &protocol.ECH{Config: cfg}, from
	case err != nil && old != nil && old.ech != nil:
		e.ech, e.from = old.ech, old.from // DNS failed for a while: keep the last one
	}
	cdnECHCache[domain] = e
}

// forgetCDNECH drops the kept configs (the CDN name or the setting changed).
func forgetCDNECH() {
	cdnECHMu.Lock()
	cdnECHCache = map[string]*cdnECHEntry{}
	cdnECHMu.Unlock()
}
