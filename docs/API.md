# Dunvex API

Everything the panel does goes through this JSON API, so scripts and bots can do it too.

## Address

```
http(s)://<server>:<panel port>/<panel path>/api/<endpoint>
```

For example `http://203.0.113.10:443/kzqpagpimgim/api/users`. The panel path is the secret part of the
panel address (`dunvex info` shows it).

## Signing in with an API key

Make a key in the panel under **Security › API keys**. It is shown once; copy it then.

```bash
curl -H "Authorization: Bearer dvx_…" http://203.0.113.10:443/kzqpagpimgim/api/users
```

- A key acts as the admin who made it, with that admin's role (owner, admin or reseller).
- A **read-only** key can only use `GET`.
- No key can manage accounts, sessions, other keys or admins, or restore a backup. Those need a
  person signed in to the panel.
- Keys can expire (30, 90 or 365 days, or never). Deleting a key stops it at once.
- Only a SHA-256 of each key is stored on the server.

Requests from the browser panel use the session cookie and must send `X-Dunvex: 1` on changes;
requests with an API key do not need that header.

## Endpoints

Role: **R** any admin (resellers see only their own users), **A** admin or owner, **O** owner only.

| Method | Path | Role | What it does |
|---|---|---|---|
| GET | `/overview` | R | Dashboard numbers: users, traffic, devices, cores |
| GET | `/status` | R | Server load, live speed, core state |
| GET | `/events?limit=200` | A | Event log |
| GET | `/network/geo` | A | The server's own location |
| GET | `/network/clients` | A | Devices of the last 5 minutes by country (counts only, looked up offline) |
| GET | `/nodes` | A | Other servers with their state |
| POST | `/nodes` | O | Add a server: `{"name":"Frankfurt","token":"dvxnode1.…"}` (pairs at once) |
| PUT | `/nodes/{id}` | O | Rename, change the address, or turn a server off and on |
| DELETE | `/nodes/{id}` | O | Remove a server and its protocols (the node is released) |
| POST | `/nodes/{id}/sync` | A | Sync a server now |
| GET | `/users` | R | All users with usage and status |
| POST | `/users` | R | Create users: `{"name":"sara","dataLimit":32212254720,"durationDays":30,"ipLimit":2}` |
| POST | `/users/bulk` | R | Act on many users at once |
| GET | `/users/{id}` | R | One user, with connected devices |
| PUT | `/users/{id}` | R | Change a user (limit, expiry, protocols, note…) |
| DELETE | `/users/{id}` | R | Delete a user |
| POST | `/users/{id}/reset` | R | Reset the user's usage |
| POST | `/users/{id}/regenerate` | R | New UUID, password and subscription link |
| GET | `/users/{id}/usage` | R | Hourly usage history |
| GET | `/users/{id}/configs` | R | Share links and subscription address |
| GET | `/presets` | A | Every protocol preset and its fields |
| GET | `/inbounds` | A | Inbounds with their errors |
| POST | `/inbounds` | A | Add an inbound: `{"preset":"vless-reality-universal","port":443,"params":{"sni":"www.speedtest.net"},"nodeId":0}` (`nodeId`: the server, 0 = this one) |
| PUT | `/inbounds/{id}` | A | Change an inbound |
| DELETE | `/inbounds/{id}` | A | Remove an inbound |
| POST | `/tools/sni` | A | Test Reality SNI candidates |
| GET | `/tools/qr?text=…` | R | QR code (SVG) |
| GET | `/cores` | A | Xray and sing-box versions and state |
| POST | `/cores/apply` | A | Rebuild and reload the core configs |
| GET | `/cores/{name}/logs` | A | Recent core log lines |
| GET | `/cores/{name}/config` | O | The generated core config |
| POST | `/cores/{name}/install` | O | Install or update a core |
| GET / PUT | `/settings` | O | Read or change settings |
| GET | `/backup` | O | Download the database |
| GET / POST | `/update` | O | Check for, or install, a new Dunvex version |
| POST | `/users/{id}/renew` | R | Renew a user with a plan: `{"planId":1}` |
| GET / PUT | `/routing` | O | Blocking, Iranian sites and WARP; the rest is in [ROUTING.md](ROUTING.md) |
| GET | `/billing/plans` · `/billing/orders` · `/wallet` | R | Plans, orders and your wallet; the rest is in [BILLING.md](BILLING.md) |

Signed-in panel only (not API keys): `/me`, `/account`, `/account/2fa/*`, `/account/sessions` (list,
`DELETE /account/sessions/{id}`, `POST /account/sessions/others`), `/account/keys`, `/admins`, `/restore`.

Errors come back as `{"error": "…"}` with a 4xx or 5xx status.
