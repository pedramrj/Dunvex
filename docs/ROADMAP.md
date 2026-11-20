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

## Phase 3: More protocols (v1.3)
- [ ] AmneziaWG 2.0, Mieru, TrustTunnel, MTProto (mtg)
- [ ] DNS tunnel wizard: XDNS / dnstt / Slipstream (for internet shutdowns)
- [ ] paqet, AIVPN, MASQUE and XDRIVE (experimental)

## Phase 4: Network (v2.0)
- [ ] Multi-server: central panel + node agent (mTLS)
- [ ] Relays and tunnels between the domestic and foreign servers, CDN helper
- [ ] Routing: Iranian sites go direct, ad blocking, WARP output
- [ ] REST API with tokens, webhooks, Prometheus metrics
