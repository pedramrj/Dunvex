// Package protocol defines every protocol preset Dunvex can serve and turns an
// inbound + its users into core configs, share links and client profiles.
package protocol

import (
	"encoding/base64"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/pedramrj/dunvex/internal/util"
)

// Field is one editable inbound parameter shown in the panel.
type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	LabelFa  string   `json:"labelFa"`
	Type     string   `json:"type"` // text | number | select | bool | secret
	Options  []string `json:"options,omitempty"`
	Advanced bool     `json:"advanced,omitempty"`
}

// Preset describes a ready-made protocol combination.
type Preset struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Engine      string  `json:"engine"` // xray | singbox
	Group       string  `json:"group"`  // reality | cdn | udp | classic
	Desc        string  `json:"desc"`
	DescFa      string  `json:"descFa"`
	Network     string  `json:"network"` // tcp | udp
	DefaultPort int     `json:"defaultPort"`
	Security    string  `json:"security"` // reality | tls | none
	NeedsDomain bool    `json:"needsDomain"`
	Status      string  `json:"status"` // ready | beta
	Recommended bool    `json:"recommended"`
	Fields      []Field `json:"fields"`

	// internal wiring
	proto     string // xray protocol / sing-box type
	transport string // raw | xhttp | grpc | ws | httpupgrade | kcp | hysteria
	flow      string
}

var (
	fSNI       = Field{Key: "sni", Label: "SNI", LabelFa: "SNI", Type: "text"}
	fTarget    = Field{Key: "target", Label: "Reality target", LabelFa: "مقصد Reality", Type: "text"}
	fShortID   = Field{Key: "shortId", Label: "Short ID", LabelFa: "Short ID", Type: "text", Advanced: true}
	fPriv      = Field{Key: "privateKey", Label: "Private key", LabelFa: "کلید خصوصی", Type: "secret", Advanced: true}
	fPub       = Field{Key: "publicKey", Label: "Public key", LabelFa: "کلید عمومی", Type: "text", Advanced: true}
	fFP        = Field{Key: "fp", Label: "Fingerprint", LabelFa: "اثرانگشت", Type: "select", Options: []string{"chrome", "firefox", "safari", "edge", "ios", "android", "random", "randomized"}, Advanced: true}
	fPath      = Field{Key: "path", Label: "Path", LabelFa: "مسیر", Type: "text"}
	fHost      = Field{Key: "host", Label: "Host header", LabelFa: "هدر Host", Type: "text", Advanced: true}
	fService   = Field{Key: "serviceName", Label: "gRPC service", LabelFa: "سرویس gRPC", Type: "text"}
	fXMode     = Field{Key: "mode", Label: "XHTTP mode", LabelFa: "حالت XHTTP", Type: "select", Options: []string{"auto", "packet-up", "stream-up", "stream-one"}, Advanced: true}
	fAddress   = Field{Key: "address", Label: "Address in links (CDN / clean IP)", LabelFa: "آدرس در لینک (CDN / آی‌پی تمیز)", Type: "text", Advanced: true}
	fObfs      = Field{Key: "obfs", Label: "Salamander obfuscation", LabelFa: "مبهم‌سازی Salamander", Type: "bool"}
	fObfsPass  = Field{Key: "obfsPassword", Label: "Obfuscation password", LabelFa: "رمز مبهم‌سازی", Type: "secret", Advanced: true}
	fSSMethod  = Field{Key: "method", Label: "Cipher", LabelFa: "رمزنگاری", Type: "select", Options: []string{"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"}}
	fSSClassic = Field{Key: "method", Label: "Cipher", LabelFa: "رمزنگاری", Type: "select", Options: []string{"chacha20-ietf-poly1305", "aes-256-gcm", "aes-128-gcm"}}
	fMTU       = Field{Key: "mtu", Label: "MTU", LabelFa: "MTU", Type: "number", Advanced: true}
	fCC        = Field{Key: "congestion", Label: "Congestion control", LabelFa: "کنترل ازدحام", Type: "select", Options: []string{"bbr", "cubic", "new_reno"}}
	fHandshake = Field{Key: "handshake", Label: "Handshake server", LabelFa: "سرور دست‌دهی", Type: "text"}
	fMasq      = Field{Key: "masquerade", Label: "Masquerade site", LabelFa: "سایت پوششی", Type: "text", Advanced: true}
	fXDNS      = Field{Key: "xdnsDomain", Label: "Tunnel domain (its NS record points to this server)", LabelFa: "دامنهٔ تونل (رکورد NS آن به این سرور اشاره می‌کند)", Type: "text"}
	fResolver  = Field{Key: "resolver", Label: "DNS resolver in links (direct = straight to this server)", LabelFa: "DNS در لینک‌ها (direct = مستقیم به همین سرور)", Type: "select", Options: []string{"8.8.8.8", "1.1.1.1", "9.9.9.9", "direct"}}
	fFakeTLS   = Field{Key: "sni", Label: "Fake TLS domain (the proxy looks like this site)", LabelFa: "دامنهٔ پوششی TLS (پراکسی شبیه این سایت دیده می‌شود)", Type: "text"}
)

var presets = []*Preset{
	// ------------------------------------------------------------ REALITY (no domain needed)
	{ID: "vless-reality-vision", Name: "VLESS · Reality · Vision", Engine: "xray", Group: "reality", Network: "tcp", DefaultPort: 2443,
		Security: "reality", Status: "ready", proto: "vless", transport: "raw", flow: "xtls-rprx-vision",
		Desc:   "Reality on Xray (newest features). Since Xray 26.9.8 it works only with Xray-based apps and mihomo, not sing-box apps.",
		DescFa: "Reality روی Xray (جدیدترین قابلیت‌ها). از Xray 26.9.8 فقط با برنامه‌های مبتنی بر Xray و mihomo کار می‌کند، نه برنامه‌های sing-box.",
		Fields: []Field{fSNI, fTarget, fFP, fShortID, fPub, fPriv}},
	{ID: "vless-reality-universal", Name: "VLESS · Reality · Vision (universal)", Engine: "singbox", Group: "reality", Network: "tcp", DefaultPort: 443,
		Security: "reality", Status: "ready", Recommended: true, proto: "vless", transport: "raw", flow: "xtls-rprx-vision",
		Desc:   "Reality on the sing-box engine: works with Xray apps (v2rayNG, Happ, Streisand) and sing-box apps (Hiddify, NekoBox, Karing).",
		DescFa: "Reality روی هستهٔ sing-box: هم با برنامه‌های Xray (v2rayNG، Happ، Streisand) کار می‌کند هم با برنامه‌های sing-box (Hiddify، NekoBox، Karing).",
		Fields: []Field{fSNI, fTarget, fFP, fShortID, fPub, fPriv}},
	{ID: "vless-reality-xhttp", Name: "VLESS · Reality · XHTTP", Engine: "xray", Group: "reality", Network: "tcp", DefaultPort: 8443,
		Security: "reality", Status: "ready", Recommended: true, proto: "vless", transport: "xhttp",
		Desc:   "Reality over XHTTP: splits traffic into normal-looking HTTP requests.",
		DescFa: "Reality روی XHTTP: ترافیک را به درخواست‌های عادی HTTP تبدیل می‌کند.",
		Fields: []Field{fSNI, fTarget, fPath, fXMode, fFP, fShortID, fPub, fPriv}},
	{ID: "vless-reality-grpc", Name: "VLESS · Reality · gRPC", Engine: "xray", Group: "reality", Network: "tcp", DefaultPort: 2083,
		Security: "reality", Status: "ready", proto: "vless", transport: "grpc",
		Desc: "Reality over gRPC multiplexing.", DescFa: "Reality روی gRPC با چندگانه‌سازی.",
		Fields: []Field{fSNI, fTarget, fService, fFP, fShortID, fPub, fPriv}},
	{ID: "trojan-reality", Name: "Trojan · Reality", Engine: "xray", Group: "reality", Network: "tcp", DefaultPort: 2096,
		Security: "reality", Status: "ready", proto: "trojan", transport: "raw",
		Desc: "Trojan protected by Reality.", DescFa: "Trojan با پوشش Reality.",
		Fields: []Field{fSNI, fTarget, fFP, fShortID, fPub, fPriv}},

	// ------------------------------------------------------------ CDN / TLS (needs a domain)
	{ID: "vless-xhttp-tls", Name: "VLESS · XHTTP · TLS", Engine: "xray", Group: "cdn", Network: "tcp", DefaultPort: 2053,
		Security: "tls", NeedsDomain: true, Status: "ready", Recommended: true, proto: "vless", transport: "xhttp",
		Desc:   "Works behind Cloudflare and other CDNs. Best CDN option.",
		DescFa: "پشت Cloudflare و CDNها کار می‌کند. بهترین گزینهٔ CDN.",
		Fields: []Field{fSNI, fPath, fXMode, fHost, fAddress, fFP}},
	{ID: "vless-ws-tls", Name: "VLESS · WebSocket · TLS", Engine: "xray", Group: "cdn", Network: "tcp", DefaultPort: 2087,
		Security: "tls", NeedsDomain: true, Status: "ready", proto: "vless", transport: "ws",
		Desc: "Classic CDN setup supported by every client.", DescFa: "چیدمان کلاسیک CDN، سازگار با همهٔ کلاینت‌ها.",
		Fields: []Field{fSNI, fPath, fHost, fAddress, fFP}},
	{ID: "vless-httpupgrade-tls", Name: "VLESS · HTTPUpgrade · TLS", Engine: "xray", Group: "cdn", Network: "tcp", DefaultPort: 8880,
		Security: "tls", NeedsDomain: true, Status: "ready", proto: "vless", transport: "httpupgrade",
		Desc: "Lighter than WebSocket, CDN friendly.", DescFa: "سبک‌تر از WebSocket و مناسب CDN.",
		Fields: []Field{fSNI, fPath, fHost, fAddress, fFP}},
	{ID: "vless-grpc-tls", Name: "VLESS · gRPC · TLS", Engine: "xray", Group: "cdn", Network: "tcp", DefaultPort: 2052,
		Security: "tls", NeedsDomain: true, Status: "ready", proto: "vless", transport: "grpc",
		Desc: "gRPC behind a CDN.", DescFa: "gRPC پشت CDN.",
		Fields: []Field{fSNI, fService, fAddress, fFP}},
	{ID: "trojan-ws-tls", Name: "Trojan · WebSocket · TLS", Engine: "xray", Group: "cdn", Network: "tcp", DefaultPort: 2086,
		Security: "tls", NeedsDomain: true, Status: "ready", proto: "trojan", transport: "ws",
		Desc: "Trojan behind a CDN.", DescFa: "Trojan پشت CDN.",
		Fields: []Field{fSNI, fPath, fHost, fAddress, fFP}},
	{ID: "vmess-ws-tls", Name: "VMess · WebSocket · TLS", Engine: "xray", Group: "cdn", Network: "tcp", DefaultPort: 2095,
		Security: "tls", NeedsDomain: true, Status: "ready", proto: "vmess", transport: "ws",
		Desc: "For older clients.", DescFa: "برای کلاینت‌های قدیمی.",
		Fields: []Field{fSNI, fPath, fHost, fAddress, fFP}},
	{ID: "vmess-ws", Name: "VMess · WebSocket", Engine: "xray", Group: "cdn", Network: "tcp", DefaultPort: 8080,
		Security: "none", Status: "ready", proto: "vmess", transport: "ws",
		Desc: "CDN without TLS (Cloudflare port 80/8080).", DescFa: "CDN بدون TLS (پورت ۸۰/۸۰۸۰ کلادفلر).",
		Fields: []Field{fPath, fHost, fAddress}},

	// ------------------------------------------------------------ TLS direct
	{ID: "trojan-tls", Name: "Trojan · TLS", Engine: "xray", Group: "classic", Network: "tcp", DefaultPort: 4443,
		Security: "tls", Status: "ready", proto: "trojan", transport: "raw",
		Desc: "Plain Trojan over TLS.", DescFa: "Trojan ساده روی TLS.",
		Fields: []Field{fSNI, fFP}},
	{ID: "vless-enc-xhttp", Name: "VLESS Encryption · XHTTP (post-quantum)", Engine: "xray", Group: "classic", Network: "tcp", DefaultPort: 8081,
		Security: "none", Status: "beta", proto: "vless", transport: "xhttp",
		Desc:   "ML-KEM-768 post-quantum encryption without TLS. Also works behind CDN on HTTP ports.",
		DescFa: "رمزنگاری پساکوانتومی ML-KEM-768 بدون TLS. پشت CDN روی پورت HTTP هم کار می‌کند.",
		Fields: []Field{fPath, fHost, fAddress}},

	// ------------------------------------------------------------ UDP / QUIC
	{ID: "hysteria2", Name: "Hysteria2", Engine: "xray", Group: "udp", Network: "udp", DefaultPort: 4444,
		Security: "tls", Status: "ready", Recommended: true, proto: "hysteria", transport: "hysteria",
		Desc:   "Very fast on lossy networks. QUIC with optional Salamander obfuscation.",
		DescFa: "روی شبکه‌های پرافت بسیار سریع. QUIC با مبهم‌سازی Salamander.",
		Fields: []Field{fSNI, fObfs, fObfsPass}},
	{ID: "tuic", Name: "TUIC v5", Engine: "singbox", Group: "udp", Network: "udp", DefaultPort: 4445,
		Security: "tls", Status: "ready", proto: "tuic",
		Desc: "QUIC proxy with 0-RTT and BBR.", DescFa: "پروکسی QUIC با BBR.",
		Fields: []Field{fSNI, fCC}},
	{ID: "hysteria2-sb", Name: "Hysteria2 · masquerade (sing-box)", Engine: "singbox", Group: "udp", Network: "udp", DefaultPort: 4446,
		Security: "tls", Status: "ready", proto: "hysteria2",
		Desc: "Hysteria2 that answers probes with a real website.", DescFa: "Hysteria2 که به کاوش‌ها با یک سایت واقعی پاسخ می‌دهد.",
		Fields: []Field{fSNI, fObfs, fObfsPass, fMasq}},
	{ID: "wireguard", Name: "WireGuard", Engine: "xray", Group: "udp", Network: "udp", DefaultPort: 51820,
		Security: "none", Status: "ready", proto: "wireguard",
		Desc: "Full VPN tunnel, one peer per user.", DescFa: "تونل VPN کامل، یک peer برای هر کاربر.",
		Fields: []Field{fMTU, fPub, fPriv}},

	// ------------------------------------------------------------ anti-TLS-in-TLS
	{ID: "anytls", Name: "AnyTLS", Engine: "singbox", Group: "classic", Network: "tcp", DefaultPort: 4447,
		Security: "tls", Status: "ready", proto: "anytls",
		Desc: "Padding scheme that defeats TLS-in-TLS detection.", DescFa: "الگوی پدینگ که تشخیص TLS-in-TLS را خنثی می‌کند.",
		Fields: []Field{fSNI}},
	{ID: "shadowtls", Name: "ShadowTLS v3 + SS2022", Engine: "singbox", Group: "classic", Network: "tcp", DefaultPort: 8444,
		Security: "none", Status: "ready", proto: "shadowtls",
		Desc: "Borrows the TLS handshake of a real site.", DescFa: "دست‌دهی TLS یک سایت واقعی را قرض می‌گیرد.",
		Fields: []Field{fHandshake}},
	{ID: "naive", Name: "NaiveProxy", Engine: "singbox", Group: "classic", Network: "tcp", DefaultPort: 4448,
		Security: "tls", NeedsDomain: true, Status: "ready", proto: "naive",
		Desc: "Chrome's own network stack; needs a real domain.", DescFa: "پشتهٔ شبکهٔ خود Chrome؛ دامنهٔ واقعی لازم دارد.",
		Fields: []Field{fSNI}},

	// ------------------------------------------------------------ classic
	{ID: "shadowsocks-2022", Name: "Shadowsocks 2022", Engine: "xray", Group: "classic", Network: "tcp", DefaultPort: 8388,
		Security: "none", Status: "ready", proto: "shadowsocks",
		Desc: "Modern Shadowsocks with per-user keys.", DescFa: "Shadowsocks مدرن با کلید جدا برای هر کاربر.",
		Fields: []Field{fSSMethod}},
	{ID: "shadowsocks", Name: "Shadowsocks (Outline)", Engine: "xray", Group: "classic", Network: "tcp", DefaultPort: 8389,
		Security: "none", Status: "ready", proto: "shadowsocks",
		Desc: "Compatible with Outline and old clients.", DescFa: "سازگار با Outline و کلاینت‌های قدیمی.",
		Fields: []Field{fSSClassic}},
	// ------------------------------------------------------------ shutdowns and Telegram
	{ID: "vless-xdns", Name: "VLESS · XDNS (DNS tunnel)", Engine: "xray", Group: "udp", Network: "udp", DefaultPort: 53,
		Security: "none", Status: "beta", proto: "vless", transport: "xdns",
		Desc:   "For internet shutdowns: traffic hidden in ordinary DNS queries that go through public resolvers. Slow, but often the last thing that still works. Needs a domain whose NS record points to this server; Xray-based apps.",
		DescFa: "برای قطعی اینترنت: ترافیک داخل درخواست‌های DNS معمولی پنهان می‌شود و از DNSهای عمومی رد می‌شود. کند است، اما اغلب آخرین چیزی است که هنوز کار می‌کند. یک دامنه لازم دارد که رکورد NS آن به این سرور اشاره کند؛ برای برنامه‌های مبتنی بر Xray.",
		Fields: []Field{fXDNS, fResolver}},
	{ID: "mtproto", Name: "MTProto · Telegram proxy", Engine: "mtg", Group: "classic", Network: "tcp", DefaultPort: 443,
		Security: "faketls", Status: "ready", proto: "mtproto",
		Desc:   "A proxy inside Telegram itself: users add it with one tap, no VPN app needed. It looks like an HTTPS visit to the site you choose, and can share port 443. One per server; one link for everyone, so usage is not counted per user.",
		DescFa: "پراکسی داخل خود تلگرام: کاربر با یک لمس اضافه‌اش می‌کند و برنامهٔ VPN لازم نیست. شبیه بازدید HTTPS از سایتی است که انتخاب می‌کنید و می‌تواند پورت ۴۴۳ را شریک شود. هر سرور یکی؛ یک لینک برای همه، پس مصرف هر کاربر جدا شمرده نمی‌شود.",
		Fields: []Field{fFakeTLS}},
	{ID: "vmess-kcp", Name: "VMess · mKCP", Engine: "xray", Group: "udp", Network: "udp", DefaultPort: 8390,
		Security: "none", Status: "beta", proto: "vmess", transport: "kcp",
		Desc: "UDP transport for unstable links.", DescFa: "ترابری UDP برای لینک‌های ناپایدار.",
		Fields: []Field{}},
	{ID: "socks", Name: "SOCKS5", Engine: "xray", Group: "classic", Network: "tcp", DefaultPort: 1080,
		Security: "none", Status: "ready", proto: "socks",
		Desc: "Plain SOCKS5 with a username per user (no encryption).", DescFa: "SOCKS5 ساده با نام کاربری برای هر کاربر (بدون رمزنگاری).",
		Fields: []Field{}},
	{ID: "http", Name: "HTTP proxy", Engine: "xray", Group: "classic", Network: "tcp", DefaultPort: 8118,
		Security: "none", Status: "ready", proto: "http",
		Desc: "Plain HTTP proxy with auth (no encryption).", DescFa: "پروکسی HTTP ساده با احراز هویت (بدون رمزنگاری).",
		Fields: []Field{}},
}

var byID = func() map[string]*Preset {
	m := map[string]*Preset{}
	for _, p := range presets {
		m[p.ID] = p
	}
	return m
}()

// All returns every preset, recommended first.
func All() []*Preset {
	out := append([]*Preset(nil), presets...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Recommended && !out[j].Recommended })
	return out
}

// Get returns a preset by id.
func Get(id string) *Preset { return byID[id] }

// DefaultReality is the default REALITY camouflage site.
const DefaultReality = "www.speedtest.net"

// DefaultFakeTLS is the default site an MTProto proxy looks like.
const DefaultFakeTLS = "storage.googleapis.com"

// MTProtoSecret is the proxy's secret: "ee" (fake TLS), 16 random bytes kept in
// the inbound, then the fake domain, all in hex. Changing the domain changes it.
func MTProtoSecret(key, domain string) string {
	return "ee" + key + hex.EncodeToString([]byte(strings.ToLower(strings.TrimSpace(domain))))
}

// Defaults fills missing parameters for a new inbound of the given preset.
func Defaults(p *Preset, params map[string]any, domain string) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	set := func(k string, v any) {
		if cur, ok := params[k]; !ok || cur == nil || cur == "" {
			params[k] = v
		}
	}
	switch p.Security {
	case "reality":
		set("sni", DefaultReality)
		set("target", "")
		if params["target"] == "" {
			params["target"] = params["sni"].(string) + ":443"
		}
		set("fp", "chrome")
		set("shortId", util.ShortID(8))
		if s, _ := params["privateKey"].(string); s == "" {
			priv, pub := util.X25519()
			params["privateKey"], params["publicKey"] = priv, pub
		} else if pub, err := util.X25519Public(s); err == nil {
			params["publicKey"] = pub
		}
	case "tls":
		set("sni", domain)
		set("fp", "chrome")
	}
	switch p.transport {
	case "ws", "httpupgrade", "xhttp":
		set("path", "/"+util.RandomLower(10))
	case "grpc":
		set("serviceName", util.RandomLower(10))
	}
	if p.transport == "xhttp" {
		set("mode", "auto")
	}
	switch p.ID {
	case "hysteria2", "hysteria2-sb":
		set("obfs", false)
		set("obfsPassword", util.RandomString(16))
		if p.ID == "hysteria2-sb" {
			set("masquerade", "https://www.bing.com")
		}
	case "tuic":
		set("congestion", "bbr")
	case "vless-xdns":
		set("resolver", "8.8.8.8")
	case "mtproto":
		set("sni", DefaultFakeTLS)
		if k, _ := params["mtKey"].(string); len(k) != 32 {
			params["mtKey"] = util.RandomHex(16)
		}
	case "shadowsocks-2022":
		set("method", "2022-blake3-aes-128-gcm")
		n := ssKeyLen(str(params["method"]))
		if k, err := base64.StdEncoding.DecodeString(str(params["serverKey"])); err != nil || len(k) != n {
			params["serverKey"] = util.SSKey(n)
		}
	case "shadowsocks":
		set("method", "chacha20-ietf-poly1305")
	case "shadowtls":
		set("handshake", DefaultReality)
		set("ssKey", util.SSKey(16))
	case "wireguard":
		set("mtu", 1420)
		if s, _ := params["privateKey"].(string); s == "" {
			priv, pub := util.WireGuardKeys()
			params["privateKey"], params["publicKey"] = priv, pub
		}
	case "vless-enc-xhttp":
		if s, _ := params["decryption"].(string); s == "" {
			priv, pub := util.X25519()
			params["decryption"] = "mlkem768x25519plus.native.600s." + priv
			params["encryption"] = "mlkem768x25519plus.native.0rtt." + pub
		}
	}
	return params
}

func ssKeyLen(method string) int {
	if method == "2022-blake3-aes-128-gcm" {
		return 16
	}
	return 32
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
