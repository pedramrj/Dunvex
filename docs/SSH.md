# SSH proxy

Dunvex has its own SSH server, built in (Go, `golang.org/x/crypto/ssh`). It does one thing: **port forwarding**.
Users sign in with their own name and password, and every connection their app forwards goes out through Xray,
so the server's routing and limits apply to it as to every other protocol.

Why: on some Iranian networks every TLS-based protocol connects but is throttled to a few KB/s, while SSH to the
same server keeps its full speed (measured on an Iranian mobile ISP in November 2026: Reality, XHTTP, Hysteria2 and
CDN configs at 1–3 KB/s, SSH at about 3.3 MB/s). SSH looks like an ordinary admin logging into a server.

## Setting it up

1. **Protocols › Add › SSH**. The port defaults to **2222**; port 22 belongs to the server's own SSH (OpenSSH), so
   pick another one. Open it for TCP in the firewall. One SSH per server.
2. A host key (Ed25519) is made once and kept in the inbound. Its public half is in every link and profile, so an
   app that checks it cannot be fooled by a man in the middle. **Protocols › SSH › Advanced** shows it; clearing
   the private key makes a new one (users then update their links or subscription).
3. Nothing else: there is nothing to install, and users who may use the inbound can sign in at once.

## The apps

| App | How it gets the config | Notes |
|---|---|---|
| **Hiddify** (Android, iOS, Windows, macOS, Linux) | The subscription, or the `ssh://` link from the user's page | `ssh://name:password@host:port/?hk=<host key>` as `hiddify/ray2sing` reads it. Hiddify turns UDP over TCP on for SSH, so calls, games and QUIC work too |
| **SlipNet** (Android) | The `slipnet://` link (or its QR) on the user's page, named "… · SlipNet" | SlipNet's own profile, tunnel type SSH. It does not check host keys |
| **Clash.Meta / mihomo** (Clash Verge, FlClash, ClashMi…) | The Clash profile | `type: ssh` with the host key pinned. mihomo's SSH carries TCP only |
| **sing-box apps** (sing-box, Karing, NekoBox…) | The sing-box profile | The `ssh` outbound with the host key pinned. TCP only (upstream sing-box has no UDP over SSH); DNS in the profile goes over HTTPS, so names resolve |
| **NetMod, HTTP Injector**, Termius, `ssh -D` | Typed in: the user's page has "Server / Port / Username / Password / Host key" to copy | These apps import only their own encrypted files, so there is no link to give them |
| v2rayNG, v2rayN, Streisand, Happ | — | Xray has no SSH client: the Xray JSON subscription leaves SSH out, and these apps skip the `ssh://` line of the plain subscription |

## What the server allows

- **Only port forwarding** (`direct-tcpip`). No shell, no commands, no SFTP or other subsystems, no agent or X11
  forwarding, no remote forwarding. An app that opens a session gets one that ends at once with exit status 1, as
  on an account whose shell is `/bin/false`; nothing ever runs.
- **Not into the server's own network**: forwards to loopback, private, link-local, shared (100.64/10) and
  multicast addresses, and to `localhost`, are refused by the SSH server itself, before Xray. A name that resolves
  to such an address is left to the server's routing in Xray, as on every other protocol.
- **Guessing is slowed down**: each wrong password costs a second, and an address with 10 wrong passwords in 10
  minutes is refused until the 10 minutes are over. A client must sign in within 30 seconds.
- A quiet client is asked for a sign of life every 30 seconds and dropped when it does not answer (a phone that
  lost its network), so stale sessions do not count as devices.
- The server says it is `SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.14`, like an ordinary Ubuntu server.

## How it fits in

- Every forward is handed to a **SOCKS inbound of Xray on loopback** (tag `in-<id>`, TCP and UDP), so routing rules
  (blocked sites, ads, Iranian sites, WARP), the torrent block and the inbound's own usage counters apply.
- That inbound has no accounts: the SSH server signs users in itself. A change of users (a new user, a pause, an
  expiry, a password change) reaches the SSH server alone and **never restarts Xray**; whoever lost access is cut
  off at once, everyone else stays connected.
- The SSH server counts each user's bytes (TCP and UDP) and knows each session's address, so **usage per user,
  online state, device limits, pauses and expiry** work as for the other protocols. A session counts as a device
  while it moved data in the last minute.
- **UDP over TCP** is sing-box's protocol (`sp.v2.udp-over-tcp.arpa`, version 2, and the legacy version 1); the
  datagrams leave through the same Xray inbound (SOCKS UDP ASSOCIATE).
- It works the same on other servers (nodes) and through relays (the link then has the relay's address and port;
  the host key is the target server's).
- **Config health** tests it with a real sing-box client and the check's own user, the outbound users get in the
  sing-box profile. **Settings › Cores** lists it as `ssh` (built in), with its log.

## Tested

- Unit tests: sign-in, wrong passwords, a client that pins another key, forwarding and counting, refused
  destinations, no commands, shell, SFTP, agent or remote forwarding, users removed or with a new password cut off
  at once, the guessing limit, UDP over TCP (versions 1 and 2, connected and not), the links and profiles (as
  `ray2sing` parses the link), SlipNet's 84 fields, the subscription and the user page.
- With real cores (`DUNVEX_TEST_BIN` with Xray 26.11.9 and sing-box 1.14.4): a real sing-box client with the
  profile's `ssh` outbound loads pages and a 5 MB download through Dunvex's SSH server and real Xray (about 3 MB/s on
  the test machine), the usage lands on the user and the inbound, a blocked site stays blocked, a wrong password
  and a disabled user are refused, the config check passes, and disabling the user cuts their open session. TCP and
  UDP over TCP through a real Xray SOCKS inbound to local servers.

```
DUNVEX_TEST_BIN=<folder with xray and sing-box> go test ./internal/engine -run TestSSHOnRealCores
DUNVEX_TEST_BIN=<folder with xray> go test ./internal/sshproxy -run TestThroughRealXray
```
