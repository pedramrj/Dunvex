# Config health

Dunvex tests every config your users get, from the server, with the same client cores their apps use, and shows
which configs work. It runs on a schedule (every 3 hours by default) and whenever you press **Check now**.

A config counts as working when a web page (`http://cp.cloudflare.com/generate_204`, or Google's when Cloudflare's
does not answer) comes back through it. That is the whole path a user's app takes: the link, the server's address
and port, the protocol, the keys and the server's routing. A protocol that is "running" can still fail that test: a
wrong public address, a closed firewall port, a changed key, a Reality camouflage site that stopped answering, a CDN
setting. The check finds those.

## What is tested

The check builds the same configs a user's subscription has: every protocol that is on, on this server and on every
node, plus the **CDN copies** (through the CDN name and each clean address) and the **relay copies** of each config.
Each one is tested on its own, with the client a user's app would use:

| Config | Client | How |
| --- | --- | --- |
| Xray protocols (VLESS, VMess, Trojan, Shadowsocks, Hysteria2, WireGuard, SOCKS, HTTP, XHTTP, gRPC, WS, mKCP, XDNS, xICMP…) | Xray | The exact share link users import, read the way v2rayNG, v2rayN and Hiddify read it |
| sing-box protocols (Reality universal, TUIC, Hysteria2 masquerade, AnyTLS, ShadowTLS) | sing-box | The same outbound the sing-box subscription has |
| dnstt | dnstt's own client (inside Dunvex) | Through the inbound's resolver, then the user's SOCKS sign-in |
| Mieru, TrustTunnel, AmneziaWG, Slipstream, the Telegram proxy | — | Not tested yet: the server has no client for them. They show as "Not tested" with the reason |

NaiveProxy is tested only when the server's sing-box has its client built in; the usual build does not, so it shows
as "Not tested".

Up to four configs are tested at once. A config gets 25 seconds; DNS and ping tunnels (XDNS, dnstt, xICMP) get 75.
A config that fails is tried once more 8 seconds later, so a core that was restarting after a change is not reported
as broken.

The check signs in as its own user, `~health`. It is made from a secret the panel creates on its first start and
sends to its nodes, so it exists on every server. It is not in the database: it never shows in the user list, takes no
seat, counts no traffic and no devices. A real user's name cannot contain `~`, so the two never mix. Protocols with no
test client do not get this user at all.

## Reading the results

The **Overview** page has a **Config health** card: how many configs work, the failures first with what went wrong,
the working ones with how long a page took, and the configs that were not tested. Each card on the **Protocols** page
shows its result: the time in milliseconds, **Fails** (with the reason, and how many of its CDN and relay copies fail),
or **Not tested**. The time includes opening the connection, so DNS and ping tunnels are naturally slower.

Common reasons:

| Message | Meaning |
| --- | --- |
| `nothing answers on 203.0.113.10:8443 (i/o timeout)` | The port does not answer at all: a firewall, a wrong public address ("Address in links"), or the core is not running |
| `the server answers on …, but no page came back through the config` | The port is open but the config is refused: wrong keys, a changed preset, a Reality site that stopped answering, or the server's routing blocks the page |
| `the link cannot be opened: …` | The share link itself is broken; users cannot import it either |
| `the server refused the account` | The server has not taken the check's user yet (a node that has not synced since the update) |
| `no answer within 75s` | A DNS or ping tunnel that does not get through: check the NS record and that UDP 53 (or ping) reaches the server |

The port check is a plain TCP connection from the server. On a test machine behind a VPN app in TUN mode every
address "answers", so the first message never shows there; on a server it is exact.

## Schedule and alerts

**Settings → Server address → Automatic config check**: every 1, 2, 3, 6, 12 or 24 hours, or off (only when you press
**Check now**). The first scheduled check waits two minutes after the panel starts, so the cores have settled.

When a config stops working, or works again, Dunvex logs a `health` event. Like every event it reaches the
**Telegram bot** (as an alert to the admins) and your **webhooks** (follow the "Config health" kind). A failure is
reported once, not again at every check while it lasts.

## Metrics and API

`/api/metrics` adds, for every tested config:

| Metric | Labels | |
| --- | --- | --- |
| `dunvex_config_up` | `id`, `protocol`, `name`, `server`, `via` | 1 when the config passed the last check (configs not tested are left out) |
| `dunvex_config_latency_milliseconds` | the same | how long the page took through a working config |
| `dunvex_config_check_timestamp_seconds` | | when the last check finished |

`via` is `direct`, `cdn` or `relay`; `server` is `panel` or the node's name. An alert rule:

```yaml
- alert: DunvexConfigDown
  expr: dunvex_config_up == 0
  labels: { severity: warning }
  annotations: { summary: "{{ $labels.name }} on {{ $labels.server }} does not work" }
```

The API (admin role or an API key):

| Method | Path | |
| --- | --- | --- |
| GET | `/health` | The last report: `at`, `took`, `running`, `hours`, `next`, `summary` (`total`, `ok`, `fail`, `skip`) and `results` (each config's `inboundId`, `preset`, `name`, `server`, `via`, `status`, `ms`, `error`) |
| POST | `/health/check` | Starts a check now (202); it runs in the background, so read `/health` until `running` is false |
