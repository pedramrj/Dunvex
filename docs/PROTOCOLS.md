# Protocols (research as of November 2026)

Versions checked: Xray-core **v26.11.9**, sing-box **v1.14.4**, mtg **v2.3.0**, amneziawg-go **v3.1.20261116**, Hysteria **v2.14.0**, Mieru (mita) **v3.39.0**, TrustTunnel **v1.3.1**, dnstt **v1.20260501.0**, Slipstream **v0.1.1**; the SSH server is built in (golang.org/x/crypto/ssh **v0.59.0**).
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
| XICMP | Ping tunnel (FinalMask + mKCP) | ✅ | For networks where only ping works: VLESS over mKCP inside ICMP echo packets. No domain and no port (the server reads pings with a raw socket, so one per server). Links carry the mask in `fm`, with the apps' system ping sockets on by default (phones, macOS, Linux; off means raw pings, for Windows apps). Dunvex checks raw ICMP sockets before Xray starts, so a server without them only parks this protocol. Tested with real Xray in both ping modes (v1.22). See [XICMP.md](XICMP.md) |
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
| TrustTunnel (AdGuard) | VPN over HTTP/2 or HTTP/3 (QUIC) that looks like an ordinary HTTPS site | ✅ endpoint 1.3.1 (Apache-2.0), downloaded from the TrustTunnel release with the first one. One per server, TCP and UDP on its port. Without a domain a certificate is made for the hostname and carried in the link (pinned); with the panel's domain its real certificate is used. Users change with a short restart (the endpoint reads them once); it never runs without users (it would let anyone in). Usage, online state and the device address per user from its local /clients. `tt://` deep links (official apps, Shadowrocket); not in Clash or sing-box profiles. Tested: the link matches the endpoint's own export, and the official CLI client set up from it works over HTTP/2 and HTTP/3. **On 443 next to the panel (v1.20)**: the router hands it TCP by its hostname, and the port's UDP (QUIC) is passed on to it while no UDP protocol holds it; tested with the real client over both. There it reaches users through the router, so it cannot see their addresses: its connections do not count toward device limits. **Hidden from probes (v1.31.2)**: the apps prove a per-server key inside the TLS ClientHello (`client_random_auth_key`, TrustTunnel apps 1.3 and newer; carried in the link). On 443 the router sends every hello without it to the hostname's real site, so a probe for `www.apple.com` gets Apple's real certificate instead of the self-signed one; on its own port the endpoint drops them. On by default for new TrustTunnels and for servers with no users yet; an older one with users keeps it off until the owner turns it on (Protocols › TrustTunnel), then users update their subscription. Not on the panel's own domain, which has a real certificate. A request with no sign-in gets a plain 404 instead of "407 Proxy Authentication Required". Tested with the real endpoint 1.3.1 and client 1.3.0 through the router over HTTP/2 and HTTP/3; older apps that ignore the key cannot connect while it is on |
| SSH (built in) | Port forwarding over SSH: looks like a server login, often keeps its speed where TLS protocols are throttled | ✅ inside Dunvex (golang.org/x/crypto/ssh), one per server on its own port (2222 by default; 22 is the server's own SSH). Each user signs in with their name and password; only `direct-tcpip` (no shell, commands, SFTP, agent, X11 or remote forwarding; private and loopback destinations refused). A host key (Ed25519) is made once and pinned in every link. Forwards leave through a SOCKS inbound of Xray on loopback, so the routing applies; users change without restarting Xray and whoever loses access is cut off at once. Usage, online state and devices per user from the SSH server. UDP over TCP (sing-box's, which the Hiddify app turns on). `ssh://` links (Hiddify app, ray2sing), `slipnet://` SSH profiles (SlipNet), Clash.Meta `type: ssh`, sing-box `ssh` outbound; NetMod and HTTP Injector take the details typed in; not in the Xray JSON. Tested with a real sing-box client through real Xray to the internet, and in the config check (v1.37). See [SSH.md](SSH.md) |
| MTProto (mtg) | Telegram proxy with FakeTLS | ✅ mtg 2.3.0 (MIT): one per server, one `tg://proxy` link for everyone (no per-user usage). Can share port 443 by SNI; probes see the fake domain's real site. Downloaded with the first Telegram proxy |
| dnstt | DNS tunnel (KCP + Noise inside DNS queries); one of the few paths that worked in the February 2026 shutdown | ✅ inside Dunvex (dnstt v1.20260501.0, CC0), any number per server. Streams go to a SOCKS inbound of Xray on loopback: each user signs in with their name and password (usage per user), or nobody when the tunnel is open (for DNSTT.XYZ). `slipnet://` links (SlipNet) and the details for DNSTT.XYZ on the user page. Shares port 53 with XDNS and Slipstream by tunnel domain. Tested with dnstt's own client and real Xray. See [DNS-TUNNELS.md](DNS-TUNNELS.md) |
| Slipstream | DNS tunnel over QUIC, faster than dnstt | ✅ the official server v0.1.1 (Apache-2.0, SHA-256 pinned), downloaded with the first one; one per server. Same sign-in, links and sharing of port 53 as dnstt; its apps do not verify the server's certificate. Tested with the official client set up from Dunvex's link and real Xray (v1.21.1) |
| VayDNS / NoizDNS | DNS tunnels of the SlipNet family | 🗺 |
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
