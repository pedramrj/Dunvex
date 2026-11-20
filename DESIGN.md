---
name: Dunvex Obsidian Navy
mode: operate
themes: [navy, oled, light, system]
colors:
  bg: "#080c13"
  bg-secondary: "#0d1420"
  surface: "#111c30"
  surface-elevated: "#172238"
  primary: "#397bff"
  electric: "#4f8cff"
  cyan: "#24cfd2"
  text: "#f4f7ff"
  text-secondary: "#9aaac2"
  border: "#26344a"
  success: "#25c99a"
  warning: "#f0b85b"
  danger: "#f06b78"
  line-reality: "#f0656f"
  line-cdn: "#4f8cff"
  line-udp: "#f0b85b"
  line-classic: "#25c99a"
  paused: "#a48bff"
type:
  persian: "Estedad (default) or Vazirmatn, variable, SIL OFL"
  latin: "Geist, variable, SIL OFL"
  mono: "JetBrains Mono, for ports, addresses, keys, versions and logs"
  base: 14px
  persian-line-height: 1.8
  figures: tabular, Persian digits in the Persian interface
radius: { control: 12px, card: 20px, sheet-edge: 0, pill: 999px }
motion: { micro: 100-160ms, popover: 150-250ms, sheet: 300-350ms, ease: "cubic-bezier(.23,1,.32,1)" }
---

# Dunvex design: Obsidian Navy

The identity comes from the owner's logo: a brushed-platinum **DV** with an electric-blue glow and
"DUNVEX · Powered by Pedram" (`assets/branding/dunvex-logo.png`). The panel is a quiet, deep navy
control room around it. One thing is allowed to be loud: the network map. Everything else stays calm,
precise and readable for hours.

## Themes
- **Navy** (default): the palette above.
- **OLED**: pure black ground for OLED screens and night shifts.
- **Light**: `#f3f6fb` ground; text, status and line colours are deepened so every label passes WCAG AA.
- **System** follows the operating system. The choice is per browser and also sets the browser's theme colour.

All colours are CSS variables per `html[data-theme]`, mapped to Tailwind tokens (`text-ink`, `bg-fg/[.04]`,
`border-hair`…), so no component carries a raw hex.

## Type
- Persian uses **Estedad** by default; **Vazirmatn** is one click away in Settings › Appearance.
- Latin and numbers use **Geist**; technical values use **JetBrains Mono** and stay left to right.
- Numbers are tabular; the Persian interface shows Persian digits and the Persian percent sign.
- Animated numbers roll digit by digit and work with both digit sets (`Num`, `Bytes`).
- Headings use balanced wrapping; no all-caps labels.

## Shell
- **Sidebar**: logo, page search, favourites, groups (Main, Monitor, Admin) with live counters,
  a health line and the account card. It collapses to icons (`Ctrl+B`).
- **Top bar**: breadcrumb, command palette (`Ctrl/Cmd+K`), quick add, notifications, presentation toggle,
  language, theme and the account menu.
- **Phone**: a compact top bar, a five-item bottom bar and a "More" sheet with every page.
- **Overlays** (menus, dialogs, sheets) stack; `Escape` closes only the one on top. Destructive
  confirmations open with Cancel focused.
- A skip link leads to the main content.

## Pages
Overview (customisable widgets, KPIs with trends, time range), Users (sortable list, context menu,
drawer with overview, configs, devices, usage and activity), Protocols, Network centre (topology,
world map with the server's real location, inspector, recent events), Servers, Analytics (zoomable
charts with legend toggles and CSV export), Logs (Xray, sing-box and panel events, live tail,
filters, export), Alerts (categories, severity, read state, quiet mode), Security (sign-in history,
protections, admins without 2FA), Settings (appearance, brand studio, server, subscription, panel,
security, admins, Telegram, cores, backup) and Mission control (a full-screen wall).

Before any of that, Login. On a fresh install it shows the first-run card instead: the one-time setup link
opens a form where the owner chooses the username and password (show/hide, strength meter, confirm). There is
never a random password to copy. Without the link the card says to run `dunvex info` on the server.

## Signature
The **network map**: the server is the interchange, each protocol family a coloured line and each
inbound a station. Packets travel faster with more traffic. The topology view is the neon version:
glowing icon nodes (shield for Reality, cloud for CDN, bolt for UDP/QUIC, cable for classic), curved
links with flowing packets, light pillars under live stations, labelled hubs and a pulsing server core.
Every label is placed where it touches no other label or node, and the view zooms to what is drawn.
On phones both become lists.

## Data components
- **KPI card**: glowing icon tile, value, a sub-line in the card's colour, a thin light along the top edge
  and, where real history exists, a full-width sparkline along the bottom. On phones the cards swipe.
- **Arc gauge**: half circle with ticks; the arc runs cyan → blue → amber → rose as load rises.
- **Timeline**: events as glowing nodes joined by a fading line, with a category tag.

## Data honesty
Every number comes from the server. Features the backend does not have yet (billing, more than one
server, session list, API keys) are shown as such, never with sample data.

## Motion
An intro plays once per session (GSAP): particles, the emblem rising, a light sweep, the wordmark and the
credit. Any key or click skips it; it can be turned off. In the panel motion only answers the user or
shows live state: sheet slides, menu pops, the rolling digits, the map packets and the online pulse.
`prefers-reduced-motion` and the in-app "reduce motion" switch stop all of it.

## Presentation mode
Blurs every secret on screen (subscription links, UUIDs, QR codes, the server address) for screen sharing.
