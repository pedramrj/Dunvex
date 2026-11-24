# Domains

[فارسی در پایین ↓](#دامنهها)

The **Domains** page (owner only) holds every name of the panel in one ordered list, with each name's DNS, its
certificate and what uses it. The CDN helper lives here too, inside each CDN name.

## Roles

| Role | The name points | What uses it |
| --- | --- | --- |
| **Direct** | straight at this server (DNS only, grey cloud) | The first direct name is the **main domain**: the TLS protocols' name, the address in links (unless you set another one), the certificate and the panel's HTTPS. Another direct name can carry **copies** of the configs |
| **CDN** | at Cloudflare, which proxies it to this server (orange cloud) | The CDN protocols' configs go through it, plus one per clean address |
| **Subscription only** | anywhere that reaches this server (direct or through the CDN) | The subscription links and the user page. No configs |

Drag a row (or use its menu, or Alt+↑ / Alt+↓ on a focused row) to change the order. The first direct name is the
main one; moving another direct name to the top makes it main, and the TLS protocols that used the old main name move
to the new one (a protocol with a name of its own keeps it).

### Copies at another name

A second direct name with **Give configs through this name too** gives users a copy of this server's TLS protocols at
that name. If the main name is filtered by its name (SNI or DNS), the copy still works. **Every protocol, not only
TLS** also copies Reality, UDP and the rest: for a name that points at another IP of the same server, so a blocked IP
has a twin. A protocol with its own **Address in links** keeps it and gets no copy.

Until the certificate covers the second name, its copies keep the main name as their SNI, so they connect either way.

### Several CDN names

Each CDN name with configs on gives the CDN protocols through it, and through each of its clean addresses. The first
CDN name's configs keep the "CDN" label they always had; the others use their label (or their name).
**Also give the direct config** (in **Links**) keeps the direct copy of the CDN protocols for all of them.

## DNS check

Every name is looked up through public resolvers (1.1.1.1, 8.8.8.8, then the server's own) when the page opens, on
**Check all**, and every six hours by itself: its `A` and `AAAA` records (each marked as this server, Cloudflare or
elsewhere), a `CNAME`, and the name servers of its zone (marked when the zone is at Cloudflare). Against its role:

| Problem | Meaning |
| --- | --- |
| No record | The name does not resolve (or the domain has ended) |
| Points elsewhere | A direct or subscription name does not reach this server |
| Proxied | A direct name is behind the orange cloud: Reality, Hysteria2 and the other direct protocols stop working |
| Not proxied | A CDN name reaches this server directly: turn the orange cloud on |
| Some elsewhere / IPv6 elsewhere | Some of its addresses (or its AAAA) are not this server: some users miss it |

A name that stops pointing where it should is reported once in **Alerts** (and to the admins on Telegram), and once
more when it is right again.

**Fix**: with a Cloudflare token kept on the page, one click points the name at this server (proxied for a CDN name,
DNS only for the others). Records that point elsewhere are only replaced after you confirm. Without a token the page
says which record to add.

## Certificate

One Let's Encrypt certificate covers every name with **In this server's certificate** on, the main one first. Every
core (Xray, sing-box) and the panel read the same files, so no core has to pick a certificate by name.

- **Automatic**: when you add a name it is validated and the certificate is made again with it; it renews itself when a
  third of its life is left (30 days of 90), checked every hour.
- **Each name on its own**: a name that fails is left out and shown with the reason (port 80 busy, the record points
  elsewhere, DNS, CAA, Let's Encrypt's limits…); the others are issued. A failed name is tried again by itself after
  an hour, then 2, 4… up to a day. **Get it now** tries at once.
- **Never worse**: a renewal never drops a name the current certificate covers while it has more than a week left, and
  a certificate is not made again when none of the new names passed.
- **HTTP or DNS validation**: by default Let's Encrypt fetches a file on port 80 (the panel opens it only for the
  validation; when the panel itself is on port 80 it answers there). **Validate through Cloudflare DNS** needs no
  port 80 and works for a name behind the CDN; it needs the Cloudflare token kept on the page.
- **Links stay up**: until the real certificate covers the name links ask for, links pin the current one as before. An
  expired certificate (its renewal kept failing) gives way to the self-signed one, pinned, so nothing disconnects.
- **IP certificate**: a server with no name can get a Let's Encrypt certificate for its IP address (Let's Encrypt's
  short-lived profile, about six days, renewed by itself). Apps then check it and no pin is needed.
- **Advanced**: your own files instead (they are never renewed here), or self-signed; Let's Encrypt's staging
  authority or another ACME directory; a contact email.

Apps pick up a new certificate at their next subscription update (pinned configs carry the certificate's hash).

### Warnings before the end

Alerts and the Telegram admins get one warning per step: 14, 7, 3 and 1 day before the certificate ends (an automatic
one only gets there when its renewal keeps failing), and when it has ended. For short-lived (IP) certificates only the
last day counts. Once a day the page also looks up when each registered domain ends at its registry (RDAP; `.ir` and
some others do not publish it) and warns 30, 14, 7, 3 and 1 day before.

## Cloudflare token

Kept on the page, the token makes DNS records in one click and validates certificates through DNS. It is checked
first, never sent back in full, and **Forget it** removes it (names validated through DNS go back to HTTP). Give it
**Zone · DNS · Edit** and **Zone · Zone Settings · Edit**, for your own domains only. The CDN setup inside a CDN name
still takes a token for one use when none is kept.

## Other servers

Each node's domain is listed with its DNS check and certificate; the node gets its own certificate. Change a node's
domain on the **Servers** page.

## Moving from older versions

On the first start the panel's domain (Settings) becomes the main domain and the CDN helper's name becomes a CDN name,
with its clean addresses and the "direct config" choice; the links users get do not change. The older settings stay
in step with the list (`domain` is the main name, `cdn` the first CDN name), so `dunvex set domain …` still works and
an older version after a downgrade sees the same setup. The certificate already in use keeps serving until the new one
is made.

## API

| Method | Path | |
| --- | --- | --- |
| GET | `/domains` | The list with each name's check, certificate state and uses; the certificate, the token state, the nodes' domains |
| PUT | `/domains` | `{"domains":[{"name":"vpn.example.com","role":"direct","cert":true,"configs":true}, …],"cdnDirect":false,"ipCert":false}`; optional `certMode` (`acme`, `custom`, `self`), `ca` (`letsencrypt`, `staging` or a directory URL), `email`, `certFile`, `keyFile`, `host` |
| POST | `/domains/check` | `{"names":[…]}` (empty = every name and node) |
| GET / POST | `/domains/cert` | The certificate's state; POST asks for it now |
| PUT | `/domains/cloudflare` | `{"token":"…"}` checks and keeps it; `{"token":""}` forgets it |
| POST | `/domains/point` | `{"name":"vpn.example.com","replace":false}`: the name's record through Cloudflare |

A domain: `name`, `role` (`direct`, `cdn`, `sub`), `label`, `configs`, `all`, `cert`, `dns01`, `addresses`
(`[{"address":"104.16.1.1","name":"MCI"}]`, CDN only), `note`. `/cdn` keeps working: it reads and sets the first CDN
name (`GET /cdn?name=` another one).

---

# دامنه‌ها

صفحهٔ **دامنه‌ها** (فقط مالک) همهٔ نام‌های پنل را در یک فهرست مرتب نگه می‌دارد: DNS هر نام، گواهی‌اش و این‌که چه چیزی
از آن استفاده می‌کند. دستیار CDN هم همین‌جاست، درون هر نام CDN.

## نقش‌ها

| نقش | نام به کجا اشاره می‌کند | چه چیزی از آن استفاده می‌کند |
| --- | --- | --- |
| **مستقیم** | مستقیم به همین سرور (DNS only، ابر خاکستری) | اولین نام مستقیم **دامنهٔ اصلی** است: نام پروتکل‌های TLS، آدرس لینک‌ها (اگر آدرس دیگری نگذاشته باشید)، گواهی و HTTPS پنل. نام مستقیم دیگر می‌تواند **نسخه‌ای** از کانفیگ‌ها را داشته باشد |
| **CDN** | به کلادفلر، که آن را به این سرور پروکسی می‌کند (ابر نارنجی) | کانفیگ پروتکل‌های CDN از راه آن، و یکی هم برای هر آی‌پی تمیز |
| **فقط اشتراک** | هر جایی که به این سرور برسد (مستقیم یا از راه CDN) | لینک اشتراک و صفحهٔ کاربر؛ بدون کانفیگ |

ترتیب را با کشیدن ردیف (یا منوی آن، یا Alt+↑ و Alt+↓ روی ردیف انتخاب‌شده) عوض کنید. اولین نام مستقیم، اصلی است؛ با
بالا بردن نام مستقیم دیگری، آن اصلی می‌شود و پروتکل‌های TLS که نام اصلی قبلی را داشتند نام جدید را می‌گیرند (پروتکلی که
نام خودش را دارد همان را نگه می‌دارد).

### نسخه با نام دیگر

نام مستقیم دوم با **کانفیگ از راه این نام هم داده شود** یک نسخه از پروتکل‌های TLS این سرور را با آن نام به کاربران
می‌دهد. اگر نام اصلی بر اساس نامش (SNI یا DNS) فیلتر شود، این نسخه کار می‌کند. **همهٔ پروتکل‌ها، نه فقط TLS** از
Reality و UDP و بقیه هم نسخه می‌سازد: برای نامی که به آی‌پی دیگری از همین سرور اشاره می‌کند، تا آی‌پی مسدود یک جفت
داشته باشد. پروتکلی که **آدرس در لینک** خودش را دارد همان را نگه می‌دارد و نسخه نمی‌گیرد.

تا وقتی گواهی نام دوم را پوشش ندهد، نسخه‌هایش نام اصلی را به‌عنوان SNI نگه می‌دارند، پس در هر حال وصل می‌شوند.

### چند نام CDN

هر نام CDN که کانفیگش روشن است پروتکل‌های CDN را از راه خودش و هر آی‌پی تمیزش می‌دهد. کانفیگ‌های اولین نام CDN همان
برچسب همیشگی «CDN» را دارند؛ بقیه برچسب (یا نام) خودشان را. **کانفیگ مستقیم هم داده شود** (در بخش **لینک‌ها**)
نسخهٔ مستقیم پروتکل‌های CDN را برای همه نگه می‌دارد.

## بررسی DNS

هر نام با resolverهای عمومی (1.1.1.1، 8.8.8.8 و بعد خود سرور) جستجو می‌شود: هنگام باز شدن صفحه، با **بررسی همه** و
هر شش ساعت خودکار. رکوردهای `A` و `AAAA` (هر آدرس با برچسب «این سرور»، «کلادفلر» یا «جای دیگر»)، `CNAME` و
nameserverهای ناحیه (با علامت وقتی DNS در کلادفلر است). در برابر نقش نام:

| مشکل | معنی |
| --- | --- |
| رکوردی نیست | نام پیدا نمی‌شود (یا دامنه تمام شده است) |
| به جای دیگر | نام مستقیم یا اشتراک به این سرور نمی‌رسد |
| پروکسی‌شده | نام مستقیم پشت ابر نارنجی است: Reality و Hysteria2 و دیگر پروتکل‌های مستقیم از کار می‌افتند |
| پروکسی‌نشده | نام CDN مستقیم به این سرور می‌رسد: ابر نارنجی را روشن کنید |
| بعضی جای دیگر / IPv6 جای دیگر | بعضی از آدرس‌ها (یا AAAA) مال این سرور نیستند: بعضی کاربران به آن نمی‌رسند |

نامی که دیگر به جای درست اشاره نکند یک بار در **هشدارها** (و برای مدیران در تلگرام) گزارش می‌شود و وقتی درست شد یک بار
دیگر.

**درست کن**: با توکن کلادفلرِ نگه‌داشته، یک کلیک نام را به این سرور وصل می‌کند (برای نام CDN پروکسی‌شده، برای بقیه
DNS only). رکوردهایی که به جای دیگر اشاره می‌کنند فقط پس از تأیید شما جایگزین می‌شوند. بدون توکن، صفحه می‌گوید چه رکوردی
بسازید.

## گواهی

یک گواهی Let's Encrypt همهٔ نام‌هایی را که **در گواهی این سرور باشد** برایشان روشن است پوشش می‌دهد، اول نام اصلی. همهٔ
هسته‌ها (Xray، sing-box) و پنل همان فایل را می‌خوانند، پس هیچ هسته‌ای لازم نیست گواهی را بر اساس نام انتخاب کند.

- **خودکار**: با افزودن یک نام، آن نام تأیید و گواهی دوباره با آن ساخته می‌شود؛ وقتی یک‌سوم عمرش مانده باشد (۳۰ روز از
  ۹۰) خودش تمدید می‌شود و هر ساعت بررسی می‌شود.
- **هر نام جدا**: نامی که تأیید نشود بیرون می‌ماند و دلیلش نشان داده می‌شود (پورت ۸۰ گرفته است، رکورد به جای دیگر
  اشاره می‌کند، DNS، CAA، سقف Let's Encrypt…)؛ بقیه صادر می‌شوند. نام ناموفق خودکار بعد از یک ساعت، بعد ۲، ۴… تا یک روز
  دوباره امتحان می‌شود. **همین حالا بگیر** فوراً امتحان می‌کند.
- **هرگز بدتر نمی‌شود**: تمدید تا وقتی بیش از یک هفته از گواهی مانده، هیچ نامی را که گواهی فعلی دارد کنار نمی‌گذارد، و
  اگر هیچ نام تازه‌ای تأیید نشود گواهی دوباره ساخته نمی‌شود.
- **تأیید HTTP یا DNS**: به‌طور پیش‌فرض Let's Encrypt فایلی را روی پورت ۸۰ می‌خواند (پنل فقط برای تأیید آن را باز
  می‌کند؛ اگر خود پنل روی پورت ۸۰ باشد همان‌جا جواب می‌دهد). **تأیید از راه DNS کلادفلر** به پورت ۸۰ نیاز ندارد و برای
  نام پشت CDN هم کار می‌کند؛ توکن کلادفلر باید در صفحه نگه داشته شود.
- **لینک‌ها قطع نمی‌شوند**: تا وقتی گواهی واقعی نامی را که لینک‌ها می‌خواهند پوشش ندهد، لینک‌ها مثل قبل گواهی فعلی را
  پین می‌کنند. گواهی تمام‌شده (که تمدیدش مدام ناموفق بوده) جایش را به گواهی خودامضای پین‌شده می‌دهد تا اتصال قطع نشود.
- **گواهی برای آی‌پی**: سرور بدون دامنه می‌تواند برای آی‌پی خودش گواهی Let's Encrypt بگیرد (پروفایل کوتاه‌مدت
  Let's Encrypt، حدود شش روز، با تمدید خودکار). اپ‌ها آن را بررسی می‌کنند و پین لازم نیست.
- **پیشرفته**: فایل‌های خودتان (که این‌جا تمدید نمی‌شوند) یا خودامضا؛ مرجع آزمایشی Let's Encrypt یا directory دیگر
  ACME؛ ایمیل تماس.

اپ‌ها گواهی تازه را با به‌روزرسانی بعدی اشتراک می‌گیرند (کانفیگ‌های پین‌شده هش گواهی را دارند).

### هشدار پیش از پایان

هشدارها و مدیران تلگرام برای هر مرحله یک پیام می‌گیرند: ۱۴، ۷، ۳ و ۱ روز پیش از پایان گواهی (گواهی خودکار فقط وقتی به
این‌جا می‌رسد که تمدیدش مدام ناموفق باشد) و وقتی تمام شد. برای گواهی کوتاه‌مدت (آی‌پی) فقط روز آخر. روزی یک بار هم
تاریخ پایان ثبت هر دامنه نزد ثبت‌کننده‌اش (RDAP؛ `.ir` و برخی دیگر منتشر نمی‌کنند) بررسی و ۳۰، ۱۴، ۷، ۳ و ۱ روز پیش از
آن هشدار داده می‌شود.

## توکن کلادفلر

توکنِ نگه‌داشته رکوردهای DNS را با یک کلیک می‌سازد و گواهی را از راه DNS تأیید می‌کند. اول بررسی می‌شود، هرگز کامل
برگردانده نمی‌شود و **فراموش کن** آن را پاک می‌کند (نام‌هایی که از راه DNS تأیید می‌شدند به HTTP برمی‌گردند). مجوزهای
**Zone · DNS · Edit** و **Zone · Zone Settings · Edit** را فقط برای دامنه‌های خودتان بدهید. راه‌اندازی کلادفلر درون نام
CDN اگر توکنی نگه داشته نشده باشد، توکن را برای یک بار می‌گیرد.

## سرورهای دیگر

دامنهٔ هر سرور (نود) با بررسی DNS و گواهی‌اش فهرست می‌شود؛ هر سرور گواهی خودش را می‌گیرد. دامنهٔ سرور را در صفحهٔ
**سرورها** عوض کنید.

## انتقال از نسخه‌های قبلی

در اولین اجرا دامنهٔ پنل (تنظیمات) دامنهٔ اصلی می‌شود و نام دستیار CDN یک نام CDN، با آی‌پی‌های تمیز و گزینهٔ «کانفیگ
مستقیم»؛ لینک‌هایی که کاربران می‌گیرند عوض نمی‌شوند. تنظیمات قبلی با فهرست هماهنگ می‌مانند (`domain` نام اصلی و `cdn`
اولین نام CDN است)، پس `dunvex set domain …` هنوز کار می‌کند و نسخهٔ قدیمی پس از بازگشت همان تنظیمات را می‌بیند. گواهی
فعلی تا ساخته شدن گواهی تازه کار می‌کند.

## API

جدول بخش انگلیسی بالا را ببینید.
