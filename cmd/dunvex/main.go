// Command dunvex is the Dunvex VPN panel: one binary for the panel, the
// subscription server, core supervision and the admin command line.
//
// Copyright (c) 2026 Pedram. All rights reserved where not licensed otherwise.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pedramrj/dunvex/internal/bot"
	"github.com/pedramrj/dunvex/internal/coremgr"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/server"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
	"github.com/pedramrj/dunvex/web"
)

// Version is set at build time with -ldflags "-X main.Version=v1.0.0".
var Version = "dev"

func defaultHome() string {
	if h := os.Getenv("DUNVEX_HOME"); h != "" {
		return h
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(".", "dunvex-home")
	}
	return "/usr/local/dunvex"
}

func main() {
	log.SetFlags(log.LstdFlags)
	home := defaultHome()
	args := os.Args[1:]
	// global --home flag
	for i := 0; i < len(args); i++ {
		if args[i] == "--home" && i+1 < len(args) {
			home = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
		if strings.HasPrefix(args[i], "--home=") {
			home = strings.TrimPrefix(args[i], "--home=")
			args = append(args[:i], args[i+1:]...)
			break
		}
	}
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve", "run":
		err = serve(home)
	case "setup":
		err = setup(home, args)
	case "info":
		err = info(home)
	case "admin":
		err = admin(home, args)
	case "set":
		err = set(home, args)
	case "core":
		err = core(home, args)
	case "menu":
		err = menu(home)
	case "update":
		var tag string
		tag, err = coremgr.UpdateSelf()
		if err == nil {
			fmt.Println("Dunvex updated to", tag, "- restart the service: systemctl restart dunvex")
		}
	case "version", "-v", "--version":
		fmt.Println("Dunvex", Version, "by Pedram")
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`Dunvex ` + Version + ` — VPN panel by Pedram

Usage: dunvex [--home DIR] <command>

  serve                 run the panel (default)
  setup [flags]         first-time setup: create admin, pick port/path, install cores
  info                  show panel address and admin user
  admin <user> <pass>   reset the admin username and password
  set <key> <value>     change a setting (panel_port, panel_path, sub_path, domain, host)
  core install          download/update Xray and sing-box
  menu                  interactive menu (status, restart, reset admin, update…)
  update                update Dunvex itself
  version               print version
`)
}

func paths(home string) (db, bin, run string) {
	return filepath.Join(home, "data.db"), filepath.Join(home, "bin"), filepath.Join(home, "run")
}

func openStore(home string) (*store.Store, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	db, _, _ := paths(home)
	// a restore uploaded from the panel is swapped in before the database is opened
	if p, err := os.ReadFile(filepath.Join(home, "restore.pending")); err == nil {
		src := strings.TrimSpace(string(p))
		if util.FileExists(src) {
			os.Remove(db + "-wal")
			os.Remove(db + "-shm")
			if err := os.Rename(src, db); err != nil {
				log.Printf("restore failed: %v", err)
			} else {
				log.Printf("database restored from backup")
			}
		}
		os.Remove(filepath.Join(home, "restore.pending"))
	}
	return store.Open(db)
}

// ensureDefaults creates the first admin and random secrets when missing.
func ensureDefaults(st *store.Store) (user, pass string, created bool) {
	if st.GetSetting("panel_port", "") == "" {
		st.SetSetting("panel_port", strconv.Itoa(randomPort()))
	}
	if st.GetSetting("panel_path", "") == "" {
		st.SetSetting("panel_path", "/"+util.RandomLower(12)+"/")
	}
	if st.GetSetting("sub_path", "") == "" {
		st.SetSetting("sub_path", "/"+util.RandomLower(8)+"/")
	}
	if st.AdminCount() == 0 {
		user, pass = "admin", util.RandomString(14)
		h, _ := server.HashPassword(pass)
		st.UpsertAdmin(user, h)
		created = true
	}
	return
}

func randomPort() int {
	for i := 0; i < 50; i++ {
		b := util.RandomBytes(2)
		p := 10000 + (int(b[0])<<8|int(b[1]))%50000
		if util.FreePort(p) {
			return p
		}
	}
	return 2096
}

func serve(home string) error {
	st, err := openStore(home)
	if err != nil {
		return err
	}
	defer st.Close()
	if u, p, created := ensureDefaults(st); created {
		log.Printf("created admin %q with password %q (change it in the panel)", u, p)
	}
	_, binDir, runDir := paths(home)
	sys := sysinfo.NewSampler()
	srv := &server.Server{Home: home, Version: Version, St: st, Sys: sys}
	mgr := engine.NewManager(st, binDir, runDir, func() protocol.Env { return srv.Env() })
	srv.Mgr = mgr
	if ui, err := fs.Sub(web.Dist, "dist"); err == nil {
		srv.UI = ui
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	restartCode := 0
	srv.Restart = func() {
		restartCode = 3 // systemd restarts us (Restart=always)
		stop()
	}

	// wait briefly for the public IP so links are right on the first apply
	for i := 0; i < 30 && sys.PublicIP() == "" && st.GetSetting("host", "") == "" && st.GetSetting("domain", "") == ""; i++ {
		time.Sleep(200 * time.Millisecond)
	}
	tg := bot.New(bot.Deps{St: st, Mgr: mgr, Sys: sys, Version: Version, NewUser: srv.NewUser,
		SubURL: func(u *store.User) string { return srv.SubURL(nil, u) }})
	srv.Bot = tg
	tg.Start()

	stopMgr := make(chan struct{})
	mgrDone := make(chan struct{})
	go func() { mgr.Run(stopMgr); close(mgrDone) }()

	addr := os.Getenv("DUNVEX_LISTEN") + ":" + st.GetSetting("panel_port", "2096")
	hs := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second}
	env := srv.Env()
	cert, key := env.CertFile, env.KeyFile
	useTLS := st.GetSetting("panel_tls", "false") == "true" && util.FileExists(cert) && util.FileExists(key)
	go func() {
		var err error
		log.Printf("Dunvex %s panel on %s%s (tls=%v)", Version, addr, st.GetSetting("panel_path", "/"), useTLS)
		if useTLS {
			err = hs.ListenAndServeTLS(cert, key)
		} else {
			err = hs.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("panel server: %v", err)
			stop()
		}
	}()
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hs.Shutdown(sctx)
	close(stopMgr)
	<-mgrDone
	if restartCode != 0 {
		os.Exit(restartCode)
	}
	return nil
}

func setup(home string, args []string) error {
	fsf := flag.NewFlagSet("setup", flag.ExitOnError)
	port := fsf.Int("port", 0, "panel port (random when 0)")
	path := fsf.String("path", "", "panel path (random when empty)")
	user := fsf.String("user", "", "admin username")
	pass := fsf.String("pass", "", "admin password (random when empty)")
	noCores := fsf.Bool("no-cores", false, "skip downloading cores")
	starter := fsf.Bool("starter", true, "create a starter Reality inbound and a demo user on a fresh install")
	fsf.Parse(args)
	st, err := openStore(home)
	if err != nil {
		return err
	}
	defer st.Close()
	fresh := st.AdminCount() == 0
	u, p, _ := ensureDefaults(st)
	if *port > 0 {
		st.SetSetting("panel_port", strconv.Itoa(*port))
	}
	if *path != "" {
		st.SetSetting("panel_path", "/"+strings.Trim(*path, "/")+"/")
	}
	if *user != "" || *pass != "" {
		if *user == "" {
			*user = "admin"
		}
		if *pass == "" {
			*pass = util.RandomString(14)
		}
		h, _ := server.HashPassword(*pass)
		if a, err := st.FirstAdmin(); err == nil {
			st.UpdateAdmin(a.ID, *user, h)
		} else {
			st.UpsertAdmin(*user, h)
		}
		u, p = *user, *pass
	}
	_, binDir, _ := paths(home)
	if !*noCores {
		fmt.Println("• Downloading Xray-core ...")
		if v, err := coremgr.InstallXray(binDir); err != nil {
			fmt.Println("  ! Xray:", err)
		} else {
			fmt.Println("  ✓ Xray", v)
		}
		fmt.Println("• Downloading sing-box ...")
		if v, err := coremgr.InstallSingBox(binDir); err != nil {
			fmt.Println("  ! sing-box:", err)
		} else {
			fmt.Println("  ✓ sing-box", v)
		}
	}
	if fresh && *starter {
		if list, _ := st.Inbounds(); len(list) == 0 {
			pr := protocol.Get("vless-reality-universal")
			port := 443
			if !util.FreePort(port) {
				port = 8443
			}
			st.CreateInbound(&store.Inbound{Preset: pr.ID, Port: port, Enabled: true, Remark: "Reality", Params: protocol.Defaults(pr, nil, "")})
			hy := protocol.Get("hysteria2")
			st.CreateInbound(&store.Inbound{Preset: hy.ID, Port: hy.DefaultPort, Enabled: true, Remark: "Hysteria2", Params: protocol.Defaults(hy, map[string]any{"obfs": true}, "")})
			fmt.Println("  ✓ starter inbounds: VLESS Reality (port", port, ") + Hysteria2 (port", hy.DefaultPort, ")")
		}
	}
	printInfo(st, u, p)
	return nil
}

func printInfo(st *store.Store, user, pass string) {
	ip := st.GetSetting("domain", "")
	if ip == "" {
		ip = st.GetSetting("host", "")
	}
	if ip == "" {
		ip = sysinfo.DetectPublicIP()
	}
	url := fmt.Sprintf("http://%s%s", net.JoinHostPort(ip, st.GetSetting("panel_port", "")), st.GetSetting("panel_path", "/"))
	fmt.Println()
	fmt.Println("  ╭──────────────────── Dunvex ────────────────────╮")
	fmt.Println("    Panel:    ", url)
	if user != "" {
		fmt.Println("    Username: ", user)
	} else if a, err := st.FirstAdmin(); err == nil {
		fmt.Println("    Username: ", a.Username)
	}
	if pass != "" {
		fmt.Println("    Password: ", pass)
	} else {
		fmt.Println("    Password:  (unchanged — reset with: dunvex admin <user> <pass>)")
	}
	fmt.Println("  ╰────────────────────────────────────────────────╯")
	fmt.Println()
}

func info(home string) error {
	st, err := openStore(home)
	if err != nil {
		return err
	}
	defer st.Close()
	printInfo(st, "", "")
	return nil
}

func admin(home string, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: dunvex admin <username> [password]")
	}
	st, err := openStore(home)
	if err != nil {
		return err
	}
	defer st.Close()
	pass := ""
	if len(args) > 1 {
		pass = args[1]
	} else {
		pass = util.RandomString(14)
	}
	h, _ := server.HashPassword(pass)
	// SSH access is the recovery path: it also turns two-step login off
	if a, err := st.FirstAdmin(); err == nil {
		a.Username, a.PasswordHash, a.TOTPSecret, a.Enabled = args[0], h, "", true
		if err := st.SaveAdmin(a); err != nil {
			return err
		}
	} else if err := st.UpsertAdmin(args[0], h); err != nil {
		return err
	}
	printInfo(st, args[0], pass)
	fmt.Println("  Two-step login is off for this admin; turn it on again in Settings › Security.")
	return nil
}

func set(home string, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: dunvex set <key> <value>")
	}
	allowed := map[string]bool{"panel_port": true, "panel_path": true, "sub_path": true, "domain": true, "host": true, "panel_tls": true, "sub_base_url": true}
	if !allowed[args[0]] {
		return fmt.Errorf("unknown key %q", args[0])
	}
	st, err := openStore(home)
	if err != nil {
		return err
	}
	defer st.Close()
	v := args[1]
	if args[0] == "panel_path" || args[0] == "sub_path" {
		v = "/" + strings.Trim(v, "/") + "/"
	}
	if err := st.SetSetting(args[0], v); err != nil {
		return err
	}
	fmt.Println("saved; restart the service to apply: systemctl restart dunvex")
	return nil
}

func core(home string, args []string) error {
	if len(args) == 0 || args[0] != "install" {
		return errors.New("usage: dunvex core install")
	}
	_, binDir, _ := paths(home)
	v, err := coremgr.InstallXray(binDir)
	if err != nil {
		return fmt.Errorf("xray: %w", err)
	}
	fmt.Println("✓ Xray", v)
	v, err = coremgr.InstallSingBox(binDir)
	if err != nil {
		return fmt.Errorf("sing-box: %w", err)
	}
	fmt.Println("✓ sing-box", v)
	return nil
}
