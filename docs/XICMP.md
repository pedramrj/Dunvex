# xICMP: a tunnel inside pings

For networks where almost everything is blocked but **ping** still works. Xray's xICMP (a "finalmask", since Xray
26.2) hides a VLESS connection over mKCP inside ordinary ICMP echo packets: the app sends echo requests (pings) to
the server, and the server answers with echo replies that carry the data.

- **No domain and no port.** The server reads every ping it receives with a raw socket; the port in the link is not
  used. It only needs pings to reach the server (some hosts and firewalls drop ICMP).
- **One per server**, because it sees every ping the server gets.
- **Slow**, like the DNS tunnels: for when nothing else gets through.
- **Apps:** Xray-based apps with Xray 26.3 or newer that read the `fm` parameter of the link (v2rayNG, v2rayN and
  others). Not in Clash or sing-box profiles.

## VLESS Encryption (since v1.31.1)

Xray 26.11 apps refuse a VLESS link to a public address that has neither TLS nor encryption ("vless without TLS
or other encryption is prohibited"), so the plain links of earlier versions no longer connect from new v2rayNG,
v2rayN and Happ builds. Since v1.31.1 the tunnel carries VLESS Encryption (ML-KEM-768 + X25519, the same as the
"VLESS Encryption · XHTTP" protocol): the server holds the private key, the link carries `encryption=` with its
pair. Protocols made by an older version get their keys when the panel starts after the update.

- Users take the new link with their next subscription update; until then the old link stops working.
- Nodes need the same version, since each node builds its own Xray config: update them from Servers.
- Apps: the Xray versions new enough for xICMP and XDNS also have VLESS Encryption.

## Ping mode of the apps

- **System ping sockets** (the default): the app uses the system's unprivileged ping sockets, which apps on phones,
  macOS and Linux may open without special rights.
- **Off (raw pings)**: the app opens a raw ICMP socket instead. Windows has no ping sockets, so Windows apps need
  this; on Windows 11 an ordinary program (not administrator) could send and receive raw pings in our test. On Linux
  and macOS raw sockets need root.

## On the server

Dunvex runs Xray as root, which raw ICMP sockets need. Before Xray starts, Dunvex checks that it can open them for
IPv4 and IPv6; if not (no root, or IPv6 switched off in the kernel), the protocol shows that reason and the other
protocols keep working (Xray would otherwise refuse to start at all).

The server's kernel also answers every ping on its own; the apps ignore those answers. To stop them (optional, and
the server then no longer answers ordinary pings):

```
sysctl -w net.ipv4.icmp_echo_ignore_all=1
sysctl -w net.ipv6.icmp.echo_ignore_all=1
```

## Limits

- It cannot go through a relay server (a relay forwards ports; pings have none).
- Device limits ignore it: Xray gives each tunnel connection a stand-in address (`fd00::` and a random ID), which is
  no device. The same applies to XDNS; before v1.22 those addresses could count as devices and pause a user who had
  a device limit.

## Tested

With real Xray 26.11.9 on Linux (as root): the server under Dunvex, the client set up from the panel's link with its
VLESS Encryption, in both ping modes, through to the internet; `xray run -test` accepts the panel's client config for
a public address, and refuses the plain one of v1.31.0; the usage is counted for the user and the tunnel's connections
do not count as devices. Over loopback: about 0.5 MB/s with system ping sockets and 1.4 MB/s with raw ones before
v1.31.1; with VLESS Encryption about 0.6 MB/s in either mode. On Windows 11, raw pings worked from an ordinary program
and ping sockets do not exist. Phone apps have not been tested yet.
