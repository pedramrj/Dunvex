package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"

	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// menu is the SSH control centre: a full-screen terminal app (Bubble Tea) with
// live status and the common server tasks. Without a terminal it falls back to a plain menu.
func menu(home string) error {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return menuLines(home)
	}
	st, err := openStore(home)
	if err != nil {
		return err
	}
	defer st.Close()
	m := newModel(home, st)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err = p.Run()
	m.stop()
	return err
}

// ---------------------------------------------------------------- helpers shared by the views

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
	if t.IsZero() || t.After(time.Now()) {
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

// coreMark: ✔ running, idle when no enabled protocol needs the core, ✖ when it should run but does not.
func coreMark(running, needed bool) string {
	switch {
	case running:
		return stOK.Render("✔")
	case !needed:
		return stDim.Render("idle")
	}
	return stBad.Render("✖")
}

func center(s string, w int) string {
	return strings.Repeat(" ", max(0, (w-lipgloss.Width(s))/2)) + s
}

func shell(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// ---------------------------------------------------------------- plain fallback

func ask(in *bufio.Reader, q string) string {
	fmt.Printf("  %s", q)
	s, _ := in.ReadString('\n')
	return strings.TrimSpace(s)
}

// menuLines is the plain menu used when stdin or stdout is not a terminal.
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
