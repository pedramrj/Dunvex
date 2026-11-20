# Protocols (research as of November 2026)

Versions checked: Xray-core **v26.11.9**, sing-box **v1.14.4**, mtg **v2.3.0**, amneziawg-go **v3.1.20261116**, Hysteria **v2.14.0**, Mieru (mita) **v3.39.0**, TrustTunnel **v1.3.1**.
Status: ✅ = built into Dunvex (passes the core config test) · 🧪 = experimental · 🗺 = on the roadmap

## Xray-core
| Protocol | Transport / security | Status | Notes |
|---|---|---|---|
| VLESS | Reality + Vision (TCP) | ✅ | Xray Reality; since 26.9.8 only for Xray-based apps and mihomo (see below) |
| VLESS | Reality + XHTTP | ✅ | Bypasses connection-length detection |
| VLESS | Reality + gRPC | ✅ | |
| VLESS | XHTTP + TLS | ✅ | Works behind a CDN |
| VLESS | WebSocket + TLS | ✅ | CDN, works with every client |
| VLESS | HTTPUpgrade + TLS | ✅ | CDN, lighter than WS |
| VLESS | gRPC + TLS | ✅ | CDN |
| VLESS Encryption | ML-KEM-768 (post-quantum), no TLS | ✅ | New in 2025/26 |
| VLESS | mKCP + FinalMask | ✅ | UDP, header obfuscation |
| VMess | WebSocket (± TLS) | ✅ | For older clients |
| Trojan | TLS / Reality / WS | ✅ | |
| Shadowsocks 2022 | blake3-aes-128/256, chacha20 | ✅ | Multi-user |
| Shadowsocks | aes-256-gcm / chacha20 | ✅ | Compatible with Outline |
| Hysteria2 | QUIC + Salamander | ✅ | Hysteria2 inbound is new in Xray 26.3 |
| WireGuard | Userspace, per-user peers | ✅ | Per-user stats via email |
| SOCKS5 / HTTP | With authentication | ✅ | |
| MASQUE | CONNECT-IP over HTTP/3 | 🗺 | New in Xray 26; few clients |
| XDNS | DNS tunnel (FinalMask + mKCP) | ✅ | For shutdowns: VLESS over mKCP inside DNS queries (EDNS0 1232, server MTU 900). Needs a domain whose NS record points to the server; links dial a public resolver (8.8.8.8 by default) or the server directly, with the mask in the `fm` parameter. On port 53 Dunvex binds the outward address, beside systemd-resolved. Tested end to end with real Xray (direct mode) |
| XICMP | ICMP tunnel | 🗺 | |
| Sudoku | FinalMask obfuscation, TCP/UDP | 🗺 | |
| XDRIVE | Tunnel through cloud storage (S3/Drive) | 🗺 | Newest Xray feature (26.10) |

## sing-box
| Protocol | Status | Notes |
|---|---|---|
| VLESS Reality Vision (universal) | ✅ | **Default.** Works with both Xray and sing-box apps |
| TUIC v5 | ✅ | QUIC, BBR |
| Hysteria2 (masquerade + Gecko) | ✅ | Gecko obfs is new in 1.14 |
| NaiveProxy | ✅ | Real Chrome fingerprint, needs a domain |
| ShadowTLS v3 | ✅ | Paired with Shadowsocks 2022 |
| AnyTLS | ✅ | Defeats TLS-in-TLS detection |
| Snell v5/v6 | 🗺 | New in 1.14 (Surge clients) |
| OpenVPN server | 🗺 | New in 1.14 |
| Hysteria v1 | — | Deprecated in 1.14 |

## Independent / lesser-known protocols (sidecars)
| Protocol | What it is | Status |
|---|---|---|
| AmneziaWG 3.1 / 2.0 | WireGuard with junk packets, random sizes and message headers, and (3.1) header protection | ✅ amneziawg-go (MIT), userspace TUN with NAT; one per server, serving every user. Each user gets a `.conf` (download and QR on the user page and in the panel) for the AmneziaVPN / AmneziaWG apps. Users are added and removed live; usage, online state and devices come from the interface's counters. Tested with a real client: no packet on the wire looks like WireGuard. Built from upstream source with each release, downloaded with the first AmneziaWG |
| Mieru (TCP or UDP) | Proxy with no fixed pattern: no handshake to fingerprint, random padding, a key from the user's name, password and the time | ✅ mita 3.39.0 (GPL-3.0), downloaded from the Mieru release with the first Mieru. One per server (mita serves every user on its port). Users join and leave by a reload, without cutting anyone off; a removed user who still has a connection open is cut at the next look at the counters (mita is started again). Usage and online state per user; no devices (mita does not report addresses). Links: `mierus://` (Karing, Exclave, NyameBox, mieru) and Clash.Meta profiles; not in sing-box profiles. Tested with the official mieru client importing Dunvex's link, over TCP and UDP |
| TrustTunnel (AdGuard) | HTTP/2/3 VPN that looks like normal traffic | 🗺 |
| MTProto (mtg) | Telegram proxy with FakeTLS | ✅ mtg 2.3.0 (MIT): one per server, one `tg://proxy` link for everyone (no per-user usage). Can share port 443 by SNI; probes see the fake domain's real site. Downloaded with the first Telegram proxy |
| dnstt / Slipstream / VayDNS | DNS tunnels; the only working path in the February 2026 shutdown | 🗺 |
| paqet | KCP over raw TCP packets (bypasses the OS stack) | 🗺 |
| AIVPN | UDP disguised as STUN/QUIC/video calls | 🗺 |
| olcRTC / qWDTT / CSQTT | Tunnels through WebRTC/TURN of video-call services | research |
| Juicity, Brook, Restls | Older options | research |
| Tor (obfs4, Snowflake, WebTunnel) | Bridges | research |

> **Reality compatibility:** since Xray 26.9.8, Xray's Reality server requires an X25519MLKEM768 key share that sing-box clients (Hiddify, NekoBox, sing-box, Karing) do not send yet ([sing-box#4520](https://github.com/SagerNet/sing-box/issues/4520)). Dunvex therefore uses the universal Reality on the sing-box engine as the default, and leaves Xray-Reality entries out of the sing-box subscription.
>
> **Test status (v1.0.1):** every preset passes the core config check; real traffic went through 25 of 26 presets with Xray and sing-box clients. NaiveProxy has only the config check (it needs a real domain and certificate).
>
> Note: claims of "resistance to filtering" in the sources are often unmeasured. Dunvex only marks a protocol ✅ after a real core config test.
