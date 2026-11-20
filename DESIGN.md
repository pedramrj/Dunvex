---
name: Dunvex night map
mode: operate
colors:
  ground: "#0a1224"
  rail: "#0c1630"
  panel: "#101b36"
  panel2: "#152443"
  hair: "#223154"
  hair2: "#2c3d64"
  ink: "#eef1f6"
  ink2: "#aab6cf"
  ink3: "#8593b2"
  line-reality: "#f0474f"
  line-cdn: "#4c8dff"
  line-udp: "#f2a93b"
  line-classic: "#34b37a"
  state-online: "#3ddc97"
  state-warn: "#f2a93b"
  state-bad: "#ff5d73"
type:
  family: "Dunvex Sans = Vazirmatn (Persian, variable) + Fira Sans (Latin, 400–700), SIL OFL"
  base: 14px
  persian-line-height: 1.8
  figures: tabular (font-feature-settings "tnum")
radius: { control: 12px, panel: 16px, pill: 999px }
---

# Dunvex design

The panel is a **night metro map**. The server is the interchange and every protocol family is a coloured line leaving it.

## World
- **Ground**: midnight enamel (`ground`, `rail`, `panel`). Dark is chosen for the use scene (operators checking a phone or laptop in the evening between messages), not by category.
- **Signage**: porcelain `ink` for text and the primary button. The bright fill appears only on things you can press (primary button = porcelain plate with midnight text).
- **Line inks** are data colours, never decoration: Reality = scarlet, CDN = cobalt, UDP/QUIC = amber, classic = green. Every place that names a protocol family uses its ink (map lines, protocol headings, legend, the user page's top bar).
- **State colours** sit on top only where they mean something: online mint, warn amber, bad rose. Status always has a text label next to its dot.

## Signature move
The **network map** on Overview: an SVG transit diagram. Lines leave the central interchange at 45°/90°. Stations are inbounds (ring = enabled, dashed = disabled, rose fill = error, porcelain core = traffic now). A porcelain dash flows along a line, faster with more traffic. An empty line is drawn faint and dashed, with a "+" terminus that opens "add protocol" for that family. On phones the map becomes vertical line strips; it is never shrunk into a miniature.

## Layout
- Desktop: right-hand rail in RTL (left in LTR), 232px; content max 1440px; panels with a 16px radius and a hairline border, no nested cards.
- Phone: top bar + bottom tab bar (4 tabs), rows instead of tables.
- Side sheets (create/edit/detail) slide from the inline-end edge; they render in a portal so animated page containers cannot clip them.
- Advanced options are always collapsed behind one "Advanced" disclosure.

## Type
Persian lines use a 1.8 line-height and no letter-spacing. Numbers are tabular and bidi-isolated (`.num`, `<bdi dir="ltr">` for ports, URLs and keys). Persian digits are shown through `Intl.NumberFormat('fa-IR')`; data, inputs and the API stay Latin.

## Motion
One entrance (`rise`: 8px translate, ease-out) that starts from a visible state, the sheet slide, the map flow, and an online pulse. Everything stops under `prefers-reduced-motion`.
