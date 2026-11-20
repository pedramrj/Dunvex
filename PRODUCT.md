# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack
delegated: React + TypeScript + Vite + Tailwind CSS, built to static files and embedded in the Go binary (one-file install, no CDN at runtime).

## Users
VPN service operators (inferred from the brief): one owner who installs Dunvex on a VPS and sells or shares access with tens to a few thousand end users. Usually Persian-speaking, manages from a phone or laptop between other work, often over slow or filtered networks. End users only see their subscription page.

## Product Purpose
Run a multi-protocol VPN server (Xray + sing-box) with one install command and manage users, quotas, expiry and protocols without touching JSON. Success: a new operator goes from a fresh VPS to working configs in minutes, and daily management (add user, renew, check usage) takes seconds.

## Positioning
One binary that hides core complexity: every protocol is a validated preset, users are first-class, usage is accounted per user across both cores. Lighter than Hiddify, friendlier than 3x-ui.

## Operating Context
Daily rituals: create or renew users, copy a subscription link or QR to send in a messenger, check who is online and who is close to the limit, glance at server load. Occasional: add a protocol, set a domain/certificate, update cores, back up.

## Capabilities and Constraints
25 protocol presets; per-user data limit, expiry, start-on-first-use, periodic reset; hourly usage history; live throughput; subscription in links / Clash / sing-box formats plus a public user page. Must work in Persian (RTL) and English. Must stay concise: explanations short, advanced options collapsed.

## Brand Commitments
Name "Dunvex", built and owned by Pedram (all attribution to Pedram). User asked for a "very stylish, complete dashboard with lots of detail" that still feels easy.

## Evidence on Hand
No customers, testimonials or benchmarks exist; none may be invented.

## Product Principles
- Simple first: the common task is one click; power is one level deeper.
- Never show raw JSON unless asked.
- Every number the operator sees is real (usage, speed, status).
- Short words over long explanations.
