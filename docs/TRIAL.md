# Free trial in the Telegram bot

Someone new to the bot, who has no subscription yet, can try the service before buying: **🎁 Free trial** gives
them a short subscription in one tap, with its link and QR code. Turn it on in **Settings › Telegram › Free trial**;
the Telegram bot must be set up.

## What a person sees

- On `/start` the bot offers the trial with its size ("1 GB · 1 days · 1 device"), next to **Buy** when the shop is
  on. `/trial` asks for it too.
- One tap makes the trial user and sends the subscription link and QR, like any user's card. Their Telegram is linked
  to it, so they can check their usage in the bot.
- **One trial per Telegram account.** The record stays when the trial user is deleted, so the same account cannot
  take another. Someone who already has a subscription gets their own card instead.
- No "running out" notes during a trial. When it ends (time or data), one message says so and offers **Buy** when the
  shop is on.

## Settings

| Setting | Default | |
| --- | --- | --- |
| Data | 1 GB | MB or GB; 0 = no limit |
| Length | 24 hours | hours or days, 1 hour to 30 days, counted from the tap |
| Devices at once | 1 | 0 = no limit |
| Protocols | every one | or only those picked |
| At most a day | 50 | trials in any 24 hours; 0 = no cap. Over it, people are asked to come back tomorrow |
| Join a channel first | none | `@name` (or its t.me link). The bot must be an admin of the channel to check who joined. When it cannot check, the trial is given and the owner gets one event about it |
| Remove ended trials after | 3 days | ended trials nobody bought are deleted this long after their end; 0 = keep them. A trial renewed by hand or bought is kept |

Trial users are ordinary users named `trial-…` with the note "🎁 Telegram trial", owned by the panel's owner.

## Numbers

The panel shows trials given, those in the last 24 hours, trial users active now, and how many **bought a plan**: an
order from the same Telegram account, or for the trial user, went through. The latest 20 trials are listed with
their user's state.

## API

| Method | Path | Role | |
| --- | --- | --- | --- |
| GET | `/trial` | O | Settings, numbers and the latest trials |
| PUT | `/trial` | O | `on`, `dataMB`, `hours`, `devices`, `inbounds` (ids; empty = all), `daily`, `channel`, `cleanupDays` |
