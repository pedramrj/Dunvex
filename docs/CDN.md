# CDN helper (v1.17)

> The CDN helper is now part of the **Domains** page: add a name with the **CDN** role (or open one) and the clean
> addresses, the check and the Cloudflare setup are in its panel. Several CDN names can each carry the CDN protocols.
> See [DOMAINS.md](DOMAINS.md); what follows still describes each piece.

The CDN protocols (VLESS over XHTTP, WebSocket, HTTPUpgrade or gRPC, Trojan and VMess over WebSocket) can sit behind
a CDN such as Cloudflare: users connect to the CDN, and the CDN carries their traffic to this server. Filtering then
sees the CDN, not your server. The CDN helper puts it together:

- **Links through the CDN**: the CDN protocols' configs use the CDN name, and one more config per **clean address**.
- **Check**: the name's DNS, each protocol through the CDN and straight to the server, and each clean address,
  with what went wrong in plain words (Cloudflare's 52x errors included).
- **Cloudflare setup**: with your own API token, the DNS record and the settings the protocols need, in one click.

## Two names

Use a separate name for the CDN, for example `cdn.example.com`, and keep your main domain **DNS only** (grey cloud).
Reality, Hysteria2, TUIC and the other direct protocols connect to the main domain: behind the CDN they would stop
working. Only the CDN name is **Proxied** (orange cloud). A first-level name (`cdn.example.com`, not
`a.cdn.example.com`) is covered by Cloudflare's free certificate.

## Setting it up

1. Add a CDN protocol on the **Protocols** page. VLESS · XHTTP · TLS works best. New CDN protocols pick a port
   Cloudflare proxies (2053, 2083, 2087, 2096 or 8443 with TLS; 8080, 8880, 2052, 2082, 2086 or 2095 without).
2. On the **CDN** page, enter the CDN name, then either:
   - paste a Cloudflare API token under **Cloudflare setup** and press **Set up Cloudflare**, or
   - in Cloudflare yourself: add an `A` record for the name pointing to this server, Proxied, and set
     **SSL/TLS** to **Full**.
3. Turn **CDN protocols go through the CDN** on and save, then press **Check**.

### The API token

In Cloudflare: **My Profile › API Tokens › Create Token**, the **Edit zone DNS** template. Add the permission
**Zone · Zone Settings · Edit** (for the SSL/TLS mode and WebSockets) and pick your domain under **Zone Resources**.
The token is sent to Cloudflare for that one setup and is never saved, logged or shown again (unless you choose to
keep one on the Domains page, for one-click records and DNS validation of certificates).

The setup:

- finds the domain in your account (it says so when the domain is not active yet, until its name servers point to
  Cloudflare);
- creates the `A` record of the CDN name, Proxied, to this server (or turns Proxied on). When the name already has
  records that point elsewhere (another `A`, an `AAAA` or a `CNAME`), nothing changes until you choose **Replace them**;
- sets **SSL/TLS** to **Full** when it is **Off**, or **Full (strict)** with this server's self-signed certificate
  (you can turn this off; the mode applies to the whole domain). **Flexible** is left alone: it only affects
  port 443;
- turns **WebSockets** on when they are off.

gRPC has no API switch: turn it on yourself in **Cloudflare › Network › gRPC**. Cloudflare documents gRPC on port
443 only.

## What users get

With the CDN on, each CDN protocol gives:

- one config to the CDN name (`Dunvex · CDN · VLESS · XHTTP · TLS`), or to the protocol's own **Address in links**
  when it has one;
- one config per clean address (`Dunvex · MCI · VLESS · XHTTP · TLS`), with the CDN name as SNI and Host;
- the config straight to the server too, only with **Also give the direct config**.

Through the CDN, apps check the CDN's certificate as usual; the pinned certificate of the direct config is left out.
Clash and sing-box profiles get the same configs.

## Clean addresses

Some CDN addresses are blocked or slow on some networks. A **clean address** is a Cloudflare IP (or a name on
Cloudflare) that your users' networks reach well; each one gives one more config, with a label such as the operator's
name. Find them with a clean-IP scanner run inside the country: the panel's check runs from this server, so it shows
whether an address serves your CDN name, not whether it opens from inside the country.

## Reading the check

| Result | Meaning |
| --- | --- |
| The name was not found | No DNS record yet, or it has not spread |
| The name points straight to this server | The record is DNS only: turn Proxied on |
| 521 | Cloudflare could not connect: the port is closed or a firewall blocks Cloudflare |
| 522 | Cloudflare's connection timed out: a firewall drops its packets |
| 525 / 526 / 520 | TLS between Cloudflare and the server: set SSL/TLS to Full, not Full (strict) |
| 403 on gRPC | gRPC is off in Cloudflare › Network |
| 404 | A web server answered, but not the protocol's path (the Host or the path differs) |
| Nothing listens on this port | The protocol is off, or the core is not running |

## API

`GET /cdn` returns the settings and the CDN protocols; `PUT /cdn` saves
`{"enabled":true,"domain":"cdn.example.com","addresses":[{"address":"104.16.1.1","name":"MCI"}],"direct":false}`.
`POST /cdn/check` runs the check. `POST /cdn/cloudflare` runs the setup:
`{"token":"…","domain":"cdn.example.com","ssl":true,"websockets":true,"replace":false}`. All are owner only.
