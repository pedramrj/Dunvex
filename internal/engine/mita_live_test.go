//go:build linux

package engine

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
)

// TestMieruOnRealMita runs Mieru through the engine with a real mita and
// connects the official mieru client, which imports the user's mierus:// link
// exactly as Dunvex makes it. Needs root, DUNVEX_MITA_BIN and DUNVEX_MIERU_BIN;
// DUNVEX_MIERU_PRESET=mieru-udp tests UDP.
func TestMieruOnRealMita(t *testing.T) {
	mitaSrc, mieruBin := os.Getenv("DUNVEX_MITA_BIN"), os.Getenv("DUNVEX_MIERU_BIN")
	if mitaSrc == "" || mieruBin == "" || os.Geteuid() != 0 {
		t.Skip("needs root, DUNVEX_MITA_BIN and DUNVEX_MIERU_BIN")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("needs curl")
	}
	preset := or(os.Getenv("DUNVEX_MIERU_PRESET"), "mieru")
	raw, err := os.ReadFile(mitaSrc)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "mita"), raw, 0o755)

	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mk := func(name string) *store.User {
		u := &store.User{Name: name, UUID: util.UUID(), Password: util.RandomString(20), SubToken: util.RandomString(20), Enabled: true, ResetPeriod: "none"}
		if err := st.CreateUser(u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	sara, reza := mk("sara"), mk("reza")
	p := protocol.Get(preset)
	port := freeUDP(31890)
	if err := st.CreateInbound(&store.Inbound{Preset: p.ID, Port: port, Enabled: true, Params: protocol.Defaults(p, nil, "")}); err != nil {
		t.Fatal(err)
	}
	ins, _ := st.Inbounds()
	in := ins[0]
	env := protocol.Env{Host: "127.0.0.1"}
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return env })
	m.Xray.APIPort, m.SingBox.APIPort, m.MTG.APIPort = nextFree(41890), nextFree(41900), nextFree(41910)
	defer m.Mita.Stop()
	if err := m.Apply(); err != nil {
		t.Fatalf("apply: %v | %v", err, m.Mita.Logs())
	}
	if st := m.mitaStatus(); st != mitaRunning {
		t.Fatalf("mita status %d | %v", st, m.Mita.Logs())
	}
	t.Logf("mita %s serves %s on port %d", m.Mita.Version(), preset, port)
	started := m.Mita.StartedAt()

	// the client imports sara's link, then gets local ports and starts
	cdir := filepath.Join(dir, "client")
	os.MkdirAll(cdir, 0o700)
	cfgFile := filepath.Join(cdir, "client.json")
	cenv := append(os.Environ(), "HOME="+cdir, "MIERU_CONFIG_JSON_FILE="+cfgFile)
	// a client set up with its local ports; the profile itself comes from the link
	socks := nextFree(41920)
	b, _ := json.Marshal(map[string]any{"activeProfile": "Dunvex Mieru", "rpcPort": nextFree(41930), "socks5Port": socks})
	os.WriteFile(cfgFile, b, 0o600)
	link := protocol.Link(p, in, sara, env, "Dunvex Mieru")
	imp := exec.Command(mieruBin, "import", "config", link)
	imp.Env = cenv
	if out, err := imp.CombinedOutput(); err != nil {
		t.Fatalf("the client refused the link: %v %s\n%s", err, out, link)
	}
	var cc struct {
		Profiles []struct {
			ProfileName string `json:"profileName"`
			User        struct{ Name, Password string }
		} `json:"profiles"`
	}
	b, _ = os.ReadFile(cfgFile)
	if json.Unmarshal(b, &cc) != nil || len(cc.Profiles) != 1 || cc.Profiles[0].User.Name != "sara" || cc.Profiles[0].User.Password != sara.Password {
		t.Fatalf("imported profile:\n%s", b)
	}
	client := exec.Command(mieruBin, "run")
	client.Env = cenv
	logf, _ := os.Create(filepath.Join(dir, "client.log"))
	client.Stdout, client.Stderr = logf, logf
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { client.Process.Kill(); client.Wait() }()
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", socks)); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	clientLog := func() string { b, _ := os.ReadFile(filepath.Join(dir, "client.log")); return lastLines(string(b), 8) }

	trace := func() (string, error) {
		out, err := exec.Command("curl", "-s", "-m", "10", "--socks5-hostname", fmt.Sprintf("127.0.0.1:%d", socks), "https://1.1.1.1/cdn-cgi/trace").CombinedOutput()
		for _, l := range strings.Split(string(out), "\n") {
			if v, ok := strings.CutPrefix(l, "ip="); ok {
				return v, nil
			}
		}
		if err == nil {
			err = fmt.Errorf("no ip in %q", out)
		}
		return "", err
	}
	exit, err := trace()
	if exit == "" {
		t.Fatalf("no internet through Mieru: %v\nclient: %s\nserver: %v", err, clientLog(), m.Mita.Logs())
	}
	t.Logf("traffic leaves as %s", exit)

	// usage per user and the online state come from mita's counters
	m.collect()
	u, _ := st.UserByName("sara")
	if u.UsedUp == 0 || u.UsedDown == 0 {
		t.Errorf("usage not counted: up %d down %d", u.UsedUp, u.UsedDown)
	}
	if !m.OnlineUsers(60)[u.ID] {
		t.Error("sara is not online")
	}
	t.Logf("counted up %d B, down %d B", u.UsedUp, u.UsedDown)

	set := func(u *store.User, on bool) {
		t.Helper()
		u, _ = st.UserByName(u.Name)
		u.Enabled = on
		st.UpdateUser(u)
		if err := m.Apply(); err != nil {
			t.Fatalf("apply: %v\n%s", err, strings.Join(m.Mita.Logs(), "\n"))
		}
	}
	// internet comes back within a few tries: the client reconnects on its own
	works := func(what string) {
		t.Helper()
		var exit string
		var err error
		for i := 0; i < 6 && exit == ""; i++ {
			if exit, err = trace(); exit == "" {
				time.Sleep(3 * time.Second)
			}
		}
		if exit == "" {
			t.Fatalf("%s: no internet: %v\nclient: %s\nserver: %s", what, err, clientLog(), lastLines(strings.Join(m.Mita.Logs(), "\n"), 8))
		}
	}

	// another user switched off and on: reloads, no restart, sara keeps working
	set(reza, false)
	works("reza switched off")
	set(reza, true)
	if !m.Mita.StartedAt().Equal(started) {
		t.Error("mita was restarted for a change of users")
	}

	// sara switched off while reza stays: new connections are refused at
	// once; one she still has open is cut at the next look at the counters
	set(sara, false)
	cut := false
	for i := 0; i < 3; i++ {
		open, _ := trace()
		m.collect() // what the engine does every few seconds
		if open == "" && !cut {
			cut = true
			continue
		}
		if open == "" {
			break
		}
		t.Logf("look %d: sara still had an open connection", i+1)
	}
	if exit, _ := trace(); exit != "" {
		t.Fatalf("sara still has internet after being switched off (%s)", exit)
	}
	t.Logf("sara cut off; mita started again for it: %v", !m.Mita.StartedAt().Equal(started))
	set(sara, true)
	works("sara switched on again")

	// nobody left: mita stops (it does not serve without users) and starts
	// again with the first user back
	set(reza, false)
	set(sara, false)
	if m.Mita.Running() {
		t.Error("mita runs with no users")
	}
	if exit, _ := trace(); exit != "" {
		t.Errorf("internet with nobody allowed (%s)", exit)
	}
	set(sara, true)
	if err := m.mitaServing(); err != nil {
		t.Fatal(err)
	}
	works("sara back after everyone was off")

	// a restart of mita must not count anything twice
	m.collect()
	before, _ := st.UserByName("sara")
	if err := m.Mita.Restart(); err != nil {
		t.Fatal(err)
	}
	if err := m.mitaServing(); err != nil {
		t.Fatal(err)
	}
	m.collect()
	after, _ := st.UserByName("sara")
	if after.UsedUp+after.UsedDown != before.UsedUp+before.UsedDown {
		t.Errorf("usage changed across a restart with no traffic: %d → %d", before.UsedUp+before.UsedDown, after.UsedUp+after.UsedDown)
	}
}
