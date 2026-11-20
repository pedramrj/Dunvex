---
name: Dunvex night interchange
mode: operate
colors:
  ground: "#060a15"
  panel: "#0e162c"
  panel2: "#141f3b"
  ink: "#f1f4fa"
  ink2: "#a9b5cf"
  ink3: "#7d8bab"
  brand-azure: "#2f8cff (gradient #1f6bff → #35b8ff)"
  line-reality: "#f0474f"
  line-cdn: "#4c8dff"
  line-udp: "#f2a93b"
  line-classic: "#34b37a"
  state-online: "#3ddc97"
  state-warn: "#f2a93b"
  state-bad: "#ff5d73"
  state-paused: "#9b7bff"
type:
  sans: "Dunvex Sans = Vazirmatn (Persian, variable) + Fira Sans (Latin 400–700), SIL OFL"
  mono: "Dunvex Mono = Fira Code, for ports, addresses, keys and versions"
  base: 14px
  persian-line-height: 1.8
  figures: tabular
radius: { control: 12px, card: 20px, sheet-edge: 0, pill: 999px }
---

# Dunvex design

The identity comes from the owner's logo: a brushed-platinum **DV** with an electric-azure glow,
"DUNVEX — Powered by Pedram" (`assets/branding/dunvex-logo.png`). The panel is the night around it:
a deep midnight ground lit by soft azure light, with the four protocol lines as the only other colour.

## World
- **Ground**: `#060a15` with a fixed "aurora" (radial azure glows plus a faint map grid that fades out).
- **Surfaces**: glass cards (two-stop navy gradient at ~80% opacity, 1px cool border, inner top highlight,
  blur 16px) with a thin light along the top edge. Sheets use a stronger glass.
- **Brand**: azure gradient on the primary button, the active nav marker and rings; platinum comes from
  the logo artwork itself. The logo is shown through CSS viewports over the approved square PNG
  (emblem, wordmark, credit, full), never cropped, redrawn or recoloured.
- **Line inks** stay data colours: Reality scarlet, CDN cobalt, UDP/QUIC amber, classic green.
- **States** always pair a colour with a text label (badge with dot): online mint, warn amber, bad rose, paused violet.

## Signature
The **network map** on Overview: the server is the interchange, each family a line, each inbound a station;
a dash flows faster with more traffic. On phones it becomes vertical strips.

## Layout
- Desktop: floating glass rail (256px) with the logo, nav and the admin card (role, 2FA shield).
  Content max 1480px; Overview is a 12-column bento (live traffic, server health rings, KPI cards,
  map, 30-day area chart, top users, cores, recent events).
- Phone: glass top bar with emblem + wordmark, bottom tab bar (only the tabs the role can use).
- Sheets slide from the inline end, rendered in a portal. Forms are grouped in labelled sections.
- Settings: vertical tab list on desktop, pill tabs on phones; tabs depend on the role.

## Data display
- Charts: smooth area charts with gradient fills, download solid / upload dashed (never colour alone),
  hover read-out with date and both values.
- Rings for CPU / RAM / disk and user data; they turn amber at 75% and rose at 90%.
- Persian digits via `Intl.NumberFormat('fa-IR')`; inputs, API and technical values stay Latin (`.mono`).

## Motion
Card entrance (`rise`, 10px, staggered), sheet slide, map flow, online pulse, skeleton shimmer.
All of it stops under `prefers-reduced-motion`.
