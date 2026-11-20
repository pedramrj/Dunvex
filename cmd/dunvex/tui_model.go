package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"

	"github.com/pedramrj/dunvex/internal/coremgr"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/server"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// ---------------------------------------------------------------- sections

type section int

const (
	secDash section = iota
	secUsers
	secProtocols
	secLogs
	secBackups
	secService
	secNetwork
	secLogin
)

var sections = []struct{ icon, name string }{
	{"◆", "Dashboard"}, {"◉", "Users"}, {"⌁", "Protocols"}, {"≡", "Live logs"},
	{"⟲", "Backups"}, {"⚙", "Service"}, {"⇅", "Network"}, {"⌂", "Login & QR"},
}

type serviceAction struct{ key, name, desc string }

// Every action has a letter: pressing it in the Service section runs it, no arrows needed.
var serviceActions = []serviceAction{
	{"r", "Restart service", "restart dunvex and its cores"},
	{"u", "Update Dunvex", "download the latest release"},
	{"c", "Update cores", "fresh Xray and sing-box"},
	{"p", "Change panel port", "move the panel to another port"},
	{"a", "Reset admin login", "new username and password (turns 2FA off)"},
	{"b", "Back up now", "copy the database to /root"},
	{"x", "Uninstall", "remove Dunvex (the database is backed up)"},
}

// ---------------------------------------------------------------- data

type backupFile struct {
	path string
	size int64
	at   time.Time
}

type snapshot struct {
	service      string
	since        time.Time
	xray, sbox   bool
	needX, needS bool
	users        []*store.User
	inbounds     []*store.Inbound
	top          []store.TopUser
	hourly       []store.UsagePoint
	today, month int64
	online       int
	backups      []backupFile
	admins       []string
	hostname     string
	osName       string
	kernel       string
	cores        int
	memTotal     uint64
	uptime       uint64
	sysctl       [][2]string
	tcp, udp     int
	listen       []string
	events       []store.Event
	ready        bool
}

type sampleMsg struct {
	cpu, ram, disk float64
	sent, recv     uint64
	at             time.Time
}
type snapMsg snapshot
type logMsg string
type panelMsg string
type actionMsg struct {
	note string
	err  error
	quit bool
}
type toastClear struct{ at time.Time }

// ---------------------------------------------------------------- modal

type modal struct {
	kind   string // confirm | input | busy
	title  string
	msg    string
	danger bool
	choice int // confirm: 0 cancel, 1 ok
	inputs []textinput.Model
	focus  int
	onOK   func(m *model, vals []string) tea.Cmd
}

// ---------------------------------------------------------------- model

type model struct {
	home     string
	st       *store.Store
	w, h     int
	sec      section
	navFocus bool
	navSel   int

	cpu, ram, disk float64
	down, up       float64
	cpuH, downH    []float64
	upH            []float64
	lastSent       uint64
	lastRecv       uint64
	lastNet        time.Time
	ticks          int

	snap  snapshot
	panel string

	filter    textinput.Model
	filtering bool
	userSel   int

	logs   []string
	logOff int
	logCh  chan string
	logCmd *exec.Cmd

	itemSel int

	palette bool
	pfilter textinput.Model
	psel    int

	modal *modal
	spin  spinner.Model

	toast    string
	toastErr bool
	toastAt  time.Time
}

func newModel(home string, st *store.Store) *model {
	f := textinput.New()
	f.Prompt = "/ "
	f.Placeholder = "filter users"
	pf := textinput.New()
	pf.Prompt = "> "
	pf.Placeholder = "Type to filter"
	pf.PromptStyle = stCyan
	pf.PlaceholderStyle = stDim
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(stCyan))
	m := &model{home: home, st: st, navFocus: true, filter: f, pfilter: pf, spin: sp, panel: "…"}
	m.logCh = make(chan string, 256)
	return m
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.sampleCmd(), m.snapCmd(), m.startLogs(), m.panelCmd(), m.spin.Tick)
}

func (m *model) sampleCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		s := sampleMsg{at: time.Now()}
		if p, err := cpu.Percent(0, false); err == nil && len(p) > 0 {
			s.cpu = p[0]
		}
		if v, err := mem.VirtualMemory(); err == nil {
			s.ram = v.UsedPercent
		}
		if d, err := disk.Usage("/"); err == nil {
			s.disk = d.UsedPercent
		}
		if io, err := psnet.IOCounters(false); err == nil && len(io) > 0 {
			s.sent, s.recv = io[0].BytesSent, io[0].BytesRecv
		}
		return s
	})
}

func (m *model) panelCmd() tea.Cmd {
	st := m.st
	return func() tea.Msg { return panelMsg(panelURL(st)) }
}

// snapCmd gathers everything slower than a sample, off the UI goroutine.
func (m *model) snapCmd() tea.Cmd {
	home, st := m.home, m.st
	return func() tea.Msg {
		var s snapshot
		out, _ := exec.Command("systemctl", "is-active", "dunvex").Output()
		s.service = strings.TrimSpace(string(out))
		if out, err := exec.Command("systemctl", "show", "-p", "ActiveEnterTimestamp", "--value", "dunvex").Output(); err == nil {
			if t, err := time.Parse("Mon 2006-01-02 15:04:05 MST", strings.TrimSpace(string(out))); err == nil {
				s.since = t
			}
		}
		_, binDir, _ := paths(home)
		s.xray, s.sbox = procRunning(filepath.Join(binDir, "xray")), procRunning(filepath.Join(binDir, "sing-box"))
		now := time.Now()
		canConnect := 0
		if us, err := st.Users(); err == nil {
			s.users = us
			for _, u := range us {
				if u.CanConnect(now.Unix()) {
					canConnect++
				}
				if u.LastOnlineAt > now.Unix()-120 {
					s.online++
				}
			}
		}
		if ib, err := st.Inbounds(); err == nil {
			s.inbounds = ib
			for _, i := range ib {
				if !i.Enabled {
					continue
				}
				if pr := protocol.Get(i.Preset); pr != nil && pr.Engine == "singbox" {
					s.needS = true
				} else {
					s.needX = true
				}
			}
		}
		s.needS = s.needS && canConnect > 0
		s.top, _ = st.TopUsers(now.Add(-30*24*time.Hour).Unix(), 6)
		s.events = st.Events(8)
		s.hourly, _ = st.UsageSeries(0, now.Add(-24*time.Hour).Unix())
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		u, d := st.TotalSince(midnight.Unix())
		s.today = u + d
		u, d = st.TotalSince(time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Unix())
		s.month = u + d
		if list, err := st.Admins(); err == nil {
			for _, a := range list {
				s.admins = append(s.admins, a.Username+" · "+a.Role)
			}
		}
		matches, _ := filepath.Glob("/root/dunvex-backup-*.db")
		for _, p := range matches {
			if fi, err := os.Stat(p); err == nil {
				s.backups = append(s.backups, backupFile{p, fi.Size(), fi.ModTime()})
			}
		}
		sort.Slice(s.backups, func(i, j int) bool { return s.backups[i].at.After(s.backups[j].at) })
		if hi, err := host.Info(); err == nil {
			s.hostname, s.osName, s.kernel, s.uptime = hi.Hostname, hi.Platform+" "+hi.PlatformVersion, hi.KernelVersion, hi.Uptime
		}
		if c, err := cpu.Counts(true); err == nil {
			s.cores = c
		}
		if v, err := mem.VirtualMemory(); err == nil {
			s.memTotal = v.Total
		}
		for _, k := range []string{"net.ipv4.tcp_congestion_control", "net.core.default_qdisc", "net.ipv4.tcp_fastopen", "net.ipv4.ip_forward"} {
			v, _ := os.ReadFile("/proc/sys/" + strings.ReplaceAll(k, ".", "/"))
			s.sysctl = append(s.sysctl, [2]string{k, strings.TrimSpace(string(v))})
		}
		if c, err := psnet.Connections("tcp"); err == nil {
			s.tcp = len(c)
			ports := map[string]bool{}
			for _, x := range c {
				if x.Status == "LISTEN" {
					ports[strconv.Itoa(int(x.Laddr.Port))+"/tcp"] = true
				}
			}
			for p := range ports {
				s.listen = append(s.listen, p)
			}
		}
		if c, err := psnet.Connections("udp"); err == nil {
			s.udp = len(c)
		}
		sort.Slice(s.listen, func(i, j int) bool { return portNum(s.listen[i]) < portNum(s.listen[j]) })
		s.ready = true
		return snapMsg(s)
	}
}

func portNum(s string) int { n, _ := strconv.Atoi(strings.SplitN(s, "/", 2)[0]); return n }

// startLogs follows the service journal into a channel.
func (m *model) startLogs() tea.Cmd {
	c := exec.Command("journalctl", "-u", "dunvex", "-n", "200", "-f", "--no-pager", "-o", "short-iso")
	pr, pw, err := os.Pipe()
	if err == nil {
		c.Stdout, c.Stderr = pw, pw
		if c.Start() == nil {
			m.logCmd = c
			pw.Close()
			go func() {
				r := bufio.NewScanner(pr)
				r.Buffer(make([]byte, 64<<10), 1<<20)
				for r.Scan() {
					m.logCh <- r.Text()
				}
			}()
		}
	}
	return m.waitLog()
}

func (m *model) waitLog() tea.Cmd {
	ch := m.logCh
	return func() tea.Msg { return logMsg(<-ch) }
}

func (m *model) say(note string, err error) {
	if err != nil {
		m.toast, m.toastErr = err.Error(), true
	} else {
		m.toast, m.toastErr = note, false
	}
	m.toastAt = time.Now()
}

func toastLater(at time.Time) tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return toastClear{at} })
}

// ---------------------------------------------------------------- actions

func (m *model) busy(title string, fn func() (string, error)) tea.Cmd {
	m.modal = &modal{kind: "busy", title: title}
	return tea.Batch(m.spin.Tick, func() tea.Msg { n, err := fn(); return actionMsg{note: n, err: err} })
}

func (m *model) runService(i int) tea.Cmd {
	home, st := m.home, m.st
	switch i {
	case 0:
		return m.busy("Restarting dunvex.service", func() (string, error) {
			if _, err := shell("systemctl", "restart", "dunvex"); err != nil {
				return "", err
			}
			time.Sleep(1500 * time.Millisecond)
			s, err := shell("systemctl", "is-active", "dunvex")
			return "Service " + s, err
		})
	case 1:
		return m.busy("Updating Dunvex", func() (string, error) {
			tag, err := coremgr.UpdateSelf()
			if err != nil {
				return "", err
			}
			_, err = shell("systemctl", "restart", "dunvex")
			return "Updated to " + tag + " · restart the menu to use it", err
		})
	case 2:
		return m.busy("Updating Xray and sing-box", func() (string, error) {
			_, binDir, _ := paths(home)
			shell("systemctl", "stop", "dunvex")
			xv, err := coremgr.InstallXray(binDir)
			if err != nil {
				shell("systemctl", "start", "dunvex")
				return "", err
			}
			sv, err := coremgr.InstallSingBox(binDir)
			if err == nil {
				coremgr.InstallMTG(binDir) // the Telegram proxy, AmneziaWG, Mieru and TrustTunnel ride along; all optional
				coremgr.InstallAWG(binDir)
				coremgr.InstallMita(binDir)
				coremgr.InstallTrustTunnel(binDir)
			}
			shell("systemctl", "start", "dunvex")
			return "Xray " + xv + " · sing-box " + sv, err
		})
	case 3:
		m.modal = &modal{kind: "input", title: "Change panel port", msg: "The panel restarts on the new port.",
			inputs: []textinput.Model{field("port", st.GetSetting("panel_port", ""))},
			onOK: func(m *model, v []string) tea.Cmd {
				p, err := strconv.Atoi(strings.TrimSpace(v[0]))
				if err != nil || p < 1 || p > 65535 {
					m.say("", fmt.Errorf("invalid port %q", v[0]))
					return toastLater(m.toastAt)
				}
				if err := checkPanelPort(st, p); err != nil {
					m.say("", err)
					return toastLater(m.toastAt)
				}
				return m.busy("Moving the panel to port "+strconv.Itoa(p), func() (string, error) {
					if err := st.SetSetting("panel_port", strconv.Itoa(p)); err != nil {
						return "", err
					}
					_, err := shell("systemctl", "restart", "dunvex")
					return "Panel now on port " + strconv.Itoa(p), err
				})
			}}
	case 4:
		user := "admin"
		if a, err := st.FirstAdmin(); err == nil {
			user = a.Username
		}
		m.modal = &modal{kind: "input", title: "Reset admin login", msg: "Leave the password empty for a random one. Two-step login turns off.",
			inputs: []textinput.Model{field("username", user), field("password", "")},
			onOK: func(m *model, v []string) tea.Cmd {
				name, pass := strings.TrimSpace(v[0]), strings.TrimSpace(v[1])
				if name == "" {
					name = "admin"
				}
				if pass == "" {
					pass = util.RandomString(14)
				}
				err := resetAdmin(st, name, pass)
				m.say("Admin "+name+" · password "+pass, err)
				return toastLater(m.toastAt)
			}}
	case 5:
		return m.backupNow()
	case 6:
		m.modal = &modal{kind: "confirm", title: "Uninstall Dunvex", danger: true,
			msg: "This stops the service and removes Dunvex from this server.\nThe database is copied to /root first.",
			onOK: func(m *model, _ []string) tea.Cmd {
				c := exec.Command("bash", "-c", "curl -fsSL https://raw.githubusercontent.com/pedramrj/Dunvex/main/install.sh | bash -s uninstall")
				return tea.ExecProcess(c, func(err error) tea.Msg { return actionMsg{note: "Dunvex removed", err: err, quit: true} })
			}}
	}
	return nil
}

func (m *model) backupNow() tea.Cmd {
	st := m.st
	return m.busy("Backing up the database", func() (string, error) {
		path := fmt.Sprintf("/root/dunvex-backup-%s.db", time.Now().Format("20060102-150405"))
		if err := st.Backup(path); err != nil {
			return "", err
		}
		fi, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		return "Saved " + path + " · " + human(uint64(fi.Size())), nil
	})
}

func field(placeholder, value string) textinput.Model {
	t := textinput.New()
	t.Placeholder = placeholder
	t.SetValue(value)
	t.Prompt = "› "
	t.PromptStyle = stCyan
	t.Width = 34
	return t
}

// resetAdmin is `dunvex admin` without printing: SSH access is the recovery path, so 2FA turns off.
func resetAdmin(st *store.Store, name, pass string) error {
	h, err := server.HashPassword(pass)
	if err != nil {
		return err
	}
	st.SetSetting("setup_token", "") // SSH recovery also ends a pending first-run setup
	if a, err := st.FirstAdmin(); err == nil {
		a.Username, a.PasswordHash, a.TOTPSecret, a.Enabled = name, h, "", true
		return st.SaveAdmin(a)
	}
	return st.UpsertAdmin(name, h)
}

// ---------------------------------------------------------------- palette

type command struct {
	name, key string
	run       func(m *model) tea.Cmd
}

func (m *model) commands() []command {
	goTo := func(s section) func(m *model) tea.Cmd {
		return func(m *model) tea.Cmd { m.sec, m.navSel, m.itemSel = s, int(s), 0; return nil }
	}
	cs := []command{}
	for i, s := range sections {
		cs = append(cs, command{"Go to " + s.name, strconv.Itoa(i + 1), goTo(section(i))})
	}
	for i, a := range serviceActions {
		i := i
		key := "6 then " + a.key
		switch i {
		case 0:
			key = "ctrl+r"
		case 5:
			key = "ctrl+b"
		}
		cs = append(cs, command{a.name, key, func(m *model) tea.Cmd { return m.runService(i) }})
	}
	cs = append(cs, command{"Quit", "q", func(m *model) tea.Cmd { return tea.Quit }})
	return cs
}

func (m *model) filteredCommands() []command {
	q := strings.ToLower(strings.TrimSpace(m.pfilter.Value()))
	out := []command{}
	for _, c := range m.commands() {
		if q == "" || strings.Contains(strings.ToLower(c.name), q) {
			out = append(out, c)
		}
	}
	return out
}

// ---------------------------------------------------------------- update

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case sampleMsg:
		m.cpu, m.ram, m.disk = msg.cpu, msg.ram, msg.disk
		if !m.lastNet.IsZero() && msg.sent >= m.lastSent && msg.recv >= m.lastRecv {
			dt := msg.at.Sub(m.lastNet).Seconds()
			if dt > 0 {
				m.up = float64(msg.sent-m.lastSent) / dt
				m.down = float64(msg.recv-m.lastRecv) / dt
			}
		}
		m.lastSent, m.lastRecv, m.lastNet = msg.sent, msg.recv, msg.at
		m.cpuH = push(m.cpuH, m.cpu)
		m.downH = push(m.downH, m.down)
		m.upH = push(m.upH, m.up)
		m.ticks++
		cmds := []tea.Cmd{m.sampleCmd()}
		if m.ticks%3 == 0 {
			cmds = append(cmds, m.snapCmd())
		}
		return m, tea.Batch(cmds...)

	case snapMsg:
		m.snap = snapshot(msg)
		return m, nil

	case panelMsg:
		m.panel = string(msg)
		return m, nil

	case logMsg:
		m.logs = append(m.logs, string(msg))
		if len(m.logs) > 2000 {
			m.logs = m.logs[len(m.logs)-2000:]
		}
		return m, m.waitLog()

	case actionMsg:
		m.modal = nil
		m.say(msg.note, msg.err)
		if msg.quit && msg.err == nil {
			return m, tea.Quit
		}
		return m, tea.Batch(m.snapCmd(), m.panelCmd(), toastLater(m.toastAt))

	case toastClear:
		if msg.at.Equal(m.toastAt) {
			m.toast = ""
		}
		return m, nil

	case spinner.TickMsg:
		var c tea.Cmd
		m.spin, c = m.spin.Update(msg)
		return m, c

	case tea.MouseMsg:
		return m, m.mouse(msg)

	case tea.KeyMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func push(h []float64, v float64) []float64 {
	h = append(h, v)
	if len(h) > 240 {
		h = h[len(h)-240:]
	}
	return h
}

func (m *model) key(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	if s == "ctrl+c" {
		return tea.Quit
	}
	// modal first
	if md := m.modal; md != nil {
		switch md.kind {
		case "busy":
			return nil
		case "help":
			m.modal = nil
			return nil
		case "confirm":
			switch s {
			case "esc", "n":
				m.modal = nil
			case "left", "right", "tab", "h", "l":
				md.choice = 1 - md.choice
			case "y":
				md.choice = 1
				fallthrough
			case "enter":
				m.modal = nil
				if md.choice == 1 && md.onOK != nil {
					return md.onOK(m, nil)
				}
			}
			return nil
		case "input":
			switch s {
			case "esc":
				m.modal = nil
				return nil
			case "tab", "down":
				md.inputs[md.focus].Blur()
				md.focus = (md.focus + 1) % len(md.inputs)
				return md.inputs[md.focus].Focus()
			case "shift+tab", "up":
				md.inputs[md.focus].Blur()
				md.focus = (md.focus - 1 + len(md.inputs)) % len(md.inputs)
				return md.inputs[md.focus].Focus()
			case "enter":
				vals := []string{}
				for _, in := range md.inputs {
					vals = append(vals, in.Value())
				}
				m.modal = nil
				return md.onOK(m, vals)
			}
			var c tea.Cmd
			md.inputs[md.focus], c = md.inputs[md.focus].Update(k)
			return c
		}
	}
	// command palette
	if m.palette {
		list := m.filteredCommands()
		switch s {
		case "esc", "ctrl+p":
			m.palette = false
			return nil
		case "up", "shift+tab":
			if len(list) > 0 {
				m.psel = (m.psel - 1 + len(list)) % len(list)
			}
			return nil
		case "down", "tab":
			if len(list) > 0 {
				m.psel = (m.psel + 1) % len(list)
			}
			return nil
		case "enter":
			m.palette = false
			if m.psel < len(list) {
				return list[m.psel].run(m)
			}
			return nil
		}
		var c tea.Cmd
		m.pfilter, c = m.pfilter.Update(k)
		m.psel = 0
		return c
	}
	// user filter
	if m.filtering {
		switch s {
		case "esc":
			m.filtering = false
			m.filter.SetValue("")
			m.filter.Blur()
			return nil
		case "enter":
			m.filtering = false
			m.filter.Blur()
			return nil
		}
		var c tea.Cmd
		m.filter, c = m.filter.Update(k)
		m.userSel = 0
		return c
	}
	// global keys
	switch s {
	case "q":
		return tea.Quit
	case "ctrl+p", ":":
		m.palette, m.psel = true, 0
		m.pfilter.SetValue("")
		return m.pfilter.Focus()
	case "ctrl+r":
		return m.runService(0)
	case "ctrl+b":
		return m.backupNow()
	case "tab":
		m.navFocus = !m.navFocus
		return nil
	case "1", "2", "3", "4", "5", "6", "7", "8":
		n, _ := strconv.Atoi(s)
		m.sec, m.navSel, m.itemSel = section(n-1), n-1, 0
		m.navFocus = false
		return nil
	case "/":
		if m.sec == secUsers {
			m.filtering = true
			return m.filter.Focus()
		}
	case "?":
		m.modal = &modal{kind: "help", title: "Keys"}
		return nil
	}
	// letters of the section on screen
	switch m.sec {
	case secService:
		for i, a := range serviceActions {
			if s == a.key {
				m.itemSel, m.navFocus = i, false
				return m.runService(i)
			}
		}
	case secBackups:
		if s == "n" {
			return m.backupNow()
		}
	}
	if m.navFocus {
		switch s {
		case "up", "k":
			m.navSel = (m.navSel - 1 + len(sections)) % len(sections)
			m.sec, m.itemSel = section(m.navSel), 0
		case "down", "j":
			m.navSel = (m.navSel + 1) % len(sections)
			m.sec, m.itemSel = section(m.navSel), 0
		case "enter", "right", "l":
			m.navFocus = false
		}
		return nil
	}
	// content focus
	switch s {
	case "left", "h", "esc":
		m.navFocus = true
		return nil
	}
	return m.contentKey(s)
}

func (m *model) contentKey(s string) tea.Cmd {
	move := func(n, total int) {
		if total == 0 {
			m.itemSel = 0
			return
		}
		m.itemSel = (m.itemSel + n + total) % total
	}
	switch m.sec {
	case secUsers:
		total := len(m.visibleUsers())
		switch s {
		case "up", "k":
			if m.userSel > 0 {
				m.userSel--
			}
		case "down", "j":
			if m.userSel < total-1 {
				m.userSel++
			}
		}
	case secLogs:
		switch s {
		case "up", "k":
			m.logOff++
		case "down", "j":
			if m.logOff > 0 {
				m.logOff--
			}
		case "pgup":
			m.logOff += 10
		case "pgdown":
			m.logOff = max(0, m.logOff-10)
		case "end", "G":
			m.logOff = 0
		}
	case secBackups:
		switch s {
		case "up", "k":
			move(-1, len(m.snap.backups)+1)
		case "down", "j":
			move(1, len(m.snap.backups)+1)
		case "enter", "n":
			if m.itemSel == 0 || s == "n" {
				return m.backupNow()
			}
		}
	case secService:
		switch s {
		case "up", "k":
			move(-1, len(serviceActions))
		case "down", "j":
			move(1, len(serviceActions))
		case "enter":
			return m.runService(m.itemSel)
		}
	}
	return nil
}

func (m *model) mouse(e tea.MouseMsg) tea.Cmd {
	if m.modal != nil || m.palette {
		return nil
	}
	switch e.Button {
	case tea.MouseButtonWheelUp:
		return m.contentKey("up")
	case tea.MouseButtonWheelDown:
		return m.contentKey("down")
	case tea.MouseButtonLeft:
		if e.Action != tea.MouseActionPress {
			return nil
		}
		// navigation rows start below the header (4) and the pane border (1) + 1 blank line
		row := e.Y - 4 - 2
		if e.X < navWidth(m.w) && row >= 0 && row < len(sections) {
			m.navSel, m.sec, m.itemSel, m.navFocus = row, section(row), 0, true
		}
	}
	return nil
}

func (m *model) visibleUsers() []*store.User {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	list := append([]*store.User(nil), m.snap.users...)
	sort.Slice(list, func(i, j int) bool { return list[i].Used() > list[j].Used() })
	if q == "" {
		return list
	}
	out := list[:0]
	for _, u := range list {
		if strings.Contains(strings.ToLower(u.Name), q) || strings.Contains(strings.ToLower(u.Note), q) {
			out = append(out, u)
		}
	}
	return out
}

func (m *model) stop() {
	if m.logCmd != nil && m.logCmd.Process != nil {
		m.logCmd.Process.Kill()
	}
}
