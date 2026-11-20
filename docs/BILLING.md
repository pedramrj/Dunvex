# Sales and payments (v1.13)

Dunvex can sell subscriptions on its own: a web shop, a **Renew** button on every user page, the Telegram bot,
and wallets for resellers. Everything is under **Sales** in the panel. Payment details are set by the owner only.

## Plans

A plan is what a buyer gets: data, days, devices at once, optionally only some protocols, and whether the time
starts at the first connection. A hidden plan is not offered in the shop or the bot, and resellers cannot use it.

When a plan is applied to an existing user (a renewal):

- an active user keeps the data left, the usage is set back to zero and the days are added to the time left;
- a user who has not connected yet gets the data and the days added to what was already bought;
- an expired or used-up user starts over with the plan;
- a disabled user is enabled again.

## Payment methods

| Method | How it is confirmed |
|---|---|
| Card to card | The buyer sends a photo of the receipt (JPEG, PNG or WebP, up to 5 MB) or a tracking number; an admin approves it in the panel or with one tap in the bot. |
| ZarinPal | Online payment; the panel verifies it with ZarinPal before the order is paid. Prices are in Toman (ZarinPal is sent Rial). Test mode uses `sandbox.zarinpal.com`, where no money moves. |
| USDT (TRC20) | Each order gets its own amount (the price over your rate, rounded up to the cent, plus a few thousandths). The panel checks your address on TronGrid every 30 seconds and pays the order when exactly that amount arrives. |

Use a TRON address that is yours alone (a personal wallet, not an exchange address shared by many people):
a matching amount sent by someone else to a shared address would pay the order. A TronGrid key is optional
and only raises the rate limit.

Each order is paid exactly once, even when two admins approve it at the same time. Unpaid orders expire after
the hours you set (USDT after two hours); an order waiting for approval never expires. A rejected receipt can be
sent again from the same order page.

## Where people buy

- **Web shop**: `https://<your address>/<subscription path>/shop/`. The link is shown under Sales.
- **Renew button**: when the shop is on and has plans, every user's subscription page shows a Renew button that
  opens the shop for that user.
- **Telegram bot**: anyone who opens the bot sees the plans; buyers pick a method, send the receipt photo in the chat,
  and get their subscription link and QR when it is approved. Admins get the receipt with Approve and Reject buttons.

A purchase without an existing user creates one, named after the contact the buyer gave.

## Reseller wallets

When wallets are on, resellers top up their balance (with any method the owner turned on) and every user they
create or renew from a plan is paid from it, at the plan price minus the reseller discount. A reseller must choose
a plan when wallets are on. If a user cannot be created after paying, the money goes back to the wallet. The owner
can see every wallet, its history, and add or take away balance by hand.

## API

| Method | Path | Role | What it does |
|---|---|---|---|
| GET | `/billing` | A | Settings (keys hidden from admins), shop link and sales numbers |
| PUT | `/billing/settings` | O | Change the shop and payment settings |
| GET | `/billing/plans` | R | Plans (resellers see the ones for sale, with their price) |
| POST / PUT / DELETE | `/billing/plans[/{id}]` | A | Add, change or remove a plan |
| GET | `/billing/orders?status=review` | R | Orders (`review`, `open`, `paid`, …; resellers see their own) |
| POST | `/billing/orders/{id}/approve` · `reject` · `cancel` | A | Act on an order |
| GET | `/billing/orders/{id}/receipt` | A | The receipt image |
| POST | `/users/{id}/renew` | R | Renew a user with `{"planId": 1}` (a reseller's wallet pays) |
| GET | `/wallet` · POST `/wallet/topup` | R | Your balance and history; start a top-up with `{"amount": 500000, "method": "card"}` |
| GET / POST | `/billing/wallets/{id}` | O | A reseller's history; change it with `{"amount": -10000, "note": "…"}` |

`POST /users` also takes `"planId"` to create users from a plan.
