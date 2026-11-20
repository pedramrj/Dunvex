package engine

// Live user changes for Xray. When only the users of some inbounds changed,
// Dunvex adds and removes them through Xray's HandlerService instead of
// restarting the core, so nobody else is disconnected.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"
)

// protocols whose Xray inbound accepts users at runtime
var liveProtocols = map[string]bool{"vless": true, "vmess": true, "trojan": true, "shadowsocks": true, "hysteria": true}

type userOp struct {
	tag    string
	remove string // email to remove
	add    []byte // encoded xray.common.protocol.User to add
	email  string
}

// xrayUserDiff returns the user operations that turn the running config into
// the new one. ok=false means something other than users changed (or a
// protocol cannot change users live) and the core must restart. gone lists
// users that are no longer in any inbound.
func xrayUserDiff(oldCfg, newCfg []byte) (ops []userOp, gone []string, ok bool) {
	var o, n map[string]any
	if json.Unmarshal(oldCfg, &o) != nil || json.Unmarshal(newCfg, &n) != nil {
		return nil, nil, false
	}
	oin, _ := o["inbounds"].([]any)
	nin, _ := n["inbounds"].([]any)
	if len(oin) != len(nin) {
		return nil, nil, false
	}
	delete(o, "inbounds")
	delete(n, "inbounds")
	if !sameJSON(o, n) {
		return nil, nil, false
	}
	removed := map[string]bool{}
	present := map[string]bool{}
	for i := range nin {
		ob, _ := oin[i].(map[string]any)
		nb, _ := nin[i].(map[string]any)
		if ob == nil || nb == nil {
			return nil, nil, false
		}
		oc := takeClients(ob)
		nc := takeClients(nb)
		if !sameJSON(ob, nb) {
			return nil, nil, false
		}
		for _, c := range nc {
			present[email(c)] = true
		}
		if sameJSON(oc, nc) {
			continue
		}
		proto, _ := nb["protocol"].(string)
		tag, _ := nb["tag"].(string)
		if !liveProtocols[proto] || tag == "" {
			return nil, nil, false
		}
		oldBy := map[string]map[string]any{}
		for _, c := range oc {
			oldBy[email(c)] = c
		}
		newBy := map[string]map[string]any{}
		for _, c := range nc {
			newBy[email(c)] = c
		}
		for e, c := range oldBy {
			if nc, still := newBy[e]; !still || !sameJSON(c, nc) {
				ops = append(ops, userOp{tag: tag, remove: e, email: e})
				if !still {
					removed[e] = true
				}
			}
		}
		for e, c := range newBy {
			if oc, had := oldBy[e]; !had || !sameJSON(c, oc) {
				enc, err := encodeXrayUser(proto, c)
				if err != nil {
					return nil, nil, false
				}
				ops = append(ops, userOp{tag: tag, add: enc, email: e})
			}
		}
	}
	for e := range removed {
		if !present[e] {
			gone = append(gone, e)
		}
	}
	return ops, gone, true
}

// takeClients removes settings.clients from an inbound and returns them.
func takeClients(ib map[string]any) []map[string]any {
	st, _ := ib["settings"].(map[string]any)
	if st == nil {
		return nil
	}
	raw, _ := st["clients"].([]any)
	delete(st, "clients")
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func email(c map[string]any) string { s, _ := c["email"].(string); return s }

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func appendStr(b []byte, num protowire.Number, s string) []byte {
	if s == "" {
		return b
	}
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendString(b, s)
}

func appendMsg(b []byte, num protowire.Number, m []byte) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, m)
}

func appendVarint(b []byte, num protowire.Number, v uint64) []byte {
	if v == 0 {
		return b
	}
	b = protowire.AppendTag(b, num, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

func typedMessage(typ string, value []byte) []byte {
	return appendMsg(appendStr(nil, 1, typ), 2, value)
}

var ssCiphers = map[string]uint64{
	"aes-128-gcm": 5, "aes-256-gcm": 6,
	"chacha20-poly1305": 7, "chacha20-ietf-poly1305": 7,
	"xchacha20-poly1305": 8, "xchacha20-ietf-poly1305": 8,
}

// encodeXrayUser encodes one client of an inbound as xray.common.protocol.User.
func encodeXrayUser(proto string, c map[string]any) ([]byte, error) {
	str := func(k string) string { s, _ := c[k].(string); return s }
	var typ string
	var acct []byte
	switch proto {
	case "vless":
		typ = "xray.proxy.vless.Account"
		acct = appendStr(appendStr(nil, 1, str("id")), 2, str("flow"))
	case "vmess":
		typ = "xray.proxy.vmess.Account"
		acct = appendMsg(appendStr(nil, 1, str("id")), 3, appendVarint(nil, 1, 2)) // security: auto
	case "trojan":
		typ = "xray.proxy.trojan.Account"
		acct = appendStr(nil, 1, str("password"))
	case "shadowsocks":
		if m := str("method"); m != "" {
			cipher, ok := ssCiphers[strings.ToLower(m)]
			if !ok {
				return nil, fmt.Errorf("cipher %s", m)
			}
			typ = "xray.proxy.shadowsocks.Account"
			acct = appendVarint(appendStr(nil, 1, str("password")), 2, cipher)
		} else {
			typ = "xray.proxy.shadowsocks_2022.Account"
			acct = appendStr(nil, 1, str("password"))
		}
	case "hysteria":
		typ = "xray.proxy.hysteria.account.Account"
		acct = appendStr(nil, 1, str("auth"))
	default:
		return nil, fmt.Errorf("protocol %s has no live users", proto)
	}
	u := appendStr(nil, 2, str("email"))
	return appendMsg(u, 3, typedMessage(typ, acct)), nil
}

func invoke(addr, method string, req []byte) ([]byte, error) {
	c, err := stats.conn(addr)
	if err != nil {
		return nil, err
	}
	var resp []byte
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	err = c.Invoke(ctx, method, &req, &resp, grpc.ForceCodec(rawCodec{}))
	return resp, err
}

// applyUserOps sends the operations to Xray's HandlerService.
func applyUserOps(addr string, ops []userOp) error {
	// removals first, so a changed user is re-added with its new credentials
	for pass := 0; pass < 2; pass++ {
		for _, op := range ops {
			var tm []byte
			switch {
			case pass == 0 && op.remove != "":
				tm = typedMessage("xray.app.proxyman.command.RemoveUserOperation", appendStr(nil, 1, op.remove))
			case pass == 1 && op.add != nil:
				tm = typedMessage("xray.app.proxyman.command.AddUserOperation", appendMsg(nil, 1, op.add))
			default:
				continue
			}
			req := appendMsg(appendStr(nil, 1, op.tag), 2, tm)
			if _, err := invoke(addr, "/xray.app.proxyman.command.HandlerService/AlterInbound", req); err != nil {
				return fmt.Errorf("%s %s: %w", op.tag, op.email, err)
			}
		}
	}
	return nil
}

// OnlineIP is one address a user is connected from.
type OnlineIP struct {
	IP       string
	LastSeen int64
}

// xrayOnlineIPs reads the addresses of connected users (Xray 26+).
func xrayOnlineIPs(addr string) (map[string][]OnlineIP, error) {
	resp, err := invoke(addr, "/xray.app.stats.command.StatsService/GetUsersStats", nil)
	if err != nil {
		return nil, err
	}
	out := map[string][]OnlineIP{}
	for len(resp) > 0 {
		num, typ, n := protowire.ConsumeTag(resp)
		if n < 0 {
			return nil, fmt.Errorf("bad users stats")
		}
		resp = resp[n:]
		if num != 1 || typ != protowire.BytesType {
			m := protowire.ConsumeFieldValue(num, typ, resp)
			if m < 0 {
				return nil, fmt.Errorf("bad users stats")
			}
			resp = resp[m:]
			continue
		}
		msg, m := protowire.ConsumeBytes(resp)
		if m < 0 {
			return nil, fmt.Errorf("bad user stat")
		}
		resp = resp[m:]
		var name string
		var ips []OnlineIP
		for len(msg) > 0 {
			fn, ft, k := protowire.ConsumeTag(msg)
			if k < 0 {
				break
			}
			msg = msg[k:]
			switch {
			case fn == 1 && ft == protowire.BytesType:
				s, k := protowire.ConsumeString(msg)
				if k < 0 {
					return out, nil
				}
				name, msg = s, msg[k:]
			case fn == 2 && ft == protowire.BytesType:
				e, k := protowire.ConsumeBytes(msg)
				if k < 0 {
					return out, nil
				}
				msg = msg[k:]
				ips = append(ips, decodeOnlineIP(e))
			default:
				k := protowire.ConsumeFieldValue(fn, ft, msg)
				if k < 0 {
					return out, nil
				}
				msg = msg[k:]
			}
		}
		if name != "" {
			out[name] = append(out[name], ips...)
		}
	}
	return out, nil
}

func decodeOnlineIP(b []byte) OnlineIP {
	var e OnlineIP
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return e
		}
		b = b[n:]
		switch {
		case num == 1 && typ == protowire.BytesType:
			s, m := protowire.ConsumeString(b)
			if m < 0 {
				return e
			}
			e.IP, b = s, b[m:]
		case num == 2 && typ == protowire.VarintType:
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return e
			}
			e.LastSeen, b = int64(v), b[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return e
			}
			b = b[m:]
		}
	}
	return e
}
