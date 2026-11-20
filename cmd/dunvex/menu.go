package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/pedramrj/dunvex/internal/coremgr"
	"github.com/pedramrj/dunvex/internal/util"
)

func run(name string, args ...string) {
	c := exec.Command(name, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	c.Run()
}

// menu is a small interactive helper for SSH sessions.
func menu(home string) error {
	in := bufio.NewReader(os.Stdin)
	ask := func(q string) string {
		fmt.Print(q)
		s, _ := in.ReadString('\n')
		return strings.TrimSpace(s)
	}
	for {
		fmt.Print(`
  ╭─────────────── Dunvex · menu ───────────────╮
    1) Status            6) Update Dunvex
    2) Restart           7) Update cores (Xray, sing-box)
    3) Login info        8) Logs
    4) Reset admin       9) Uninstall
    5) Change panel port 0) Exit
  ╰──────────────────────────────────────────────╯
`)
		switch ask("  › ") {
		case "1":
			run("systemctl", "status", "dunvex", "--no-pager", "-n", "0")
		case "2":
			run("systemctl", "restart", "dunvex")
			fmt.Println("  ✓ restarted")
		case "3":
			info(home)
		case "4":
			user := ask("  username [admin]: ")
			if user == "" {
				user = "admin"
			}
			pass := ask("  new password [random]: ")
			if pass == "" {
				pass = util.RandomString(14)
			}
			if err := admin(home, []string{user, pass}); err != nil {
				fmt.Println("  !", err)
			}
		case "5":
			p, err := strconv.Atoi(ask("  new panel port: "))
			if err != nil || p < 1 || p > 65535 {
				fmt.Println("  ! invalid port")
				continue
			}
			if err := set(home, []string{"panel_port", strconv.Itoa(p)}); err != nil {
				fmt.Println("  !", err)
				continue
			}
			run("systemctl", "restart", "dunvex")
			info(home)
		case "6":
			tag, err := coremgr.UpdateSelf()
			if err != nil {
				fmt.Println("  !", err)
				continue
			}
			run("systemctl", "restart", "dunvex")
			fmt.Println("  ✓ updated to", tag)
		case "7":
			run("systemctl", "stop", "dunvex")
			if err := core(home, []string{"install"}); err != nil {
				fmt.Println("  !", err)
			}
			run("systemctl", "start", "dunvex")
		case "8":
			run("journalctl", "-u", "dunvex", "-n", "80", "--no-pager")
		case "9":
			if ask("  type 'yes' to remove Dunvex (the database is backed up to /root): ") == "yes" {
				run("bash", "-c", "curl -fsSL https://raw.githubusercontent.com/pedramrj/Dunvex/main/install.sh | bash -s uninstall")
				return nil
			}
		case "0", "q", "":
			return nil
		}
	}
}
