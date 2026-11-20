// Command dunvex is the Dunvex VPN panel: one binary for the panel, the
// subscription server, core supervision and the admin command line.
//
// Copyright (c) 2026 Pedram. All rights reserved where not licensed otherwise.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pedramrj/dunvex/internal/billing"
	"github.com/pedramrj/dunvex/internal/bot"
	"github.com/pedramrj/dunvex/internal/coremgr"
	"github.com/pedramrj/dunvex/internal/edge"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/geoip"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/relay"
	"github.com/pedramrj/dunvex/internal/server"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/tlsmgr"
	"github.com/pedramrj/dunvex/internal/util"
	"github.com/pedramrj/dunvex/internal/webhook"
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
	case "geoip":
		err = geoipCmd(home, args)
	case "warp":
		err = warpCmd(home, args)
	case "node":
		err = nodeCmd(home, args)
	case "menu":
		err = menu(home)
	case "qr":
		if len(args) == 0 {
			err = fmt.Errorf("usage: dunvex qr <text>")
		} else {
			err = printQR(strings.Join(args, " "), "")
		}
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
  core install          download/update Xray, sing-box and the GeoIP data
  geoip [update]        show (or download) the offline country data for the map
  warp [on|check|off]   Cloudflare WARP for this server (choose what uses it in the panel: Routing)
  node <init|serve|token|reset>   run protocols for a panel on this server (see: dunvex node)
  menu                  interactive control centre for SSH (status, users, logs, restart, update…)
  qr <text>             print a QR code in the terminal
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
		// 443 when free: the panel, the subscription page and Reality share it
		// through the edge router, so a server needs one open port and looks
		// like any other HTTPS site
		port := 443
		if !util.FreePort(port) {
			port = randomPort()
		}
		st.SetSetting("panel_port", strconv.Itoa(port))
	}
	if st.GetSetting("panel_path", "") == "" {
		st.SetSetting("panel_path", "/"+util.RandomLower(12)+"/")
	}
	if st.GetSetting("sub_path", "") == "" {
		st.SetSetting("sub_path", "/"+util.RandomLower(8)+"/")
	}
	// no random password: the owner chooses one on the first visit, through a one-time link
	if st.AdminCount() == 0 && st.GetSetting("setup_token", "") == "" {
		st.SetSetting("setup_token", util.RandomString(32))
		created = true
	}
	return
}

// setupLink is the one-time first-run link, or "" when the owner already exists.
func setupLink(st *store.Store, panel string) string {
	tok := st.GetSetting("setup_token", "")
	if tok == "" || st.AdminCount() > 0 {
		return ""
	}
	return panel + "#/setup/" + tok
}

// checkPanelPort says whether the panel may move to port: the inbounds already
// there must be able to share it, and no other program may hold it.
func checkPanelPort(st *store.Store, port int) error {
	if strconv.Itoa(port) == st.GetSetting("panel_port", "") {
		return nil
	}
	all, _ := st.Inbounds()
	var list []*store.Inbound
	for _, in := range all {
		if in.NodeID == 0 {
			list = append(list, in)
		}
	}
	if msg := protocol.PanelPortConflict(list, port); msg != "" {
		return errors.New(msg)
	}
	if !protocol.UsesPort(list, port) && !util.FreePort(port) {
		return fmt.Errorf("port %d is used by another program on this server", port)
	}
	return nil
}

// listenEdge opens the shared port, waiting a little for a previous process
// (or a core that used to listen there) to let it go.
func listenEdge(addr string) (*edge.Router, error) {
	var err error
	for i := 0; i < 30; i++ {
		var r *edge.Router
		if r, err = edge.Listen(addr); err == nil {
			return r, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return nil, err
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
	ensureDefaults(st)
	if st.AdminCount() == 0 {
		log.Printf("first-run setup is waiting: run `dunvex info` on this server for the one-time setup link")
	}
	_, binDir, runDir := paths(home)
	sys := sysinfo.NewSampler()
	srv := &server.Server{Home: home, Version: Version, St: st, Sys: sys}
	mgr := engine.NewManager(st, binDir, runDir, func() protocol.Env { return srv.Env() })
	mgr.InstallUsque = func() error { _, err := coremgr.InstallUsque(binDir); return err }
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
	bill := &billing.Service{St: st, NewUser: srv.NewUser, Apply: mgr.RequestApply, ReceiptDir: srv.ReceiptDir()}
	srv.Bill = bill
	go bill.Run(ctx)
	tg := bot.New(bot.Deps{St: st, Mgr: mgr, Sys: sys, Version: Version, NewUser: srv.NewUser, Bill: bill,
		SubURL:  func(u *store.User) string { return srv.SubURL(nil, u) },
		ShopURL: func() string { return srv.ShopURL(nil) }})
	srv.Bot = tg
	tg.Start()
	// every event also goes to the webhooks (and is counted for the metrics)
	srv.Hooks = &webhook.Sender{St: st, Version: Version, Panel: func() string { return st.GetSetting("sub_title", "Dunvex") }}
	prevLog := st.OnLog
	st.OnLog = func(kind, msg string) {
		if prevLog != nil {
			prevLog(kind, msg)
		}
		srv.OnEvent(kind, msg)
	}

	// the panel's port is shared: Reality inbounds on it are reached by SNI,
	// everything else (panel, subscription page, probes) gets the panel
	panelPort, _ := strconv.Atoi(st.GetSetting("panel_port", "2096"))
	addr := os.Getenv("DUNVEX_LISTEN") + ":" + strconv.Itoa(panelPort)
	router, err := listenEdge(addr)
	if err != nil {
		return fmt.Errorf("panel port %d: %w", panelPort, err)
	}
	env := srv.Env()
	cert, key := env.CertFile, env.KeyFile
	useTLS := st.GetSetting("panel_tls", "false") == "true" && util.FileExists(cert) && util.FileExists(key)
	router.PanelTLS = useTLS
	mgr.SharedPort = func() int { return panelPort }
	mgr.OnRoutes = router.SetRoutes
	// TrustTunnel's QUIC when it shares the panel's port: the public UDP port
	// is passed on to its loopback port
	quic := relay.New(nil)
	defer quic.Close()
	mgr.OnUDP = func(m map[int]string) {
		var list []relay.Forward
		for port, addr := range m {
			list = append(list, relay.Forward{Name: "TrustTunnel (HTTP/3)", Listen: port, Network: "udp", Target: addr})
		}
		for _, s := range quic.Stats() {
			if !s.Listening { // a port that could not be opened is tried again
				quic.Apply(nil)
				break
			}
		}
		quic.Apply(list)
	}
	mgr.ClientOf = router.ClientOf
	mgr.OnApplied = srv.KickNodes // other servers follow every change
	go router.Serve()

	stopMgr := make(chan struct{})
	mgrDone := make(chan struct{})
	go func() { mgr.Run(stopMgr); close(mgrDone) }()
	go keepGeoIP(home, stopMgr)
	go srv.RunCertRenewal(stopMgr)
	srv.Tunnels = relay.NewTunnels(os.Getenv("DUNVEX_LISTEN")) // links to reverse relays in front of this server
	defer srv.Tunnels.Close()
	go srv.RunNodes(stopMgr)

	hs := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		var err error
		log.Printf("Dunvex %s panel on %s%s (tls=%v, shared with Reality by SNI)", Version, addr, st.GetSetting("panel_path", "/"), useTLS)
		if useTLS {
			// read again when the files change, so a renewed certificate needs no restart
			hs.TLSConfig = &tls.Config{GetCertificate: (&tlsmgr.Reloader{Cert: cert, Key: key}).GetCertificate}
			err = hs.ServeTLS(router.PanelListener(), "", "")
		} else {
			err = hs.Serve(router.PanelListener())
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
	router.Close()
	close(stopMgr)
	<-mgrDone
	if restartCode != 0 {
		os.Exit(restartCode)
	}
	return nil
}

func setup(home string, args []string) error {
	fsf := flag.NewFlagSet("setup", flag.ExitOnError)
	port := fsf.Int("port", 0, "panel port (443 when free, else random; shared with Reality)")
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
		if err := checkPanelPort(st, *port); err != nil {
			return err
		}
		st.SetSetting("panel_port", strconv.Itoa(*port))
	}
	if *path != "" {
		st.SetSetting("panel_path", "/"+strings.Trim(*path, "/")+"/")
	}
	if *pass != "" { // unattended installs may still set the password up front
		if *user == "" {
			*user = "admin"
		}
		h, _ := server.HashPassword(*pass)
		if a, err := st.FirstAdmin(); err == nil {
			st.UpdateAdmin(a.ID, *user, h)
		} else {
			st.UpsertAdmin(*user, h)
		}
		u, p = *user, *pass
		st.SetSetting("setup_token", "")
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
		fmt.Println("• Downloading mtg (Telegram proxy) ...")
		if v, err := coremgr.InstallMTG(binDir); err != nil {
			fmt.Println("  ! mtg:", err)
		} else {
			fmt.Println("  ✓ mtg", v)
		}
		fmt.Println("• Downloading amneziawg-go (AmneziaWG) ...")
		if v, err := coremgr.InstallAWG(binDir); err != nil {
			fmt.Println("  ! amneziawg-go:", err)
		} else {
			fmt.Println("  ✓ amneziawg-go", v)
		}
		fmt.Println("• Downloading mita (Mieru) ...")
		if v, err := coremgr.InstallMita(binDir); err != nil {
			fmt.Println("  ! mita:", err)
		} else {
			fmt.Println("  ✓ mita", v)
		}
		fmt.Println("• Downloading the TrustTunnel endpoint ...")
		if v, err := coremgr.InstallTrustTunnel(binDir); err != nil {
			fmt.Println("  ! trusttunnel:", err)
		} else {
			fmt.Println("  ✓ trusttunnel", v)
		}
		fmt.Println("• Downloading GeoIP country data ...")
		if src, err := coremgr.InstallGeoIP(home); err != nil {
			fmt.Println("  ! GeoIP:", err)
		} else {
			fmt.Println("  ✓", src)
		}
	}
	if fresh && *starter {
		if list, _ := st.Inbounds(); len(list) == 0 {
			// 443 for everything when we can: Reality shares the panel's TCP port
			// by SNI, Hysteria2 takes UDP 443 and looks like ordinary HTTP/3
			pp, _ := strconv.Atoi(st.GetSetting("panel_port", "0"))
			pr := protocol.Get("vless-reality-universal")
			port := 443
			if port != pp && !util.FreePort(port) {
				port = 8443
			}
			st.CreateInbound(&store.Inbound{Preset: pr.ID, Port: port, Enabled: true, Remark: "Reality", Params: protocol.Defaults(pr, nil, "")})
			hy := protocol.Get("hysteria2")
			hport := 443
			if !util.FreeUDPPort(hport) {
				hport = hy.DefaultPort
			}
			st.CreateInbound(&store.Inbound{Preset: hy.ID, Port: hport, Enabled: true, Remark: "Hysteria2", Params: protocol.Defaults(hy, map[string]any{"obfs": true}, "")})
			fmt.Println("  ✓ starter inbounds: VLESS Reality (port", port, ") + Hysteria2 (port", hport, ")")
		}
	}
	printInfo(st, u, p)
	return nil
}

func printInfo(st *store.Store, user, pass string) {
	url := panelURL(st)
	fmt.Println()
	fmt.Println("  ╭──────────────────── Dunvex ────────────────────╮")
	fmt.Println("    Panel:    ", url)
	if link := setupLink(st, url); link != "" {
		fmt.Println("    Setup:    ", link)
		fmt.Println("    Username:  (you choose it on the first visit)")
		fmt.Println("    Password:  (you choose it on the first visit)")
	} else {
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
	}
	fmt.Println("  ╰────────────────────────────────────────────────╯")
	fmt.Println()
}

func info(home string) error {
	st, err := openStore(home)
	if err != nil {
		return err
	}
	if isNode(st) { // a node has no panel: show its token or pairing
		st.Close()
		return nodeToken(home, false)
	}
	defer st.Close()
	ensureDefaults(st) // a database that was never set up gets its setup link here
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
	st.SetSetting("setup_token", "")
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
	if args[0] == "panel_port" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid port %q", v)
		}
		if err := checkPanelPort(st, p); err != nil {
			return err
		}
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
	if v, err := coremgr.InstallMTG(binDir); err != nil {
		fmt.Println("! mtg:", err)
	} else {
		fmt.Println("✓ mtg", v)
	}
	if v, err := coremgr.InstallAWG(binDir); err != nil {
		fmt.Println("! amneziawg-go:", err)
	} else {
		fmt.Println("✓ amneziawg-go", v)
	}
	if v, err := coremgr.InstallMita(binDir); err != nil {
		fmt.Println("! mita:", err)
	} else {
		fmt.Println("✓ mita", v)
	}
	if v, err := coremgr.InstallTrustTunnel(binDir); err != nil {
		fmt.Println("! trusttunnel:", err)
	} else {
		fmt.Println("✓ trusttunnel", v)
	}
	if src, err := coremgr.InstallGeoIP(home); err != nil {
		fmt.Println("! GeoIP:", err)
	} else {
		fmt.Println("✓ GeoIP", src)
	}
	return nil
}

// geoipCmd shows the offline country data, or downloads it with "update".
func geoipCmd(home string, args []string) error {
	if len(args) > 0 && args[0] == "update" {
		src, err := coremgr.InstallGeoIP(home)
		if err != nil {
			return err
		}
		fmt.Println("✓", src)
		return nil
	} else if len(args) > 0 {
		return errors.New("usage: dunvex geoip [update]")
	}
	l := &geoip.Loader{Paths: []string{filepath.Join(home, coremgr.GeoIPFile), filepath.Join(home, "bin", "geoip.dat")}}
	db, err := l.Get()
	if err != nil {
		return fmt.Errorf("%v; run: dunvex geoip update", err)
	}
	v4, v6 := db.Ranges()
	fmt.Println(db.Source)
	fmt.Printf("%d countries · %d IPv4 + %d IPv6 ranges\n", db.Countries(), v4, v6)
	return nil
}

// keepGeoIP fetches the country data when it is missing or older than 40
// days (each release publishes a fresh copy), and checks again every day.
func keepGeoIP(home string, stop <-chan struct{}) {
	for {
		st, err := os.Stat(filepath.Join(home, coremgr.GeoIPFile))
		if err != nil || time.Since(st.ModTime()) > 40*24*time.Hour {
			if src, err := coremgr.InstallGeoIP(home); err != nil {
				log.Printf("GeoIP data: %v", err)
			} else {
				log.Printf("GeoIP data updated: %s", src)
			}
		}
		select {
		case <-stop:
			return
		case <-time.After(24 * time.Hour):
		}
	}
}
