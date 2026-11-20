package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Dunvex "Obsidian Navy" palette for the terminal (Lip Gloss lowers it to 256 colours when needed).
var (
	colText    = lipgloss.Color("#F4F7FF")
	colMuted   = lipgloss.Color("#9AAAC2")
	colDim     = lipgloss.Color("#5B6B85")
	colLine    = lipgloss.Color("#26344A")
	colLine2   = lipgloss.Color("#34466A")
	colBlue    = lipgloss.Color("#397BFF")
	colElec    = lipgloss.Color("#4F8CFF")
	colCyan    = lipgloss.Color("#24CFD2")
	colViolet  = lipgloss.Color("#A48BFF")
	colPink    = lipgloss.Color("#FF6AD5")
	colOK      = lipgloss.Color("#25C99A")
	colWarn    = lipgloss.Color("#F0B85B")
	colBad     = lipgloss.Color("#F06B78")
	colSel     = lipgloss.Color("#5B47F5")
	colBar     = lipgloss.Color("#1A2540")
	colSurface = lipgloss.Color("#111C30")
)

var (
	stText  = lipgloss.NewStyle().Foreground(colText)
	stBold  = lipgloss.NewStyle().Foreground(colText).Bold(true)
	stMuted = lipgloss.NewStyle().Foreground(colMuted)
	stDim   = lipgloss.NewStyle().Foreground(colDim)
	stCyan  = lipgloss.NewStyle().Foreground(colCyan)
	stElec  = lipgloss.NewStyle().Foreground(colElec)
	stVio   = lipgloss.NewStyle().Foreground(colViolet)
	stPink  = lipgloss.NewStyle().Foreground(colPink).Bold(true)
	stOK    = lipgloss.NewStyle().Foreground(colOK)
	stWarn  = lipgloss.NewStyle().Foreground(colWarn)
	stBad   = lipgloss.NewStyle().Foreground(colBad)
	stKey   = lipgloss.NewStyle().Foreground(colMuted).Bold(true)
)

// ---------------------------------------------------------------- gradients

type rgb struct{ r, g, b float64 }

func hexRGB(h string) rgb {
	h = strings.TrimPrefix(h, "#")
	v, _ := strconv.ParseUint(h, 16, 32)
	return rgb{float64(v >> 16 & 255), float64(v >> 8 & 255), float64(v & 255)}
}

// gradient returns n colours spread over the stops.
func gradient(n int, stops ...string) []lipgloss.Color {
	out := make([]lipgloss.Color, n)
	if n == 0 {
		return out
	}
	cs := make([]rgb, len(stops))
	for i, s := range stops {
		cs[i] = hexRGB(s)
	}
	for i := 0; i < n; i++ {
		t := 0.0
		if n > 1 {
			t = float64(i) / float64(n-1)
		}
		seg := t * float64(len(cs)-1)
		k := int(seg)
		if k >= len(cs)-1 {
			k = len(cs) - 2
		}
		f := seg - float64(k)
		a, b := cs[k], cs[k+1]
		out[i] = lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", int(a.r+(b.r-a.r)*f), int(a.g+(b.g-a.g)*f), int(a.b+(b.b-a.b)*f)))
	}
	return out
}

// paint colours each rune of s with the next colour.
func paint(s string, cols []lipgloss.Color, bold bool) string {
	var b strings.Builder
	i := 0
	for _, r := range s {
		if r == ' ' {
			b.WriteRune(' ')
			i++
			continue
		}
		c := cols[min(i, len(cols)-1)]
		b.WriteString(lipgloss.NewStyle().Foreground(c).Bold(bold).Render(string(r)))
		i++
	}
	return b.String()
}

var brandStops = []string{"#397BFF", "#7B6BFF", "#A48BFF", "#24CFD2"}

// stripes is a band of diagonal lines in the brand gradient (Crush style).
func stripes(n, offset, total int) string {
	if n <= 0 {
		return ""
	}
	cols := gradient(total, brandStops...)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(lipgloss.NewStyle().Foreground(cols[min(offset+i, total-1)]).Render("╱"))
	}
	return b.String()
}

// ---------------------------------------------------------------- pixel wordmark

var glyphs = map[rune][]string{
	'D': {"#####.", "##..##", "##..##", "##..##", "##..##", "#####."},
	'U': {"##..##", "##..##", "##..##", "##..##", "##..##", ".####."},
	'N': {"##...##", "###..##", "####.##", "##.####", "##..###", "##...##"},
	'V': {"##...##", "##...##", ".##.##.", ".##.##.", "..###..", "...#..."},
	'E': {"######", "##....", "#####.", "##....", "##....", "######"},
	'X': {"##...##", ".##.##.", "..###..", "..###..", ".##.##.", "##...##"},
	'0': {".###.", "##.##", "##.##", "##.##", "##.##", ".###."},
	'1': {".##", "###", ".##", ".##", ".##", ".##"},
	'2': {"####.", "...##", "..##.", ".##..", "##...", "#####"},
	'3': {"####.", "...##", ".###.", "...##", "...##", "####."},
	'4': {"##.##", "##.##", "#####", "...##", "...##", "...##"},
	'5': {"#####", "##...", "####.", "...##", "...##", "####."},
	'6': {".###.", "##...", "####.", "##.##", "##.##", ".###."},
	'7': {"#####", "...##", "..##.", ".##..", ".##..", ".##.."},
	'8': {".###.", "##.##", ".###.", "##.##", "##.##", ".###."},
	'9': {".###.", "##.##", "##.##", ".####", "...##", ".###."},
	'%': {"##...#", "##..##", "...##.", "..##..", ".##.##", "##..##"},
	'/': {"...#", "..#.", "..#.", ".#..", ".#..", "#..."},
	' ': {"..", "..", "..", "..", "..", ".."},
}

// bigText draws text in the pixel font (three rows) with one gradient across it.
func bigText(text string, stops ...string) []string {
	var px [6]string
	for i, ch := range text {
		g, ok := glyphs[ch]
		if !ok {
			continue
		}
		for r := 0; r < 6; r++ {
			if i > 0 {
				px[r] += "."
			}
			px[r] += g[r]
		}
	}
	w := len(px[0])
	cols := gradient(max(w, 1), stops...)
	rows := make([]string, 3)
	for tr := 0; tr < 3; tr++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			top, bot := px[tr*2][x] == '#', px[tr*2+1][x] == '#'
			switch {
			case top && bot:
				b.WriteString(lipgloss.NewStyle().Foreground(cols[x]).Render("█"))
			case top:
				b.WriteString(lipgloss.NewStyle().Foreground(cols[x]).Render("▀"))
			case bot:
				b.WriteString(lipgloss.NewStyle().Foreground(cols[x]).Render("▄"))
			default:
				b.WriteString(" ")
			}
		}
		rows[tr] = b.String()
	}
	return rows
}

// wordmarkRows draws DUNVEX in half blocks: three text rows, one gradient across the width.
func wordmarkRows() []string {
	word := "DUNVEX"
	var px [6]string
	for i, ch := range word {
		for r := 0; r < 6; r++ {
			if i > 0 {
				px[r] += "."
			}
			px[r] += glyphs[ch][r]
		}
	}
	w := len(px[0])
	cols := gradient(w, "#4F8CFF", "#7B6BFF", "#C084FC", "#24CFD2")
	rows := make([]string, 3)
	for tr := 0; tr < 3; tr++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			top, bot := px[tr*2][x] == '#', px[tr*2+1][x] == '#'
			ch := " "
			switch {
			case top && bot:
				ch = "█"
			case top:
				ch = "▀"
			case bot:
				ch = "▄"
			}
			if ch == " " {
				b.WriteString(" ")
				continue
			}
			b.WriteString(lipgloss.NewStyle().Foreground(cols[x]).Render(ch))
		}
		rows[tr] = b.String()
	}
	return rows
}

var wordmarkWidth = func() int {
	w := 0
	for i, ch := range "DUNVEX" {
		if i > 0 {
			w++
		}
		w += len(glyphs[ch][0])
	}
	return w
}()

// header is the top band: stripes, Pedram™, the wordmark and the version.
func header(width int, version string) string {
	lead := 4
	logo := wordmarkRows()
	right := width - lead - 2 - wordmarkWidth - 2
	if right < 0 {
		right = 0
	}
	top := stPink.Render("Pedram™")
	ver := stDim.Render(version)
	gap := wordmarkWidth - lipgloss.Width(top) - lipgloss.Width(ver)
	if gap < 1 {
		gap = 1
	}
	lines := []string{
		stripes(lead, 0, width) + "  " + top + strings.Repeat(" ", gap) + ver + "  " + stripes(right, lead+4+wordmarkWidth, width),
	}
	for _, r := range logo {
		lines = append(lines, stripes(lead, 0, width)+"  "+r+"  "+stripes(right, lead+4+wordmarkWidth, width))
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------- panes

// pane draws a rounded box with a title set into the top border.
func pane(title string, w, h int, active bool, body string) string {
	if w < 4 || h < 3 {
		return ""
	}
	bc := colLine2
	tc := colMuted
	if active {
		bc = colElec
		tc = colText
	}
	border := lipgloss.NewStyle().Foreground(bc)
	inner := w - 2
	t := ""
	if title != "" {
		t = " " + lipgloss.NewStyle().Foreground(tc).Bold(true).Render(title) + " "
	}
	tw := lipgloss.Width(t)
	if tw > inner-2 {
		t, tw = "", 0
	}
	top := border.Render("╭─") + t + border.Render(strings.Repeat("─", max(0, inner-1-tw))+"╮")
	lines := strings.Split(body, "\n")
	out := []string{top}
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		l = ansi.Truncate(l, inner-2, "…")
		out = append(out, border.Render("│")+" "+l+strings.Repeat(" ", max(0, inner-2-lipgloss.Width(l)))+" "+border.Render("│"))
	}
	out = append(out, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(out, "\n")
}

// overlay places fg on bg with its top-left corner at (x, y).
func overlay(bg, fg string, x, y int) string {
	bl := strings.Split(bg, "\n")
	fl := strings.Split(fg, "\n")
	for i, f := range fl {
		row := y + i
		if row < 0 || row >= len(bl) {
			continue
		}
		b := bl[row]
		fw := lipgloss.Width(f)
		left := ansi.Truncate(b, x, "")
		if pad := x - lipgloss.Width(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ansi.TruncateLeft(b, x+fw, "")
		bl[row] = left + "\x1b[0m" + f + "\x1b[0m" + right
	}
	return strings.Join(bl, "\n")
}

// bar is a gradient progress bar; it turns amber and rose near full.
func bar(p float64, w int) string {
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	n := int(p*float64(w)/100 + 0.5)
	stops := []string{"#24CFD2", "#4F8CFF", "#7B6BFF"}
	if p >= 90 {
		stops = []string{"#F0B85B", "#F06B78"}
	} else if p >= 75 {
		stops = []string{"#4F8CFF", "#F0B85B"}
	}
	cols := gradient(max(n, 1), stops...)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(lipgloss.NewStyle().Foreground(cols[i]).Render("━"))
	}
	b.WriteString(lipgloss.NewStyle().Foreground(colLine2).Render(strings.Repeat("━", w-n)))
	return b.String()
}

// spark is a block sparkline of the last values, scaled to the largest.
func spark(vals []float64, w int, stops ...string) string {
	levels := []rune("▁▂▃▄▅▆▇█")
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	mx := 0.0
	for _, v := range vals {
		mx = max(mx, v)
	}
	cols := gradient(w, stops...)
	var b strings.Builder
	pad := w - len(vals)
	b.WriteString(lipgloss.NewStyle().Foreground(colLine2).Render(strings.Repeat("▁", pad)))
	for i, v := range vals {
		k := 0
		if mx > 0 {
			k = int(v / mx * float64(len(levels)-1))
		}
		b.WriteString(lipgloss.NewStyle().Foreground(cols[pad+i]).Render(string(levels[k])))
	}
	return b.String()
}

// pill is a small coloured status label.
func pill(text string, fg lipgloss.Color) string {
	return lipgloss.NewStyle().Foreground(fg).Render("● " + text)
}

// keyHint renders "key action" pairs joined by dots (Crush style).
func keyHint(pairs ...string) string {
	parts := []string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, stKey.Render(pairs[i])+" "+stDim.Render(pairs[i+1]))
	}
	return strings.Join(parts, stDim.Render(" • "))
}

// areaChart draws the last w values as a filled area, h rows tall.
func areaChart(vals []float64, w, h int, stops ...string) []string {
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	mx := 0.0
	for _, v := range vals {
		mx = max(mx, v)
	}
	levels := []rune(" ▁▂▃▄▅▆▇█")
	cols := gradient(h, stops...)
	pad := w - len(vals)
	out := make([]string, h)
	for row := 0; row < h; row++ {
		var b strings.Builder
		st := lipgloss.NewStyle().Foreground(cols[row])
		if pad > 0 {
			if row == h-1 {
				b.WriteString(lipgloss.NewStyle().Foreground(colLine2).Render(strings.Repeat("▁", pad)))
			} else {
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
		var run strings.Builder
		for _, v := range vals {
			units := 0.0
			if mx > 0 {
				units = v / mx * float64(h*8)
			}
			fill := units - float64((h-1-row)*8)
			k := 0
			switch {
			case fill >= 8:
				k = 8
			case fill > 0:
				k = int(fill)
			}
			if k == 0 && row == h-1 {
				k = 1
			}
			run.WriteRune(levels[k])
		}
		b.WriteString(st.Render(run.String()))
		out[row] = b.String()
	}
	return out
}

// card is a small rounded box used for KPI tiles.
func card(w int, lines ...string) string {
	return pane("", w, len(lines)+2, false, strings.Join(lines, "\n"))
}
