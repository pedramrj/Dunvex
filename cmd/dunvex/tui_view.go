package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

func navWidth(w int) int {
	if w >= 100 {
		return 26
	}
	return 22
}

func rightWidth(w int) int {
	if w >= 124 {
		return 36
	}
	return 0
}

func (m *model) View() string {
	if m.w == 0 {
		return ""
	}
	if m.w < 64 || m.h < 18 {
		return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center,
			stBold.Render("DUNVEX")+"\n"+stDim.Render("make the terminal at least 64×18"))
	}
	bodyH := m.h - 5
	nw, rw := navWidth(m.w), rightWidth(m.w)
	cw := m.w - nw - rw
	nav := pane("Navigate", nw, bodyH, m.navFocus, m.viewNav(nw-4))
	title := sections[m.sec].icon + " " + sections[m.sec].name
	center := pane(title, cw, bodyH, !m.navFocus, m.viewSection(cw-4, bodyH-2))
	cols := []string{nav, center}
	if rw > 0 {
		cols = append(cols, pane("Server", rw, bodyH, false, m.viewServer(rw-4)))
	}
	screen := header(m.w, Version) + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, cols...) + "\n" + m.statusBar()

	if m.toast != "" {
		screen = m.drawToast(screen)
	}
	if m.palette {
		box := m.viewPalette()
		screen = overlay(screen, box, (m.w-lipgloss.Width(box))/2, max(2, (m.h-lipgloss.Height(box))/2-2))
	}
	if m.modal != nil {
		box := m.viewModal()
		screen = overlay(screen, box, (m.w-lipgloss.Width(box))/2, max(2, (m.h-lipgloss.Height(box))/2-1))
	}
	return screen
}

// ---------------------------------------------------------------- navigation

func (m *model) viewNav(w int) string {
	counts := map[section]string{}
	if m.snap.ready {
		counts[secUsers] = strconv.Itoa(len(m.snap.users))
		on := 0
		for _, i := range m.snap.inbounds {
			if i.Enabled {
				on++
			}
		}
		counts[secProtocols] = fmt.Sprintf("%d/%d", on, len(m.snap.inbounds))
		counts[secBackups] = strconv.Itoa(len(m.snap.backups))
		counts[secLogs] = "live"
	}
	lines := []string{""}
	for i, s := range sections {
		label := s.icon + "  " + s.name
		c := counts[section(i)]
		gap := max(1, w-lipgloss.Width(label)-lipgloss.Width(c)-2)
		row := " " + label + strings.Repeat(" ", gap) + c + " "
		switch {
		case i == m.navSel && m.navFocus:
			row = lipgloss.NewStyle().Background(colSel).Foreground(colText).Bold(true).Width(w).Render(row)
		case i == m.navSel:
			row = lipgloss.NewStyle().Background(colBar).Foreground(colCyan).Bold(true).Width(w).Render(row)
		default:
			row = " " + stCyan.Render(s.icon) + "  " + stText.Render(s.name) + strings.Repeat(" ", gap) + stDim.Render(c)
		}
		lines = append(lines, row)
	}
	lines = append(lines, "", stDim.Render(" ───"), " "+stKey.Render("ctrl+p")+" "+stDim.Render("commands"), " "+stKey.Render("1-8")+"    "+stDim.Render("jump"), " "+stKey.Render("tab")+"    "+stDim.Render("switch pane"))
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------- sections

func (m *model) viewSection(w, h int) string {
	switch m.sec {
	case secDash:
		return m.viewDash(w)
	case secUsers:
		return m.viewUsers(w, h)
	case secProtocols:
		return m.viewProtocols(w)
	case secLogs:
		return m.viewLogs(w, h)
	case secBackups:
		return m.viewBackups(w)
	case secService:
		return m.viewService(w)
	case secNetwork:
		return m.viewNetwork(w)
	case secLogin:
		return m.viewLogin(w)
	}
	return ""
}

func label(s string) string { return stDim.Render(fmt.Sprintf("%-6s", s)) }

func (m *model) viewDash(w int) string {
	now2 := time.Now().Unix()
	st := map[string]int{}
	for _, u := range m.snap.users {
		st[u.Status(now2)]++
	}
	// KPI cards with big numbers
	gap := 1
	cw := (w - 3*gap) / 4
	big := func(v float64) []string { return bigText(fmt.Sprintf("%.0f%%", v), "#24CFD2", "#4F8CFF", "#A48BFF") }
	ramTxt := ""
	if m.snap.memTotal > 0 {
		ramTxt = human(uint64(m.ram/100*float64(m.snap.memTotal))) + " of " + human(m.snap.memTotal)
	}
	tile := func(title, sub string, rows []string, p float64) string {
		lines := []string{stVio.Bold(true).Render(title)}
		if lipgloss.Width(rows[0]) > cw-4 { // narrow terminal: plain number instead of the pixel font
			rows = []string{"", stBold.Render(fmt.Sprintf("%.0f%%", p)), ""}
			if title == "ONLINE" {
				rows[1] = stBold.Render(strconv.Itoa(m.snap.online))
			}
		}
		lines = append(lines, rows...)
		lines = append(lines, bar(p, cw-4), stDim.Render(trunc(sub, cw-4)))
		return card(cw, lines...)
	}
	online := bigText(strconv.Itoa(m.snap.online), "#25C99A", "#24CFD2")
	cards := lipgloss.JoinHorizontal(lipgloss.Top,
		tile("CPU", fmt.Sprintf("%d vCPU · live", m.snap.cores), big(m.cpu), m.cpu), " ",
		tile("MEMORY", ramTxt, big(m.ram), m.ram), " ",
		tile("DISK", "root filesystem", big(m.disk), m.disk), " ",
		tile("ONLINE", fmt.Sprintf("%d users · %d active", len(m.snap.users), st[store.StatusActive]), online, pctOf(m.snap.online, len(m.snap.users))))
	lines := []string{cards, ""}

	// network: download and upload side by side
	half := (w - 3) / 2
	dl := areaChart(m.downH, half, 5, "#4F8CFF", "#24CFD2")
	ul := areaChart(m.upH, half, 5, "#FF6AD5", "#7B6BFF")
	lines = append(lines,
		padTo(stCyan.Bold(true).Render("↓ DOWNLOAD")+"  "+stBold.Render(human(uint64(m.down))+"/s")+stDim.Render("  peak "+human(uint64(peak(m.downH)))+"/s"), half)+"   "+
			stVio.Bold(true).Render("↑ UPLOAD")+"  "+stBold.Render(human(uint64(m.up))+"/s")+stDim.Render("  peak "+human(uint64(peak(m.upH)))+"/s"))
	for i := range dl {
		lines = append(lines, dl[i]+"   "+ul[i])
	}
	shown := min(len(m.downH), half)
	caption := fmt.Sprintf("live · last %ds · one column per second", shown)
	if shown >= 120 {
		caption = fmt.Sprintf("live · last %d min · one column per second", shown/60)
	}
	lines = append(lines, stDim.Render(caption), "")

	// traffic in the last 24 hours, one bar per hour
	slots := make([]float64, 24)
	now := time.Now().Truncate(time.Hour)
	for _, pt := range m.snap.hourly {
		k := 23 - int(now.Sub(time.Unix(pt.Hour, 0))/time.Hour)
		if k >= 0 && k < 24 {
			slots[k] += float64(pt.Up + pt.Down)
		}
	}
	lines = append(lines, stVio.Bold(true).Render("TRAFFIC · 24 HOURS")+"   "+stDim.Render("today ")+stBold.Render(human(uint64(m.snap.today)))+stDim.Render("   this month ")+stBold.Render(human(uint64(m.snap.month))))
	lines = append(lines, bars(slots, w, 4)...)

	// users by status
	on := 0
	for _, i := range m.snap.inbounds {
		if i.Enabled {
			on++
		}
	}
	lines = append(lines, "", stVio.Bold(true).Render("USERS")+"   "+
		stOK.Render("● "+strconv.Itoa(st[store.StatusActive])+" active")+"   "+
		stCyan.Render("● "+strconv.Itoa(st[store.StatusPending])+" waiting")+"   "+
		stWarn.Render("● "+strconv.Itoa(st[store.StatusLimited])+" quota used")+"   "+
		stBad.Render("● "+strconv.Itoa(st[store.StatusExpired])+" expired")+"   "+
		stDim.Render("● "+strconv.Itoa(st[store.StatusDisabled])+" off"))
	lines = append(lines, stack(w, []float64{float64(st[store.StatusActive]), float64(st[store.StatusPending]), float64(st[store.StatusLimited]), float64(st[store.StatusExpired]), float64(st[store.StatusDisabled])},
		[]lipgloss.Color{colOK, colCyan, colWarn, colBad, colDim}))
	if len(m.snap.top) > 0 && m.snap.top[0].Bytes > 0 {
		lines = append(lines, "", stDim.Render("top users · 30 days"))
		mx := float64(m.snap.top[0].Bytes)
		for _, t := range m.snap.top {
			lines = append(lines, " "+stText.Render(fmt.Sprintf("%-16s", trunc(t.Name, 16)))+" "+bar(float64(t.Bytes)/mx*100, max(6, w-32))+" "+stMuted.Render(human(uint64(t.Bytes))))
		}
	}

	// protocols as chips
	lines = append(lines, "", stVio.Bold(true).Render("PROTOCOLS")+"   "+stBold.Render(fmt.Sprintf("%d/%d", on, len(m.snap.inbounds)))+stDim.Render(" on"))
	chips := " "
	for _, in := range m.snap.inbounds {
		pr := protocol.Get(in.Preset)
		group, name := "classic", in.Preset
		if pr != nil {
			group, name = pr.Group, pr.Name
		}
		if in.Remark != "" {
			name = in.Remark
		}
		dot := lipgloss.NewStyle().Foreground(famColor(group)).Render("●")
		if !in.Enabled {
			dot = stDim.Render("○")
		}
		chip := dot + " " + stText.Render(trunc(name, 18)) + stCyan.Render(" :"+strconv.Itoa(in.Port)) + "    "
		if lipgloss.Width(chips)+lipgloss.Width(chip) > w {
			lines = append(lines, chips)
			chips = " "
		}
		chips += chip
	}
	lines = append(lines, chips)

	// recent events
	if len(m.snap.events) > 0 {
		lines = append(lines, "", stVio.Bold(true).Render("RECENT EVENTS"))
		for _, e := range m.snap.events {
			c := colElec
			switch e.Kind {
			case "core":
				c = colWarn
			case "auth":
				c = colOK
			case "limit":
				c = colViolet
			}
			lines = append(lines, " "+lipgloss.NewStyle().Foreground(c).Render("◆")+" "+stDim.Render(fmt.Sprintf("%-6s", since(time.Unix(e.At, 0))))+" "+stMuted.Render(trunc(e.Message, w-12)))
		}
	}
	return strings.Join(lines, "\n")
}

func pctOf(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) * 100 / float64(b)
}

func peak(v []float64) float64 {
	mx := 0.0
	for _, x := range v {
		mx = max(mx, x)
	}
	return mx
}

func padTo(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

// stack is one bar split into coloured parts.
func stack(w int, parts []float64, cols []lipgloss.Color) string {
	total := 0.0
	for _, p := range parts {
		total += p
	}
	if total == 0 {
		return lipgloss.NewStyle().Foreground(colLine2).Render(strings.Repeat("▬", w))
	}
	var b strings.Builder
	used := 0
	for i, p := range parts {
		n := int(p / total * float64(w))
		if i == len(parts)-1 {
			n = w - used
		}
		used += n
		b.WriteString(lipgloss.NewStyle().Foreground(cols[i]).Render(strings.Repeat("▬", max(0, n))))
	}
	return b.String()
}

// bars draws vertical gradient bars, height rows tall.
func bars(vals []float64, w, height int) []string {
	n := len(vals)
	bw := 1
	if w >= n*3 {
		bw = 2
	}
	mx := 0.0
	for _, v := range vals {
		mx = max(mx, v)
	}
	levels := []rune(" ▁▂▃▄▅▆▇█")
	cols := gradient(height, "#7B6BFF", "#4F8CFF", "#24CFD2")
	out := make([]string, height)
	for row := 0; row < height; row++ {
		var b strings.Builder
		for _, v := range vals {
			units := 0.0
			if mx > 0 {
				units = v / mx * float64(height*8)
			}
			fill := units - float64((height-1-row)*8)
			k := 0
			switch {
			case fill >= 8:
				k = 8
			case fill > 0:
				k = int(fill)
			}
			ch := string(levels[k])
			if k == 0 && row == height-1 {
				b.WriteString(lipgloss.NewStyle().Foreground(colLine2).Render(strings.Repeat("▁", bw)) + " ")
				continue
			}
			b.WriteString(lipgloss.NewStyle().Foreground(cols[height-1-row]).Render(strings.Repeat(ch, bw)) + " ")
		}
		out[row] = b.String()
	}
	return out
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func statusColor(s string) lipgloss.Color {
	switch s {
	case store.StatusActive:
		return colOK
	case store.StatusPending:
		return colCyan
	case store.StatusLimited:
		return colWarn
	case store.StatusExpired:
		return colBad
	case "paused":
		return colViolet
	}
	return colDim
}

func (m *model) viewUsers(w, h int) string {
	list := m.visibleUsers()
	lines := []string{}
	if m.filtering || m.filter.Value() != "" {
		lines = append(lines, m.filter.View(), "")
	} else {
		lines = append(lines, stDim.Render("/ filter · ↑↓ move · tab back to the menu"), "")
	}
	bw := max(6, w-72)
	narrow := w < 78 // small terminals: name, status and usage only
	if narrow {
		lines = append(lines, row(w, false, cell{"NAME", stDim, 16}, cell{"STATUS", stDim, 11}, cell{"USAGE", stDim, 0}))
	} else {
		lines = append(lines, row(w, false, cell{"NAME", stDim, 16}, cell{"STATUS", stDim, 11}, cell{"USAGE", stDim, bw}, cell{"", stDim, 20}, cell{"EXPIRES", stDim, 13}, cell{"DEVICES", stDim, 0}))
	}
	if len(list) == 0 {
		lines = append(lines, "", stDim.Render(" no users yet · add them from the web panel"))
		return strings.Join(lines, "\n")
	}
	room := max(3, h-4)
	if m.userSel >= len(list) {
		m.userSel = len(list) - 1
	}
	off := 0
	if m.userSel >= room {
		off = m.userSel - room + 1
	}
	now := time.Now().Unix()
	for i := off; i < len(list) && i < off+room; i++ {
		u := list[i]
		status := u.Status(now)
		used := u.Used()
		limit, p := "∞", 0.0
		if u.DataLimit > 0 {
			limit = human(uint64(u.DataLimit))
			p = float64(used) / float64(u.DataLimit) * 100
		}
		exp := "never"
		if u.ExpireAt > 0 {
			exp = time.Unix(u.ExpireAt, 0).Format("2006-01-02")
		} else if u.DurationDays > 0 {
			exp = strconv.Itoa(u.DurationDays) + "d from use"
		}
		dev := "any"
		if u.IPLimit > 0 {
			dev = strconv.Itoa(u.IPLimit)
		}
		sel := i == m.userSel && !m.navFocus
		usage := cell{bar(p, bw), lipgloss.NewStyle(), bw}
		if sel {
			usage = cell{strings.Repeat("━", int(p*float64(bw)/100)) + strings.Repeat("─", bw-int(p*float64(bw)/100)), lipgloss.NewStyle(), bw}
		}
		if narrow {
			lines = append(lines, row(w, sel, cell{u.Name, stText, 16},
				cell{"● " + status, lipgloss.NewStyle().Foreground(statusColor(status)), 11},
				cell{human(uint64(used)) + " / " + limit, stMuted, 0}))
			continue
		}
		lines = append(lines, row(w, sel,
			cell{u.Name, stText, 16},
			cell{"● " + status, lipgloss.NewStyle().Foreground(statusColor(status)), 11},
			usage,
			cell{human(uint64(used)) + " / " + limit, stMuted, 20},
			cell{exp, stMuted, 13},
			cell{dev, stDim, 0}))
	}
	return strings.Join(lines, "\n")
}

func famColor(group string) lipgloss.Color {
	switch group {
	case "reality":
		return lipgloss.Color("#F0656F")
	case "cdn":
		return colElec
	case "udp":
		return colWarn
	}
	return colOK
}

func (m *model) viewProtocols(w int) string {
	lines := []string{"", stDim.Render(fmt.Sprintf(" %-18s %-40s %-7s %-9s %s", "NAME", "PROTOCOL", "PORT", "CORE", "STATE"))}
	for _, in := range m.snap.inbounds {
		pr := protocol.Get(in.Preset)
		name, group, engine := in.Preset, "classic", "xray"
		if pr != nil {
			name, group, engine = pr.Name, pr.Group, pr.Engine
		}
		remark := in.Remark
		if remark == "" {
			remark = name
		}
		state := stOK.Render("● on")
		if !in.Enabled {
			state = stDim.Render("○ off")
		}
		if engine == "singbox" {
			engine = "sing-box"
		}
		lines = append(lines, fmt.Sprintf(" %s %s %s %s %s %s",
			lipgloss.NewStyle().Foreground(famColor(group)).Render("●"),
			stText.Render(fmt.Sprintf("%-16s", trunc(remark, 16))),
			stMuted.Render(fmt.Sprintf("%-40s", trunc(name, 40))),
			stCyan.Render(fmt.Sprintf("%-7s", ":"+strconv.Itoa(in.Port))),
			stDim.Render(fmt.Sprintf("%-9s", engine)), state))
	}
	if len(m.snap.inbounds) == 0 {
		lines = append(lines, "", stDim.Render(" no protocols yet · add them from the web panel"))
	}
	lines = append(lines, "", stDim.Render(" "+stKey.Render("●")+" Reality  ")+lipgloss.NewStyle().Foreground(colElec).Render("●")+stDim.Render(" CDN  ")+stWarn.Render("●")+stDim.Render(" UDP/QUIC  ")+stOK.Render("●")+stDim.Render(" classic   ·   add or change protocols in the web panel"))
	return strings.Join(lines, "\n")
}

func (m *model) viewLogs(w, h int) string {
	room := max(3, h-2)
	end := len(m.logs) - m.logOff
	if end < 0 {
		end = 0
	}
	start := max(0, end-room)
	lines := []string{}
	for _, l := range m.logs[start:end] {
		ts, rest, _ := strings.Cut(l, " ")
		if len(ts) > 19 {
			ts = ts[11:19]
		}
		low := strings.ToLower(rest)
		st := stMuted
		switch {
		case strings.Contains(low, "error") || strings.Contains(low, "fail") || strings.Contains(low, "panic"):
			st = stBad
		case strings.Contains(low, "warn"):
			st = stWarn
		case strings.Contains(low, "started") || strings.Contains(low, "running"):
			st = stOK
		}
		lines = append(lines, stDim.Render(ts)+" "+st.Render(rest))
	}
	for len(lines) < room {
		lines = append([]string{""}, lines...)
	}
	state := stOK.Render("● following")
	if m.logOff > 0 {
		state = stWarn.Render(fmt.Sprintf("❚❚ paused · %d lines up · end to follow", m.logOff))
	}
	lines = append(lines, "", state+stDim.Render("   ↑↓ scroll · pgup/pgdn"))
	return strings.Join(lines, "\n")
}

// cell is one column of a list row: text, its colour and a fixed width (0 = the rest).
type cell struct {
	text  string
	style lipgloss.Style
	width int
}

// row lays out cells; a selected row is drawn as one solid bar so no colour breaks it.
func row(w int, selected bool, cells ...cell) string {
	var plain, styled strings.Builder
	used := 0
	for i, c := range cells {
		cw := c.width
		if cw == 0 {
			cw = max(0, w-used)
		}
		t := trunc(c.text, max(1, cw))
		t += strings.Repeat(" ", max(0, cw-lipgloss.Width(t)))
		if i < len(cells)-1 {
			t += " "
			cw++
		}
		used += cw
		plain.WriteString(t)
		styled.WriteString(c.style.Render(t))
	}
	if selected {
		return lipgloss.NewStyle().Background(colSel).Foreground(colText).Bold(true).Render(padTo(trunc(plain.String(), w), w))
	}
	return styled.String()
}

func (m *model) viewBackups(w int) string {
	sel := func(i int) bool { return i == m.itemSel && !m.navFocus }
	lines := []string{"", row(w, sel(0), cell{"＋", stCyan, 2}, cell{"Back up now", stBold, 22}, cell{"copies the database to /root  ·  ctrl+b", stDim, 0}), ""}
	if len(m.snap.backups) == 0 {
		lines = append(lines, stDim.Render(" no backups in /root yet"))
	}
	for i, b := range m.snap.backups {
		lines = append(lines, row(w, sel(i+1), cell{"⟲", stVio, 2}, cell{b.path, stText, 42}, cell{human(uint64(b.size)), stMuted, 10}, cell{b.at.Format("2006-01-02 15:04"), stDim, 0}))
	}
	lines = append(lines, "", stDim.Render(" restore a backup in the web panel: Settings › Backup"))
	return strings.Join(lines, "\n")
}

func (m *model) viewService(w int) string {
	lines := []string{""}
	for i, a := range serviceActions {
		ns := stBold
		if i == len(serviceActions)-1 {
			ns = stBad.Bold(true)
		}
		lines = append(lines, row(w, i == m.itemSel && !m.navFocus, cell{"▸", stCyan, 1}, cell{a.name, ns, 20}, cell{a.desc, stDim, 0}))
	}
	lines = append(lines, "", stDim.Render(" enter runs the action · risky ones ask first · ctrl+r restart · ctrl+b backup"))
	return strings.Join(lines, "\n")
}

func (m *model) viewNetwork(w int) string {
	want := map[string]string{"net.ipv4.tcp_congestion_control": "bbr", "net.core.default_qdisc": "fq", "net.ipv4.tcp_fastopen": "3", "net.ipv4.ip_forward": "1"}
	lines := []string{"", stVio.Bold(true).Render("KERNEL TUNING")}
	for _, kv := range m.snap.sysctl {
		mark := stOK.Render("✔")
		if kv[1] != want[kv[0]] {
			mark = stWarn.Render("≠ " + want[kv[0]])
		}
		lines = append(lines, fmt.Sprintf(" %s %s %s", stDim.Render(fmt.Sprintf("%-34s", kv[0])), stBold.Render(fmt.Sprintf("%-6s", kv[1])), mark))
	}
	lines = append(lines, "", stVio.Bold(true).Render("CONNECTIONS")+"   "+stDim.Render("tcp ")+stBold.Render(strconv.Itoa(m.snap.tcp))+stDim.Render("   udp ")+stBold.Render(strconv.Itoa(m.snap.udp)),
		"", stVio.Bold(true).Render("LISTENING TCP PORTS"))
	row := " "
	for _, p := range m.snap.listen {
		chip := stCyan.Render(p) + "  "
		if lipgloss.Width(row)+lipgloss.Width(chip) > w {
			lines = append(lines, row)
			row = " "
		}
		row += chip
	}
	lines = append(lines, row, "", stVio.Bold(true).Render("TRAFFIC NOW")+"   "+stCyan.Render("↓ "+human(uint64(m.down))+"/s")+"   "+stVio.Render("↑ "+human(uint64(m.up))+"/s"))
	return strings.Join(lines, "\n")
}

func (m *model) viewLogin(w int) string {
	row := func(k, v string) string { return " " + stDim.Render(fmt.Sprintf("%-10s", k)) + stBold.Render(v) }
	lines := []string{"",
		row("PANEL", m.panel),
		row("ADMINS", strings.Join(m.snap.admins, ", ")),
		row("PASSWORD", "hidden · reset it in Service"),
		row("SUB PATH", m.st.GetSetting("sub_path", "/sub/")),
		"", stDim.Render(" scan to open the panel on your phone"), ""}
	for _, l := range qrLines(m.panel) {
		lines = append(lines, " "+l)
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------- server card

func (m *model) viewServer(w int) string {
	lines := []string{""}
	for _, e := range emblemSmall {
		lines = append(lines, strings.Repeat(" ", max(0, (w-21)/2))+e)
	}
	s := m.snap
	svc := stOK.Render("● " + s.service)
	if s.service != "active" {
		svc = stBad.Render("● " + orDash(s.service))
	}
	row := func(k, v string) string { return stDim.Render(fmt.Sprintf("%-8s", k)) + v }
	lines = append(lines, "",
		center(stPink.Render("Pedram™")+stDim.Render("  ·  ")+stMuted.Render(Version), w),
		"",
		row("service", svc+stDim.Render("  up "+since(s.since))),
		row("cores", "xray "+coreMark(s.xray, s.needX)+"  sing-box "+coreMark(s.sbox, s.needS)),
		row("host", stText.Render(trunc(orDash(s.hostname), w-9))),
		row("system", stText.Render(trunc(orDash(s.osName), w-9))),
		row("kernel", stMuted.Render(trunc(orDash(s.kernel), w-9))),
		row("cpu", stText.Render(fmt.Sprintf("%d vCPU", s.cores))),
		row("memory", stText.Render(human(s.memTotal))),
		row("boot", stMuted.Render(since(time.Now().Add(-time.Duration(s.uptime)*time.Second)))),
		"",
		stDim.Render("panel"),
		stCyan.Render(trunc(m.panel, w)),
	)
	return strings.Join(lines, "\n")
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// ---------------------------------------------------------------- status bar, toast, palette, modal

func (m *model) statusBar() string {
	seg := func(bg lipgloss.Color, fg lipgloss.Color, bold bool, s string) string {
		return lipgloss.NewStyle().Background(bg).Foreground(fg).Bold(bold).Padding(0, 1).Render(s)
	}
	svc := "● " + orDash(m.snap.service)
	svcFg := colOK
	if m.snap.service != "active" {
		svcFg = colBad
	}
	left := seg(colSel, colText, true, "DUNVEX") +
		seg(colBar, svcFg, false, svc) +
		seg(colSurface, colCyan, false, "↓ "+human(uint64(m.down))+"/s  ↑ "+human(uint64(m.up))+"/s") +
		seg(colSurface, colMuted, false, fmt.Sprintf("users %d · online %d", len(m.snap.users), m.snap.online))
	right := seg(colSurface, colDim, false, keyHint("ctrl+p", "commands", "tab", "focus", "q", "quit")) +
		seg(colSel, colText, true, time.Now().Format("15:04:05"))
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		right = seg(colSel, colText, true, time.Now().Format("15:04"))
		gap = max(0, m.w-lipgloss.Width(left)-lipgloss.Width(right))
	}
	return left + lipgloss.NewStyle().Background(colSurface).Render(strings.Repeat(" ", gap)) + right
}

func (m *model) drawToast(screen string) string {
	bc, icon := colOK, "✔"
	if m.toastErr {
		bc, icon = colBad, "✖"
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(bc).Padding(0, 1).
		Render(lipgloss.NewStyle().Foreground(bc).Bold(true).Render(icon) + " " + stText.Render(trunc(m.toast, min(70, m.w-12))))
	return overlay(screen, box, m.w-lipgloss.Width(box)-2, m.h-1-lipgloss.Height(box))
}

func (m *model) viewPalette() string {
	w := min(66, m.w-6)
	inner := w - 4
	list := m.filteredCommands()
	title := lipgloss.NewStyle().Foreground(colViolet).Bold(true).Render("Commands") + " " + stripes(inner-9, 0, inner-9)
	lines := []string{title, "", m.pfilter.View(), ""}
	room := 10
	off := 0
	if m.psel >= room {
		off = m.psel - room + 1
	}
	for i := off; i < len(list) && i < off+room; i++ {
		c := list[i]
		gap := max(1, inner-lipgloss.Width(c.name)-lipgloss.Width(c.key))
		row := c.name + strings.Repeat(" ", gap) + c.key
		if i == m.psel {
			row = lipgloss.NewStyle().Background(colSel).Foreground(colText).Bold(true).Width(inner).Render(row)
		} else {
			row = stText.Render(c.name) + strings.Repeat(" ", gap) + stDim.Render(c.key)
		}
		lines = append(lines, row)
	}
	if len(list) == 0 {
		lines = append(lines, stDim.Render("nothing matches"))
	}
	lines = append(lines, "", keyHint("tab", "switch selection", "↑↓", "choose", "enter", "confirm", "esc", "cancel"))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colSel).Padding(0, 1).Width(w).Render(strings.Join(lines, "\n"))
}

func (m *model) viewModal() string {
	md := m.modal
	w := min(64, m.w-6)
	accent := colSel
	if md.danger {
		accent = colBad
	}
	title := lipgloss.NewStyle().Foreground(accent).Bold(true).Render(md.title)
	lines := []string{title, ""}
	switch md.kind {
	case "busy":
		lines = append(lines, m.spin.View()+" "+stMuted.Render("working… please wait"))
	case "confirm":
		for _, l := range strings.Split(md.msg, "\n") {
			lines = append(lines, stText.Render(l))
		}
		btn := func(text string, on bool, c lipgloss.Color) string {
			st := lipgloss.NewStyle().Padding(0, 2).Foreground(colMuted).Background(colBar)
			if on {
				st = st.Foreground(colText).Background(c).Bold(true)
			}
			return st.Render(text)
		}
		ok := "Confirm"
		if md.danger {
			ok = "Yes, remove"
		}
		lines = append(lines, "", btn("Cancel", md.choice == 0, colSel)+"  "+btn(ok, md.choice == 1, accent), "", keyHint("←→", "choose", "enter", "confirm", "esc", "cancel"))
	case "input":
		if md.msg != "" {
			lines = append(lines, stMuted.Render(md.msg), "")
		}
		for i := range md.inputs {
			if i == md.focus {
				md.inputs[i].Focus()
			}
			lines = append(lines, stDim.Render(md.inputs[i].Placeholder), md.inputs[i].View(), "")
		}
		lines = append(lines, keyHint("tab", "next field", "enter", "save", "esc", "cancel"))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(1, 2).Width(w).Render(strings.Join(lines, "\n"))
}
