package main

import (
	"log"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// upgradeInbounds fills in what a newer Dunvex needs on protocols an older one
// made, and returns how many it changed.
//
//   - v1.31.1: the xDNS and xICMP tunnels carry VLESS Encryption keys. Xray
//     26.11 apps refuse a VLESS link to a public address that has neither TLS
//     nor encryption, so without the keys those links no longer connect.
//   - v1.31.2: TrustTunnel can hide from probes (its apps prove a key in the
//     ClientHello). A server with no users yet turns it on, since nobody holds
//     an old link; one with users keeps it off, so the links people have keep
//     working until the owner turns it on and they update their subscription.
func upgradeInbounds(st *store.Store) int {
	ins, err := st.Inbounds()
	if err != nil {
		return 0
	}
	users, _ := st.Users()
	n := 0
	for _, in := range ins {
		p := protocol.Get(in.Preset)
		if p == nil {
			continue
		}
		var note string
		switch {
		case protocol.VLESSEncrypted(p.ID) && in.Str("decryption") == "":
			note = "VLESS Encryption keys added (Xray 26.11 apps need them); users get the new link with their next subscription update"
		case protocol.IsTrustTunnel(p) && in.Params["ttHide"] == nil:
			if in.Params == nil {
				in.Params = map[string]any{}
			}
			in.Params["ttHide"] = len(users) == 0
			note = "hiding from probes is on (no users yet)"
			if len(users) > 0 {
				note = "can now hide from probes (Protocols > TrustTunnel); off so the links users have keep working"
			}
		default:
			continue
		}
		in.Params = protocol.Defaults(p, in.Params, "")
		if err := st.UpdateInbound(in); err != nil {
			log.Printf("%s: could not update: %v", in.Remark, err)
			continue
		}
		log.Printf("%s: %s", in.Remark, note)
		n++
	}
	return n
}
