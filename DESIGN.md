---
name: Dunvex Signal Console
mode: operate
themes: [console, oled, light, system]
colors:
  bg: "#08090b"
  rail: "#060708"
  surface: "#121317"
  surface-2: "#17191d"
  surface-3: "#1d2025"
  border: "#22252b"
  border-2: "#2d3138"
  text: "#ece9e2"
  text-2: "#aaa69c"
  text-3: "#8a867d"
  action: "#2f6af0"
  led-blue: "#3d8bff"
  ok: "#3ad48a"
  warn: "#ffb23f"
  bad: "#ff4f5e"
  line-reality: "#ff5a66"
  line-cdn: "#3d8bff"
  line-udp: "#ffb23f"
  line-classic: "#3ad48a"
  server-pins: ["#3d8bff", "#24cfd2", "#a48bff", "#3ad48a", "#ff8fb1", "#f2c94c"]
type:
  persian: "Vazirmatn for text and labels; Estedad for page titles"
  latin: "Geist"
  mono: "JetBrains Mono: printed labels (caps, 0.14em), ports, addresses, keys"
  base: 14px
  persian-min: 13px
  figures: Persian digits in Persian; tabular only in columns, never on the decimal sign
radius: { control: 10px, panel: 16px, pill: 999px }
motion: { micro: 120-160ms, panel: 200-250ms, map-draw: 1.4s once, ease: "cubic-bezier(.23,1,.32,1)" }
---

# Dunvex design: Signal Console

A VPN is signal routing, so the panel is drawn like a high-end studio console or a synth rack:
black anodized ground, machined panels with a lit top edge, labels printed in small mono capitals,
LEDs and segment meters for state, and one action blue. The owner's brushed-platinum DV logo sits
on it unchanged. "Luxury hacker" here means precision and finish, never costume: every control is a
standard web control (buttons, tabs, inputs, tables); the console lends only palette, type, density
and the signature moves below. No glitch, no scanline wallpaper, no fake terminal noise.

Hiddify is a reference for which jobs a screen does, never for how it looks, its layout, components
or wording.

## Themes
- **Console** (default, the `navy` id in stored preferences): the palette above.
- **OLED**: pure black ground. **Light**: the same structure with deepened state colours. **System** follows the OS.
- All colours are CSS variables per `html[data-theme]`, mapped to Tailwind tokens; no component carries a raw hex.

## Surfaces
- `.mach`: the machined panel (two-stop vertical gradient, 1px border, a 1px light along the top, a soft drop).
  Glass and blur only for overlays (menus, sheets). No tiled grid backgrounds, no drifting halos, no grain.
- `.field-recess`: inputs and search sit slightly sunk into the panel.
- `.ss`: printed label, mono caps with tracking, Latin only (Persian is never letter-spaced).
- `.led` (on, am, rd, bl, live): state lights; state is always said in words too.
- `.segbar`: a segment meter (lit cells), used for shares and remaining data.

## Type
- Persian text and labels in Vazirmatn (clearer than Estedad at small sizes); page titles may use Estedad.
- Latin in Geist; technical values (ports, addresses, AS numbers, keys) in JetBrains Mono, left to right.
- Persian text never below 13px. Persian digits and the Persian decimal sign; tabular figures only in
  table columns, never applied to the decimal sign (it widens to a digit's width: «۱۶ , ۹»).

## Navigation
Grouped by what the owner does: **Daily** (overview, users, live map, support), **Network** (protocols,
servers, network centre, routing, CDN), **Sales & messages** (sales, wallet, broadcast), **Reports**
(analytics, logs, alerts), **Admin** (security, settings). Phones keep the bottom bar and a More sheet.

## Signature: the live map
A real Earth (Natural Earth 1:50m coastlines and borders, Natural Earth projection, Antarctica left
out, 270 KB loaded only with the map). Every server and node is a diamond pin in its own colour
(relays dashed, offline grey); every place users connect from is an amber light sized by its devices;
an arc runs from each place to each server its devices reach, colour of that server, and a white
packet rides each arc. Places are cities from the server's own offline data (DB-IP City and ASN Lite,
CC BY 4.0, `geoip-city.bin.gz`, fetched with the country data), never a third-party service or tiles.
Users behind a relay are counted as relayed, since their own place is behind it.

The map page lists everything by **country → city → users**, by **server**, and by **network operator**
(well-known Iranian networks by their everyday names: ایرانسل، همراه اول، مخابرات…), with a user search
that lights that user's places and arcs. Wheel or buttons zoom, drag moves, labels keep their size.
Resellers see only their own users. The overview carries a small live map; the user drawer has a
**Connections** tab with that user's own map and each address (city, country, operator and AS number,
server or relay, last seen).

## The user's own page
Signal Console too: remaining data and days as large figures over a 20-segment meter, Persian digits
and Persian (Jalali) dates, connect steps for the visitor's own system (detected; iPhone, Android or
desktop) with the recommended app first, the last 7 days of use with a forecast of when the data runs
out at that pace, and **the user's own connected devices** (city, country, operator, server, the
address shortened to its network). Only that user's devices; nothing about anyone else.

## Motion
Motion shows state or answers the user: the map's arcs draw once, packets ride them, live pins and the
online LED breathe, segment meters fill cell by cell, drawers slide. `prefers-reduced-motion` and the
in-app "reduce motion" switch stop all of it. GSAP stays for the intro only.

## Data honesty
Every number and place comes from the server. Places are as precise as the offline data allows (a city,
or a country centre when the city file is missing, which the map says). Nothing is sampled or invented
in release builds; sample connections exist only in builds made with `-tags devseed`.

## Presentation mode
Blurs every secret on screen (subscription links, UUIDs, QR codes, addresses) for screen sharing.
