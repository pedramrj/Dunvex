# DNS tunnels: XDNS, dnstt and Slipstream

For internet shutdowns. When everything else is blocked, ordinary DNS lookups often still work: the tunnel hides
its traffic in DNS queries for names under your tunnel domain, and public resolvers (8.8.8.8, 1.1.1.1…) deliver
them to your server, because the domain's NS record points there. It is slow, but it is often the last thing that
still works.

| | XDNS | dnstt | Slipstream |
|---|---|---|---|
| Runs as | Xray (finalmask + mKCP) | inside Dunvex (dnstt v1.20260501.0, CC0) | the official Slipstream server v0.1.1 (Apache-2.0), downloaded with the first one |
| Apps | Xray-based apps (v2rayNG, Happ…) | SlipNet (Android); DNSTT.XYZ (Android, desktop) when open; any dnstt client | SlipNet; DNSTT.XYZ when open |
| Speed | slow | slow | often several times faster than dnstt (QUIC) |
| Encryption | VLESS over mKCP | Noise, to this server's key (the apps check it) | TLS 1.3 inside QUIC; the apps do not verify the server's certificate |
| Per server | any number | any number | one |

## Set up the DNS records

In your domain's DNS (for example at Cloudflare, with the proxy **off**):

1. An **A** record such as `ns.example.com` pointing to this server's IP.
2. An **NS** record such as `t2.example.com` pointing to `ns.example.com`.

`t2.example.com` is then the tunnel domain. The A record's name must not be under the NS record's name. UDP port 53
must be open in the server's firewall.

## Several tunnels on port 53

XDNS, dnstt and Slipstream can all use port 53 at once, each with its **own** tunnel domain: add one NS record per
tunnel (`t1.example.com`, `t2.example.com`, `t3.example.com`), all pointing to the same `ns.example.com`.

When a port has dnstt, Slipstream or more than one XDNS, Dunvex puts a small DNS splitter on it. Each query goes to
the tunnel whose domain it names; a name no tunnel serves gets **REFUSED**, as from any DNS server that does not
serve that name (the answer is never larger than the question, so the port cannot be used to amplify attacks). XDNS
alone on its port works as before, with no splitter. On port 53 Dunvex listens on the address facing the internet,
next to systemd-resolved (which holds 127.0.0.53:53). DNS over TCP on port 53 is not served.

## Sign-in

dnstt and Slipstream carry the apps' connections to a SOCKS5 inbound of Xray on the server's loopback:

- **Each user's name and password** (the default): usage is counted per user and the user's limits, expiry and
  protocol access apply. For SlipNet: the link on the user's page carries their name and password.
- **Open, no password**: for apps that cannot send a SOCKS password, such as DNSTT.XYZ. Anyone who knows the tunnel
  domain (and dnstt's public key) can connect, and usage is counted only for the protocol, not per user. The tunnel
  runs whenever it is enabled, even with no users.

Routing rules (WARP, blocked sites…) apply to both, as to every Xray protocol.

## Links

- **SlipNet**: each user's page has a `slipnet://` link and its QR (import it in SlipNet). These links are kept out of
  the subscription for Xray and sing-box apps, which cannot read them.
- **DNSTT.XYZ** (open tunnels): the user's page also shows the domain, the DNS address and, for dnstt, the public
  key, to type in. In the panel, the user's configs have a button that copies them.
- In the link, the resolver is the one chosen on the protocol (8.8.8.8 by default), or the server itself
  ("direct", marked as authoritative so the app may query faster).

## Limits

- Device limits do not apply: the tunnels reach Xray from the server itself, so the users' addresses are not seen.
- DNS tunnels cannot go through a relay server (they travel by DNS records, not to an address).
- Slipstream's apps do not check the server's certificate, so on a path an attacker controls, a per-user password
  could be read. dnstt checks the server's key.
- Slipstream's builds need glibc and OpenSSL 3 (Ubuntu 22.04+, Debian 12+); the download is checked against its
  SHA-256, pinned in Dunvex.
- Slipstream v0.1.1 cannot be told which address to listen on, so besides port 53 (through the splitter) it also
  answers on its internal port (40000 and up) on every address. That port only serves the same tunnel; close
  40000–65000 UDP in a firewall if you want it hidden.

## Tested

- dnstt: a real dnstt client (dnstt's own client code) through the splitter to the in-process server; with real Xray
  on Linux, through the user's SOCKS sign-in to the internet; a wrong password is refused, and the usage is counted
  for the user and the protocol. A client with another public key gets no session.
- XDNS behind the splitter, next to dnstt on the same port, with a real Xray client.
- The `slipnet://` link has the 84 fields of SlipNet's format v30, in SlipNet's own order.
- Slipstream: the official server v0.1.1 under Dunvex, behind the splitter next to dnstt, with the official client
  set up from the panel's SlipNet link, on Linux with real Xray: through the user's SOCKS sign-in to the internet,
  a wrong password refused, every DNS query answered and the usage counted for the user (2 MB at about 2.8 MB/s
  straight to the server; through public resolvers it is much slower).
