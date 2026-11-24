# Support inbox

Users write to the **📡 Support line** in the Telegram bot and get the answer in the same chat. Every message lands on the
panel's **Support** page, one conversation per person, next to their subscription; the answer goes back through the
bot. The Telegram bot must be set up (Settings › Telegram). Support is on by default; its settings are in
**Settings › Telegram › Support line in the bot**.

[فارسی](#صندوق-پشتیبانی)

## What a user sees

- **📡 Support line** under their usage card (on `/start`, after "My account", in the shop menu), or `/support`. The
  support screen shows their last few messages and answers, where things stand (waiting, seen, answered) and the
  owner's note ("We answer from 9 am to 11 pm"), then takes what they write next: text, or a picture with a line
  about it (a picture sent as a file works too). Their own lines are marked ◇ and the team's ◆.
- Their messages get receipts as reactions: ⚡ reached the team, 👨‍💻 someone on the team opened it, 💯 answered.
  While an admin writes the answer in the panel, the chat shows "typing…".
- One "sent to the team" note per burst; they can keep writing for 30 minutes, or tap **That's all**. Any other button or
  `/start` ends writing, so nothing meant for the bot lands in support by mistake. A card receipt for an
  order always goes to the order, never to support.
- Answers arrive signed **📡 Support line**, never with the admin's name, with a **💬 Write back** button.

Who may write: users whose Telegram is linked to their subscription (the bot link on their subscription page). With
**People without a subscription may write too**, anyone who opens the bot can, a buyer with a question for example.

## The Support page

- One conversation per Telegram chat, the newest message first, with an unread count. The menu shows how many
  conversations have messages nobody opened (sidebar, phone tab bar and the More sheet).
- Filters: **Open**, **Waiting** (open, the person wrote last), **Closed**, **All**; search by the user's name or
  note, the Telegram name or @username, or the chat ID.
- Beside the conversation: the user's name, status, data used of their limit, days left, devices, reseller and note,
  with a button to open the user; their Telegram name, @username and chat ID. A guest shows "No subscription".
- The answer box: text up to 3,500 characters, a photo (button, paste or drag; JPEG, PNG, WebP or GIF up to 5 MB),
  `Ctrl+Enter` sends. Each answer shows who on the team wrote it.
- **Close** files a conversation away; it opens again by itself when the person writes. You can still answer a
  closed one. **Delete** removes its messages and photos (the user's subscription stays).
- Opening a conversation marks it read and gives the person 👨‍💻. The page refreshes itself every few seconds.

**Who sees what.** The owner and admins see every conversation. A reseller sees only the conversations of their own
users (as for broadcast), and never a guest's. When a user moves to another reseller, their conversation moves too.

## From Telegram

The bot's admins get every new message (turn it off with **Send each new message to the bot's admins too**): who
wrote, their status, their Telegram, the text or the photo, with **⚡ Answer** and **🧾 Thread**. Answer, then
send the text or a photo; it goes out exactly like an answer from the panel. **📡 Support line** in the admin menu (or
`/support`) lists the open conversations, newest first, marked 🔴 (unread), 🟡 (waiting) or ✅ (answered); a conversation shows
its last messages with **Answer** and **Close**/**Reopen**.

## Events and webhooks

Every new message is a panel event of kind `support` ("ali wrote to support: …", "[photo]" for a photo), on the
Alerts page under **Support** and sent to webhooks that follow `support` (or all kinds). Answers are kept in the
conversation, not as events.

## Settings

| Setting | Default | |
| --- | --- | --- |
| Support is on | on | off: the button goes and `/support` says support is off |
| Send each new message to the bot's admins too | on | with Answer and Conversation buttons |
| People without a subscription may write too | off | their conversations are visible to the owner and admins only |
| Messages an hour per person | 20 | 0 = no limit. Over it the person is told once to wait; the extra messages are not kept |
| Longest message | 2,000 characters | 50 to 4,000 |
| Messages kept per conversation | 300 | the oldest go first; 0 = all |
| Remove messages after | 180 days | 0 = never. A conversation with no message left goes too |
| A note on the user's support screen | none | up to 300 characters, e.g. your hours |

Retention runs every hour, also while the bot is off. Photos are kept on the server in `support/` under the Dunvex
folder and removed with their message; database backups do not include them.

## API

| Method | Path | Role | |
| --- | --- | --- | --- |
| GET | `/support` | R | Conversations (`?status=open\|waiting\|closed`, `?q=`), counts, the bot's state |
| GET | `/support/unread` | R | Counts for the menu badge: `open`, `waiting`, `unread`, `closed` |
| GET | `/support/threads/{id}` | R | A conversation, the user beside it, its newest 80 messages (`?before=` a message id for older ones) |
| POST | `/support/threads/{id}/seen` | R | Mark it read (👨‍💻 to the person) |
| POST | `/support/threads/{id}/reply` | R | Answer: `{"text": "…"}`, or a form with `text` and `photo` |
| POST | `/support/threads/{id}/typing` | R | Show "typing…" to the person |
| PUT | `/support/threads/{id}` | R | `{"open": false}` closes it, `{"open": true}` opens it again |
| DELETE | `/support/threads/{id}` | R | Delete it with its messages and photos |
| GET | `/support/photos/{name}` | R | A photo of a conversation you may see |
| GET / PUT | `/support/settings` | O | `on`, `guests`, `alert`, `rate`, `maxLen`, `keep`, `days`, `note` |

A reseller's calls only reach their own users' conversations; anything else is 404.

---

# صندوق پشتیبانی

کاربر روی **📡 خط پشتیبانی** در ربات تلگرام پیام می‌دهد و پاسخ را در همان چت می‌گیرد. هر پیام در صفحهٔ **پشتیبانی** پنل می‌رسد،
برای هر نفر یک گفتگو و کنار آن مشخصات اشتراکش؛ پاسخ شما از راه ربات به او برمی‌گردد. ربات تلگرام باید راه افتاده باشد
(تنظیمات › تلگرام). پشتیبانی به‌طور پیش‌فرض روشن است و تنظیماتش در **تنظیمات › تلگرام › خط پشتیبانی در ربات** است.

## کاربر چه می‌بیند

- دکمهٔ **📡 خط پشتیبانی** زیر کارت مصرف (با `/start`، بعد از «حساب من» و در منوی فروشگاه)، یا فرمان `/support`. صفحهٔ
  پشتیبانی چند پیام و پاسخ آخر، وضعیت (در انتظار، دیده شد، پاسخ داده شد) و یادداشت شما («از ۹ صبح تا ۱۱ شب پاسخ
  می‌دهیم») را نشان می‌دهد و پیام بعدی کاربر را می‌گیرد: متن، یا عکس با یک خط توضیح (عکسی که به‌صورت فایل فرستاده
  شود هم پذیرفته می‌شود). نوشته‌های کاربر با ◇ و نوشته‌های تیم با ◆ مشخص می‌شوند.
- روی پیام‌های کاربر رسید به شکل واکنش می‌خورد: ⚡ به تیم رسید، 👨‍💻 کسی از تیم بازش کرد، 💯 جواب داده شد. وقتی مدیر
  در پنل جواب می‌نویسد، در چت کاربر «در حال نوشتن…» دیده می‌شود.
- برای هر چند پیام پشت‌سرهم یک‌بار «برای تیم رفت» می‌آید؛ کاربر تا ۳۰ دقیقه می‌تواند ادامه دهد یا **همین بود** را بزند. هر
  دکمهٔ دیگر یا `/start` نوشتن را تمام می‌کند تا چیزی که برای ربات بود اشتباهی به پشتیبانی نرود. رسید
  کارت‌به‌کارت سفارش همیشه به سفارش می‌رود، نه به پشتیبانی.
- جواب‌ها با امضای **📡 خط پشتیبانی** می‌رسند، هرگز با نام مدیر، و دکمهٔ **💬 جواب دادن** دارند.

چه کسی می‌تواند بنویسد: کاربرانی که تلگرامشان را به اشتراک وصل کرده‌اند (لینک ربات در صفحهٔ اشتراک). با گزینهٔ
**کسانی که اشتراک ندارند هم بنویسند** هر کسی که ربات را باز کند می‌تواند؛ مثلاً خریداری که سؤالی دارد.

## صفحهٔ پشتیبانی

- برای هر چت تلگرام یک گفتگو، تازه‌ترین پیام بالا، با تعداد خوانده‌نشده‌ها. منو نشان می‌دهد چند گفتگو پیام
  باز‌نشده دارد (نوار کناری، نوار پایین گوشی و برگهٔ «بیشتر»).
- فیلترها: **باز**، **منتظر پاسخ** (باز و آخرین پیام از کاربر)، **بسته**، **همه**؛ جستجو با نام یا یادداشت کاربر، نام
  یا ‎@نام‌کاربری تلگرام، یا شناسهٔ چت.
- کنار گفتگو: نام کاربر، وضعیت، مصرف از سقف حجم، روزهای مانده، دستگاه‌ها، نماینده و یادداشت، با دکمهٔ باز کردن کاربر؛
  نام، نام کاربری و شناسهٔ چت تلگرام. مهمان «بدون اشتراک» نشان داده می‌شود.
- جعبهٔ پاسخ: متن تا ۳٬۵۰۰ نویسه، یک عکس (با دکمه، چسباندن یا کشیدن؛ JPEG، PNG، WebP یا GIF تا ۵ مگابایت)؛
  `Ctrl+Enter` می‌فرستد. کنار هر پاسخ نوشته می‌شود کدام عضو تیم آن را نوشته.
- **بستن** گفتگو را کنار می‌گذارد و با پیام تازهٔ کاربر خودش باز می‌شود؛ به گفتگوی بسته هم می‌شود پاسخ داد. **حذف**
  پیام‌ها و عکس‌های گفتگو را پاک می‌کند (اشتراک کاربر دست نمی‌خورد).
- باز کردن گفتگو آن را خوانده‌شده می‌کند و کاربر 👨‍💻 می‌گیرد. صفحه هر چند ثانیه خودش به‌روز می‌شود.

**چه کسی چه می‌بیند.** مالک و مدیرها همهٔ گفتگوها را می‌بینند. نمایندهٔ فروش فقط گفتگوهای کاربران خودش را می‌بیند
(مثل پیام همگانی) و گفتگوی مهمان‌ها را هرگز. اگر کاربری به نمایندهٔ دیگری منتقل شود، گفتگویش هم با او می‌رود.

## از خود تلگرام

مدیران ربات هر پیام تازه را می‌گیرند (با گزینهٔ **هر پیام تازه برای مدیران ربات هم فرستاده شود** خاموش می‌شود): چه
کسی نوشته، وضعیتش، تلگرامش، متن یا عکس، با دکمه‌های **⚡ جواب** و **🧾 رشتهٔ گفتگو**. «جواب» را بزنید و متن یا عکس را
بفرستید؛ درست مثل جواب از پنل می‌رود. **📡 خط پشتیبانی** در منوی مدیر (یا `/support`) گفتگوهای باز را از تازه‌ترین نشان می‌دهد،
خوانده‌نشده‌ها با 🔴، منتظرها با 🟡 و پاسخ‌داده‌ها با ✅؛ هر گفتگو چند پیام آخر را با **پاسخ** و **بستن**/**باز کردن** نشان می‌دهد.

## رویدادها و وب‌هوک

هر پیام تازه یک رویداد پنل از نوع `support` است («ali wrote to support: …» و برای عکس «[photo]»)؛ در صفحهٔ هشدارها زیر
**پشتیبانی** دیده می‌شود و به وب‌هوک‌هایی که `support` (یا همهٔ نوع‌ها) را دنبال می‌کنند فرستاده می‌شود. پاسخ‌ها در خود
گفتگو می‌مانند و رویداد جدا نمی‌سازند.

## تنظیمات

| تنظیم | پیش‌فرض | |
| --- | --- | --- |
| پشتیبانی روشن باشد | روشن | خاموش: دکمه برداشته می‌شود و `/support` می‌گوید پشتیبانی خاموش است |
| هر پیام تازه برای مدیران ربات هم فرستاده شود | روشن | با دکمه‌های پاسخ و گفتگو |
| کسانی که اشتراک ندارند هم بنویسند | خاموش | گفتگوهایشان را فقط مالک و مدیرها می‌بینند |
| پیام در ساعت برای هر نفر | ۲۰ | ۰ یعنی بی‌سقف. بیش از آن، یک‌بار به کاربر گفته می‌شود صبر کند و پیام‌های اضافه نگه داشته نمی‌شوند |
| بیشترین طول هر پیام | ۲٬۰۰۰ نویسه | ۵۰ تا ۴٬۰۰۰ |
| پیام نگه‌داشته در هر گفتگو | ۳۰۰ | قدیمی‌ترها اول پاک می‌شوند؛ ۰ یعنی همه |
| پاک شدن پیام‌ها پس از | ۱۸۰ روز | ۰ یعنی هرگز. گفتگویی که پیامی در آن نماند هم پاک می‌شود |
| یادداشت روی صفحهٔ پشتیبانی کاربر | ندارد | تا ۳۰۰ نویسه؛ مثلاً ساعت پاسخ‌گویی |

نگه‌داری هر ساعت اجرا می‌شود، حتی وقتی ربات خاموش است. عکس‌ها روی سرور در پوشهٔ `support/` داخل پوشهٔ Dunvex
نگه داشته می‌شوند و با پیامشان پاک می‌شوند؛ پشتیبان پایگاه داده آن‌ها را ندارد.

## API

فهرست مسیرها در بخش انگلیسی بالا آمده است. فراخوانی‌های نمایندهٔ فروش فقط به گفتگوهای کاربران خودش می‌رسد و بقیه
۴۰۴ برمی‌گرداند.
