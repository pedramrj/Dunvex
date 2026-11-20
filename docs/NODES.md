# Other servers (nodes)

One panel runs protocols on several servers. On another server run the node installer:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/pedramrj/Dunvex/main/install.sh) node
```

and paste the token it prints under **Servers › Add server**. Each protocol then runs on the server you pick; users
get one subscription with every server, and their usage, devices and limits are counted in one place. The panel and
its nodes talk over mutual TLS with pinned certificates. A node can also be a relay in front of another server: see
[RELAY.md](RELAY.md).

## A domain and a real certificate (v1.18)

Without a domain a node serves its TLS protocols (Hysteria2, TUIC, TrustTunnel…) with a self-signed certificate
that the links pin. With a domain it gets a real one:

1. Point an `A` record of the domain (for example `de.example.com`) to the node's IP. In Cloudflare keep it
   **DNS only** (grey cloud).
2. Keep port 80 free on the node: Let's Encrypt checks the domain there.
3. On the **Servers** page press the globe on the node's card (or fill **Domain** when adding it) and save.

The node asks Let's Encrypt for the certificate (with the email from **Settings**), uses it for its protocols and
renews it a month before it expires. Its card shows the certificate and its expiry, or why there is none yet; a
failed attempt is tried again an hour later (Let's Encrypt limits failed checks).

With the domain set:

- **NaiveProxy and the CDN protocols** (XHTTP, WebSocket, HTTPUpgrade and gRPC over TLS, Trojan and VMess over
  WebSocket) can run on the node.
- Its TLS protocols use the domain as their server name, and once the certificate is there links stop pinning a
  certificate: apps check it like any website's. A changed domain moves the protocols that used the old one.

The panel's own Let's Encrypt certificate (**Settings › Certificate**) is renewed the same way now, and the panel and
its protocols pick up the new one without a restart.

## Updates in one click (v1.18)

When a node runs an older Dunvex than the panel, its card says so and offers **Update** (**Update all servers** with
more than one). The panel sends its own binary over the pinned link when the node runs on the same system (so nodes
that cannot reach GitHub, like relays in Iran, update too); otherwise the node downloads the panel's release from
GitHub, checked against its published digest.

The node runs the new binary once to check that it works on that server and is the version asked for, swaps it in,
and restarts (a few seconds). A node newer than the panel is never downgraded.

Nodes older than v1.18 cannot take updates from the panel yet: update them once on the server itself
(`dunvex update`, then `systemctl restart dunvex`).

## API

`POST /nodes` and `PUT /nodes/{id}` take `"domain"` (`""` removes it). `POST /nodes/{id}/update` updates one node,
`POST /nodes/update` every node that is behind. A node's view has `domain`, `realCert` and `update` (`via`:
`panel` or `github`; `newer`; `reason` when it cannot be updated from the panel); its status has `domain`,
`realCert`, `certExpiry`, `certError`, `os` and `arch`.
