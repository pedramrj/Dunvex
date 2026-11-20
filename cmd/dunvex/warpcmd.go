package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/coremgr"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/routing"
	"github.com/pedramrj/dunvex/internal/warp"
)

const warpUsage = `usage: dunvex warp [status|on|check|license <key>|off]

  status          the WARP device and what goes through it (default)
  on              register a free Cloudflare WARP device for this server
  check           the address the internet sees through WARP
  license <key>   add a WARP+ key from the 1.1.1.1 app
  off             stop using WARP and remove the device from Cloudflare

Choose what goes through WARP in the panel: Routing.`

// warpCmd manages the server's Cloudflare WARP device from SSH.
func warpCmd(home string, args []string) error {
	what := "status"
	if len(args) > 0 {
		what = args[0]
	}
	st, err := openStore(home)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := &warp.Client{}
	a := routing.Account(st)
	restart := func() { fmt.Println("Restart the service to apply it: systemctl restart dunvex") }
	switch what {
	case "status":
		if a == nil {
			fmt.Println("WARP is off. Turn it on with: dunvex warp on")
			return nil
		}
		set := routing.Load(st)
		via := "WireGuard (" + a.Endpoint + "; run dunvex warp on to move it to MASQUE)"
		if a.Masque() {
			via = "MASQUE (" + a.MasqueV4 + ":443)"
		}
		fmt.Printf("WARP device: %s (%s), address %s, %s, since %s\n", a.ID, a.Type, a.AddressV4, via,
			time.Unix(a.Created, 0).Format("2006-01-02"))
		sends := map[string]string{routing.WarpOff: "nothing", routing.WarpAll: "everything",
			routing.WarpSites: "sites: " + strings.Join(append(append([]string{}, set.WarpGroups...), set.WarpDomains...), ", ")}[set.Warp]
		fmt.Printf("Through WARP: %s; Iranian sites: %s\n", sends, set.Iran)
	case "on":
		switch {
		case a == nil:
			if a, err = c.Register(ctx); err != nil {
				return err
			}
			st.Log("settings", "WARP turned on from SSH (%s)", a.AddressV4)
			fmt.Printf("✓ WARP device registered: %s (%s)\n", a.AddressV4, a.Type)
		case !a.Masque():
			if err := c.EnrollMasque(ctx, a); err != nil {
				return err
			}
			st.Log("settings", "WARP moved to MASQUE from SSH")
			fmt.Println("✓ WARP moved to MASQUE (HTTP/3 on port 443)")
		default:
			fmt.Printf("WARP is already on (%s, %s, MASQUE)\n", a.AddressV4, a.Type)
		}
		if err := routing.SaveAccount(st, a); err != nil {
			return err
		}
		_, bin, _ := paths(home)
		if _, err := os.Stat(filepath.Join(bin, "usque")); err != nil {
			fmt.Println("installing usque (WARP over MASQUE)…")
			v, err := coremgr.InstallUsque(bin)
			if err != nil {
				return err
			}
			fmt.Println("✓ usque", v)
		}
		fmt.Println("Choose what goes through it in the panel: Routing.")
		restart()
	case "check":
		if a == nil {
			return errors.New("WARP is off; turn it on with: dunvex warp on")
		}
		via, err := warp.Trace(ctx, "127.0.0.1:"+strconv.Itoa(engine.WarpTestPort))
		if err != nil {
			return fmt.Errorf("WARP did not answer: %w (if it was just turned on, restart the service)", err)
		}
		fmt.Printf("Through WARP: %s (warp=%s, Cloudflare %s, %s)\n", via.IP, via.Warp, via.Colo, via.Loc)
		if direct, err := warp.Trace(ctx, ""); err == nil {
			fmt.Printf("Directly:     %s\n", direct.IP)
		}
	case "license":
		if a == nil {
			return errors.New("WARP is off; turn it on with: dunvex warp on")
		}
		if len(args) < 2 {
			return errors.New("usage: dunvex warp license <key>")
		}
		if err := c.SetLicense(ctx, a, args[1]); err != nil {
			return err
		}
		routing.SaveAccount(st, a)
		fmt.Printf("✓ key added: the device is now %s\n", a.Type)
	case "off":
		set := routing.Load(st)
		if set.UsesWarp() {
			set.Warp = routing.WarpOff
			if set.Iran == routing.IranWarp {
				set.Iran = routing.IranDirect
			}
			if _, err := routing.Save(st, set); err != nil {
				return err
			}
		}
		if a != nil {
			c.Delete(ctx, a) // best effort: the device is forgotten either way
		}
		routing.SaveAccount(st, nil)
		st.Log("settings", "WARP turned off from SSH")
		fmt.Println("✓ WARP is off")
		restart()
	default:
		fmt.Println(warpUsage)
		return errors.New("unknown warp command")
	}
	return nil
}
