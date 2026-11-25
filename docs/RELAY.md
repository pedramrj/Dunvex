# Relay servers (v1.15) and reverse tunnels (v1.16)

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

## Forward, reverse or bridge

- **Forward** (default): the relay connects to the target server for each user connection.
- **Reverse**: the target server connects to the relay instead and keeps a few links open to the relay's **tunnel
  port**; user connections travel back over those links. Nothing ever connects from the relay to the server abroad,
  which helps when connections from Iran to abroad are blocked or slow while the other direction still works.
- **Bridge**: the same shape as reverse, but the one TLS link is replaced by the **Dunvex Bridge**, a resilient
  multi-carrier tunnel (ICMP, fake-TCP, UDP, a CDN with ECH, DNS) that probes every second and fails over on its own.
  For the worst conditions, when no single protocol survives the border. See [BRIDGE.md](BRIDGE.md).

The reverse links are TLS 1.3 to the relay's own certificate (the same one the panel pins for the relay), plus a
32-character secret the panel makes for each relay. Many user connections share each link (multiplexed with
[smux](https://github.com/xtaci/smux), MIT), and the links reconnect by themselves. The target server reaches only
the ports of the protocols it relays, never anything else on itself. The relay's card shows the tunnel: how many
links are up and where they come from.

The tunnel port must be open on the relay and differ from the protocols' ports (with a port shift, from the shifted
ones). The target can be this panel's server or a node; a node keeps its links even after a restart.

## What it forwards

Every protocol of the target, on the same port, TCP and UDP as the protocol needs (Reality, XHTTP, Trojan,
Hysteria2, TUIC, WireGuard, AmneziaWG, Mieru, TrustTunnel, the Telegram proxy and the rest). When the target's
protocols change, the relay follows within seconds.

Not forwarded: **NaiveProxy** (apps dial its domain, not an address), the **DNS tunnels** (XDNS, dnstt and
Slipstream work through DNS records), the **ping tunnel** (xICMP has no port to forward) and **CDN configs** (they already go through the CDN's address).

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

- The target sees users' connections coming from the relay's address (in reverse mode, from itself). Device limits
  count all of a user's devices that come through one relay as one device.
- The relay needs its ports open in its own firewall. In forward mode it must be able to reach the target; in reverse
  mode the target must be able to reach the relay's tunnel port.
- A relay runs no routing rules and never registers WARP: those belong to the server that runs the protocols.
- A relay cannot point at another relay, and a server with protocols must be emptied before it becomes a relay.

## API

`POST /nodes` and `PUT /nodes/{id}` take `"role": "relay"`, `"relayTarget"` (0 = this server, else a node id),
`"relayShift"`, `"relayOnly"`, `"relayMode"` (`forward`, `reverse` or `bridge`) and `"relayPort"` (the tunnel port in
reverse mode). In bridge mode they also take `"bridge"`: `{ "carriers": [{ "kind", "on", "priority", "capMBps",
"port", "serverRequests" }], "tlsDomain", "dnsZone" }`. A relay's status lists its `forwards`, in reverse mode its
`tunnel`, and in bridge mode its `bridge` (every carrier's live state). See [BRIDGE.md](BRIDGE.md).
