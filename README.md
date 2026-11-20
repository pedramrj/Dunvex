<div align="center">

# Dunvex

**A light, all-in-one VPN panel: every line, one interchange.**
Xray + sing-box · 26 ready protocols · per-user accounting · one-line install

by **Pedram**

[فارسی](#فارسی) · [English](#english)

</div>

---

## English

### Install (one line)

```bash
bash <(curl -Ls https://raw.githubusercontent.com/pedramrj/Dunvex/main/install.sh)
```

Ubuntu, Debian, CentOS/Alma/Rocky, Fedora, Arch · amd64 & arm64 · root.
At the end you get the panel address, username and password (also saved in `/usr/local/dunvex/install-info.txt`).
The first install already serves **VLESS Reality** and **Hysteria2**: add a user and share the link.

Manage from SSH: `dunvex menu` · Update: same command with `update` · Remove: `uninstall`.

### What you get
- **26 protocol presets**, all validated against the real cores (Xray 26.11, sing-box 1.14):
  VLESS Reality (Vision · XHTTP · gRPC · universal), XHTTP/WS/HTTPUpgrade/gRPC over TLS (CDN),
  post-quantum VLESS Encryption, Hysteria2, TUIC v5, WireGuard, AnyTLS, ShadowTLS v3, NaiveProxy,
  Shadowsocks 2022, Trojan, VMess, mKCP, SOCKS5, HTTP. See [docs/PROTOCOLS.md](docs/PROTOCOLS.md).
- **Users**: data limit, expiry, start-on-first-connection, daily/weekly/monthly reset, per-protocol access, bulk actions.
- **Exact per-user usage** across both cores, hourly history, live speed, who is online.
- **Subscription** that adapts to the app: links, Clash Meta, sing-box JSON, plus a user page with QR codes and one-tap import (Hiddify, v2rayNG, Happ, Streisand, V2Box, Shadowrocket, NekoBox, sing-box, Clash).
- **Dashboard**: live network map, CPU/RAM/disk/network, 30-day usage, top users, core health (Persian & English).
- **Self-healing**: a busy port only pauses that one protocol; a crashed core restarts itself.
- Reality keys and SNI tester, Let's Encrypt or self-signed (pinned) certificates, backup & restore, self-update.

### Build from source
```bash
cd web && npm ci && npm run build && cd ..
go build -o dunvex ./cmd/dunvex
```

---

## فارسی

### نصب با یک دستور

```bash
bash <(curl -Ls https://raw.githubusercontent.com/pedramrj/Dunvex/main/install.sh)
```

در پایان آدرس پنل، نام کاربری و رمز نمایش داده می‌شود (در `/usr/local/dunvex/install-info.txt` هم ذخیره می‌شود).
بعد از نصب، **VLESS Reality** و **Hysteria2** آماده‌اند؛ فقط کاربر بسازید و لینک را بفرستید.

مدیریت از SSH: ‏`dunvex menu` · به‌روزرسانی: همان دستور با `update` · حذف: `uninstall`.

### امکانات
- **۲۶ پروتکل آماده** که همه با هسته‌های واقعی تست شده‌اند (فهرست کامل در [docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **کاربران**: حجم، تاریخ انقضا، شروع از اولین اتصال، صفر شدن دوره‌ای، محدودیت پروتکل، عملیات گروهی.
- **مصرف دقیق هر کاربر**، تاریخچهٔ ساعتی، سرعت لحظه‌ای، کاربران آنلاین.
- **اشتراک هوشمند** برای همهٔ برنامه‌ها + صفحهٔ کاربر با QR و افزودن یک‌لمسی.
- **داشبورد** با نقشهٔ زندهٔ شبکه، منابع سرور و نمودار مصرف (فارسی و انگلیسی).
- **خودترمیم**: اگر پورتی اشغال باشد فقط همان پروتکل متوقف می‌شود.

نقشهٔ راه: [docs/ROADMAP.md](docs/ROADMAP.md)

---

Dunvex © 2026 Pedram · Apache-2.0. Cores (Xray-core, sing-box) are downloaded at install time and keep their own licenses; see [NOTICE](NOTICE).
