# Relay servers (v1.15)

A **relay** is a second server, usually inside Iran, that users connect to instead of the server abroad. It
passes their traffic, unchanged, to the server that runs the protocols. Users reach a domestic address (often
faster and less filtered), and only the relay talks to the server abroad.

The relay runs no protocols and holds no keys: the protocols' encryption goes end to end, from the phone to the
target server. Usage, limits and expiry are still counted on the target, exactly as before.

## Setting one up

1. On the relay server run the node installer: `bash <(curl -Ls https://raw.githubusercontent.com/pedramrj/Dunvex/main/install.sh) node`
   (on a server that cannot reach GitHub, copy the `dunvex` binary and use `DUNVEX_BINARY`).
2. In the panel: **Servers › Add server**, paste the token, choose **Relay**, and pick the **target server**: this
   panel's server or one of your nodes.

The panel and the relay talk over mutual TLS with pinned certificates, as with any node.

## What it forwards

Every protocol of the target, on the same port, TCP and UDP as the protocol needs (Reality, XHTTP, Trojan,
Hysteria2, TUIC, WireGuard, AmneziaWG, Mieru, TrustTunnel, the Telegram proxy and the rest). When the target's
protocols change, the relay follows within seconds.

Not forwarded: **NaiveProxy** (apps dial its domain, not an address), the **DNS tunnel** (XDNS works through DNS
records) and **CDN configs** (they already go through the CDN's address).

## Options

- **Port shift**: the relay listens on each of the target's ports plus this number. With 1000, the target's 443 is
  the relay's 1443. Useful when a port is already taken on the relay (a website on 443, for example).
- **Only through the relay**: users get only the relay's configs for the target; its direct configs are left out.

## What users get

Each relayed protocol appears once more in the subscription and on the user page, named after the relay (for
example `Dunvex · Tehran · Reality`), with the relay's address and port. AmneziaWG gets its own `.conf` for the
relay. Clash and sing-box profiles include the relayed configs too.

## The Servers page

A relay's card shows its target, every forwarded port with its state (listening, or why not), open connections,
bytes in each direction and the delay from the relay to the target (or that the target is unreachable).

## Good to know

- The target sees users' connections coming from the relay's address. Device limits count all of a user's devices
  that come through one relay as one device.
- The relay needs its ports open in its own firewall and must be able to reach the target.
- A relay cannot point at another relay, and a server with protocols must be emptied before it becomes a relay.

## API

`POST /nodes` and `PUT /nodes/{id}` take `"role": "relay"`, `"relayTarget"` (0 = this server, else a node id),
`"relayShift"` and `"relayOnly"`. A relay's status lists its `forwards`.
