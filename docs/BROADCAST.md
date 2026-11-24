# Broadcast

**Broadcast** (in the main menu) sends one message to many users through the Telegram bot: news, a new server, a
maintenance window, a renewal reminder. It reaches users who linked their Telegram to their subscription (the bot
button on their user page, which opens the bot with `/start` and their token). The bot must be set up in
**Settings › Telegram**.

## Who gets it

| Audience | Users |
| --- | --- |
| Everyone | every user |
| Active | users who can connect now (active, or waiting for their first connection) |
| Running out | active users whose time ends within N days (7 by default, 1 to 60) or who used more than 80% of their data |
| Ended | users over their data limit or past their expiry: for a renewal message |
| Picked users | select users on the **Users** page and press **Message** |

Each audience shows two numbers: how many have linked Telegram (they get the message), and how many users it has in
all. A **reseller** sees and reaches only their own users, and sees only their own messages.

## The message

- Up to 3500 characters. Each user's details go in with `{name}`, `{days}` (days left), `{data}` (data left) and
  `{link}` (their subscription address); with no limit they read `∞`.
- As written, by default. With **Telegram HTML formatting** on, the text may use Telegram's tags: `<b>`, `<i>`, `<u>`,
  `<s>`, `<a href="…">`, `<code>`, `<pre>`, `<blockquote>` and `<tg-spoiler>`. The panel checks the tags before
  anything is sent; write `&lt;` for a plain "<".
- The **My account** button under the message shows the user their usage and time left, and **Renew** when the shop
  is on.
- The preview shows the message as Telegram will, with a sample user. **Send me a test** sends it to the bot's admins
  first, filled in with a real user of the audience (owner and admins only).

## Sending

Messages go one by one, about 20 a second, under Telegram's limit for bots; when Telegram asks to slow down ("retry
after"), Dunvex waits and tries again. One broadcast runs at a time. The page shows the progress and a **Stop** button;
messages already sent stay sent.

- A user who **blocked the bot** or deleted their Telegram account is counted apart, and their Telegram link is removed,
  so usage warnings stop going to a closed door. They link again from their user page.
- If Telegram refuses the message itself (formatting it cannot read), sending stops at the first user instead of
  failing for everyone.
- The last 30 broadcasts stay in the history with their text, audience, sender and result (delivered, failed, blocked
  the bot, stopped). A broadcast cut short by a panel restart is marked as such. Each finished broadcast is also an
  event (`user` kind) on the Alerts page and for webhooks.

## From the Telegram bot

Bot admins can broadcast from their phone: **📣 Broadcast** in the bot's menu (or `/broadcast`), pick the audience
(each button shows how many it reaches), send the text, and the bot shows it as users will get it, with **Send to N**
and **Cancel**. A message then follows the progress, with **Stop**, and ends with the result. The text is sent as
written, with the same `{name}`, `{days}`, `{data}` and `{link}`.

## API

| Method | Path | Role | |
| --- | --- | --- | --- |
| GET | `/broadcasts?days=7` | R | Audience sizes (`users`, `linked`), the bot's state and the history |
| POST | `/broadcasts` | R | Send: `text`, `html`, `audience` (`all`, `active`, `ending`, `ended`, `selected`), `userIds`, `days`, `button`; `"test": true` sends to the bot's admins only (owner and admins). Answers 202 with the broadcast |
| POST | `/broadcasts/count` | R | The size of a picked audience: `{"userIds": [...]}` |
| POST | `/broadcasts/{id}/cancel` | R | Stop the running broadcast |
