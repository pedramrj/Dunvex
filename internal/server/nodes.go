package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/node"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// nodeHub keeps every other server in step with the panel: it pushes each one
// its protocols and the users who may connect, pulls traffic and devices, and
// remembers how each server is doing.
type nodeHub struct {
	s    *Server
	kick chan struct{}

	mu      sync.Mutex
	cert    *tls.Certificate
	clients map[int64]*node.Client
	live    map[int64]*nodeLive
}

// nodeLive is what the panel knows about a node right now (not stored).
type nodeLive struct {
	Status     *node.Status `json:"status"`
	Error      string       `json:"error"`
	LastOK     int64        `json:"lastOk"`
	LastTry    int64        `json:"lastTry"`
	Fails      int          `json:"-"`
	RateUp     int64        `json:"rateUp"` // bytes/s through the node's protocols
	RateDown   int64        `json:"rateDown"`
	pushedHash string
	pushedAt   int64
	ack        int64
	statsAt    int64
}

const nodeOffline = 3 // failed rounds before a node counts as offline

func (s *Server) hub() *nodeHub {
	s.nodeOnce.Do(func() {
		s.nodes = &nodeHub{s: s, kick: make(chan struct{}, 1), clients: map[int64]*node.Client{}, live: map[int64]*nodeLive{}}
	})
	return s.nodes
}

// KickNodes asks for a sync round soon (after any change).
func (s *Server) KickNodes() {
	select {
	case s.hub().kick <- struct{}{}:
	default:
	}
}

// RunNodes syncs every server every 10 seconds, and right after changes.
func (s *Server) RunNodes(stop <-chan struct{}) {
	h := s.hub()
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		h.round()
		select {
		case <-stop:
			return
		case <-t.C:
		case <-h.kick:
			time.Sleep(500 * time.Millisecond) // let a burst of edits settle
		}
	}
}

func (h *nodeHub) panelCert() (tls.Certificate, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cert != nil {
		return *h.cert, nil
	}
	c, _, err := node.LoadOrCreateCert(filepath.Join(h.s.Home, "certs"), "panel-client", "dunvex-panel")
	if err != nil {
		return c, err
	}
	h.cert = &c
	return c, nil
}

func (h *nodeHub) client(n *store.Node) (*node.Client, error) {
	cert, err := h.panelCert()
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(n.Address, strconv.Itoa(n.AgentPort))
	h.mu.Lock()
	defer h.mu.Unlock()
	if c := h.clients[n.ID]; c != nil && c.Addr == addr && c.Fingerprint == n.Fingerprint {
		return c, nil
	}
	c := node.NewClient(addr, n.Fingerprint, cert)
	h.clients[n.ID] = c
	return c, nil
}

func (h *nodeHub) state(id int64) *nodeLive {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.live[id]
	if l == nil {
		l = &nodeLive{}
		h.live[id] = l
	}
	return l
}

// snapshot copies the live state for the API.
func (h *nodeHub) snapshot(id int64) nodeLive {
	h.mu.Lock()
	defer h.mu.Unlock()
	if l := h.live[id]; l != nil {
		return *l
	}
	return nodeLive{}
}

func (h *nodeHub) round() {
	list, err := h.s.St.Nodes()
	if err != nil {
		return
	}
	var wg sync.WaitGroup
	for _, n := range list {
		if !n.Enabled {
			continue
		}
		wg.Add(1)
		go func(n *store.Node) {
			defer wg.Done()
			h.sync(n)
		}(n)
	}
	wg.Wait()
}

// desired is everything node n should serve: its protocols, and the users who
// may connect and may use at least one of them.
func (s *Server) desired(n *store.Node) node.State {
	st := node.State{Inbounds: []*store.Inbound{}, Users: []node.User{}, Settings: map[string]string{
		"block_torrent": s.St.GetSetting("block_torrent", "true"),
	}}
	inbounds, _ := s.St.Inbounds()
	for _, in := range inbounds {
		if in.NodeID == n.ID {
			st.Inbounds = append(st.Inbounds, in)
		}
	}
	users, _ := s.St.Users()
	now := time.Now().Unix()
	for _, u := range users {
		if !u.CanConnect(now) {
			continue
		}
		uses := false
		for _, in := range st.Inbounds {
			if in.Enabled && u.AllowsInbound(in.ID) {
				uses = true
				break
			}
		}
		if uses {
			st.Users = append(st.Users, node.User{Name: u.Name, UUID: u.UUID, Password: u.Password, InboundIDs: u.InboundIDs,
				WGPrivate: u.WGPrivate, WGPublic: u.WGPublic, WGIndex: u.WGIndex})
		}
	}
	return st
}

func stateHash(st node.State) string {
	// counters change all the time and say nothing about what to serve
	clean := node.State{Users: st.Users, Settings: st.Settings}
	for _, in := range st.Inbounds {
		c := *in
		c.Up, c.Down = 0, 0
		clean.Inbounds = append(clean.Inbounds, &c)
	}
	b, _ := json.Marshal(clean)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sync runs one round with one node: pair, push, status, traffic.
func (h *nodeHub) sync(n *store.Node) {
	s := h.s
	l := h.state(n.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	now := time.Now().Unix()
	fail := func(err error) {
		h.mu.Lock()
		l.LastTry, l.Error = now, err.Error()
		l.Fails++
		fails := l.Fails
		h.mu.Unlock()
		if fails == nodeOffline {
			s.St.Log("core", "server %s is not reachable: %v", n.Name, err)
		}
	}
	c, err := h.client(n)
	if err != nil {
		fail(err)
		return
	}
	if !n.Paired() {
		if err := c.Pair(ctx, n.Secret); err != nil {
			fail(fmt.Errorf("pairing failed: %w", err))
			return
		}
		n.Secret = ""
		s.St.UpdateNode(n)
		s.St.Log("node", "server %s was added and paired", n.Name)
	}

	want := s.desired(n)
	hash := stateHash(want)
	var status node.Status
	h.mu.Lock()
	push := hash != l.pushedHash || now-l.pushedAt > 300
	h.mu.Unlock()
	if push {
		status, err = c.Push(ctx, want)
		if err == nil {
			h.mu.Lock()
			l.pushedHash, l.pushedAt = hash, now
			h.mu.Unlock()
		}
	} else {
		status, err = c.Status(ctx)
	}
	if err != nil {
		fail(err)
		return
	}

	h.mu.Lock()
	ack := l.ack
	h.mu.Unlock()
	b, err := c.Stats(ctx, ack)
	if err != nil {
		fail(err)
		return
	}
	if b.Epoch != "" && b.Epoch != n.Epoch {
		// a new install of the node: its batch numbers start again
		s.St.SetNodeEpoch(n.ID, b.Epoch)
		n.Epoch = b.Epoch
	}
	if _, err := s.Mgr.AddRemote(n.ID, b.Seq, b.Users, b.Inbounds, b.Devices); err != nil {
		fail(err)
		return
	}
	var up, down int64
	for _, t := range b.Inbounds {
		up, down = up+t.Up, down+t.Down
	}

	if status.CertSHA256 != n.CertSHA256 {
		n.CertSHA256 = status.CertSHA256 // links pin the node's own certificate
	}
	n.LastSeen = now
	s.St.UpdateNode(n)

	h.mu.Lock()
	back := l.Fails >= nodeOffline
	if l.statsAt > 0 && now > l.statsAt {
		l.RateUp, l.RateDown = up/(now-l.statsAt), down/(now-l.statsAt)
	}
	l.statsAt, l.ack = now, b.Seq
	l.Status, l.Error, l.LastOK, l.LastTry, l.Fails = &status, "", now, now, 0
	h.mu.Unlock()
	if back {
		s.St.Log("node", "server %s is reachable again", n.Name)
	}
}

// nodeError explains why a node's inbound does not run, or "".
func (s *Server) nodeError(in *store.Inbound, nodes map[int64]*store.Node) string {
	n := nodes[in.NodeID]
	if n == nil {
		return "this server was removed"
	}
	if !n.Enabled {
		return fmt.Sprintf("server %s is turned off", n.Name)
	}
	l := s.hub().snapshot(n.ID)
	switch {
	case !n.Paired() && l.Error != "":
		return fmt.Sprintf("server %s is not paired yet: %s", n.Name, l.Error)
	case l.Fails >= nodeOffline:
		return fmt.Sprintf("server %s is not reachable: %s", n.Name, l.Error)
	case l.Status != nil:
		if e := l.Status.InboundErrors[in.ID]; e != "" {
			return e
		}
		// a core that cannot start takes every protocol on it down
		if p := protocol.Get(in.Preset); p != nil {
			core := "xray"
			if p.Engine == "singbox" {
				core = "sing-box"
			}
			if e := l.Status.CoreErrors[core]; e != "" {
				return fmt.Sprintf("%s on %s: %s", core, n.Name, e)
			}
		}
	}
	return ""
}

func (s *Server) nodeMap() map[int64]*store.Node {
	out := map[int64]*store.Node{}
	list, _ := s.St.Nodes()
	for _, n := range list {
		out[n.ID] = n
	}
	return out
}

// envFor is the link environment of an inbound: its own server's address, and
// for a node the node's certificate (self-signed, pinned in links).
func (s *Server) envFor(in *store.Inbound, local protocol.Env, nodes map[int64]*store.Node) protocol.Env {
	if in.NodeID == 0 {
		return local
	}
	n := nodes[in.NodeID]
	if n == nil {
		return local
	}
	return protocol.Env{Host: n.Address, SelfSigned: true, CertSHA256: n.CertSHA256}
}

// remarkFor names a config in client apps; a node's configs carry the server name.
func remarkFor(in *store.Inbound, p *protocol.Preset, u *store.User, title string, nodes map[int64]*store.Node) string {
	if n := nodes[in.NodeID]; in.NodeID != 0 && n != nil {
		r := in.Remark
		if r == "" {
			r = p.Name
		}
		if title != "" {
			return title + " · " + n.Name + " · " + r
		}
		return n.Name + " · " + r
	}
	return protocol.Remark(in, p, u, title)
}

// ---------------------------------------------------------------- API

type nodeView struct {
	*store.Node
	Paired   bool         `json:"paired"`
	Online   bool         `json:"online"`
	Error    string       `json:"error"`
	LastTry  int64        `json:"lastTry"`
	Status   *node.Status `json:"status"`
	Inbounds int          `json:"inbounds"`
	RateUp   int64        `json:"rateUp"`
	RateDown int64        `json:"rateDown"`
}

func (s *Server) viewNode(n *store.Node, inbounds []*store.Inbound) nodeView {
	l := s.hub().snapshot(n.ID)
	v := nodeView{Node: n, Paired: n.Paired(), Error: l.Error, LastTry: l.LastTry, Status: l.Status, RateUp: l.RateUp, RateDown: l.RateDown}
	v.Online = n.Enabled && l.Status != nil && l.Fails < nodeOffline && time.Now().Unix()-l.LastOK < 60
	for _, in := range inbounds {
		if in.NodeID == n.ID {
			v.Inbounds++
		}
	}
	return v
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	list, err := s.St.Nodes()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	inbounds, _ := s.St.Inbounds()
	out := []nodeView{}
	for _, n := range list {
		out = append(out, s.viewNode(n, inbounds))
	}
	writeJSON(w, 200, out)
}

func (s *Server) createNode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Token string `json:"token"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 32 {
		fail(w, 400, "give the server a name of up to 32 characters")
		return
	}
	tok, err := node.ParseToken(in.Token)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	host, port, _ := net.SplitHostPort(tok.Addr)
	p, _ := strconv.Atoi(port)
	list, _ := s.St.Nodes()
	for _, n := range list {
		if n.Fingerprint == tok.Fingerprint {
			fail(w, 400, "this server is already added as "+n.Name)
			return
		}
	}
	n := &store.Node{Name: in.Name, Address: host, AgentPort: p, Fingerprint: tok.Fingerprint, Secret: tok.Secret, Enabled: true}
	if err := s.St.CreateNode(n); err != nil {
		fail(w, 400, err.Error())
		return
	}
	a := adminOf(r)
	s.St.Log("node", "%s added the server %s (%s)", a.Username, n.Name, n.Address)
	s.hub().sync(n) // pair now, so the answer says whether it worked
	inbounds, _ := s.St.Inbounds()
	if fresh, err := s.St.NodeByID(n.ID); err == nil {
		n = fresh
	}
	writeJSON(w, 200, s.viewNode(n, inbounds))
}

func (s *Server) updateNode(w http.ResponseWriter, r *http.Request) {
	n, err := s.St.NodeByID(pathID(r))
	if err != nil {
		fail(w, 404, "server not found")
		return
	}
	var in struct {
		Name    *string `json:"name"`
		Address *string `json:"address"`
		Enabled *bool   `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil {
		fail(w, 400, "bad request")
		return
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len([]rune(name)) > 32 {
			fail(w, 400, "give the server a name of up to 32 characters")
			return
		}
		n.Name = name
	}
	if in.Address != nil {
		addr := strings.TrimSpace(*in.Address)
		if addr == "" || strings.ContainsAny(addr, " /:") && net.ParseIP(addr) == nil {
			fail(w, 400, "the address is a host name or an IP, without a port")
			return
		}
		n.Address = addr
	}
	if in.Enabled != nil {
		n.Enabled = *in.Enabled
	}
	if err := s.St.UpdateNode(n); err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.KickNodes()
	inbounds, _ := s.St.Inbounds()
	writeJSON(w, 200, s.viewNode(n, inbounds))
}

func (s *Server) deleteNode(w http.ResponseWriter, r *http.Request) {
	n, err := s.St.NodeByID(pathID(r))
	if err != nil {
		fail(w, 404, "server not found")
		return
	}
	// tell the node it is free again (best effort: it may be gone already)
	if c, err := s.hub().client(n); err == nil && n.Paired() {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		if err := c.Release(ctx); err != nil {
			log.Printf("node %s: release: %v", n.Name, err)
		}
		cancel()
	}
	if err := s.St.DeleteNode(n.ID); err != nil {
		fail(w, 500, err.Error())
		return
	}
	h := s.hub()
	h.mu.Lock()
	delete(h.live, n.ID)
	delete(h.clients, n.ID)
	h.mu.Unlock()
	s.St.Log("node", "%s removed the server %s and its protocols", adminOf(r).Username, n.Name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) syncNodeNow(w http.ResponseWriter, r *http.Request) {
	n, err := s.St.NodeByID(pathID(r))
	if err != nil {
		fail(w, 404, "server not found")
		return
	}
	s.hub().sync(n)
	if fresh, err := s.St.NodeByID(n.ID); err == nil {
		n = fresh
	}
	inbounds, _ := s.St.Inbounds()
	writeJSON(w, 200, s.viewNode(n, inbounds))
}
