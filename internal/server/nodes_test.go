package server

import (
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/node"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
)

func TestPanelWithNode(t *testing.T) {
	// the node: its own database, engine (no cores here) and agent behind mutual TLS
	ndir := t.TempDir()
	nst, err := store.Open(filepath.Join(ndir, "node.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer nst.Close()
	nenv := protocol.Env{Host: "127.0.0.1"}
	agent := &node.Agent{Version: "test", St: nst, Sys: sysinfo.NewSampler(), Env: func() protocol.Env { return nenv }}
	agent.Mgr = engine.NewManager(nst, filepath.Join(ndir, "bin"), filepath.Join(ndir, "run"), agent.Env)
	ncert, _, _ := node.LoadOrCreateCert(filepath.Join(ndir, "certs"), "agent", "dunvex-node")
	nts := httptest.NewUnstartedServer(agent.Handler())
	nts.TLS = agent.TLSConfig(ncert)
	nts.StartTLS()
	defer nts.Close()
	nport := nts.Listener.Addr().(*net.TCPAddr).Port
	tok, err := node.Init(nst, filepath.Join(ndir, "certs"), "127.0.0.1", nport, false)
	if err != nil {
		t.Fatal(err)
	}

	// the panel
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("owner-pass-1")
	st.UpsertAdmin("boss", hash)
	st.SetSetting("panel_port", "443")
	srv := &Server{Home: dir, Version: "test", St: st, Sys: sysinfo.NewSampler()}
	srv.Mgr = engine.NewManager(st, dir, filepath.Join(dir, "run"), func() protocol.Env { return srv.Env() })
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	owner := newClient(t, ts.URL)
	if code, _ := owner.do("POST", "/login", map[string]string{"username": "boss", "password": "owner-pass-1"}); code != 200 {
		t.Fatal("login")
	}

	// add the server with its token: it pairs at once
	if code, out := owner.do("POST", "/nodes", map[string]any{"name": "Frankfurt", "token": "not a token"}); code != 400 {
		t.Fatalf("bad token: %d %v", code, out)
	}
	code, out := owner.do("POST", "/nodes", map[string]any{"name": "Frankfurt", "token": tok.String()})
	if code != 200 || out["paired"] != true || out["online"] != true {
		t.Fatalf("add node: %d %v", code, out)
	}
	nodeID := int64(out["id"].(float64))
	if !node.Paired(nst) {
		t.Fatal("the node should be paired")
	}
	if code, _ := owner.do("POST", "/nodes", map[string]any{"name": "Again", "token": tok.String()}); code != 400 {
		t.Fatal("the same node twice must be refused")
	}

	// protocols: 443 on this server and on the node do not clash; the agent's port is reserved
	if code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "vless-reality-vision", "port": 443, "nodeId": nodeID}); code != 200 {
		t.Fatalf("node inbound: %d %v", code, out)
	}
	if code, _ := owner.do("POST", "/inbounds", map[string]any{"preset": "vless-reality-vision", "port": nport, "nodeId": nodeID}); code != 400 {
		t.Fatal("the agent port must be refused")
	}
	if code, _ := owner.do("POST", "/inbounds", map[string]any{"preset": "vless-ws-tls", "nodeId": nodeID}); code != 400 {
		t.Fatal("a protocol that needs a domain must be refused on a node")
	}
	if code, out := owner.do("POST", "/inbounds", map[string]any{"preset": "hysteria2", "port": 4444}); code != 200 {
		t.Fatalf("local inbound: %d %v", code, out)
	}
	owner.do("POST", "/users", map[string]any{"name": "sara"})
	owner.do("POST", "/users", map[string]any{"name": "limited", "dataLimit": 1000})

	h := srv.hub()
	n, _ := st.NodeByID(nodeID)
	h.sync(n)
	nins, _ := nst.Inbounds()
	nusers, _ := nst.Users()
	if len(nins) != 1 || nins[0].Port != 443 || len(nusers) != 2 {
		t.Fatalf("node state: %d inbounds, %d users", len(nins), len(nusers))
	}
	// the local engine leaves the node's inbound alone
	x, _, _, _, _ := srv.Mgr.Configs()
	if strings.Contains(string(x), `"port": 443`) {
		t.Fatal("the node's inbound must not run here")
	}

	// traffic on the node reaches the panel once, however often we sync
	ids := map[string]int64{}
	for _, u := range nusers {
		ids[u.Name] = u.ID
	}
	nst.AddTraffic(time.Now(), map[int64]store.Traffic{ids["sara"]: {Up: 300, Down: 3000}, ids["limited"]: {Up: 600, Down: 600}}, map[int64]store.Traffic{nins[0].ID: {Up: 900, Down: 3600}})
	for i := 0; i < 4; i++ {
		n, _ = st.NodeByID(nodeID)
		h.sync(n)
	}
	sara, _ := st.UserByName("sara")
	if sara.UsedUp != 300 || sara.UsedDown != 3000 {
		t.Fatalf("sara counted %d/%d, want 300/3000", sara.UsedUp, sara.UsedDown)
	}
	if !srv.Mgr.OnlineUsers(60)[sara.ID] {
		t.Fatal("traffic on a node makes the user online")
	}
	in, _ := st.InboundByID(nins[0].ID)
	if in.Up != 900 || in.Down != 3600 {
		t.Fatalf("inbound counted %d/%d", in.Up, in.Down)
	}

	// the user over the limit leaves the node at the next round
	n, _ = st.NodeByID(nodeID)
	h.sync(n)
	nusers, _ = nst.Users()
	if len(nusers) != 1 || nusers[0].Name != "sara" {
		t.Fatalf("only sara may stay: %+v", nusers)
	}

	// links of the node's protocol use the node's address and carry its name
	cfgs := srv.configsFor(sara)
	found := false
	for _, c := range cfgs {
		if c.InboundID == nins[0].ID {
			found = true
			if !strings.Contains(c.Link, "@127.0.0.1:443") || !strings.Contains(c.Name, "Frankfurt") {
				t.Fatalf("node link: %s / %s", c.Link, c.Name)
			}
		}
	}
	if !found {
		t.Fatal("sara should get the node's config")
	}

	// the list shows the node online with its protocol
	_, out = owner.do("GET", "/nodes", nil)
	list := out["list"].([]any)
	nv := list[0].(map[string]any)
	if nv["online"] != true || nv["inbounds"].(float64) != 1 {
		t.Fatalf("node view: %v", nv)
	}

	// removing the server frees the node and drops its protocols
	if code, _ := owner.do("DELETE", "/nodes/"+jsonID(float64(nodeID)), nil); code != 200 {
		t.Fatal("delete node")
	}
	if node.Paired(nst) {
		t.Fatal("the node should be released")
	}
	if left, _ := nst.Inbounds(); len(left) != 0 {
		t.Fatal("a released node serves nothing")
	}
	all, _ := st.Inbounds()
	for _, in := range all {
		if in.NodeID == nodeID {
			t.Fatal("the node's inbounds go with it")
		}
	}
}
