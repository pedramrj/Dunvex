# Dunvex Roadmap

Dunvex is built and owned by **Pedram**. One binary, one database file, and one install command.

## Principles
- **Easy install**: `bash <(curl -Ls https://raw.githubusercontent.com/pedramrj/Dunvex/main/install.sh)`, ready in under 2 minutes.
- **Easy management**: the panel shows only the needed options; advanced settings stay collapsed.
- **Fast and light**: Go, SQLite, and an embedded UI. No Docker, Postgres or Redis.
- **Honest**: a protocol is "Ready" only after it passes a real core config test.

## Phase 1: Core (v1.0) ✅
- [x] Single `dunvex` binary with the embedded panel, SQLite and no external dependencies
- [x] Xray (v26.11) and sing-box (v1.14) engines: supervision, automatic restart, logs
- [x] Protocol catalogue: 20+ ready presets (Reality, XHTTP, Hysteria2, TUIC, …), see [PROTOCOLS.md](PROTOCOLS.md)
- [x] Users: data limit, expiry, "starts on first connection", periodic reset, enable/disable
- [x] Per-user traffic accounting (Xray Stats + sing-box V2Ray API), hourly charts
- [x] Subscription: links, Clash Meta, sing-box JSON + a user page with QR codes and app links
- [x] Dashboard: CPU/RAM/network, online users, consumption, top users (Persian/English, dark)
- [x] Automatic Reality keys, SNI tester, self-signed or Let's Encrypt certificates
- [x] Install script + `dunvex` menu (status, restart, update, reset password, uninstall)
- [x] Database backup and restore from the panel

## Phase 2: Smart management (v1.1) ✅
- [x] Live user changes on Xray through HandlerService (add, remove, limit, renew) with no restart;
      a removed user who stays connected is cut off by one restart. sing-box has no user API, so it restarts (about 1 s)
- [x] Device limit per user: addresses come from Xray's online-IP stats and the sing-box connection log;
      a user above the limit for 2 minutes is paused for a few minutes (no IP bans, so shared mobile IPs are safe)
- [x] Telegram bot: server status, users, one-line "new user", +30 days / +10 GB, enable/disable, link + QR,
      backup, alerts, daily report; users link their chat from the subscription page and get 80%/95%/3-day/1-day warnings
- [x] Admin roles: owner, admin (no server settings) and reseller (only their own users, user and data quota)
- [x] Two-step login (TOTP) with replay protection; `dunvex admin` from SSH turns it off for recovery
- [x] New panel design (logo, glass surfaces, live charts, rings, device lists) and user page

## Panel redesign (v1.2) ✅
- [x] "Obsidian Navy" design system: Navy, OLED and Light themes, density, Estedad / Vazirmatn + Geist + JetBrains Mono
- [x] Shell: grouped sidebar with favourites and counters, command palette (Ctrl/Cmd+K), notifications, quick actions
- [x] Overview: KPI cards with live sparklines, CPU gauge, live traffic, network map, timeline of events, customisable widgets
- [x] Network centre (neon topology + world map with the server's real location), Servers, Analytics, Logs, Alerts,
      Security and a full-screen Mission control
- [x] Presentation mode, intro, Persian event texts, WCAG AA contrast in every theme, phone layouts
- [x] License changed to GNU AGPL-3.0-or-later (© Pedram)

## Terminal (v1.3) ✅
- [x] Installer with the DV logo, matrix intro, live step log, download bar, checksum, access box and panel QR
- [x] `dunvex menu`: full-screen control centre with live status and the common server tasks
- [x] Offline install from a local binary (`DUNVEX_BINARY`), `dunvex qr`

## SSH control centre (v1.4) ✅
- [x] Full-screen menu (Bubble Tea): gradient header, panes, live dashboard, users, protocols, logs, backups, service,
      network and login + QR; command palette, dialogs, toasts, mouse; works down to 80×24

## One port and first-run setup (v1.5) ✅
- [x] Port 443 for everything: an SNI router shares TCP 443 between the panel, the subscription page and any number of
      Reality inbounds (each with its own SNI); Hysteria2 and other UDP protocols use UDP 443
- [x] Xray behind the router gets the client's address through the PROXY protocol; sing-box (which dropped it) gets
      its connection log translated back, so device limits and lists keep the real IPs
- [x] No random admin password: a one-time setup link (and QR) lets the owner choose the username and password
- [x] SSH menu without arrow keys: 1–8 open sections, letters run actions, `?` shows every key

## Sessions and API keys (v1.6) ✅
- [x] Every sign-in is a session you can see (device, IP, last activity) and sign out from another device;
      "sign out everywhere else"; the owner sees every admin's sessions
- [x] API keys for scripts and bots (`Authorization: Bearer dvx_…`): read-only or full, with expiry, shown once,
      stored as a hash; keys cannot manage accounts, sessions, keys, admins or restores. See [API.md](API.md)

## Offline map arcs (v1.7) ✅
- [x] The world map draws an arc from every country users connect from to the server, with a moving packet,
      a marker sized by devices and a side list by country (names in Persian or English)
- [x] Offline lookup only: DB-IP country data (CC BY 4.0) is built into a 2 MB file with every release and
      downloaded with a checksum; client addresses never leave the server and the API returns counts only
- [x] Falls back to Xray's geoip.dat (Iran, Russia, China) when the full data is missing; the server's own
      location falls back to the offline data when the online lookup fails; `dunvex geoip [update]`

## Several servers (v1.8) ✅
- [x] Nodes: `install.sh node` turns any server into a node; its token is pasted in the panel (Servers › Add server)
- [x] Mutual TLS with both certificates pinned and a one-time pairing secret; a removed node is released
- [x] Each protocol runs on a chosen server; only users who may connect are sent to a node
- [x] One subscription for every server; configs carry the server's name and address
- [x] Usage from nodes is counted exactly once (acknowledged batches), with online status, devices and limits
- [x] Servers page with each node's health, cores, speed and errors; nodes going offline appear as alerts

## Sales (v1.13) ✅
- [x] Plans (data, days, devices, protocols) sold in a web shop, a Renew button on the user page and the Telegram bot
- [x] Card to card with receipt photos approved in the panel or the bot; ZarinPal with online verification and test
      mode; USDT on TRON with a unique amount per order, found on TronGrid automatically
- [x] Reseller wallets: top-ups, a discount, every user created or renewed from a plan is paid from the balance,
      refunds when a user cannot be created; the owner sees and changes every wallet
- [x] Each order is paid exactly once; unpaid orders expire; sales numbers on the Sales page. See [BILLING.md](BILLING.md)

## Phase 3: More protocols
- [x] XDNS DNS tunnel for shutdowns, with the DNS records explained in the panel (v1.9)
- [x] Telegram proxy (MTProto, mtg) that can share port 443 by SNI (v1.9)
- [x] AmneziaWG 3.1 and 2.0 with a .conf and QR per user, live user changes, usage and devices (v1.10)
- [x] Mieru over TCP or UDP, with per-user usage and live user changes (v1.11)
- [x] TrustTunnel (AdGuard) over HTTP/2 and HTTP/3, with per-user usage and devices (v1.12)
- [x] TrustTunnel on port 443 next to the panel: TCP by its hostname through the router, QUIC passed on from UDP 443
      while no UDP protocol holds it (v1.20)
- [x] dnstt (inside Dunvex) and Slipstream DNS tunnels for SlipNet and DNSTT.XYZ, with per-user sign-in, sharing
      port 53 with XDNS by tunnel domain (v1.21). See [DNS-TUNNELS.md](DNS-TUNNELS.md)
- [x] xICMP ping tunnel, one per server, with a raw-socket check before Xray starts (v1.22). See [XICMP.md](XICMP.md)
- [x] A fresh install turns on every protocol that needs no domain: Reality, Reality XHTTP (its camouflage site
      tested at install), TrustTunnel and the Telegram proxy on TCP 443, Hysteria2 on UDP 443, AmneziaWG, Mieru and
      xICMP (v1.23)
- [ ] paqet, AIVPN, MASQUE and XDRIVE (experimental)

## Phase 4: Network (v2.0)
- [x] Multi-server: central panel + node agent (mTLS) (v1.8)
- [x] Nodes: domains with Let's Encrypt certificates (renewed by themselves) for TLS protocols, one-click node
      updates from the panel's own binary or GitHub; the panel's certificate is renewed too (v1.18)
- [x] Relay servers: a domestic server forwards every protocol (TCP and UDP) to a server abroad, with a port shift,
      relay-only configs and per-port traffic and delay (v1.15)
- [x] Reverse tunnels: the server abroad keeps pinned TLS links to the relay, multiplexed, TCP and UDP, limited to the
      relayed ports (v1.16)
- [x] CDN helper: links through the CDN name and clean addresses, a check of every leg, and the Cloudflare DNS and
      SSL/TLS setup with the owner's API token (v1.17)
- [x] Routing: Iranian sites direct, blocked or through WARP (server) and direct on devices (subscriptions), ad and
      adult blocking, own domains; Cloudflare WARP for chosen sites or everything, for Mieru and TrustTunnel too (v1.14)
- [x] REST API with tokens (v1.6)
- [x] Webhooks (signed JSON, Slack, Discord; retries and a delivery log) and Prometheus metrics (v1.19)
- [x] Config health: every config (each protocol on every server, its CDN and relay copies) tried from the server with
      real client cores (Xray, sing-box, dnstt) on a schedule and on demand, with alerts and metrics (v1.24). See
      [CONFIG-HEALTH.md](CONFIG-HEALTH.md)
- [x] Xray JSON subscription (whole configs with routing for v2rayNG, v2rayN and Streisand) and the TLS fragment for
      every TLS and Reality config, in the links (`fm`) and that subscription; the router on 443 reads fragmented
      ClientHellos (v1.25). See [XRAY-JSON.md](XRAY-JSON.md)
- [x] Broadcast through the Telegram bot: audiences (everyone, active, running out, ended, picked users), placeholders,
      Telegram HTML checked before sending, a test to the bot's admins, paced sending with Stop, history, blocked users
      unlinked, resellers limited to their users; from the panel and from the bot (v1.26). See [BROADCAST.md](BROADCAST.md)
- [x] Automatic backups: scheduled and before updates, rotated, sent to Telegram and an S3-compatible bucket, optional
      passphrase encryption, restore from the list or an uploaded .db/.gz/.enc file, failures as alerts (v1.27). See
      [BACKUP.md](BACKUP.md)
- [x] Free trial in the bot: one per Telegram account, chosen data, hours, devices and protocols, a daily cap, an
      optional channel to join, the shop offered at the end, ended trials removed, and how many bought (v1.28). See
      [TRIAL.md](TRIAL.md)
