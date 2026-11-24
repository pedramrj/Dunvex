package protocol

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// Env carries server-wide values needed to build configs and links.
type Env struct {
	Host       string // public address used in links (domain or IP)
	Domain     string // configured domain, may be empty
	CertFile   string
	KeyFile    string
	SelfSigned bool      // certificate is self-signed: clients must skip verification
	Listen     string    // bind address for inbounds ("" = all interfaces)
	CertSHA256 string    // hex SHA-256 of the certificate, for pinning self-signed certs
	Fragment   *Fragment // TLS fragment for Xray clients (links and the Xray JSON subscription); nil = off
}

type obj = map[string]any

// LinkHost returns the address clients should dial for an inbound.
func LinkHost(in *store.Inbound, env Env) string {
	if a := strings.TrimSpace(in.Str("address")); a != "" {
		return a
	}
	return env.Host
}

func sni(in *store.Inbound, env Env) string {
	if s := in.Str("sni"); s != "" {
		return s
	}
	if env.Domain != "" {
		return env.Domain
	}
	return env.Host
}

func wgAddress(u *store.User) string {
	i := u.WGIndex
	if i < 2 {
		i = 2
	}
	return fmt.Sprintf("10.66.%d.%d", (i/254)%256, i%254+1)
}

// --------------------------------------------------------------------------- Xray

// XrayInbound builds an Xray inbound object.
func XrayInbound(p *Preset, in *store.Inbound, users []*store.User, env Env) (obj, error) {
	ib := obj{
		"tag":      in.Tag(),
		"listen":   or(env.Listen, "0.0.0.0"),
		"port":     in.Port,
		"protocol": p.proto,
	}
	clients := []obj{}
	switch p.proto {
	case "vless":
		for _, u := range users {
			c := obj{"id": u.UUID, "email": u.Name, "level": 0}
			if p.flow != "" {
				c["flow"] = p.flow
			}
			clients = append(clients, c)
		}
		dec := "none"
		if p.ID == "vless-enc-xhttp" {
			dec = in.Str("decryption")
		}
		ib["settings"] = obj{"clients": clients, "decryption": dec}
	case "vmess":
		for _, u := range users {
			clients = append(clients, obj{"id": u.UUID, "email": u.Name, "level": 0})
		}
		ib["settings"] = obj{"clients": clients}
	case "trojan":
		for _, u := range users {
			clients = append(clients, obj{"password": u.Password, "email": u.Name, "level": 0})
		}
		ib["settings"] = obj{"clients": clients}
	case "shadowsocks":
		method := in.Str("method")
		if strings.HasPrefix(method, "2022-") {
			n := ssKeyLen(method)
			for _, u := range users {
				clients = append(clients, obj{"password": util.SSKeyFrom(u.UUID, n), "email": u.Name, "level": 0})
			}
			ib["settings"] = obj{"method": method, "password": in.Str("serverKey"), "clients": clients, "network": "tcp,udp"}
		} else {
			for _, u := range users {
				clients = append(clients, obj{"method": method, "password": u.Password, "email": u.Name, "level": 0})
			}
			ib["settings"] = obj{"method": method, "clients": clients, "network": "tcp,udp"}
		}
	case "hysteria":
		for _, u := range users {
			clients = append(clients, obj{"auth": u.Password, "email": u.Name, "level": 0})
		}
		ib["settings"] = obj{"version": 2, "clients": clients}
	case "wireguard":
		peers := []obj{}
		for _, u := range users {
			if u.WGPublic == "" {
				continue
			}
			peers = append(peers, obj{"publicKey": u.WGPublic, "allowedIPs": []string{wgAddress(u) + "/32"}, "email": u.Name, "level": 0})
		}
		mtu := in.Int("mtu")
		if mtu == 0 {
			mtu = 1420
		}
		ib["settings"] = obj{"secretKey": in.Str("privateKey"), "peers": peers, "mtu": mtu}
		return ib, nil
	case "socks":
		accts := []obj{}
		for _, u := range users {
			accts = append(accts, obj{"user": u.Name, "pass": u.Password})
		}
		ib["settings"] = obj{"auth": "password", "accounts": accts, "udp": true}
		return ib, nil
	case "http":
		accts := []obj{}
		for _, u := range users {
			accts = append(accts, obj{"user": u.Name, "pass": u.Password})
		}
		ib["settings"] = obj{"accounts": accts}
		return ib, nil
	default:
		return nil, fmt.Errorf("preset %s is not an Xray preset", p.ID)
	}

	stream := obj{}
	switch p.transport {
	case "", "raw":
		stream["network"] = "raw"
	case "xhttp":
		stream["network"] = "xhttp"
		x := obj{"path": in.Str("path"), "mode": or(in.Str("mode"), "auto")}
		if h := in.Str("host"); h != "" {
			x["host"] = h
		}
		stream["xhttpSettings"] = x
	case "grpc":
		stream["network"] = "grpc"
		stream["grpcSettings"] = obj{"serviceName": in.Str("serviceName")}
	case "ws":
		stream["network"] = "ws"
		w := obj{"path": in.Str("path")}
		if h := in.Str("host"); h != "" {
			w["host"] = h
		}
		stream["wsSettings"] = w
	case "httpupgrade":
		stream["network"] = "httpupgrade"
		w := obj{"path": in.Str("path")}
		if h := in.Str("host"); h != "" {
			w["host"] = h
		}
		stream["httpupgradeSettings"] = w
	case "kcp":
		stream["network"] = "kcp"
		stream["kcpSettings"] = obj{"mtu": 1350, "tti": 20, "uplinkCapacity": 12, "downlinkCapacity": 100}
	case "xdns":
		// mKCP inside DNS queries (Xray finalmask). The server MTU fits a TXT
		// answer; clients need none. The domain's NS record points here.
		dom := XDNSDomain(in)
		if dom == "" {
			return nil, fmt.Errorf("%s needs its tunnel domain (an NS record that points to this server)", p.Name)
		}
		stream["network"] = "kcp"
		stream["kcpSettings"] = obj{"mtu": 900, "tti": 200, "uplinkCapacity": 5, "downlinkCapacity": 20}
		// EDNS0 1232: answers carry ~930 bytes in TXT, which the MTU of 900 fits
		stream["finalmask"] = obj{"udp": []obj{{"type": "xdns", "settings": obj{"domains": []obj{{"names": []string{dom}, "edns0": 1232}}}}}}
	case "xicmp":
		// mKCP inside ping packets (Xray finalmask). xICMP binds no port: it
		// reads every echo request with a raw socket and answers with replies.
		stream["network"] = "kcp"
		stream["kcpSettings"] = obj{"mtu": 1350, "tti": 20, "uplinkCapacity": 12, "downlinkCapacity": 100}
		stream["finalmask"] = obj{"udp": []obj{{"type": "xicmp", "settings": obj{}}}}
	case "hysteria":
		stream["network"] = "hysteria"
		stream["hysteriaSettings"] = obj{"version": 2}
		if in.Bool("obfs") {
			stream["finalmask"] = obj{"udp": []obj{{"type": "salamander", "settings": obj{"password": in.Str("obfsPassword")}}}}
		}
	}
	switch p.Security {
	case "reality":
		stream["security"] = "reality"
		stream["realitySettings"] = obj{
			"show":        false,
			"target":      or(in.Str("target"), in.Str("sni")+":443"),
			"xver":        0,
			"serverNames": []string{in.Str("sni")},
			"privateKey":  in.Str("privateKey"),
			"shortIds":    []string{"", in.Str("shortId")},
		}
	case "tls":
		if env.CertFile == "" {
			return nil, fmt.Errorf("%s needs a TLS certificate; set one in Settings", p.Name)
		}
		t := obj{"certificates": []obj{{"certificateFile": env.CertFile, "keyFile": env.KeyFile}}}
		switch p.transport {
		case "hysteria":
			t["alpn"] = []string{"h3"}
		case "grpc":
			t["alpn"] = []string{"h2"}
		case "xhttp":
			t["alpn"] = []string{"h2", "http/1.1"}
		default:
			t["alpn"] = []string{"http/1.1"}
		}
		stream["security"] = "tls"
		stream["tlsSettings"] = t
	default:
		stream["security"] = "none"
	}
	ib["streamSettings"] = stream
	if p.proto != "hysteria" {
		ib["sniffing"] = obj{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true}
	}
	return ib, nil
}

// --------------------------------------------------------------------------- sing-box

// SingboxInbounds builds sing-box inbound objects (ShadowTLS needs two).
func SingboxInbounds(p *Preset, in *store.Inbound, users []*store.User, env Env) ([]obj, error) {
	tlsObj := func(alpn ...string) (obj, error) {
		if env.CertFile == "" {
			return nil, fmt.Errorf("%s needs a TLS certificate; set one in Settings", p.Name)
		}
		t := obj{"enabled": true, "certificate_path": env.CertFile, "key_path": env.KeyFile}
		if s := in.Str("sni"); s != "" {
			t["server_name"] = s
		}
		if len(alpn) > 0 {
			t["alpn"] = alpn
		}
		return t, nil
	}
	base := obj{"type": p.proto, "tag": in.Tag(), "listen": or(env.Listen, "::"), "listen_port": in.Port}
	switch p.proto {
	case "vless":
		us := []obj{}
		for _, u := range users {
			c := obj{"name": u.Name, "uuid": u.UUID}
			if p.flow != "" {
				c["flow"] = p.flow
			}
			us = append(us, c)
		}
		base["users"] = us
		host, port := in.Str("sni"), 443
		if t := in.Str("target"); t != "" {
			if h, ps, err := net.SplitHostPort(t); err == nil {
				host = h
				port, _ = strconv.Atoi(ps)
			}
		}
		base["tls"] = obj{"enabled": true, "server_name": in.Str("sni"), "reality": obj{
			"enabled": true, "handshake": obj{"server": host, "server_port": port},
			"private_key": in.Str("privateKey"), "short_id": []string{"", in.Str("shortId")}}}
	case "tuic":
		us := []obj{}
		for _, u := range users {
			us = append(us, obj{"name": u.Name, "uuid": u.UUID, "password": u.Password})
		}
		t, err := tlsObj("h3")
		if err != nil {
			return nil, err
		}
		base["users"] = us
		base["congestion_control"] = or(in.Str("congestion"), "bbr")
		base["tls"] = t
	case "hysteria2":
		us := []obj{}
		for _, u := range users {
			us = append(us, obj{"name": u.Name, "password": u.Password})
		}
		t, err := tlsObj("h3")
		if err != nil {
			return nil, err
		}
		base["users"] = us
		base["tls"] = t
		if in.Bool("obfs") {
			base["obfs"] = obj{"type": "salamander", "password": in.Str("obfsPassword")}
		} else if m := in.Str("masquerade"); m != "" {
			base["masquerade"] = m
		}
	case "anytls":
		us := []obj{}
		for _, u := range users {
			us = append(us, obj{"name": u.Name, "password": u.Password})
		}
		t, err := tlsObj()
		if err != nil {
			return nil, err
		}
		base["users"] = us
		base["tls"] = t
	case "naive":
		us := []obj{}
		for _, u := range users {
			us = append(us, obj{"username": u.Name, "password": u.Password})
		}
		t, err := tlsObj()
		if err != nil {
			return nil, err
		}
		base["users"] = us
		base["tls"] = t
	case "shadowtls":
		stUsers, ssUsers := []obj{}, []obj{}
		for _, u := range users {
			stUsers = append(stUsers, obj{"name": u.Name, "password": u.Password})
			ssUsers = append(ssUsers, obj{"name": u.Name, "password": util.SSKeyFrom(u.UUID, 16)})
		}
		innerTag := in.Tag() + "-ss"
		base["version"] = 3
		base["users"] = stUsers
		base["strict_mode"] = true
		base["handshake"] = obj{"server": or(in.Str("handshake"), DefaultReality), "server_port": 443}
		base["detour"] = innerTag
		inner := obj{"type": "shadowsocks", "tag": innerTag, "listen": "127.0.0.1", "network": "tcp",
			"method": "2022-blake3-aes-128-gcm", "password": in.Str("ssKey"), "users": ssUsers}
		return []obj{base, inner}, nil
	default:
		return nil, fmt.Errorf("preset %s is not a sing-box preset", p.ID)
	}
	return []obj{base}, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
