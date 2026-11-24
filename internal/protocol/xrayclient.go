package protocol

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ErrNoXrayClient: the link is not one an Xray client can open.
var ErrNoXrayClient = errors.New("an Xray client cannot open this link")

// XrayOutbound reads a share link the way an Xray app (v2rayNG, v2rayN,
// Hiddify) does and returns the outbound it would build, tagged tag. It knows
// the links Dunvex makes for its Xray protocols: vless, vmess, trojan, ss,
// hysteria2, wireguard, socks and http. The config health check runs these
// to test the exact link a user imports.
func XrayOutbound(link, tag string) (map[string]any, error) {
	link = strings.TrimSpace(link)
	scheme, _, ok := strings.Cut(link, "://")
	if !ok {
		return nil, ErrNoXrayClient
	}
	var (
		o   obj
		err error
	)
	switch strings.ToLower(scheme) {
	case "vless":
		o, err = xrayVLESS(link)
	case "vmess":
		o, err = xrayVMess(link)
	case "trojan":
		o, err = xrayTrojan(link)
	case "ss":
		o, err = xraySS(link)
	case "hysteria2", "hy2":
		o, err = xrayHysteria(link)
	case "wireguard", "wg":
		o, err = xrayWireGuard(link)
	case "socks", "socks5":
		o, err = xraySocks(link)
	case "http", "https":
		o, err = xrayHTTP(link)
	default:
		return nil, ErrNoXrayClient
	}
	if err != nil {
		return nil, err
	}
	o["tag"] = tag
	return o, nil
}

// privateNets never go through the VPN (no geo files needed for them).
var privateNets = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "169.254.0.0/16",
	"100.64.0.0/10", "fc00::/7", "fe80::/10", "::1/128"}

// XrayClientConfig is one whole config of the Xray JSON subscription, in the
// form v2rayNG, v2rayN and Streisand import: SOCKS on 10808 and HTTP on
// 10809 (where those apps send the device's traffic), proxy as the way out,
// local networks direct. directDomains and directIPs (Xray rule syntax) go
// straight from the device too, resolved by its own DNS. No geo files are
// named, so it runs whatever files the app has.
func XrayClientConfig(remarks string, proxy map[string]any, directDomains, directIPs []string) map[string]any {
	sniff := obj{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}
	rules := []obj{{"type": "field", "ip": privateNets, "outboundTag": "direct"}}
	servers := []any{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"}
	if len(directDomains) > 0 {
		rules = append(rules, obj{"type": "field", "domain": directDomains, "outboundTag": "direct"})
		servers = append(servers, obj{"address": "localhost", "domains": directDomains, "skipFallback": true})
	}
	if len(directIPs) > 0 {
		rules = append(rules, obj{"type": "field", "ip": directIPs, "outboundTag": "direct"})
	}
	return obj{
		"remarks": remarks,
		"log":     obj{"loglevel": "warning"},
		"dns":     obj{"servers": servers, "queryStrategy": "UseIP"},
		"inbounds": []obj{
			{"tag": "socks", "listen": "127.0.0.1", "port": 10808, "protocol": "socks", "settings": obj{"auth": "noauth", "udp": true}, "sniffing": sniff},
			{"tag": "http", "listen": "127.0.0.1", "port": 10809, "protocol": "http", "sniffing": sniff},
		},
		"outbounds": []obj{proxy, {"tag": "direct", "protocol": "freedom"}},
		"routing":   obj{"domainStrategy": "AsIs", "rules": rules},
	}
}

// linkAddr is the server address and port of a parsed link.
func linkAddr(u *url.URL) (string, int, error) {
	host, ps := u.Hostname(), u.Port()
	port, err := strconv.Atoi(ps)
	if host == "" || err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("the link has no server address and port")
	}
	return host, port, nil
}

func xrayVLESS(link string) (obj, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	host, port, err := linkAddr(u)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	user := obj{"id": u.User.Username(), "encryption": or(q.Get("encryption"), "none")}
	if f := q.Get("flow"); f != "" {
		user["flow"] = f
	}
	stream, err := xrayStream(q)
	if err != nil {
		return nil, err
	}
	return obj{"protocol": "vless", "settings": obj{"vnext": []obj{{"address": host, "port": port, "users": []obj{user}}}},
		"streamSettings": stream}, nil
}

func xrayTrojan(link string) (obj, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	host, port, err := linkAddr(u)
	if err != nil {
		return nil, err
	}
	stream, err := xrayStream(u.Query())
	if err != nil {
		return nil, err
	}
	return obj{"protocol": "trojan", "settings": obj{"servers": []obj{{"address": host, "port": port, "password": u.User.Username()}}},
		"streamSettings": stream}, nil
}

// xrayVMess reads the base64 JSON of a vmess:// link (v2rayN's format).
func xrayVMess(link string) (obj, error) {
	raw := link[strings.Index(link, "://")+3:]
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	b, err := decodeB64(raw)
	if err != nil {
		return nil, fmt.Errorf("vmess link: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("vmess link: %v", err)
	}
	str := func(k string) string {
		switch x := v[k].(type) {
		case string:
			return x
		case float64:
			return strconv.Itoa(int(x))
		case bool:
			if x {
				return "1"
			}
		}
		return ""
	}
	port, err := strconv.Atoi(str("port"))
	if err != nil || str("add") == "" {
		return nil, fmt.Errorf("the link has no server address and port")
	}
	// the same details a vless link carries in its query
	q := url.Values{}
	q.Set("type", or(str("net"), "tcp"))
	q.Set("path", str("path"))
	q.Set("host", str("host"))
	if str("tls") == "tls" {
		q.Set("security", "tls")
		q.Set("sni", str("sni"))
		q.Set("fp", str("fp"))
		q.Set("alpn", str("alpn"))
		q.Set("pcs", str("pcs"))
	}
	stream, err := xrayStream(q)
	if err != nil {
		return nil, err
	}
	user := obj{"id": str("id"), "alterId": 0, "security": or(str("scy"), "auto")}
	return obj{"protocol": "vmess", "settings": obj{"vnext": []obj{{"address": str("add"), "port": port, "users": []obj{user}}}},
		"streamSettings": stream}, nil
}

// xraySS reads both SIP002 forms: base64 "method:password", and the
// percent-encoded userinfo of the 2022 ciphers.
func xraySS(link string) (obj, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	host, port, err := linkAddr(u)
	if err != nil {
		return nil, err
	}
	method, pass := "", ""
	if p, ok := u.User.Password(); ok {
		method, pass = u.User.Username(), p
	} else {
		b, err := decodeB64(u.User.Username())
		if err != nil {
			return nil, fmt.Errorf("shadowsocks link: %v", err)
		}
		method, pass, _ = strings.Cut(string(b), ":")
	}
	if method == "" || pass == "" {
		return nil, fmt.Errorf("shadowsocks link without its method and password")
	}
	return obj{"protocol": "shadowsocks", "settings": obj{"servers": []obj{{"address": host, "port": port, "method": method, "password": pass}}}}, nil
}

func xrayHysteria(link string) (obj, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	host, port, err := linkAddr(u)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	t := obj{"serverName": or(q.Get("sni"), host), "alpn": []string{"h3"}}
	if pin := q.Get("pinSHA256"); pin != "" {
		t["pinnedPeerCertSha256"] = pin
	} else if q.Get("insecure") == "1" {
		return nil, fmt.Errorf("a self-signed certificate without its fingerprint: Xray clients cannot check it")
	}
	stream := obj{"network": "hysteria", "hysteriaSettings": obj{"version": 2, "auth": u.User.Username()},
		"security": "tls", "tlsSettings": t}
	if q.Get("obfs") == "salamander" {
		stream["finalmask"] = obj{"udp": []obj{{"type": "salamander", "settings": obj{"password": q.Get("obfs-password")}}}}
	}
	return obj{"protocol": "hysteria", "settings": obj{"version": 2, "address": host, "port": port}, "streamSettings": stream}, nil
}

func xrayWireGuard(link string) (obj, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	host, port, err := linkAddr(u)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	if q.Get("publickey") == "" || u.User.Username() == "" {
		return nil, fmt.Errorf("wireguard link without its keys")
	}
	mtu, _ := strconv.Atoi(q.Get("mtu"))
	s := obj{"secretKey": u.User.Username(), "address": strings.Split(q.Get("address"), ","), "noKernelTun": true,
		"peers": []obj{{"publicKey": q.Get("publickey"), "endpoint": net.JoinHostPort(host, strconv.Itoa(port)), "allowedIPs": []string{"0.0.0.0/0", "::/0"}}}}
	if mtu > 0 {
		s["mtu"] = mtu
	}
	return obj{"protocol": "wireguard", "settings": s}, nil
}

// xraySocks splits the link by hand: its base64 "user:pass" (v2rayN's form)
// may hold a "/", which a URL parser takes for the start of a path.
func xraySocks(link string) (obj, error) {
	rest := link[strings.Index(link, "://")+3:]
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest = rest[:i]
	}
	cred := ""
	if i := strings.LastIndexByte(rest, '@'); i >= 0 {
		cred, rest = rest[:i], rest[i+1:]
	}
	u, err := url.Parse("socks://" + strings.TrimRight(rest, "/"))
	if err != nil {
		return nil, err
	}
	host, port, err := linkAddr(u)
	if err != nil {
		return nil, err
	}
	srv := obj{"address": host, "port": port}
	if cred != "" {
		user, pass, plain := strings.Cut(cred, ":")
		if b, err := decodeB64(cred); !plain && err == nil {
			user, pass, _ = strings.Cut(string(b), ":")
		} else if plain {
			user, _ = url.PathUnescape(user)
			pass, _ = url.PathUnescape(pass)
		}
		srv["users"] = []obj{{"user": user, "pass": pass}}
	}
	return obj{"protocol": "socks", "settings": obj{"servers": []obj{srv}}}, nil
}

func xrayHTTP(link string) (obj, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	host, port, err := linkAddr(u)
	if err != nil {
		return nil, err
	}
	srv := obj{"address": host, "port": port}
	if name := u.User.Username(); name != "" {
		pass, _ := u.User.Password()
		srv["users"] = []obj{{"user": name, "pass": pass}}
	}
	o := obj{"protocol": "http", "settings": obj{"servers": []obj{srv}}}
	if u.Scheme == "https" {
		o["streamSettings"] = obj{"network": "raw", "security": "tls", "tlsSettings": obj{"serverName": host}}
	}
	return o, nil
}

// xrayStream builds streamSettings from the query of a vless/trojan link.
func xrayStream(q url.Values) (obj, error) {
	s := obj{}
	setPathHost := func(key string) {
		w := obj{"path": or(q.Get("path"), "/")}
		if h := q.Get("host"); h != "" {
			w["host"] = h
		}
		s[key] = w
	}
	switch t := q.Get("type"); t {
	case "", "tcp", "raw":
		s["network"] = "raw"
	case "xhttp", "splithttp":
		s["network"] = "xhttp"
		x := obj{"path": or(q.Get("path"), "/"), "mode": or(q.Get("mode"), "auto")}
		if h := q.Get("host"); h != "" {
			x["host"] = h
		}
		s["xhttpSettings"] = x
	case "grpc":
		s["network"] = "grpc"
		s["grpcSettings"] = obj{"serviceName": q.Get("serviceName"), "multiMode": q.Get("mode") == "multi"}
	case "ws":
		s["network"] = "ws"
		setPathHost("wsSettings")
	case "httpupgrade":
		s["network"] = "httpupgrade"
		setPathHost("httpupgradeSettings")
	case "kcp", "mkcp":
		s["network"] = "kcp"
	default:
		return nil, fmt.Errorf("transport %q is not one Xray knows", t)
	}
	if fm := q.Get("fm"); fm != "" {
		var m any
		if err := json.Unmarshal([]byte(fm), &m); err != nil {
			return nil, fmt.Errorf("the link's finalmask (fm): %v", err)
		}
		s["finalmask"] = m
	}
	switch sec := q.Get("security"); sec {
	case "", "none":
		s["security"] = "none"
	case "reality":
		if q.Get("pbk") == "" {
			return nil, fmt.Errorf("a Reality link without its public key (pbk)")
		}
		s["security"] = "reality"
		s["realitySettings"] = obj{"serverName": q.Get("sni"), "fingerprint": or(q.Get("fp"), "chrome"),
			"publicKey": q.Get("pbk"), "shortId": q.Get("sid"), "spiderX": or(q.Get("spx"), "/")}
	case "tls":
		t := obj{"serverName": q.Get("sni"), "fingerprint": or(q.Get("fp"), "chrome")}
		if a := q.Get("alpn"); a != "" {
			t["alpn"] = strings.Split(a, ",")
		}
		if pcs := q.Get("pcs"); pcs != "" {
			t["pinnedPeerCertSha256"] = pcs
		}
		s["security"] = "tls"
		s["tlsSettings"] = t
	default:
		return nil, fmt.Errorf("security %q is not one Xray knows", sec)
	}
	return s, nil
}

// decodeB64 reads base64 in any of the encodings share links use.
func decodeB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if v, err := url.PathUnescape(s); err == nil {
		s = v
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("not base64")
}
