<div align="center">

<img src="web/public/brand/dunvex-logo-512.png" alt="Dunvex" width="240">

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
At the end you get the panel address and a **one-time setup link** (also as a QR code): open it and choose your own
username and password. There is no random password to copy; `dunvex info` shows the link again until you use it.
The first install already serves **VLESS Reality** and **Hysteria2**: add a user and share the link.

**One port for everything: 443.** The panel, the subscription page and Reality share TCP 443: a connection that names
a Reality SNI goes to Reality, everything else reaches the panel (and a stranger who opens the IP sees only a blank
404). Hysteria2 uses UDP 443, so it looks like ordinary HTTP/3. Other Reality protocols can join 443 with their own
SNI; protocols that need a TLS certificate keep their own port.

Manage from SSH: `dunvex menu` · Update: same command with `update` · Remove: `uninstall`.
Another server for the same panel: the same command with `node` (it prints a token for Servers › Add server).

The installer shows the DV logo, a live step log with download progress and checksum check, and ends with the
login details and a QR code of the panel address. `dunvex menu` is a full-screen control centre built with Bubble Tea:
a gradient Dunvex header, navigation, a live dashboard (big CPU/RAM/disk numbers, download and upload charts,
24-hour traffic, users, protocols, recent events), users, protocols, live logs, backups, service actions, network and
a login card with QR code. No arrow keys needed: **1–8** open a section, the letter in **[ ]** runs an action
(in Service: r restart, u update, c cores, p panel port, a admin login, b backup, x uninstall), **?** lists every key.
Ctrl+P opens a command palette; risky actions ask first; mouse works too. Plain output: `DUNVEX_PLAIN=1`. GitHub blocked on the server? Copy the binary over
and install it offline with `DUNVEX_BINARY=/root/dunvex bash install.sh`.

### What you get
- **32 protocol presets**, all validated against the real cores (Xray 26.11, sing-box 1.14, mtg 2.3, amneziawg-go 3.1, mita 3.39, TrustTunnel 1.3):
  VLESS Reality (Vision · XHTTP · gRPC · universal), XHTTP/WS/HTTPUpgrade/gRPC over TLS (CDN),
  post-quantum VLESS Encryption, Hysteria2, TUIC v5, WireGuard, AnyTLS, ShadowTLS v3, NaiveProxy,
  Shadowsocks 2022, Trojan, VMess, mKCP, SOCKS5, HTTP, **AmneziaWG** (WireGuard that DPI cannot recognise,
  versions 3.1 and 2.0, a `.conf` file and QR for each user), **Mieru** over TCP or UDP (no fixed pattern; for
  Clash.Meta apps, Karing and Exclave), **TrustTunnel** (AdGuard's HTTP/2 and HTTP/3 VPN that looks like an
  HTTPS site, for the official TrustTunnel apps and Shadowrocket), the **XDNS DNS tunnel** for internet shutdowns and a
  **Telegram proxy** (MTProto) that can share port 443. See [docs/PROTOCOLS.md](docs/PROTOCOLS.md).
- **Users**: data limit, expiry, start-on-first-connection, daily/weekly/monthly reset, per-protocol access, bulk actions.
- **Live changes**: adding, limiting or renewing a user reaches Xray at once, without disconnecting anyone else.
- **Device limit**: set how many devices may connect at once; the panel shows each user's connected addresses.
- **Admins**: owner, admin and reseller roles (resellers manage only their own users, within a user and data quota), two-step login (TOTP).
- **Sessions and API keys**: see every signed-in browser and sign it out from anywhere; API keys (read-only or full,
  with expiry) for scripts and bots. See [docs/API.md](docs/API.md).
- **Telegram bot**: status, users, one-line new user, renew, backup, alerts and a daily report; users get usage warnings.
- **CDN helper (v1.17)**: put the CDN protocols behind Cloudflare. Their configs go through the CDN name plus one per
  clean address (labelled, for example by operator); a check tries the DNS, each protocol through the CDN and straight
  to the server, and each clean address, explaining Cloudflare's errors; and one click with your own API token (never
  saved) makes the proxied DNS record and fixes SSL/TLS and WebSockets. See [docs/CDN.md](docs/CDN.md).
- **Relay servers (v1.15) and reverse tunnels (v1.16)**: put a server in Iran in front of the one abroad. It forwards
  every protocol unchanged (TCP and UDP, encryption end to end), users get an extra config through it (or only through
  it), and the Servers page shows each relayed port with its traffic and delay. In reverse mode the server abroad
  connects to the relay over pinned TLS links, so nothing has to connect from Iran to abroad. See [docs/RELAY.md](docs/RELAY.md).
- **Routing and WARP (v1.14)**: block ads, adult sites, torrents or your own domains; Iranian sites direct, blocked
  or through WARP on the server, and straight from the phone in sing-box and Clash subscriptions; one click registers a
  free Cloudflare WARP device for chosen sites (AI, Google, Netflix…) or for everything. The rules cover Mieru and
  TrustTunnel too. See [docs/ROUTING.md](docs/ROUTING.md).
- **Sales (v1.13)**: plans sold in a web shop, through a Renew button on every user page and in the Telegram bot.
  Payment by card to card (receipt photo, approved with one tap), ZarinPal (verified online, with a test mode) or
  USDT on TRON (found on the chain automatically). Resellers top up a wallet and pay for each user from it, with
  their own discount. See [docs/BILLING.md](docs/BILLING.md).
- **Exact per-user usage** across both cores, hourly history, live speed, who is online.
- **Subscription** that adapts to the app: links, Clash Meta, sing-box JSON, plus a user page with QR codes and one-tap import (Hiddify, v2rayNG, Happ, Streisand, V2Box, Shadowrocket, NekoBox, sing-box, Clash).
- **Several servers (v1.8)**: run `install.sh node` on another server and paste its token under Servers › Add server.
  Each protocol runs on the server you pick; users get one subscription with every server, and their usage, devices
  and limits are counted in one place. Panel and nodes talk over mutual TLS with pinned certificates. Since v1.18 a
  node can have its own domain and Let's Encrypt certificate (renewed by itself), so NaiveProxy and the CDN protocols
  run on it too, and nodes update to the panel's version in one click, even without GitHub access. See
  [docs/NODES.md](docs/NODES.md).
- **Where users connect from (v1.7)**: the world map draws arcs from each country to the server, looked up offline
  on the server itself (DB-IP country data, CC BY 4.0); addresses are never sent anywhere.
- **Control-room panel (v1.2)**: KPI cards with live sparklines, a neon network topology and world map, CPU gauge,
  zoomable analytics with CSV export, live logs, an alert centre, a security page and a full-screen mission wall.
  Ctrl/Cmd+K command palette, Navy / OLED / Light themes, density and Persian-font choice, presentation mode that
  blurs every secret, and a short intro. Persian (RTL) and English, phones included, WCAG AA contrast.
- **Self-healing**: a busy port only pauses that one protocol; a crashed core restarts itself.
- Reality keys and SNI tester, Let's Encrypt or self-signed (pinned) certificates, backup & restore, self-update.

### License
Dunvex is licensed under the **GNU AGPL-3.0-or-later**, © 2026 Pedram. Anyone who changes Dunvex, or runs a changed
version for other people, must publish its full source under the same license and keep the author's name.
The Dunvex name and logo are not covered by the AGPL. See [LICENSE](LICENSE) and [NOTICE](NOTICE).

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

در پایان آدرس پنل و یک **لینک راه‌اندازی یک‌بارمصرف** (همراه با QR) نمایش داده می‌شود: آن را باز کنید و نام کاربری
و رمز دلخواه خودتان را بگذارید. رمز تصادفی‌ای در کار نیست؛ تا وقتی لینک استفاده نشده، `dunvex info` دوباره نشانش می‌دهد.
بعد از نصب، **VLESS Reality** و **Hysteria2** آماده‌اند؛ فقط کاربر بسازید و لینک را بفرستید.

**یک پورت برای همه: ۴۴۳.** پنل، صفحهٔ اشتراک و Reality روی TCP 443 شریک‌اند: اتصالی که SNI مربوط به Reality را دارد
به Reality می‌رسد و بقیه به پنل (کسی که IP را باز کند فقط یک صفحهٔ خالی 404 می‌بیند). Hysteria2 روی UDP 443 است و
شبیه HTTP/3 معمولی دیده می‌شود. پروتکل‌های Reality دیگر هم با SNI خودشان می‌توانند روی ۴۴۳ بیایند؛ پروتکل‌هایی که
گواهی TLS لازم دارند پورت جدا می‌گیرند.

مدیریت از SSH: ‏`dunvex menu` · به‌روزرسانی: همان دستور با `update` · حذف: `uninstall`.
سرور دیگر برای همین پنل: همان دستور با `node` (توکنی می‌دهد که در «سرورها › افزودن سرور» گذاشته می‌شود).

نصب‌کننده لوگوی DV، گزارش زندهٔ مراحل با نوار دانلود و بررسی checksum را نشان می‌دهد و در پایان اطلاعات ورود و QR آدرس پنل را می‌دهد.
‏`dunvex menu` یک مرکز کنترل تمام‌صفحه است: وضعیت زندهٔ سرویس، هسته‌ها، CPU، RAM، دیسک، شبکه و کاربران،
به‌علاوهٔ ری‌استارت، فهرست کاربران، لاگ زنده، بازنشانی مدیر، پورت پنل، پشتیبان، به‌روزرسانی و تنظیمات شبکه.
بدون کلید جهت هم کار می‌کند: عدد **۱ تا ۸** هر بخش را باز می‌کند، حرف داخل **[ ]** همان کار را انجام می‌دهد
(در بخش سرویس: r ری‌استارت، u به‌روزرسانی، c هسته‌ها، p پورت پنل، a ورود مدیر، b پشتیبان، x حذف) و **?** همهٔ کلیدها را نشان می‌دهد.
اگر گیت‌هاب روی سرور بسته است، فایل باینری را منتقل کنید و با `DUNVEX_BINARY=/root/dunvex bash install.sh` نصب کنید.

### امکانات
- **۳۲ پروتکل آماده** که همه با هسته‌های واقعی تست شده‌اند، از جمله **AmneziaWG** (وایرگاردی که فیلترینگ تشخیصش
  نمی‌دهد، نسخهٔ ۳٫۱ و ۲٫۰، با فایل .conf و QR برای هر کاربر)، **Mieru** روی TCP یا UDP (بدون الگوی ثابت، برای
  برنامه‌های Clash.Meta، Karing و Exclave)، **TrustTunnel** (VPN مبتنی بر HTTP/2 و HTTP/3 از AdGuard که شبیه یک سایت
  HTTPS است، برای برنامه‌های رسمی TrustTunnel و Shadowrocket)، **تونل DNS (XDNS)** برای قطعی اینترنت و
  **پراکسی تلگرام (MTProto)** که می‌تواند پورت ۴۴۳ را شریک شود (فهرست کامل در [docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **کاربران**: حجم، تاریخ انقضا، شروع از اولین اتصال، صفر شدن دوره‌ای، محدودیت پروتکل، عملیات گروهی.
- **تغییر زنده**: ساختن، محدود کردن یا تمدید کاربر بدون قطع شدن بقیه اعمال می‌شود.
- **محدودیت دستگاه**: تعداد دستگاه هم‌زمان هر کاربر، با نمایش IPهای متصل.
- **مدیران**: نقش مالک، مدیر و نماینده فروش (با سقف کاربر و حجم) و ورود دو مرحله‌ای.
- **نشست‌ها و کلید API**: همهٔ مرورگرهای واردشده را ببینید و از هر جا ببندید؛ کلید API (فقط خواندنی یا کامل، با تاریخ انقضا)
  برای اسکریپت و ربات. راهنما: [docs/API.md](docs/API.md).
- **ربات تلگرام**: وضعیت سرور، مدیریت کاربران، پشتیبان، هشدار و گزارش روزانه؛ کاربران هم هشدار مصرف می‌گیرند.
- **دستیار CDN (نسخهٔ ۱.۱۷)**: پروتکل‌های CDN پشت کلادفلر؛ کانفیگ‌ها از راه نام CDN و یکی هم برای هر آی‌پی تمیز (با
  برچسب، مثلاً نام اپراتور)؛ بررسی DNS، هر پروتکل از راه CDN و مستقیم و هر آی‌پی تمیز با توضیح خطاهای کلادفلر؛ و با یک
  کلیک و توکن API خودتان (که ذخیره نمی‌شود) رکورد DNS پروکسی ساخته و SSL/TLS و WebSockets درست می‌شود. راهنما:
  [docs/CDN.md](docs/CDN.md).
- **سرور رله (نسخهٔ ۱.۱۵) و تونل معکوس (نسخهٔ ۱.۱۶)**: یک سرور ایران جلوی سرور خارج؛ همهٔ پروتکل‌ها را بدون تغییر رد
  می‌کند (TCP و UDP، رمزگذاری از گوشی تا سرور مقصد)، کاربران یک کانفیگ اضافه از راه رله می‌گیرند (یا فقط از راه رله) و صفحهٔ
  سرورها هر پورت رله را با ترافیک و تأخیرش نشان می‌دهد. در حالت معکوس سرور خارج با لینک‌های TLS به رله وصل می‌شود و هیچ
  اتصالی از ایران به خارج لازم نیست. راهنما: [docs/RELAY.md](docs/RELAY.md).
- **مسیریابی و WARP (نسخهٔ ۱.۱۴)**: مسدود کردن تبلیغات، سایت‌های بزرگسال، تورنت یا دامنه‌های دلخواه؛ سایت‌های ایرانی
  روی سرور مستقیم، مسدود یا از WARP، و روی گوشی کاربر بیرون از VPN (اشتراک sing-box و Clash)؛ ثبت WARP رایگان کلادفلر
  با یک کلیک برای سایت‌های انتخابی (هوش مصنوعی، گوگل، نتفلیکس…) یا همهٔ ترافیک. Mieru و TrustTunnel هم از همین قانون‌ها
  پیروی می‌کنند. راهنما: [docs/ROUTING.md](docs/ROUTING.md).
- **فروش (نسخهٔ ۱.۱۳)**: فروش پلن در فروشگاه وب، با دکمهٔ «تمدید» روی صفحهٔ هر کاربر و داخل ربات تلگرام. پرداخت با
  کارت‌به‌کارت (عکس رسید و تأیید با یک دکمه)، درگاه زرین‌پال (تأیید آنلاین، با حالت آزمایشی) یا تتر روی شبکهٔ ترون (پیدا شدن
  خودکار روی بلاکچین). نماینده‌ها کیف پولشان را شارژ می‌کنند و هزینهٔ هر کاربر با تخفیف خودشان از آن کم می‌شود.
  راهنما: [docs/BILLING.md](docs/BILLING.md).
- **مصرف دقیق هر کاربر**، تاریخچهٔ ساعتی، سرعت لحظه‌ای، کاربران آنلاین.
- **اشتراک هوشمند** برای همهٔ برنامه‌ها + صفحهٔ کاربر با QR و افزودن یک‌لمسی.
- **چند سرور (نسخهٔ ۱.۸)**: روی سرور دیگر `install.sh node` را اجرا کنید و توکنش را در «سرورها › افزودن سرور» بگذارید.
  هر پروتکل روی سروری که انتخاب می‌کنید اجرا می‌شود؛ کاربر یک اشتراک واحد با همهٔ سرورها می‌گیرد و مصرف، دستگاه‌ها و
  محدودیت‌هایش یکجا شمرده می‌شود. ارتباط پنل و سرورها با TLS دوطرفه و گواهی‌های سنجاق‌شده است. از نسخهٔ ۱.۱۸ هر سرور
  دامنه و گواهی Let's Encrypt خودش را دارد (و خودش تمدید می‌کند)، پس NaiveProxy و پروتکل‌های CDN هم رویش اجرا می‌شوند؛
  و سرورها با یک کلیک به نسخهٔ پنل به‌روز می‌شوند، حتی بدون دسترسی به گیت‌هاب. راهنما: [docs/NODES.md](docs/NODES.md).
- **کاربران از کجا وصل‌اند (نسخهٔ ۱.۷)**: نقشهٔ جهان از هر کشور تا سرور کمان می‌کشد؛ جستجوی کشور روی خود سرور و آفلاین
  انجام می‌شود (دادهٔ کشورهای DB-IP) و هیچ آدرسی به جایی فرستاده نمی‌شود.
- **پنل اتاق کنترل (نسخهٔ ۱.۲)**: کارت‌های آماری با نمودار زنده، توپولوژی نئونی شبکه و نقشهٔ جهان، گیج پردازنده،
  تحلیل ترافیک با زوم و خروجی CSV، لاگ زنده، مرکز اعلان‌ها، صفحهٔ امنیت و حالت دیوار نمایش.
  پالت فرمان با Ctrl+K، پوستهٔ سرمه‌ای، OLED و روشن، انتخاب فونت فارسی، حالت ارائه که اطلاعات حساس را محو می‌کند و اینترو.
- **خودترمیم**: اگر پورتی اشغال باشد فقط همان پروتکل متوقف می‌شود.

نقشهٔ راه: [docs/ROADMAP.md](docs/ROADMAP.md)

### لایسنس
Dunvex تحت لایسنس **GNU AGPL-3.0** و متعلق به Pedram است. هر کس آن را تغییر دهد یا نسخهٔ تغییریافته را
به‌صورت سرویس در اختیار دیگران بگذارد، باید کد کامل همان نسخه را با همین لایسنس منتشر کند و نام سازنده را حفظ کند.
نام و لوگوی Dunvex شامل این لایسنس نیستند.

---

Dunvex © 2026 Pedram · AGPL-3.0-or-later. Cores (Xray-core, sing-box) are downloaded at install time and keep their own licenses; see [NOTICE](NOTICE).
