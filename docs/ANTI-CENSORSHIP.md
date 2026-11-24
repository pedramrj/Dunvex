# Anti-censorship options

**Settings → Subscription** has, next to the [TLS fragment](XRAY-JSON.md#tls-fragment), four more options that make
configs harder to filter: **ECH**, **multiplexing (with TCP Brutal)**, **WireGuard noise** and **XHTTP with separate
upload and download**. Each goes only where the user's app reads it; an app that does not know an option connects as
before. Saving any of them starts a [config check](CONFIG-HEALTH.md) at once.

## Who reads what

| Option | Links (v2rayNG, v2rayN, Streisand) | Links (Hiddify) | Xray JSON | sing-box profile | Clash Meta | The server |
| --- | --- | --- | --- | --- | --- | --- |
| TLS fragment | `fm` | `fragment` | finalmask | `tls.fragment` + `tls.record_fragment` | no | nothing |
| ECH, this server's | `ech` | `ech` | `echConfigList` | `tls.ech` | `ech-opts` | keys on its TLS inbounds |
| ECH, the CDN's | `ech` | `ech` | `echConfigList` | `tls.ech` | `ech-opts` | nothing (the CDN holds the keys) |
| Mux (Xray's Mux.Cool) | no app reads it | no | `mux` | — | — | nothing (Xray always takes it) |
| Mux (sing-box's multiplex) | — | `muxtype`… | — | `multiplex` | `smux` | `multiplex` on its sing-box inbounds |
| TCP Brutal | — | `muxup`, `muxdown` | — | `multiplex.brutal` | `smux.brutal-opts` | `multiplex.brutal` + the kernel module |
| WireGuard noise | `fm` | `ifp`, `ifps`, `ifpd` | finalmask | no (sing-box has none) | `amnezia-wg-option` | nothing |
| XHTTP split | `extra` | `extra` | `extra.downloadSettings` | no XHTTP | no XHTTP | nothing (one inbound, two ways) |

These names come from the apps' own code (v2rayNG `FmtBase.kt` and `WireguardFmt.kt`, v2rayN `BaseFmt.cs`,
Hiddify's ray2sing), Xray-core 26.11 and sing-box 1.14, as of November 2026.

## ECH (Encrypted Client Hello)

ECH encrypts the start of the TLS handshake, the server name included; a filter sees only the config's **public
name**. Two kinds:

- **This server's own ECH** (*This server's TLS configs with its own ECH*). The panel makes an X25519 key and its
  config for the public name you choose (default `www.speedtest.net`; any hostname works, the server does not need to
  own it). The keys go to the server's TLS inbounds over TCP (Xray's VLESS, VMess and Trojan with TLS: `echServerKeys`;
  sing-box's AnyTLS: `tls.ech.key`), the config to their apps. Reality (which has its own camouflage), QUIC (Hysteria2,
  TUIC) and the other protocols stay as they are. A config whose **Address in links** points elsewhere (often a CDN,
  which could not open it) is left without; relay copies keep it, as a relay passes TLS through untouched. Another
  server's configs get none: that server builds its cores from its own settings. A new public name makes new keys,
  and the apps take them with their next subscription update.
- **The CDN's ECH** (*CDN configs with the CDN's own ECH*): the configs that go through the [CDN](CDN.md) carry the
  ECH config the CDN publishes in the DNS HTTPS record of the CDN name (Cloudflare's public name is
  `cloudflare-ech.com`). The server reads it over DNS over HTTPS (Cloudflare's, then Google's), from outside the
  filtered network, and puts it in the config, so the apps need no DNS lookup of their own; it is looked up again every
  30 minutes. A name on Cloudflare without a record of its own (ECH off in its zone) gets Cloudflare's shared one, as
  Hiddify does; the panel shows which name it came from. With no ECH found, turn on **Encrypted ClientHello** in
  Cloudflare › SSL/TLS › Edge Certificates.

The AnyTLS link has no place for ECH that an app reads, so AnyTLS has ECH in the sing-box profile only.

## Multiplexing (Mux) and TCP Brutal

The app carries many connections over one connection to the server: fewer handshakes for a filter to watch and slow
down, and pages that open sooner. There are two kinds, and a server speaks only its own:

- **Xray's Mux.Cool**, in the Xray JSON subscription (v2rayNG, v2rayN and Streisand import the whole config; no app
  reads mux from a link): VLESS without a flow, VMess, Trojan and Shadowsocks over raw TCP, WebSocket, HTTPUpgrade or
  mKCP, on Xray servers (which always take it). Vision breaks it (the server refuses it), XHTTP and gRPC multiplex on
  their own, and the DNS and ping tunnels are too narrow. QUIC to port 443 is refused inside it (`xudpProxyUDP443:
  reject`), so browsers fall back to TCP.
- **sing-box's multiplex** (h2mux, smux or yamux, with padding), for sing-box apps, Clash (`smux`) and Hiddify (the
  link's `muxtype`, `muxsmax`, `muxpad`): only on this server's sing-box protocols that carry it, the universal VLESS
  Reality (Vision included) and the Shadowsocks behind ShadowTLS, whose inbounds turn it on. An Xray server does not
  speak it. Apps without it connect as before.

**TCP Brutal** makes the server send at a fixed rate instead of slowing down on the losses a filtered network causes.
It rides on sing-box's multiplex and needs the `tcp-brutal` module in the server's Linux kernel:

```bash
bash <(curl -fsSL https://tcp.hy2.sh/)
```

The panel looks for it (`/proc/sys/net/ipv4/tcp_available_congestion_control`, trying `modprobe brutal` once) and
shows whether it is ready. Without it Brutal goes into **no** config: a sing-box server refuses an app's Brutal request
it cannot meet and the whole connection fails, and on anything but Linux sing-box does not even start with it. The
speeds are from the user's side (upload, download); the server sends at most the download speed. A phone without the
module still gets the server's Brutal on downloads.

## WireGuard noise

Before WireGuard's first packet the app sends a few random UDP packets (default 5, 50-100 bytes, 10-20 ms apart), so
a filter that knows the start of a WireGuard tunnel sees junk first. The server drops them (WireGuard ignores what it
cannot decrypt) and needs nothing.

- v2rayNG and v2rayN read it from the WireGuard link's `fm` (Xray's UDP finalmask `noise`, which Xray's WireGuard
  sends through); the Xray JSON subscription has it too.
- Hiddify reads `ifp` (packets), `ifps` (size) and `ifpd` (pause).
- Clash Meta gets AmneziaWG junk packets (`jc`, `jmin`, `jmax`) only: without S1, S2 or H1 to H4 the tunnel is plain
  WireGuard, so any WireGuard server takes it.
- sing-box has no WireGuard noise: sing-box apps connect as before.

## XHTTP with separate upload and download

For VLESS · XHTTP · TLS while the CDN is on, each config through the CDN can send its downloads (or its uploads)
straight to the server: one session, two ways. Xray matches the two by the session, so both reach the same inbound.

| Setting | Uploads | Downloads |
| --- | --- | --- |
| Both the same way | through the CDN | through the CDN |
| Upload via CDN, download straight | through the CDN (the config's address) | straight to the server |
| Upload straight, download via CDN | straight to the server | through the CDN (the CDN name or the clean address) |

It is Xray's `downloadSettings`, in the link's `extra` (v2rayNG, v2rayN and Hiddify read it) and in the Xray JSON
subscription. The straight way pins the server's certificate when it is self-signed and carries this server's ECH when
that is on; the CDN way carries the CDN's. An inbound set to `stream-one` (both ways in one request) is left alone.
sing-box and Clash have no XHTTP.

## TLS fragment, extended

The fragment of v1.25 now also reaches Hiddify (the link's `fragment`, its own form) and sing-box apps (`tls.fragment`
and `tls.record_fragment` on TLS configs: sing-box takes no sizes or pauses, and its Reality ignores both). **Reality
configs** get the ClientHello split into TCP pieces (`1-1`) instead of TLS records: Xray's Reality server fails a
ClientHello that arrives as many TLS records sent apart ("target sent incorrect server hello"), which broke every Xray
Reality config on its own port while the fragment was on. Behind the panel's router on 443 the records reach it
together, which is why that case worked.

## Left out, and why

| Hiddify offers | Why Dunvex leaves it out |
| --- | --- |
| Mixed-case SNI (`mc=1`), TLS padding (`padsize`) | No current app applies them from a subscription: Hiddify's own link reader (ray2sing) has its TLS tricks commented out, and Xray 26, sing-box 1.14 and Clash Meta have no such option. |
| Mux in plain links for Xray apps | v2rayNG, v2rayN and Streisand read no mux from a link; they get Mux.Cool in the Xray JSON subscription. |
| sing-box's multiplex on Xray protocols | An Xray server speaks only Mux.Cool. |
| TLS fragment in Clash | Clash Meta has no fragment. |
| WireGuard noise for sing-box apps | sing-box has none. |
| XHTTP split for sing-box and Clash | They have no XHTTP. |
| ECH on QUIC and Reality | Reality has its own camouflage; QUIC is left alone until the apps' QUIC takes ECH. |
| sing-box's `tls.spoof` | It needs administrator rights on the device (Linux, macOS, Windows), which phone apps do not have. |

## Tested

Windows 11 with the real Xray 26.11.9 and sing-box 1.14.4 (`go test ./internal/server -run 'Tricks|Split|Noise|ECH'`
with `DUNVEX_TEST_BIN` set to a folder with the cores):

- **Every option on at once** (fragment, this server's ECH, Mux with smux, Brutal asked for, WireGuard noise): the
  cores started with the servers' side (ECH keys on Xray, sing-box's multiplex), and the config check loaded a page
  through all 22 configs (every Reality, every TLS transport, Hysteria2, TUIC, AnyTLS, ShadowTLS, Shadowsocks, mKCP,
  WireGuard) plus the sing-box profiles of the 10 Xray protocols sing-box apps get, with their ECH and fragment: 32 of
  32. Brutal stayed out, as the server was not Linux.
- **XHTTP split**: uploads and downloads through two different forwarders to one inbound, in `auto`, `packet-up` and
  `stream-up`; the page came back each time and each way carried its half.
- **WireGuard noise**: a UDP relay in front of the server counted the junk packets before the handshake (6 of 6); the
  page came back.
- **The CDN's ECH**: the server's lookup of a Cloudflare name gave `cloudflare-ech.com`'s config, and a real TLS
  handshake with it was accepted (`sni=encrypted`).
- sing-box's multiplex over VLESS Reality Vision, sing-box at both ends, and an Xray app (without mux) against the
  same server: both loaded the page.

Not tested here: TCP Brutal's speed (it needs a Linux server with the module; sing-box parses the settings and the
panel keeps them out without it) and Clash Meta (no Clash core in the test set; the fields follow its documentation).
