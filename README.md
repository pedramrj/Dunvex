<div align="center">

<img src="web/public/brand/dunvex-logo-512.png" alt="Dunvex" width="240">

**A light, all-in-one VPN panel: every line, one interchange.**
Xray + sing-box · 35 ready protocols · per-user accounting · one-line install

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
The first install already serves **every protocol that needs no domain**: VLESS Reality and Reality XHTTP,
TrustTunnel and the Telegram proxy (all on TCP 443), Hysteria2 (UDP 443), AmneziaWG and Mieru (a port each) and the
xICMP ping tunnel. Add a user and share the link; turn off what you do not need on the Protocols page. The CDN
protocols and the DNS tunnels need your domain, so you add them later.

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
- **35 protocol presets**, all validated against the real cores (Xray 26.11, sing-box 1.14, mtg 2.3, amneziawg-go 3.1, mita 3.39, TrustTunnel 1.3):
  VLESS Reality (Vision · XHTTP · gRPC · universal), XHTTP/WS/HTTPUpgrade/gRPC over TLS (CDN),
  post-quantum VLESS Encryption, Hysteria2, TUIC v5, WireGuard, AnyTLS, ShadowTLS v3, NaiveProxy,
  Shadowsocks 2022, Trojan, VMess, mKCP, SOCKS5, HTTP, **AmneziaWG** (WireGuard that DPI cannot recognise,
  versions 3.1 and 2.0, a `.conf` file and QR for each user), **Mieru** over TCP or UDP (no fixed pattern; for
  Clash.Meta apps, Karing and Exclave), **TrustTunnel** (AdGuard's HTTP/2 and HTTP/3 VPN that looks like an
  HTTPS site, for the official TrustTunnel apps and Shadowrocket; it can share 443 with the panel and Reality), **DNS tunnels** for internet shutdowns (XDNS, plus **dnstt** and
  **Slipstream** for SlipNet and DNSTT.XYZ, all sharing port 53; see [docs/DNS-TUNNELS.md](docs/DNS-TUNNELS.md)), the
  **xICMP ping tunnel** for networks where only ping works ([docs/XICMP.md](docs/XICMP.md)) and a
  **Telegram proxy** (MTProto) that can share port 443. See [docs/PROTOCOLS.md](docs/PROTOCOLS.md).
- **Users**: data limit, expiry, start-on-first-connection, daily/weekly/monthly reset, per-protocol access, bulk actions.
- **Live changes**: adding, limiting or renewing a user reaches Xray at once, without disconnecting anyone else.
- **Device limit**: set how many devices may connect at once; the panel shows each user's connected addresses.
- **Admins**: owner, admin and reseller roles (resellers manage only their own users, within a user and data quota), two-step login (TOTP).
- **Sessions and API keys**: see every signed-in browser and sign it out from anywhere; API keys (read-only or full,
  with expiry) for scripts and bots. See [docs/API.md](docs/API.md).
- **Telegram bot**: status, users, one-line new user, renew, backup, alerts and a daily report; users get usage warnings.
- **Broadcast (v1.26)**: one message to every user, the active ones, those running out or ended, or users you pick,
  through the bot, with each user's name, days and data filled in, a live preview, a test to yourself, progress with
  Stop, and a history; from the panel or the bot itself. Resellers reach their own users. See
  [docs/BROADCAST.md](docs/BROADCAST.md).
- **Automatic backups (v1.27)**: every 6 hours by default and before each update, gzipped and rotated on the server,
  sent off it to the bot's admins on Telegram and to an S3-compatible bucket (R2, ArvanCloud, B2, MinIO, AWS),
  optionally encrypted with a passphrase; restore from the list or a file in two clicks. A copy that did not leave the
  server is an alert. See [docs/BACKUP.md](docs/BACKUP.md).
- **Free trial in the bot (v1.28)**: a person new to the bot gets a short subscription in one tap (data, hours,
  devices and protocols you choose), once per Telegram account, with an optional channel to join first and a daily
  cap; when it ends the bot offers a plan, and ended trials nobody bought are removed. The panel counts how many
  bought. See [docs/TRIAL.md](docs/TRIAL.md).
- **Tags and filters (v1.29)**: your own labels on users (`vip`, `family`, a city…), added one by one or to many at
  once; filter the Users page by tags, users running out, never connected, unlimited data or reseller, then act on
  exactly that group, or send a broadcast to one tag. See [docs/USERS.md](docs/USERS.md).
- **Webhooks and Prometheus metrics (v1.19)**: every event (users limited or expired, servers offline, core errors,
  sign-ins, orders…) goes to your own code as signed JSON, or to Slack and Discord, with retries and a delivery log;
  and `/api/metrics` gives Prometheus users, traffic per user and protocol, protocol and core health, each server's
  resources, certificate expiry and relays, behind a read-only API key. See [docs/INTEGRATIONS.md](docs/INTEGRATIONS.md).
- **Config health (v1.24)**: every config your users get (each protocol on every server, and its CDN and relay
  copies) is tried from the server with the same client cores the apps use (Xray, sing-box, dnstt), every few hours
  and on demand. The Overview shows how many work, the failures with the reason, and how long a page took through each
  one; a config that stops working or works again reaches Telegram, webhooks and Prometheus.
  See [docs/CONFIG-HEALTH.md](docs/CONFIG-HEALTH.md).
- **Xray JSON subscription and TLS fragment (v1.25)**: every user also gets their configs as whole Xray configs (with
  routing and DNS) for v2rayNG, v2rayN and Streisand, one tap from the user page; and the owner can turn on the TLS
  fragment (the ClientHello sent in small pieces) for every TLS and Reality config, in the links and in that
  subscription. See [docs/XRAY-JSON.md](docs/XRAY-JSON.md).
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
بعد از نصب، **همهٔ پروتکل‌هایی که دامنه لازم ندارند** آماده‌اند: VLESS Reality و Reality XHTTP، TrustTunnel و پراکسی
تلگرام (همه روی TCP 443)، Hysteria2 (UDP 443)، AmneziaWG و Mieru (هر کدام یک پورت) و تونل پینگ xICMP. فقط کاربر بسازید
و لینک را بفرستید؛ هرکدام را نخواستید در صفحهٔ «پروتکل‌ها» خاموش کنید. پروتکل‌های CDN و تونل‌های DNS دامنهٔ شما را لازم
دارند و بعداً اضافه می‌شوند.

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
- **۳۵ پروتکل آماده** که همه با هسته‌های واقعی تست شده‌اند، از جمله **AmneziaWG** (وایرگاردی که فیلترینگ تشخیصش
  نمی‌دهد، نسخهٔ ۳٫۱ و ۲٫۰، با فایل .conf و QR برای هر کاربر)، **Mieru** روی TCP یا UDP (بدون الگوی ثابت، برای
  برنامه‌های Clash.Meta، Karing و Exclave)، **TrustTunnel** (VPN مبتنی بر HTTP/2 و HTTP/3 از AdGuard که شبیه یک سایت
  HTTPS است، برای برنامه‌های رسمی TrustTunnel و Shadowrocket؛ می‌تواند پورت ۴۴۳ را با پنل و Reality شریک شود)، **تونل‌های DNS** برای قطعی اینترنت (XDNS، و **dnstt** و
  **Slipstream** برای SlipNet و DNSTT.XYZ که همه پورت ۵۳ را شریک می‌شوند؛ راهنما: [docs/DNS-TUNNELS.md](docs/DNS-TUNNELS.md))، **تونل پینگ (xICMP)** برای
  شبکه‌هایی که فقط پینگ در آن‌ها کار می‌کند ([docs/XICMP.md](docs/XICMP.md)) و
  **پراکسی تلگرام (MTProto)** که می‌تواند پورت ۴۴۳ را شریک شود (فهرست کامل در [docs/PROTOCOLS.md](docs/PROTOCOLS.md)).
- **کاربران**: حجم، تاریخ انقضا، شروع از اولین اتصال، صفر شدن دوره‌ای، محدودیت پروتکل، عملیات گروهی.
- **تغییر زنده**: ساختن، محدود کردن یا تمدید کاربر بدون قطع شدن بقیه اعمال می‌شود.
- **محدودیت دستگاه**: تعداد دستگاه هم‌زمان هر کاربر، با نمایش IPهای متصل.
- **مدیران**: نقش مالک، مدیر و نماینده فروش (با سقف کاربر و حجم) و ورود دو مرحله‌ای.
- **نشست‌ها و کلید API**: همهٔ مرورگرهای واردشده را ببینید و از هر جا ببندید؛ کلید API (فقط خواندنی یا کامل، با تاریخ انقضا)
  برای اسکریپت و ربات. راهنما: [docs/API.md](docs/API.md).
- **ربات تلگرام**: وضعیت سرور، مدیریت کاربران، پشتیبان، هشدار و گزارش روزانه؛ کاربران هم هشدار مصرف می‌گیرند.
- **پیام همگانی (نسخهٔ ۱.۲۶)**: یک پیام برای همهٔ کاربران، فعال‌ها، کسانی که اشتراکشان رو به پایان یا تمام شده، یا
  کاربرانی که خودتان انتخاب می‌کنید، از راه ربات؛ با نام، روز و حجم مانده‌ی هر کاربر، پیش‌نمایش زنده، ارسال آزمایشی برای
  خودتان، نوار پیشرفت با دکمهٔ توقف و تاریخچه؛ از پنل یا خود ربات. نماینده‌ها فقط به کاربران خودشان پیام می‌دهند.
  راهنما: [docs/BROADCAST.md](docs/BROADCAST.md).
- **پشتیبان‌گیری خودکار (نسخهٔ ۱.۲۷)**: به‌طور پیش‌فرض هر ۶ ساعت و پیش از هر به‌روزرسانی؛ فشرده و چرخشی روی سرور،
  و بیرون از سرور به مدیران ربات در تلگرام و به یک باکت سازگار با S3 (R2، ابر آروان، B2، MinIO، AWS)؛ با رمزگذاری
  اختیاری با عبارت عبور؛ بازگردانی از فهرست یا از فایل با دو کلیک. نسخه‌ای که از سرور بیرون نرسد هشدار می‌دهد.
  راهنما: [docs/BACKUP.md](docs/BACKUP.md).
- **تست رایگان در ربات (نسخهٔ ۱.۲۸)**: کسی که تازه به ربات می‌آید با یک لمس یک اشتراک کوتاه می‌گیرد (حجم، مدت، دستگاه و
  پروتکل‌ها به انتخاب شما)، هر حساب تلگرام فقط یک‌بار؛ با عضویت اجباری اختیاری در کانال و سقف روزانه. وقتی تمام شود ربات
  خرید را پیشنهاد می‌کند و تست‌های تمام‌شده‌ای که خرید نکرده‌اند پاک می‌شوند. پنل نشان می‌دهد چند نفر خرید کردند.
  راهنما: [docs/TRIAL.md](docs/TRIAL.md).
- **برچسب و فیلتر کاربران (نسخهٔ ۱.۲۹)**: برچسب‌های خودتان روی کاربران (`vip`، خانواده، یک شهر و…)، تک‌تک یا برای
  چند کاربر با هم؛ فیلتر صفحهٔ کاربران با برچسب، کاربران رو به اتمام، هرگز وصل‌نشده، حجم نامحدود یا نماینده، و بعد
  عملیات گروهی روی همان دسته، یا پیام همگانی به یک برچسب. راهنما: [docs/USERS.md](docs/USERS.md).
- **وب‌هوک و آمار Prometheus (نسخهٔ ۱.۱۹)**: هر رویداد (محدود یا منقضی شدن کاربران، قطع شدن سرورها، خطای هسته‌ها، ورودها،
  سفارش‌ها و…) به برنامهٔ خودتان با JSON امضاشده یا به Slack و Discord فرستاده می‌شود، با تلاش دوباره و فهرست ارسال‌ها؛ و
  ‎`/api/metrics`‎ برای Prometheus کاربران، مصرف هر کاربر و پروتکل، سلامت پروتکل‌ها و هسته‌ها، منابع هر سرور، انقضای گواهی‌ها
  و رله‌ها را با یک کلید API فقط‌خواندنی می‌دهد. راهنما: [docs/INTEGRATIONS.md](docs/INTEGRATIONS.md).
- **سلامت کانفیگ‌ها (نسخهٔ ۱.۲۴)**: هر کانفیگی که کاربران می‌گیرند (هر پروتکل روی هر سرور، و نسخه‌های CDN و رلهٔ آن)
  از خود سرور با همان هسته‌هایی که برنامه‌ها به کار می‌برند (Xray، sing-box و dnstt) امتحان می‌شود؛ هر چند ساعت یک‌بار
  و هر وقت بخواهید. صفحهٔ «نمای کلی» نشان می‌دهد چندتا کار می‌کنند، خرابی‌ها با دلیلشان، و یک صفحه از راه هرکدام چقدر
  طول کشید. کانفیگی که از کار بیفتد یا دوباره درست شود به تلگرام، وب‌هوک‌ها و Prometheus خبر داده می‌شود.
  راهنما: [docs/CONFIG-HEALTH.md](docs/CONFIG-HEALTH.md).
- **اشتراک Xray JSON و Fragment (نسخهٔ ۱.۲۵)**: هر کاربر کانفیگ‌هایش را به شکل کانفیگ کامل Xray (با مسیریابی و DNS) هم
  می‌گیرد، برای v2rayNG، v2rayN و Streisand، با یک لمس از صفحهٔ کاربر؛ و مالک می‌تواند Fragment (فرستادن ClientHello در
  تکه‌های کوچک) را برای همهٔ کانفیگ‌های TLS و Reality روشن کند، هم در لینک‌ها و هم در همین اشتراک.
  راهنما: [docs/XRAY-JSON.md](docs/XRAY-JSON.md).
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
