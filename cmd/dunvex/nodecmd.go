package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/pedramrj/dunvex/internal/coremgr"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/node"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// isNode reports whether this home belongs to a node (not a panel).
func isNode(st *store.Store) bool { return st.GetSetting("node_epoch", "") != "" }

func nodeCmd(home string, args []string) error {
	sub := "help"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "init":
		return nodeInit(home, args)
	case "serve", "run":
		return nodeServe(home)
	case "token":
		return nodeToken(home, false)
	case "reset":
		return nodeToken(home, true)
	}
	fmt.Print(`Dunvex node: run protocols for a panel on another server.

  dunvex node init [--port 62050] [--host IP] [--no-cores]   prepare this server and print its token
  dunvex node serve                                           run the node (systemd does this)
  dunvex node token                                           show the token again (until a panel pairs)
  dunvex node reset                                           forget the panel and make a new token

Paste the token in the panel under Servers › Add server.
`)
	return nil
}

func nodePort(st *store.Store) int {
	p, _ := strconv.Atoi(st.GetSetting("node_port", ""))
	if p < 1 || p > 65535 {
		p = node.DefaultPort
	}
	return p
}

func nodeHost(st *store.Store) string {
	if h := st.GetSetting("node_host", ""); h != "" {
		return h
	}
	if ip := sysinfo.DetectPublicIP(); ip != "" {
		return ip
	}
	return "127.0.0.1"
}

func printToken(tok node.Token) {
	fmt.Println()
	fmt.Println("  ╭──────────────── Dunvex node ────────────────╮")
	fmt.Println("    Agent:    ", tok.Addr)
	fmt.Println("    Token:    ", tok.String())
	fmt.Println("    Next:      panel › Servers › Add server, paste the token")
	fmt.Println("  ╰──────────────────────────────────────────────╯")
	fmt.Println()
}

func nodeInit(home string, args []string) error {
	fs := flag.NewFlagSet("node init", flag.ExitOnError)
	port := fs.Int("port", 0, "agent port (62050 when 0)")
	host := fs.String("host", "", "address the panel reaches this server at (public IP when empty)")
	noCores := fs.Bool("no-cores", false, "skip downloading Xray and sing-box")
	fs.Parse(args)
	st, err := openStore(home)
	if err != nil {
		return err
	}
	defer st.Close()
	if st.AdminCount() > 0 {
		return errors.New("this server runs a panel; a node needs its own server (or another --home)")
	}
	if *port > 0 {
		st.SetSetting("node_port", strconv.Itoa(*port))
	}
	if *host != "" {
		st.SetSetting("node_host", *host)
	}
	if !*noCores {
		_, binDir, _ := paths(home)
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
	}
	tok, err := node.Init(st, filepath.Join(home, "certs"), nodeHost(st), nodePort(st), false)
	if err != nil {
		return err
	}
	if node.Paired(st) {
		fmt.Println("This node is already paired with a panel. To pair it with another one: dunvex node reset")
		return nil
	}
	printToken(tok)
	return nil
}

func nodeToken(home string, reset bool) error {
	st, err := openStore(home)
	if err != nil {
		return err
	}
	defer st.Close()
	if !isNode(st) {
		return errors.New("this server is not a node yet: dunvex node init")
	}
	if node.Paired(st) && !reset {
		fmt.Println("This node is paired with a panel. To pair it with another one: dunvex node reset")
		return nil
	}
	tok, err := node.Init(st, filepath.Join(home, "certs"), nodeHost(st), nodePort(st), reset)
	if err != nil {
		return err
	}
	if reset {
		fmt.Println("The old panel can no longer reach this node.")
	}
	printToken(tok)
	return nil
}

// nodeServe runs the cores and the agent the panel talks to.
func nodeServe(home string) error {
	st, err := openStore(home)
	if err != nil {
		return err
	}
	defer st.Close()
	if !isNode(st) {
		return errors.New("this server is not a node yet: dunvex node init")
	}
	certDir := filepath.Join(home, "certs")
	cert, _, err := node.LoadOrCreateCert(certDir, "agent", "dunvex-node")
	if err != nil {
		return err
	}
	_, binDir, runDir := paths(home)
	sys := sysinfo.NewSampler()
	env := func() protocol.Env {
		host := nodeHost(st)
		e := protocol.Env{Host: host, Listen: os.Getenv("DUNVEX_LISTEN")}
		// protocols that need TLS get a self-signed certificate; links pin it
		if c, k, err := util.SelfSignedCert(certDir, host); err == nil {
			e.CertFile, e.KeyFile, e.SelfSigned = c, k, true
			e.CertSHA256 = util.CertSHA256(c)
		}
		return e
	}
	mgr := engine.NewManager(st, binDir, runDir, env)
	agent := &node.Agent{Version: Version, St: st, Mgr: mgr, Sys: sys, Env: env}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stopMgr := make(chan struct{})
	mgrDone := make(chan struct{})
	go func() { mgr.Run(stopMgr); close(mgrDone) }()

	addr := os.Getenv("DUNVEX_LISTEN") + ":" + strconv.Itoa(nodePort(st))
	hs := &http.Server{Addr: addr, Handler: agent.Handler(), TLSConfig: agent.TLSConfig(cert),
		ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		paired := "waiting for a panel to pair (dunvex node token)"
		if node.Paired(st) {
			paired = "paired"
		}
		log.Printf("Dunvex %s node agent on %s (%s)", Version, addr, paired)
		if err := hs.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("node agent: %v", err)
			stop()
		}
	}()
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hs.Shutdown(sctx)
	close(stopMgr)
	<-mgrDone
	return nil
}
