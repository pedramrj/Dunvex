package warp

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// TraceInfo is what https://www.cloudflare.com/cdn-cgi/trace reports.
type TraceInfo struct {
	IP   string `json:"ip"`
	Loc  string `json:"loc"`
	Colo string `json:"colo"`
	Warp string `json:"warp"` // off, on or plus
}

// Trace loads Cloudflare's trace page, through a local SOCKS5 proxy when given.
func Trace(ctx context.Context, socks string) (*TraceInfo, error) {
	tr := &http.Transport{TLSHandshakeTimeout: 10 * time.Second}
	if socks != "" {
		d, err := proxy.SOCKS5("tcp", socks, nil, &net.Dialer{Timeout: 5 * time.Second})
		if err != nil {
			return nil, err
		}
		cd, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("no SOCKS dialer")
		}
		tr.DialContext = cd.DialContext
	}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.cloudflare.com/cdn-cgi/trace", nil)
	res, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			return nil, errors.New("Xray is not running")
		}
		return nil, err
	}
	defer res.Body.Close()
	t := &TraceInfo{}
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		switch k {
		case "ip":
			t.IP = v
		case "loc":
			t.Loc = v
		case "colo":
			t.Colo = v
		case "warp":
			t.Warp = v
		}
	}
	if t.IP == "" {
		return nil, errors.New("unexpected answer from Cloudflare")
	}
	return t, nil
}
