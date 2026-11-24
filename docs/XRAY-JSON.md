# Xray JSON subscription and TLS fragment

## Xray JSON subscription

Next to the plain link list, the Clash profile and the sing-box profile, every user has an **Xray JSON
subscription**: their subscription address with `/xray` at the end (or `?format=xray`). It is a JSON array with one
whole Xray config per config of the user, the form v2rayNG, v2rayN and Streisand import as a subscription.

Each config has:

- the proxy, built from the user's own share link, so it matches the link exactly;
- SOCKS on `127.0.0.1:10808` and HTTP on `127.0.0.1:10809`, where those apps send the device's traffic;
- routing: local networks direct, and, when **Routing → Iranian sites direct on devices** is on, Iranian sites and
  addresses direct too, resolved by the device's own DNS. Rules are spelled out (`domain:ir`, address ranges), so no
  geo file is needed on the device;
- DNS over HTTPS (1.1.1.1 and 8.8.8.8) through the proxy;
- the **TLS fragment** (below) on every TCP config with TLS or Reality, VMess included;
- the config's name in `remarks`, as in the other formats, with the usage line first when **Show data and days in the
  config list** is on. A user who is over the limit, expired or disabled gets one config that says so.

Configs Xray cannot open are left out: TUIC, AnyTLS, ShadowTLS, NaiveProxy, Mieru, TrustTunnel, AmneziaWG, the DNS
tunnels for SlipNet and the Telegram proxy. They stay in the user's other formats.

Where to find it: the **Users** sheet of each user (under the subscription link), and the user page, where
**v2rayNG · Xray JSON** adds it to v2rayNG in one tap. In v2rayN, add it as a subscription address.

## TLS fragment

**Settings → Subscription → TLS fragment**. With it on, Xray apps send the start of each connection (the TLS
ClientHello, which carries the site's name) in small pieces with short pauses, so filters that read the name from
one packet miss it. It is the client's doing: the server needs no change.

| Setting | Default | |
| --- | --- | --- |
| What is split | The ClientHello only (`tlshello`) | or the first packet (`1-1`), or the first three (`1-3`) |
| Piece size | `100-200` bytes | a number or a range, 1 to 4000 |
| Pause between pieces | `10-20` ms | a number or a range, 0 to 1000 |

It uses Xray's current form, a TCP finalmask: `streamSettings.finalmask.tcp: [{"type": "fragment", ...}]`. The old
`fragment` of the freedom outbound is deprecated since Xray 26. Where it goes:

- **Links** of TLS and Reality configs over TCP (VLESS and Trojan; raw, XHTTP, gRPC, WebSocket and HTTPUpgrade) carry
  it as `fm`, which v2rayNG, v2rayN and other Xray apps read. VMess links have no place for it, so VMess gets it only
  in the Xray JSON subscription.
- **The Xray JSON subscription** has it on every such config.
- UDP and QUIC protocols (Hysteria2, TUIC, WireGuard, mKCP, XDNS, xICMP) and plain configs are left alone. sing-box
  and Clash profiles do not change.

Apps whose Xray is older than March 2026 (the first with TCP finalmask) ignore it and connect as before.

When the setting changes, the panel runs a [config check](CONFIG-HEALTH.md) at once. The check reads the same links,
so it tests every config with the fragment as the users get it.

### On port 443 next to the panel

A fragmented ClientHello arrives in many small TLS records. The router on the panel's port, which sends Reality,
TrustTunnel and the Telegram proxy their connections by server name, joins those records until the ClientHello is
whole before it reads the name (v1.25). Before that, only the first protocol on 443 worked with the fragment.

### Tested

Ubuntu 24.04 with real Xray 26.11 and sing-box 1.14, the fragment on (ClientHello, 100-200 bytes, 10-20 ms): the
config check passed for all 15 testable configs, among them every Reality preset on 443 by name and on its own port
(sing-box's universal Reality too) and every TLS transport. Each config of a user's Xray JSON subscription ran in real
Xray and loaded a page; xICMP did so once the machine allowed unprivileged ping sockets, as phones do.
