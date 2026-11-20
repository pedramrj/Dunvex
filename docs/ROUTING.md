# Routing and Cloudflare WARP (v1.14)

**Routing** in the panel (owner only) decides what the server blocks, how Iranian sites open, and which traffic
leaves through Cloudflare WARP instead of the server's own address.

The rules run in Xray and sing-box. Mieru and TrustTunnel send their traffic through a local Xray port whenever a
rule is on, so they follow the same rules. AmneziaWG (a network interface) and the Telegram proxy are not covered.
Other servers (nodes) get the same rules from the panel.

## Blocking

- **BitTorrent**: on by default.
- **Ads and trackers**: the `category-ads-all` list.
- **Adult sites**: the `category-porn` list.
- **Your own domains**: one per line. A domain also blocks its subdomains; URLs and `*.` are cleaned up.

Private and local addresses are always blocked, so nobody can reach the server's own services through the VPN.

## Iranian sites

On the server, Iranian sites (the `category-ir` list, every `.ir` name and Iran's IP ranges) can be:

| Choice | What happens |
|---|---|
| Direct | They open from the server's address (the usual way). |
| Blocked | They do not open through the VPN, and Iranian sites never see the server's address. |
| Through WARP | They open with a Cloudflare address; the server's address stays hidden. |

**On users' devices** (on by default): the sing-box and Clash subscriptions send Iranian sites and addresses
straight from the phone, outside the VPN, and resolve Iranian names with the phone's own DNS. Banks and sites that
refuse foreign addresses work, and that traffic does not use the server. Apps that only take links (v2rayNG,
Hiddify, Streisand…) have their own "bypass Iran" setting.

## Cloudflare WARP

**Turn WARP on** registers a free, anonymous WARP device with Cloudflare (no email or password) and keeps its
WireGuard keys on the server. Then choose what goes through it:

- **Chosen sites**: AI (ChatGPT, Gemini, Claude, Copilot), Google, YouTube, Netflix, Disney+, Spotify, Instagram /
  Facebook / WhatsApp, X and Grok, plus your own domains. Useful for sites that refuse data-centre addresses or keep
  showing captchas.
- **Everything**: all traffic leaves with a Cloudflare address. Domains in "never through WARP" and Iranian sites set
  to Direct still use the server's address.

**Check the connection** loads Cloudflare's trace page through WARP and shows the address the internet sees.
A **WARP+ key** from the 1.1.1.1 app can be added. **Remove WARP** turns its rules off and deletes the device at
Cloudflare. WARP runs inside the cores in user space: it creates no network interface or routing table on the server.

Each node registers its own WARP device the first time the panel's rules need it (one device cannot be used from
two servers at once).

From SSH: `dunvex warp` shows the device, `dunvex warp on` registers it, `dunvex warp check` shows the address seen
through WARP, `dunvex warp license <key>` adds a WARP+ key and `dunvex warp off` removes it.

## Where the lists come from

The site and country lists are Xray's own `geosite.dat` and `geoip.dat`, installed and updated with Xray. sing-box
gets the same lists as rule-set files that Dunvex writes from them, so both cores always agree.

## API

| Method | Path | Role | What it does |
|---|---|---|---|
| GET | `/routing` | O | The rules, the WARP device (without its keys) and the site groups |
| PUT | `/routing` | O | Save: `{"blockTorrent":true,"settings":{"blockAds":true,"iran":"block","clientIran":true,"warp":"sites","warpGroups":["ai"],"warpDomains":["grok.com"],"blockDomains":[],"directDomains":[]}}` |
| POST | `/routing/warp` | O | Register the WARP device |
| DELETE | `/routing/warp` | O | Turn WARP off and remove the device |
| POST | `/routing/warp/license` | O | Add a WARP+ key: `{"license":"…"}` |
| POST | `/routing/warp/check` | O | The address seen through WARP and directly |
