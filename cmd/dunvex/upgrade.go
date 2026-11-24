package main

import (
	"log"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// upgradeInbounds fills in what a newer Dunvex needs on protocols an older one
// made. Since v1.31.1 the xDNS and xICMP tunnels carry VLESS Encryption keys:
// Xray 26.11 apps refuse a VLESS link to a public address that has neither TLS
// nor encryption, so without the keys those links no longer connect.
// It returns how many protocols it changed.
func upgradeInbounds(st *store.Store) int {
	ins, err := st.Inbounds()
	if err != nil {
		return 0
	}
	n := 0
	for _, in := range ins {
		p := protocol.Get(in.Preset)
		if p == nil || !protocol.VLESSEncrypted(p.ID) || in.Str("decryption") != "" {
			continue
		}
		in.Params = protocol.Defaults(p, in.Params, "")
		if err := st.UpdateInbound(in); err != nil {
			log.Printf("%s: could not add VLESS Encryption keys: %v", in.Remark, err)
			continue
		}
		log.Printf("%s: VLESS Encryption keys added (Xray 26.11 apps need them); users get the new link with their next subscription update", in.Remark)
		n++
	}
	return n
}
