# Webhooks and Prometheus metrics (v1.19)

Both are under **Settings › Webhooks & metrics** (owner only).

## Webhooks

Every Dunvex event goes to the addresses you add: users created, renewed, limited or expired, servers going offline
and back, core errors, sign-ins and API keys, orders and payments, certificates, updates. These are the same
events as the **Alerts** page.
Each webhook follows all events or the kinds you pick: `user`, `limit`, `auth`, `billing`, `node`, `core`, `tls`,
`inbound`, `health` (configs that stop working or work again; see [CONFIG-HEALTH.md](CONFIG-HEALTH.md)), `backup`
(a backup that failed or did not leave the server; see [BACKUP.md](BACKUP.md)), `settings`, `update`.

Formats:

- **JSON** for your own code: the body is

  ```json
  {"id": "evt_…", "type": "user", "message": "sara is now limited and was disconnected",
   "at": 1795000000, "panel": "Dunvex", "version": "v1.19.0"}
  ```

  with the headers `X-Dunvex-Event` (the type), `X-Dunvex-Delivery` (the id) and `X-Dunvex-Signature`.
- **Slack** (`{"text": …}`, also Mattermost and Rocket.Chat) and **Discord** (`{"content": …}`): a one-line message
  for a channel's incoming-webhook address.

A delivery that gets no answer, a 429 or a 5xx is tried again after 10 seconds, 1 minute and 5 minutes; other
answers are final. **Test** sends a test event at once, and **Deliveries** shows the last 30 attempts with their
answers.

### Checking the signature

`X-Dunvex-Signature: t=<unix time>,v1=<hex>`, where `v1` is HMAC-SHA256 of `<unix time>.<body>` with the
webhook's secret (`whsec_…`, shown when the webhook is made and when you edit it; **New secret** replaces it).
Compare in constant time and refuse old requests:

```python
import hashlib, hmac, time

def valid(secret: str, header: str, body: bytes, tolerance=300) -> bool:
    parts = dict(p.split("=", 1) for p in header.split(","))
    t, sig = parts.get("t", ""), parts.get("v1", "")
    if not t.isdigit() or abs(time.time() - int(t)) > tolerance:
        return False
    want = hmac.new(secret.encode(), f"{t}.".encode() + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(want, sig)
```

## Prometheus metrics

`GET <panel address>/<panel path>/api/metrics` returns the panel's state in Prometheus' text format. It needs an
API key in the `Authorization: Bearer` header; a read-only key (**Security › API keys**) is enough.

```yaml
scrape_configs:
  - job_name: dunvex
    scheme: https
    metrics_path: /your-panel-path/api/metrics
    authorization:
      credentials: dvx_…   # a read-only API key
    static_configs:
      - targets: ["panel.example.com:2096"]
```

| Metric | Labels | |
| --- | --- | --- |
| `dunvex_info` | `version`, `panel` | always 1 |
| `dunvex_users` | `status` | users by status (active, pending, limited, expired, disabled, paused) |
| `dunvex_users_online` | | users seen in the last minute |
| `dunvex_user_traffic_bytes_total` | `user`, `direction` | traffic in the current period (restarts when usage is reset) |
| `dunvex_inbound_traffic_bytes_total` | `id`, `protocol`, `name`, `server`, `direction` | traffic through each protocol |
| `dunvex_inbound_up` | `id`, `protocol`, `name`, `server` | 1 when the protocol is on and running |
| `dunvex_config_up` | `id`, `protocol`, `name`, `server`, `via` | 1 when the config passed the last [config health](CONFIG-HEALTH.md) check |
| `dunvex_config_latency_milliseconds` | `id`, `protocol`, `name`, `server`, `via` | how long a page took through each working config |
| `dunvex_config_check_timestamp_seconds` | | when the last config check finished |
| `dunvex_server_up` | `server`, `role` | 1 when a server answers (`panel` is this one) |
| `dunvex_server_cpu_percent`, `…_memory_used_bytes`, `…_memory_total_bytes`, `…_disk_used_bytes`, `…_disk_total_bytes`, `…_uptime_seconds` | `server` | each server's resources |
| `dunvex_server_network_bytes_per_second` | `server`, `direction` | network speed now |
| `dunvex_server_last_seen_timestamp_seconds` | `server` | when a node last answered |
| `dunvex_core_up`, `dunvex_core_restarts_total` | `server`, `core` | each installed core |
| `dunvex_cert_expiry_timestamp_seconds` | `server`, `domain` | expiry of each Let's Encrypt certificate |
| `dunvex_relay_connections`, `dunvex_relay_traffic_bytes_total` | `relay`, `port` (`direction`) | relay ports |
| `dunvex_events_total` | `kind` | events since the panel started |
| `dunvex_webhook_deliveries_total` | `result` | webhook deliveries since the panel started |

On very large panels `?users=false` leaves out the per-user series. Example alerts:

```yaml
- alert: DunvexServerDown
  expr: dunvex_server_up == 0
  for: 3m
- alert: DunvexProtocolDown
  expr: dunvex_inbound_up == 0
  for: 5m
- alert: DunvexCertificateExpiring
  expr: dunvex_cert_expiry_timestamp_seconds - time() < 14 * 86400
```

## API

`GET /webhooks` lists the webhooks (and the event kinds); `POST /webhooks` adds one
(`{"name","url","format","kinds","enabled"}`); `PUT /webhooks/{id}` changes it (`"rotateSecret": true` makes a new
secret); `DELETE /webhooks/{id}`; `POST /webhooks/{id}/test`; `GET /webhooks/{id}/deliveries`. All are owner only.
