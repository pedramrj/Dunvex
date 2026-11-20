package main

import (
	"bufio"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"

	"github.com/pedramrj/dunvex/internal/coremgr"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// ---------------------------------------------------------------- look

const (
	cR    = "\x1b[0m"
	cB    = "\x1b[1m"
	cC1   = "\x1b[38;5;51m"
	cC2   = "\x1b[38;5;45m"
	cC3   = "\x1b[38;5;39m"
	cG    = "\x1b[38;5;48m"
	cM    = "\x1b[38;5;141m"
	cRed  = "\x1b[38;5;204m"
	cY    = "\x1b[38;5;221m"
	cDim  = "\x1b[38;5;60m"
	cGray = "\x1b[38;5;246m"
	cW    = "\x1b[38;5;255m"
	cSel  = "\x1b[48;5;24m\x1b[38;5;231m"
)

var wordmark = []string{
	"██████╗ ██╗   ██╗███╗   ██╗██╗   ██╗███████╗██╗  ██╗",
	"██╔══██╗██║   ██║████╗  ██║██║   ██║██╔════╝╚██╗██╔╝",
	"██║  ██║██║   ██║██╔██╗ ██║██║   ██║█████╗   ╚███╔╝ ",
	"██║  ██║██║   ██║██║╚██╗██║╚██╗ ██╔╝██╔══╝   ██╔██╗ ",
	"██████╔╝╚██████╔╝██║ ╚████║ ╚████╔╝ ███████╗██╔╝ ██╗",
	"╚═════╝  ╚═════╝ ╚═╝  ╚═══╝  ╚═══╝  ╚══════╝╚═╝  ╚═╝",
}
var wordGrad = []string{"\x1b[38;5;27m", "\x1b[38;5;33m", "\x1b[38;5;39m", "\x1b[38;5;45m", "\x1b[38;5;51m", "\x1b[38;5;45m"}

// ---------------------------------------------------------------- live status

type menuStatus struct {
	mu                 sync.Mutex
	service            string
	since              time.Time
	xray, singbox      bool
	needX, needS       bool
	cpu, ram, disk     float64
	down, up           uint64
	users, active      int
	inbounds, inOn     int
	panel              string
	lastSent, lastRecv uint64
	lastNet            time.Time
}

func (s *menuStatus) refresh(home string, st *store.Store) {
	svc, _ := exec.Command("systemctl", "is-active", "dunvex").Output()
	since := time.Time{}
	if out, err := exec.Command("systemctl", "show", "-p", "ActiveEnterTimestamp", "--value", "dunvex").Output(); err == nil {
		if t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", strings.TrimSpace(string(out))); err == nil {
			since = t
		}
	}
	_, binDir, _ := paths(home)
	x, sb := procRunning(filepath.Join(binDir, "xray")), procRunning(filepath.Join(binDir, "sing-box"))
	var cpuP float64
	if p, err := cpu.Percent(400*time.Millisecond, false); err == nil && len(p) > 0 {
		cpuP = p[0]
	}
	var ramP, diskP float64
	if vm, err := mem.VirtualMemory(); err == nil {
		ramP = vm.UsedPercent
	}
	if du, err := disk.Usage("/"); err == nil {
		diskP = du.UsedPercent
	}
	users, active, inb, inOn := 0, 0, 0, 0
	needX, needS, canConnect := false, false, 0
	if st != nil {
		now := time.Now().Unix()
		if list, err := st.Users(); err == nil {
			users = len(list)
			for _, u := range list {
				if u.Status(now) == store.StatusActive {
					active++
				}
				if u.CanConnect(now) {
					canConnect++
				}
			}
		}
		if list, err := st.Inbounds(); err == nil {
			inb = len(list)
			for _, i := range list {
				if i.Enabled {
					inOn++
					if pr := protocol.Get(i.Preset); pr != nil && pr.Engine == "singbox" {
						needS = true
					} else {
						needX = true
					}
				}
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if io, err := psnet.IOCounters(false); err == nil && len(io) > 0 {
		now := time.Now()
		if dt := now.Sub(s.lastNet).Seconds(); s.lastSent > 0 && dt > 0 && io[0].BytesSent >= s.lastSent && io[0].BytesRecv >= s.lastRecv {
			s.up = uint64(float64(io[0].BytesSent-s.lastSent) / dt)
			s.down = uint64(float64(io[0].BytesRecv-s.lastRecv) / dt)
		}
		s.lastSent, s.lastRecv, s.lastNet = io[0].BytesSent, io[0].BytesRecv, now
	}
	s.service, s.since = strings.TrimSpace(string(svc)), since
	s.xray, s.singbox = x, sb
	// sing-box only starts once a user can connect (it refuses user inbounds without users)
	s.needX, s.needS = needX, needS && canConnect > 0
	s.cpu, s.ram, s.disk = cpuP, ramP, diskP
	s.users, s.active, s.inbounds, s.inOn = users, active, inb, inOn
}

// procRunning tells whether a process runs from exactly this binary path.
func procRunning(bin string) bool {
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		if exe, err := os.Readlink("/proc/" + e.Name() + "/exe"); err == nil && exe == bin {
			return true
		}
	}
	return false
}

func panelURL(st *store.Store) string {
	host := st.GetSetting("domain", "")
	if host == "" {
		host = st.GetSetting("host", "")
	}
	if host == "" {
		host = sysinfo.DetectPublicIP()
	}
	scheme := "http"
	if st.GetSetting("panel_tls", "") == "true" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s%s", scheme, net.JoinHostPort(host, st.GetSetting("panel_port", "")), st.GetSetting("panel_path", "/"))
}

// ---------------------------------------------------------------- drawing helpers

func meter(p float64, w int) string {
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	n := int(p*float64(w)/100 + 0.5)
	col := cC1
	if p >= 90 {
		col = cRed
	} else if p >= 75 {
		col = cY
	}
	return col + strings.Repeat("▰", n) + cDim + strings.Repeat("▱", w-n) + cR
}

func human(b uint64) string {
	f, u := float64(b), []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for f >= 1024 && i < len(u)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", b, u[i])
	}
	return fmt.Sprintf("%.1f %s", f, u[i])
}

func since(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}

func mark(ok bool) string {
	if ok {
		return cG + "✔" + cR
	}
	return cRed + "✖" + cR
}

// coreMark: ✔ running, idle when no enabled protocol needs the core, ✖ when it should run but does not.
func coreMark(running, needed bool) string {
	switch {
	case running:
		return cG + "✔" + cR
	case !needed:
		return cDim + "idle" + cR
	}
	return cRed + "✖" + cR
}

func visLen(s string) int { // visible width (no escape codes)
	n, esc := 0, false
	for _, r := range s {
		switch {
		case r == 0x1b:
			esc = true
		case esc:
			if r == 'm' {
				esc = false
			}
		default:
			n++
		}
	}
	return n
}

func center(s string, w int) string {
	pad := (w - visLen(s)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}

// ---------------------------------------------------------------- the menu

type menuItem struct {
	key, label, hint string
	run              func(t *tui) bool // true = leave the menu
}

type tui struct {
	home    string
	st      *store.Store
	stat    *menuStatus
	sel     int
	items   []menuItem
	w, h    int
	restore func()
	out     *bufio.Writer
	keys    chan string
	noWait  bool
}

// menu is the SSH control centre: live status plus the common server tasks.
func menu(home string) error {
	fd := int(os.Stdin.Fd())
	if !isTTY(fd) {
		return menuLines(home)
	}
	st, err := openStore(home)
	if err != nil {
		return err
	}
	defer st.Close()
	t := &tui{home: home, st: st, stat: &menuStatus{}, out: bufio.NewWriterSize(os.Stdout, 1<<16)}
	t.items = t.buildItems()
	t.stat.panel = panelURL(st)
	t.stat.refresh(home, st)

	restore, err := makeRaw(fd)
	if err != nil {
		return menuLines(home)
	}
	t.restore = restore
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	leave := func() {
		fmt.Fprint(os.Stdout, "\x1b[?25h\x1b[?1049l"+cR)
		restore()
	}
	defer leave()
	go func() {
		<-sig
		leave()
		os.Exit(0)
	}()
	fmt.Fprint(os.Stdout, "\x1b[?1049h\x1b[?25l")

	keys := make(chan string, 32)
	t.keys = keys
	go readKeys(keys)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	refreshing := false
	done := make(chan struct{}, 1)

	t.intro()
	for {
		t.w, t.h = termSize(fd)
		t.draw()
		select {
		case k := <-keys:
			if t.handle(k) {
				return nil
			}
		case <-tick.C:
			if !refreshing {
				refreshing = true
				go func() { t.stat.refresh(home, st); done <- struct{}{} }()
			}
		case <-done:
			refreshing = false
		}
	}
}

func readKeys(ch chan<- string) {
	buf := make([]byte, 16)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			close(ch)
			return
		}
		ch <- string(buf[:n])
	}
}

// handle reacts to one key press; true leaves the menu.
func (t *tui) handle(k string) bool {
	switch k {
	case "\x1b[A", "k":
		t.sel = (t.sel - 1 + len(t.items)) % len(t.items)
	case "\x1b[B", "j", "\t":
		t.sel = (t.sel + 1) % len(t.items)
	case "\x1b[D":
		if t.sel >= (len(t.items)+1)/2 {
			t.sel -= (len(t.items) + 1) / 2
		}
	case "\x1b[C":
		if t.sel+(len(t.items)+1)/2 < len(t.items) {
			t.sel += (len(t.items) + 1) / 2
		}
	case "\r", "\n", " ":
		return t.items[t.sel].run(t)
	case "q", "Q", "\x1b", "\x03":
		return true
	default:
		for i, it := range t.items {
			if k == it.key {
				t.sel = i
				t.draw()
				return it.run(t)
			}
		}
	}
	return false
}

func (t *tui) header(b *strings.Builder, noise int) {
	wide := t.w >= 78
	glyphs := []rune("█▓▒░#%&@01")
	line := func(i int) string {
		s := wordmark[i]
		if noise > 0 {
			rs := []rune(s)
			for j, r := range rs {
				if r != ' ' && rand.Intn(8) < noise {
					rs[j] = glyphs[rand.Intn(len(glyphs))]
				}
			}
			s = string(rs)
		}
		return wordGrad[i] + s + cR
	}
	credit := cM + cB + "P O W E R E D   B Y   P E D R A M" + cR + cDim + "  ·  " + Version + cR
	b.WriteString("\r\n")
	if wide {
		for i := 0; i < len(emblemSmall); i++ {
			b.WriteString("  " + emblemSmall[i] + strings.Repeat(" ", 2+21-visLen(emblemSmall[i])))
			switch {
			case i < len(wordmark):
				b.WriteString(line(i))
			case i == len(wordmark):
				b.WriteString(credit)
			}
			b.WriteString("\x1b[K\r\n")
		}
	} else {
		for i := range wordmark {
			b.WriteString(center(line(i), t.w) + "\x1b[K\r\n")
		}
		b.WriteString(center(credit, t.w) + "\x1b[K\r\n")
	}
}

func (t *tui) intro() {
	for _, n := range []int{6, 4, 2, 1} {
		var b strings.Builder
		b.WriteString("\x1b[H")
		t.w, t.h = termSize(int(os.Stdin.Fd()))
		t.header(&b, n)
		fmt.Fprint(t.out, b.String())
		t.out.Flush()
		time.Sleep(55 * time.Millisecond)
	}
}

func (t *tui) draw() {
	var b strings.Builder
	b.WriteString("\x1b[H")
	t.header(&b, 0)
	rule := cDim + "  " + strings.Repeat("─", max(20, min(t.w, 100)-4)) + cR + "\x1b[K\r\n"
	b.WriteString(rule)

	s := t.stat
	s.mu.Lock()
	svcCol := cG + "●"
	if s.service != "active" {
		svcCol = cRed + "●"
	}
	fmt.Fprintf(&b, "  %s %sSERVICE%s %s%-9s%s  %sUP%s %s%-8s%s  %sCORES%s xray %s  sing-box %s\x1b[K\r\n",
		svcCol+cR, cDim, cR, cW+cB, s.service, cR, cDim, cR, cW, since(s.since), cR, cDim, cR, coreMark(s.xray, s.needX), coreMark(s.singbox, s.needS))
	fmt.Fprintf(&b, "  %sCPU%s  %s %s%3.0f%%%s   %sRAM%s %s %s%3.0f%%%s   %sDISK%s %s %s%3.0f%%%s\x1b[K\r\n",
		cDim, cR, meter(s.cpu, 10), cW, s.cpu, cR, cDim, cR, meter(s.ram, 10), cW, s.ram, cR, cDim, cR, meter(s.disk, 8), cW, s.disk, cR)
	fmt.Fprintf(&b, "  %sNET%s  %s↓ %s/s  ↑ %s/s%s   %sUSERS%s %s%d%s %s(%d active)%s   %sPROTOCOLS%s %s%d/%d%s\x1b[K\r\n",
		cDim, cR, cC2, human(s.down), human(s.up), cR, cDim, cR, cW+cB, s.users, cR, cGray, s.active, cR, cDim, cR, cW, s.inOn, s.inbounds, cR)
	fmt.Fprintf(&b, "  %sPANEL%s %s%s%s\x1b[K\r\n", cDim, cR, cC1, s.panel, cR)
	s.mu.Unlock()
	b.WriteString(rule)

	half := (len(t.items) + 1) / 2
	cols := 1
	if t.w >= 74 {
		cols = 2
	}
	cell := func(i int) string {
		it := t.items[i]
		txt := fmt.Sprintf(" %s%s%s  %-22s", cC2, it.key, cR, it.label)
		if i == t.sel {
			return cSel + " ▸" + strings.Replace(txt, cC2, cSel+cB, 1) + cR
		}
		return "  " + txt
	}
	if cols == 2 {
		for r := 0; r < half; r++ {
			b.WriteString("  " + cell(r))
			if r+half < len(t.items) {
				b.WriteString("   " + cell(r+half))
			}
			b.WriteString("\x1b[K\r\n")
		}
	} else {
		for i := range t.items {
			b.WriteString("  " + cell(i) + "\x1b[K\r\n")
		}
	}
	b.WriteString(rule)
	fmt.Fprintf(&b, "  %s↑↓←→%s move   %senter%s run   %s0-9 a-c%s jump   %sq%s quit      %s%s%s\x1b[K\r\n",
		cC2, cDim, cC2, cDim, cC2, cDim, cC2, cDim, cGray, t.items[t.sel].hint, cR)
	b.WriteString("\x1b[J")
	fmt.Fprint(t.out, b.String())
	t.out.Flush()
}

// ---------------------------------------------------------------- actions

// cooked shows an action screen and waits for a key afterwards. The terminal stays in
// raw mode and every key comes from the one reader, so nothing is lost between screens.
func (t *tui) cooked(title string, fn func()) {
	fmt.Fprint(os.Stdout, "\x1b[2J\x1b[H")
	fmt.Printf("\r\n  %s⟩%s %s%s%s\r\n\r\n", cC2, cR, cM+cB, title, cR)
	fn()
	if t.noWait {
		t.noWait = false
	} else {
		fmt.Printf("\r\n  %spress any key to return%s", cDim, cR)
		<-t.keys
	}
	fmt.Fprint(os.Stdout, "\x1b[?25l\x1b[2J")
}

// ask reads one line from the shared key reader, with echo and backspace.
func (t *tui) ask(q string) string {
	fmt.Printf("  %s%s%s\x1b[?25h", cC2, q, cR)
	defer fmt.Print("\x1b[?25l")
	var buf []rune
	for k := range t.keys {
		if strings.HasPrefix(k, "\x1b") { // arrows and escape cancel nothing; ignore
			continue
		}
		for _, r := range k {
			switch r {
			case '\r', '\n':
				fmt.Print("\r\n")
				return strings.TrimSpace(string(buf))
			case 0x7f, '\b':
				if len(buf) > 0 {
					buf = buf[:len(buf)-1]
					fmt.Print("\b \b")
				}
			default:
				if r >= 0x20 {
					buf = append(buf, r)
					fmt.Print(string(r))
				}
			}
		}
	}
	return ""
}

func ask(in *bufio.Reader, q string) string {
	fmt.Printf("  %s%s%s", cC2, q, cR)
	s, _ := in.ReadString('\n')
	return strings.TrimSpace(s)
}

// job prints a hacker-style step line with a spinner while fn runs.
func job(tag, label string, fn func() (string, error)) error {
	frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	done := make(chan struct{})
	var note string
	var err error
	go func() { note, err = fn(); close(done) }()
	head := fmt.Sprintf("  %s⟩%s %s%-8s%s %s%-30s%s", cC2, cR, cM+cB, tag, cR, cW, label, cR)
	start := time.Now()
	for i := 0; ; i++ {
		select {
		case <-done:
			if err != nil {
				fmt.Printf("\r\x1b[K%s %s✖ %v%s\n", head, cRed, err, cR)
				return err
			}
			fmt.Printf("\r\x1b[K%s %s✔%s %s%s%s\n", head, cG, cR, cGray, note, cR)
			return nil
		case <-time.After(80 * time.Millisecond):
			fmt.Printf("\r%s %s%c%s %s%.0fs%s", head, cC1, frames[i%len(frames)], cR, cDim, time.Since(start).Seconds(), cR)
		}
	}
}

func shell(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (t *tui) buildItems() []menuItem {
	home := t.home
	return []menuItem{
		{"1", "Login info + QR", "panel address, admin, phone QR", func(t *tui) bool {
			t.cooked("LOGIN", func() {
				url := panelURL(t.st)
				admins := []string{}
				if list, err := t.st.Admins(); err == nil {
					for _, a := range list {
						admins = append(admins, a.Username+" ("+a.Role+")")
					}
				}
				row := func(k, v string) { fmt.Printf("  %s%-10s%s %s%s%s\r\n", cDim, k, cR, cW+cB, v, cR) }
				row("PANEL", url)
				row("ADMINS", strings.Join(admins, ", "))
				row("PASSWORD", "hidden · set a new one with option 5")
				row("SUB PATH", t.st.GetSetting("sub_path", "/sub/"))
				fmt.Printf("\r\n  %sscan to open the panel on your phone%s\r\n", cDim, cR)
				printQR(url, "  ")
			})
			return false
		}},
		{"2", "Restart service", "systemctl restart dunvex", func(t *tui) bool {
			t.cooked("RESTART", func() {
				job("RESTART", "dunvex.service", func() (string, error) {
					if _, err := shell("systemctl", "restart", "dunvex"); err != nil {
						return "", err
					}
					time.Sleep(1500 * time.Millisecond)
					return shell("systemctl", "is-active", "dunvex")
				})
			})
			return false
		}},
		{"3", "Users", "every user with status and usage", func(t *tui) bool {
			t.cooked("USERS", func() { printUsers(t.st) })
			return false
		}},
		{"4", "Live logs", "follow the service log, any key stops", func(t *tui) bool {
			t.cooked("LIVE LOGS  (any key stops)", func() {
				c := exec.Command("journalctl", "-u", "dunvex", "-n", "40", "-f", "--no-pager", "-o", "short-iso")
				pr, pw, err := os.Pipe()
				if err != nil {
					return
				}
				c.Stdout, c.Stderr = pw, pw
				if err := c.Start(); err != nil {
					fmt.Printf("  %s✖ %v%s\r\n", cRed, err, cR)
					return
				}
				pw.Close()
				go func() { // raw mode: turn bare newlines into CR LF
					r := bufio.NewReader(pr)
					for {
						line, err := r.ReadString('\n')
						if line = strings.TrimRight(line, "\n"); line != "" {
							ts, rest, _ := strings.Cut(line, " ")
							fmt.Print("  " + cDim + ts + cR + " " + cGray + rest + cR + "\r\n")
						}
						if err != nil {
							return
						}
					}
				}()
				<-t.keys
				c.Process.Kill()
				c.Wait()
				t.noWait = true
			})
			return false
		}},
		{"5", "Reset admin login", "new username and password", func(t *tui) bool {
			t.cooked("RESET ADMIN", func() {
				user := t.ask("username [admin]: ")
				if user == "" {
					user = "admin"
				}
				pass := t.ask("new password [random]: ")
				if pass == "" {
					pass = util.RandomString(14)
				}
				if err := admin(home, []string{user, pass}); err != nil {
					fmt.Printf("  %s✖ %v%s\r\n", cRed, err, cR)
				}
			})
			return false
		}},
		{"6", "Change panel port", "move the panel to another port", func(t *tui) bool {
			t.cooked("PANEL PORT", func() {
				p, err := strconv.Atoi(t.ask("new panel port: "))
				if err != nil || p < 1 || p > 65535 {
					fmt.Printf("  %s✖ invalid port%s\n", cRed, cR)
					return
				}
				job("PORT", fmt.Sprintf("panel → %d", p), func() (string, error) {
					if err := set(home, []string{"panel_port", strconv.Itoa(p)}); err != nil {
						return "", err
					}
					_, err := shell("systemctl", "restart", "dunvex")
					return "restarted", err
				})
				t.stat.panel = panelURL(t.st)
				fmt.Printf("\n  %sPANEL%s %s%s%s\n", cDim, cR, cC1, t.stat.panel, cR)
			})
			return false
		}},
		{"7", "Backup database", "copy the database to /root", func(t *tui) bool {
			t.cooked("BACKUP", func() {
				path := fmt.Sprintf("/root/dunvex-backup-%s.db", time.Now().Format("20060102-150405"))
				job("BACKUP", "database snapshot", func() (string, error) {
					if err := t.st.Backup(path); err != nil {
						return "", err
					}
					fi, err := os.Stat(path)
					if err != nil {
						return "", err
					}
					return path + " · " + human(uint64(fi.Size())), nil
				})
			})
			return false
		}},
		{"8", "Update Dunvex", "download the latest release", func(t *tui) bool {
			t.cooked("UPDATE", func() {
				var tag string
				if job("FETCH", "latest release", func() (string, error) {
					var err error
					tag, err = coremgr.UpdateSelf()
					return tag, err
				}) != nil {
					return
				}
				job("RESTART", "dunvex.service", func() (string, error) {
					_, err := shell("systemctl", "restart", "dunvex")
					return "running " + tag, err
				})
			})
			return false
		}},
		{"9", "Update cores", "fresh Xray and sing-box", func(t *tui) bool {
			t.cooked("CORES", func() {
				_, binDir, _ := paths(home)
				job("STOP", "dunvex.service", func() (string, error) { _, err := shell("systemctl", "stop", "dunvex"); return "stopped", err })
				job("XRAY", "download xray-core", func() (string, error) { return coremgr.InstallXray(binDir) })
				job("SINGBOX", "download sing-box", func() (string, error) { return coremgr.InstallSingBox(binDir) })
				job("START", "dunvex.service", func() (string, error) { _, err := shell("systemctl", "start", "dunvex"); return "running", err })
			})
			return false
		}},
		{"a", "Network tuning", "BBR, queues and buffers", func(t *tui) bool {
			t.cooked("NETWORK", func() {
				for _, k := range []string{"net.ipv4.tcp_congestion_control", "net.core.default_qdisc", "net.ipv4.tcp_fastopen", "net.ipv4.ip_forward"} {
					v, _ := shell("sysctl", "-n", k)
					fmt.Printf("  %s%-34s%s %s%s%s\n", cDim, k, cR, cW, v, cR)
				}
			})
			return false
		}},
		{"b", "Uninstall", "remove Dunvex (database is backed up)", func(t *tui) bool {
			back := false
			t.cooked("UNINSTALL", func() {
				if t.ask("type 'yes' to remove Dunvex (the database is backed up to /root): ") == "yes" {
					run("bash", "-c", "curl -fsSL https://raw.githubusercontent.com/pedramrj/Dunvex/main/install.sh | DUNVEX_PLAIN=1 bash -s uninstall")
					back = true
				}
			})
			return back
		}},
		{"0", "Exit", "leave the menu", func(t *tui) bool { return true }},
	}
}

func printUsers(st *store.Store) {
	list, err := st.Users()
	if err != nil {
		fmt.Printf("  %s✖ %v%s\n", cRed, err, cR)
		return
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UsedUp+list[i].UsedDown > list[j].UsedUp+list[j].UsedDown })
	now := time.Now().Unix()
	fmt.Printf("  %s%-20s %-9s %-22s %-12s%s\n", cDim, "NAME", "STATUS", "USAGE", "EXPIRES", cR)
	for _, u := range list {
		used := uint64(u.UsedUp + u.UsedDown)
		limit := "∞"
		if u.DataLimit > 0 {
			limit = human(uint64(u.DataLimit))
		}
		exp := "never"
		if u.ExpireAt > 0 {
			exp = time.Unix(u.ExpireAt, 0).Format("2006-01-02")
		} else if u.DurationDays > 0 {
			exp = fmt.Sprintf("%dd after use", u.DurationDays)
		}
		status := u.Status(now)
		col := cG
		switch status {
		case store.StatusExpired, store.StatusLimited, store.StatusDisabled:
			col = cRed
		case store.StatusPending:
			col = cC2
		}
		name := u.Name
		if len(name) > 20 {
			name = name[:19] + "…"
		}
		fmt.Printf("  %s%-20s%s %s%-9s%s %-22s %s%-12s%s\n", cW, name, cR, col, status, cR, human(used)+" / "+limit, cGray, exp, cR)
	}
	if len(list) == 0 {
		fmt.Printf("  %sno users yet — add them from the panel%s\n", cDim, cR)
	}
}

// menuLines is the plain fallback when stdin is not a terminal.
func menuLines(home string) error {
	in := bufio.NewReader(os.Stdin)
	for {
		fmt.Print(`
  Dunvex · menu (by Pedram)
    1) Status   2) Restart   3) Login info   4) Reset admin   5) Logs   0) Exit
`)
		switch ask(in, "› ") {
		case "1":
			run("systemctl", "status", "dunvex", "--no-pager", "-n", "0")
		case "2":
			run("systemctl", "restart", "dunvex")
		case "3":
			info(home)
		case "4":
			user := ask(in, "username [admin]: ")
			if user == "" {
				user = "admin"
			}
			pass := ask(in, "new password [random]: ")
			if pass == "" {
				pass = util.RandomString(14)
			}
			if err := admin(home, []string{user, pass}); err != nil {
				fmt.Println("  !", err)
			}
		case "5":
			run("journalctl", "-u", "dunvex", "-n", "80", "--no-pager")
		case "0", "q", "":
			return nil
		}
	}
}

func run(name string, args ...string) {
	c := exec.Command(name, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	c.Run()
}
