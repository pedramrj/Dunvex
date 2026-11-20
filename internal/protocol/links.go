package protocol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// Remark builds the display name of a config in client apps.
func Remark(in *store.Inbound, p *Preset, u *store.User, title string) string {
	r := in.Remark
	if r == "" {
		r = p.Name
	}
	if title != "" {
		r = title + " · " + r
	}
	return r
}

func hostPort(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }

// Link returns the share URI of an inbound for one user ("" when the protocol has no standard URI).
func Link(p *Preset, in *store.Inbound, u *store.User, env Env, remark string) string {
	host := LinkHost(in, env)
	frag := "#" + url.PathEscape(remark)
	q := url.Values{}
	addTransport := func() {
		switch p.transport {
		case "", "raw":
			q.Set("type", "tcp")
		case "xhttp":
			q.Set("type", "xhttp")
			q.Set("path", in.Str("path"))
			q.Set("mode", or(in.Str("mode"), "auto"))
			if h := in.Str("host"); h != "" {
				q.Set("host", h)
			}
		case "grpc":
			q.Set("type", "grpc")
			q.Set("serviceName", in.Str("serviceName"))
			q.Set("mode", "gun")
		case "ws", "httpupgrade":
			q.Set("type", p.transport)
			q.Set("path", in.Str("path"))
			if h := in.Str("host"); h != "" {
				q.Set("host", h)
			} else if env.Domain != "" {
				q.Set("host", env.Domain)
			}
		case "kcp":
			q.Set("type", "kcp")
		case "xdns":
			q.Set("type", "kcp")
			q.Set("fm", xdnsMask(in, env))
		}
	}
	addSecurity := func() {
		switch p.Security {
		case "reality":
			q.Set("security", "reality")
			q.Set("sni", in.Str("sni"))
			q.Set("fp", or(in.Str("fp"), "chrome"))
			q.Set("pbk", in.Str("publicKey"))
			q.Set("sid", in.Str("shortId"))
			q.Set("spx", "/")
		case "tls":
			q.Set("security", "tls")
			q.Set("sni", sni(in, env))
			q.Set("fp", or(in.Str("fp"), "chrome"))
			switch p.transport {
			case "grpc":
				q.Set("alpn", "h2")
			case "xhttp":
				q.Set("alpn", "h2,http/1.1")
			case "ws", "httpupgrade":
				q.Set("alpn", "http/1.1")
			}
			if env.SelfSigned && env.CertSHA256 != "" {
				// Xray 26 removed allowInsecure: clients pin the certificate instead
				q.Set("pcs", env.CertSHA256)
			}
		default:
			q.Set("security", "none")
		}
	}

	switch p.proto {
	case "vless":
		q.Set("encryption", "none")
		if p.ID == "vless-enc-xhttp" {
			q.Set("encryption", in.Str("encryption"))
		}
		if p.flow != "" {
			q.Set("flow", p.flow)
		}
		addTransport()
		addSecurity()
		if p.transport == "xdns" {
			h, port := xdnsTarget(in, env)
			return "vless://" + u.UUID + "@" + hostPort(h, port) + "?" + q.Encode() + frag
		}
		return "vless://" + u.UUID + "@" + hostPort(host, in.Port) + "?" + q.Encode() + frag
	case "amneziawg":
		// not a URI: the .conf the AmneziaWG apps import (or scan as a QR)
		return AWGConfig(in, u, env)
	case "mieru":
		return mieruLink(p, in, u, env, remark)
	case "trusttunnel":
		return ttLink(in, u, env, remark)
	case "dnstt", "slipstream":
		// a SlipNet profile; DNSTT.XYZ takes the same details typed in (TunnelDetails)
		return slipnetLink(p, in, u, env, remark)
	case "mtproto":
		// opens straight in Telegram; one secret for everyone
		q.Set("server", host)
		q.Set("port", strconv.Itoa(in.Port))
		q.Set("secret", MTProtoSecret(in.Str("mtKey"), in.Str("sni")))
		return "tg://proxy?" + q.Encode()
	case "trojan":
		addTransport()
		addSecurity()
		return "trojan://" + url.PathEscape(u.Password) + "@" + hostPort(host, in.Port) + "?" + q.Encode() + frag
	case "vmess":
		v := obj{"v": "2", "ps": remark, "add": host, "port": strconv.Itoa(in.Port), "id": u.UUID, "aid": "0",
			"scy": "auto", "net": p.transport, "type": "none", "path": in.Str("path"), "host": in.Str("host"), "tls": ""}
		if p.transport == "ws" && in.Str("host") == "" && env.Domain != "" {
			v["host"] = env.Domain
		}
		if p.Security == "tls" {
			v["tls"] = "tls"
			v["sni"] = sni(in, env)
			v["fp"] = or(in.Str("fp"), "chrome")
			v["alpn"] = "http/1.1"
			if env.SelfSigned {
				v["allowInsecure"] = true
				v["pcs"] = env.CertSHA256
			}
		}
		b, _ := json.Marshal(v)
		return "vmess://" + base64.StdEncoding.EncodeToString(b)
	case "shadowsocks":
		method := in.Str("method")
		pass := u.Password
		if strings.HasPrefix(method, "2022-") {
			pass = in.Str("serverKey") + ":" + util.SSKeyFrom(u.UUID, ssKeyLen(method))
			// SIP002: 2022 ciphers keep userinfo percent-encoded instead of base64.
			return "ss://" + url.PathEscape(method+":"+pass) + "@" + hostPort(host, in.Port) + frag
		}
		ui := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + pass))
		return "ss://" + ui + "@" + hostPort(host, in.Port) + frag
	case "hysteria", "hysteria2":
		q.Set("sni", sni(in, env))
		if env.SelfSigned {
			q.Set("insecure", "1")
			if env.CertSHA256 != "" {
				q.Set("pinSHA256", env.CertSHA256)
			}
		}
		if in.Bool("obfs") {
			q.Set("obfs", "salamander")
			q.Set("obfs-password", in.Str("obfsPassword"))
		}
		return "hysteria2://" + url.PathEscape(u.Password) + "@" + hostPort(host, in.Port) + "/?" + q.Encode() + frag
	case "tuic":
		q.Set("sni", sni(in, env))
		q.Set("alpn", "h3")
		q.Set("congestion_control", or(in.Str("congestion"), "bbr"))
		q.Set("udp_relay_mode", "native")
		if env.SelfSigned {
			q.Set("allow_insecure", "1")
			q.Set("insecure", "1")
		}
		return "tuic://" + u.UUID + ":" + url.PathEscape(u.Password) + "@" + hostPort(host, in.Port) + "?" + q.Encode() + frag
	case "anytls":
		q.Set("sni", sni(in, env))
		if env.SelfSigned {
			q.Set("insecure", "1")
		}
		return "anytls://" + url.PathEscape(u.Password) + "@" + hostPort(host, in.Port) + "/?" + q.Encode() + frag
	case "naive":
		return "naive+https://" + url.PathEscape(u.Name) + ":" + url.PathEscape(u.Password) + "@" + hostPort(sni(in, env), in.Port) + frag
	case "wireguard":
		q.Set("publickey", in.Str("publicKey"))
		q.Set("address", wgAddress(u)+"/32")
		q.Set("mtu", strconv.Itoa(max(in.Int("mtu"), 1280)))
		return "wireguard://" + url.PathEscape(u.WGPrivate) + "@" + hostPort(host, in.Port) + "?" + q.Encode() + frag
	case "socks":
		return "socks://" + base64.StdEncoding.EncodeToString([]byte(u.Name+":"+u.Password)) + "@" + hostPort(host, in.Port) + frag
	case "http":
		return "http://" + url.PathEscape(u.Name) + ":" + url.PathEscape(u.Password) + "@" + hostPort(host, in.Port) + frag
	}
	return ""
}

// --------------------------------------------------------------------------- Clash Meta (mihomo)

// Clash returns a mihomo proxy entry, or nil when unsupported.
func Clash(p *Preset, in *store.Inbound, u *store.User, env Env, name string) obj {
	host := LinkHost(in, env)
	c := obj{"name": name, "server": host, "port": in.Port, "udp": true}
	tlsOpts := func() {
		switch p.Security {
		case "reality":
			c["tls"] = true
			c["servername"] = in.Str("sni")
			c["client-fingerprint"] = or(in.Str("fp"), "chrome")
			c["reality-opts"] = obj{"public-key": in.Str("publicKey"), "short-id": in.Str("shortId")}
		case "tls":
			c["tls"] = true
			c["servername"] = sni(in, env)
			c["client-fingerprint"] = or(in.Str("fp"), "chrome")
			if env.SelfSigned {
				c["skip-cert-verify"] = true
			}
		}
	}
	netOpts := func() bool {
		switch p.transport {
		case "", "raw":
			c["network"] = "tcp"
		case "grpc":
			c["network"] = "grpc"
			c["grpc-opts"] = obj{"grpc-service-name": in.Str("serviceName")}
		case "ws", "httpupgrade":
			c["network"] = "ws"
			h := or(in.Str("host"), env.Domain)
			wo := obj{"path": in.Str("path")}
			if h != "" {
				wo["headers"] = obj{"Host": h}
			}
			if p.transport == "httpupgrade" {
				wo["v2ray-http-upgrade"] = true
			}
			c["ws-opts"] = wo
		default: // xhttp and kcp are left out: not every mihomo build understands them
			return false
		}
		return true
	}
	switch p.proto {
	case "vless":
		c["type"], c["uuid"] = "vless", u.UUID
		if p.flow != "" {
			c["flow"] = p.flow
		}
		if !netOpts() {
			return nil
		}
		tlsOpts()
	case "vmess":
		c["type"], c["uuid"], c["alterId"], c["cipher"] = "vmess", u.UUID, 0, "aes-128-gcm"
		if !netOpts() {
			return nil
		}
		tlsOpts()
	case "trojan":
		c["type"], c["password"] = "trojan", u.Password
		if !netOpts() {
			return nil
		}
		tlsOpts()
		if s, ok := c["servername"]; ok {
			c["sni"] = s
			delete(c, "servername")
		}
		delete(c, "tls")
	case "shadowsocks":
		method := in.Str("method")
		c["type"], c["cipher"], c["password"] = "ss", method, u.Password
		if strings.HasPrefix(method, "2022-") {
			c["password"] = in.Str("serverKey") + ":" + util.SSKeyFrom(u.UUID, ssKeyLen(method))
		}
	case "hysteria", "hysteria2":
		c["type"], c["password"], c["sni"] = "hysteria2", u.Password, sni(in, env)
		if env.SelfSigned {
			c["skip-cert-verify"] = true
		}
		if in.Bool("obfs") {
			c["obfs"], c["obfs-password"] = "salamander", in.Str("obfsPassword")
		}
	case "tuic":
		c["type"], c["uuid"], c["password"], c["sni"] = "tuic", u.UUID, u.Password, sni(in, env)
		c["alpn"] = []string{"h3"}
		c["congestion-controller"] = or(in.Str("congestion"), "bbr")
		if env.SelfSigned {
			c["skip-cert-verify"] = true
		}
	case "anytls":
		c["type"], c["password"], c["sni"] = "anytls", u.Password, sni(in, env)
		c["client-fingerprint"] = "chrome"
		if env.SelfSigned {
			c["skip-cert-verify"] = true
		}
	case "shadowtls":
		c["type"], c["cipher"] = "ss", "2022-blake3-aes-128-gcm"
		c["password"] = in.Str("ssKey") + ":" + util.SSKeyFrom(u.UUID, 16)
		c["plugin"] = "shadow-tls"
		c["client-fingerprint"] = "chrome"
		c["plugin-opts"] = obj{"host": or(in.Str("handshake"), DefaultReality), "password": u.Password, "version": 3}
	case "wireguard":
		c["type"], c["ip"], c["private-key"], c["public-key"] = "wireguard", wgAddress(u), u.WGPrivate, in.Str("publicKey")
		c["mtu"] = max(in.Int("mtu"), 1280)
	case "socks":
		c["type"], c["username"], c["password"] = "socks5", u.Name, u.Password
	case "http":
		c["type"], c["username"], c["password"] = "http", u.Name, u.Password
		delete(c, "udp")
	case "mieru":
		mieruClash(p, in, u, c)
	default:
		return nil
	}
	return c
}

// --------------------------------------------------------------------------- sing-box client

// SingboxOutbound returns a sing-box outbound (or endpoint for WireGuard) and any helper outbounds.
// kind is "outbound" or "endpoint". Returns nil when unsupported (e.g. XHTTP).
func SingboxOutbound(p *Preset, in *store.Inbound, u *store.User, env Env, tag string) (kind string, items []obj) {
	host := LinkHost(in, env)
	o := obj{"tag": tag, "server": host, "server_port": in.Port}
	tlsObj := func() obj {
		switch p.Security {
		case "reality":
			return obj{"enabled": true, "server_name": in.Str("sni"),
				"utls":    obj{"enabled": true, "fingerprint": or(in.Str("fp"), "chrome")},
				"reality": obj{"enabled": true, "public_key": in.Str("publicKey"), "short_id": in.Str("shortId")}}
		case "tls":
			t := obj{"enabled": true, "server_name": sni(in, env), "utls": obj{"enabled": true, "fingerprint": or(in.Str("fp"), "chrome")}}
			if env.SelfSigned {
				t["insecure"] = true
			}
			return t
		}
		return nil
	}
	transport := func() (obj, bool) {
		switch p.transport {
		case "", "raw":
			return nil, true
		case "grpc":
			return obj{"type": "grpc", "service_name": in.Str("serviceName")}, true
		case "ws":
			t := obj{"type": "ws", "path": in.Str("path")}
			if h := or(in.Str("host"), env.Domain); h != "" {
				t["headers"] = obj{"Host": h}
			}
			return t, true
		case "httpupgrade":
			t := obj{"type": "httpupgrade", "path": in.Str("path")}
			if h := or(in.Str("host"), env.Domain); h != "" {
				t["host"] = h
			}
			return t, true
		}
		return nil, false // xhttp / kcp are not supported by sing-box
	}
	quicTLS := func() obj {
		t := obj{"enabled": true, "server_name": sni(in, env), "alpn": []string{"h3"}}
		if env.SelfSigned {
			t["insecure"] = true
		}
		return t
	}
	switch p.proto {
	case "vless", "vmess", "trojan":
		if p.ID == "vless-enc-xhttp" {
			return "", nil
		}
		// Xray ≥ 26.9.8 REALITY servers require an X25519MLKEM768 key share that
		// sing-box clients do not send yet (SagerNet/sing-box#4520): leave them out
		// so sing-box apps only get configs that connect.
		if p.Engine == "xray" && p.Security == "reality" {
			return "", nil
		}
		tr, ok := transport()
		if !ok {
			return "", nil
		}
		o["type"] = p.proto
		switch p.proto {
		case "vless":
			o["uuid"] = u.UUID
			if p.flow != "" {
				o["flow"] = p.flow
			}
		case "vmess":
			// "auto" lets sing-box pick a mode Xray 26 refuses over TLS
			o["uuid"], o["security"], o["alter_id"] = u.UUID, "aes-128-gcm", 0
		case "trojan":
			o["password"] = u.Password
		}
		if t := tlsObj(); t != nil {
			o["tls"] = t
		}
		if tr != nil {
			o["transport"] = tr
		}
	case "shadowsocks":
		method := in.Str("method")
		o["type"], o["method"], o["password"] = "shadowsocks", method, u.Password
		if strings.HasPrefix(method, "2022-") {
			o["password"] = in.Str("serverKey") + ":" + util.SSKeyFrom(u.UUID, ssKeyLen(method))
		}
	case "hysteria", "hysteria2":
		o["type"], o["password"], o["tls"] = "hysteria2", u.Password, quicTLS()
		if in.Bool("obfs") {
			o["obfs"] = obj{"type": "salamander", "password": in.Str("obfsPassword")}
		}
	case "tuic":
		o["type"], o["uuid"], o["password"] = "tuic", u.UUID, u.Password
		o["congestion_control"] = or(in.Str("congestion"), "bbr")
		o["tls"] = quicTLS()
	case "anytls":
		o["type"], o["password"] = "anytls", u.Password
		t := obj{"enabled": true, "server_name": sni(in, env), "utls": obj{"enabled": true, "fingerprint": "chrome"}}
		if env.SelfSigned {
			t["insecure"] = true
		}
		o["tls"] = t
	case "naive":
		o["type"], o["username"], o["password"] = "naive", u.Name, u.Password
		o["server"] = sni(in, env)
		o["tls"] = obj{"enabled": true, "server_name": sni(in, env)}
	case "shadowtls":
		stTag := tag + "-stls"
		st := obj{"type": "shadowtls", "tag": stTag, "server": host, "server_port": in.Port, "version": 3, "password": u.Password,
			"tls": obj{"enabled": true, "server_name": or(in.Str("handshake"), DefaultReality), "utls": obj{"enabled": true, "fingerprint": "chrome"}}}
		ss := obj{"type": "shadowsocks", "tag": tag, "method": "2022-blake3-aes-128-gcm",
			"password": in.Str("ssKey") + ":" + util.SSKeyFrom(u.UUID, 16), "detour": stTag}
		return "outbound", []obj{ss, st}
	case "wireguard":
		ep := obj{"type": "wireguard", "tag": tag, "address": []string{wgAddress(u) + "/32"}, "private_key": u.WGPrivate,
			"mtu":   max(in.Int("mtu"), 1280),
			"peers": []obj{{"address": host, "port": in.Port, "public_key": in.Str("publicKey"), "allowed_ips": []string{"0.0.0.0/0", "::/0"}}}}
		return "endpoint", []obj{ep}
	case "socks":
		o["type"], o["username"], o["password"] = "socks", u.Name, u.Password
	case "http":
		o["type"], o["username"], o["password"] = "http", u.Name, u.Password
	default:
		return "", nil
	}
	return "outbound", []obj{o}
}

// FormatBytes is a tiny helper used in profile titles.
func FormatBytes(b int64) string { return util.HumanBytes(b) }

var _ = fmt.Sprintf
