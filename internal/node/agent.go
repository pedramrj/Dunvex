package node

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/coremgr"
	"github.com/pedramrj/dunvex/internal/engine"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/relay"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/sysinfo"
	"github.com/pedramrj/dunvex/internal/util"
)

// settings a node keeps in its own database
const (
	keySecret   = "node_secret"   // one-time pairing secret ("" once paired)
	keyPanel    = "node_panel_fp" // pinned client certificate of the panel
	keyEpoch    = "node_epoch"    // random id of this install
	keySeq      = "node_seq"      // last batch number handed out
	keyReported = "node_reported" // counters the panel has acknowledged
	keyPending  = "node_pending"  // batch waiting for its acknowledgement
)

// Agent is the node side.
type Agent struct {
	Version string
	St      *store.Store
	Mgr     *engine.Manager
	Sys     *sysinfo.Sampler
	Env     func() protocol.Env
	Relay   *relay.Relay // forwards to another server when this node is a relay

	mu    sync.Mutex // one state push or stats batch at a time
	fails int
}

type counters struct {
	Users    map[string]store.Traffic `json:"users"`
	Inbounds map[int64]store.Traffic  `json:"inbounds"`
}

type pending struct {
	Seq  int64    `json:"seq"`
	Snap counters `json:"snap"`
}

// Init prepares a node's settings and returns its pairing token. A node that
// is already paired keeps its pairing; reset makes it accept a new panel.
func Init(st *store.Store, certDir, host string, port int, reset bool) (Token, error) {
	_, fp, err := LoadOrCreateCert(certDir, "agent", "dunvex-node")
	if err != nil {
		return Token{}, err
	}
	if st.GetSetting(keyEpoch, "") == "" {
		st.SetSetting(keyEpoch, util.RandomString(16))
	}
	if reset {
		st.SetSetting(keyPanel, "")
		st.SetSetting(keySecret, "")
	}
	if st.GetSetting(keyPanel, "") == "" && st.GetSetting(keySecret, "") == "" {
		st.SetSetting(keySecret, util.RandomString(32))
	}
	st.SetSetting("node_port", strconv.Itoa(port))
	return Token{Addr: hostPort(host, port), Fingerprint: fp, Secret: st.GetSetting(keySecret, "")}, nil
}

// Paired reports whether a panel has taken this node.
func Paired(st *store.Store) bool { return st.GetSetting(keyPanel, "") != "" }

// TLSConfig is the agent's server side: its own certificate, and the panel's
// client certificate required (pinned once paired).
func (a *Agent) TLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAnyClientCert,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("a client certificate is required")
			}
			if pinned := a.St.GetSetting(keyPanel, ""); pinned != "" && Fingerprint(raw[0]) != pinned {
				return errors.New("this node belongs to another panel")
			}
			return nil
		},
	}
}

// Handler serves the agent API.
func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pair", a.pair)
	mux.HandleFunc("GET /status", a.paired(a.status))
	mux.HandleFunc("PUT /state", a.paired(a.state))
	mux.HandleFunc("POST /stats", a.paired(a.stats))
	mux.HandleFunc("POST /release", a.paired(a.release))
	return mux
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func clientFP(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	return Fingerprint(r.TLS.PeerCertificates[0].Raw)
}

// paired lets only the pinned panel through.
func (a *Agent) paired(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pinned := a.St.GetSetting(keyPanel, "")
		if pinned == "" || clientFP(r) != pinned {
			reply(w, http.StatusForbidden, map[string]string{"error": "not paired with this panel"})
			return
		}
		h(w, r)
	}
}

func (a *Agent) pair(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fp := clientFP(r)
	if fp == "" {
		reply(w, http.StatusForbidden, map[string]string{"error": "a client certificate is required"})
		return
	}
	if pinned := a.St.GetSetting(keyPanel, ""); pinned != "" {
		if pinned == fp {
			reply(w, http.StatusOK, map[string]bool{"ok": true}) // asked again: already ours
			return
		}
		reply(w, http.StatusConflict, map[string]string{"error": "this node already belongs to another panel"})
		return
	}
	secret := a.St.GetSetting(keySecret, "")
	got := r.Header.Get("X-Dunvex-Secret")
	if secret == "" || subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		a.fails++
		time.Sleep(time.Duration(min(a.fails, 10)) * 300 * time.Millisecond) // slow down guessing
		reply(w, http.StatusForbidden, map[string]string{"error": "wrong pairing secret"})
		return
	}
	a.fails = 0
	a.St.SetSetting(keyPanel, fp)
	a.St.SetSetting(keySecret, "")
	log.Printf("node: paired with panel %s…", fp[:16])
	a.St.Log("auth", "this node was paired with a panel (%s…)", fp[:16])
	reply(w, http.StatusOK, map[string]bool{"ok": true})
}

// release: the panel removed this node. It stops serving, forgets the panel and
// makes a new pairing secret (`dunvex node token` shows the new token).
func (a *Agent) release(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.St.ReplaceInbounds(nil)
	a.St.SyncUsers(nil)
	a.Mgr.Apply()
	a.St.SetSetting(keyPanel, "")
	a.St.SetSetting(keySecret, util.RandomString(32))
	a.St.SetSetting(keyPending, "")
	log.Printf("node: released by its panel; waiting for a new pairing")
	reply(w, http.StatusOK, map[string]bool{"ok": true})
}

// Status reports the node's health.
func (a *Agent) Status() Status {
	snap := a.Sys.Get()
	ierr, cerr := a.Mgr.Errors()
	users, _ := a.St.Users()
	inbounds, _ := a.St.Inbounds()
	env := a.Env()
	st := Status{
		Version: a.Version, Epoch: a.St.GetSetting(keyEpoch, ""), Hostname: snap.Hostname, PublicIP: a.Sys.PublicIP(),
		CertSHA256: env.CertSHA256, Cores: a.Mgr.CoreStatuses(),
		CoreErrors: cerr, InboundErrors: ierr, CPU: snap.CPU, MemUsed: snap.MemUsed, MemTotal: snap.MemTotal,
		DiskUsed: snap.DiskUsed, DiskTotal: snap.DiskTotal, Uptime: snap.Uptime, NetUp: snap.NetUp, NetDown: snap.NetDown,
		Users: len(users), Inbounds: len(inbounds), Time: time.Now().Unix(),
	}
	if a.Relay != nil {
		st.Forwards = a.Relay.Stats()
	}
	if st.CoreErrors == nil {
		st.CoreErrors = map[string]string{}
	}
	if st.InboundErrors == nil {
		st.InboundErrors = map[int64]string{}
	}
	return st
}

func (a *Agent) status(w http.ResponseWriter, r *http.Request) { reply(w, http.StatusOK, a.Status()) }

// state replaces what the node serves and applies it at once.
func (a *Agent) state(w http.ResponseWriter, r *http.Request) {
	var s State
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<20)).Decode(&s); err != nil {
		reply(w, http.StatusBadRequest, map[string]string{"error": "bad state"})
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.Apply(s); err != nil {
		reply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	reply(w, http.StatusOK, a.Status())
}

// Apply stores a state and reloads the cores.
func (a *Agent) Apply(s State) error {
	for _, in := range s.Inbounds {
		in.NodeID = 0 // on the node itself every inbound is local
		p := protocol.Get(in.Preset)
		if p == nil {
			return fmt.Errorf("this node does not know the protocol %q; update it", in.Preset)
		}
		// mtg and amneziawg-go arrive with the first inbound that needs them
		if p.Engine == "mtg" && in.Enabled && !a.Mgr.MTG.Installed() {
			if _, err := coremgr.InstallMTG(a.Mgr.MTG.BinDir); err != nil {
				log.Printf("node: mtg: %v", err) // the status reports it as a core error
			}
		}
		if p.Engine == "trusttunnel" && in.Enabled && !a.Mgr.TT.Installed() {
			if _, err := coremgr.InstallTrustTunnel(a.Mgr.TT.BinDir); err != nil {
				log.Printf("node: trusttunnel: %v", err)
			}
		}
		if p.Engine == "mita" && in.Enabled && !a.Mgr.Mita.Installed() {
			if _, err := coremgr.InstallMita(a.Mgr.Mita.BinDir); err != nil {
				log.Printf("node: mita: %v", err)
			}
		}
		if p.Engine == "awg" && in.Enabled && !a.Mgr.AWG.Installed() {
			if _, err := coremgr.InstallAWG(a.Mgr.AWG.BinDir); err != nil {
				log.Printf("node: amneziawg-go: %v", err)
			}
		}
	}
	users := make([]*store.User, 0, len(s.Users))
	for _, u := range s.Users {
		users = append(users, &store.User{Name: u.Name, UUID: u.UUID, Password: u.Password, InboundIDs: u.InboundIDs,
			WGPrivate: u.WGPrivate, WGPublic: u.WGPublic, WGIndex: u.WGIndex})
	}
	if err := a.St.ReplaceInbounds(s.Inbounds); err != nil {
		return err
	}
	if err := a.St.SyncUsers(users); err != nil {
		return err
	}
	for _, k := range []string{"block_torrent", "routing"} {
		if v, ok := s.Settings[k]; ok {
			a.St.SetSetting(k, v)
		}
	}
	if a.Relay != nil {
		a.Relay.Apply(s.Forwards)
		a.saveJSON(keyForwards, s.Forwards) // brought back at the next start
	}
	a.Mgr.ClearPortErrors()
	if err := a.Mgr.Apply(); err != nil {
		log.Printf("node apply: %v", err) // reported per core and inbound in the status
	}
	return nil
}

// ---------------------------------------------------------------- traffic

// keyForwards keeps a relay's forwards across restarts.
const keyForwards = "relay_forwards"

// RestoreForwards starts the forwards the panel sent last.
func (a *Agent) RestoreForwards() {
	var list []relay.Forward
	a.loadJSON(keyForwards, &list)
	if a.Relay != nil && len(list) > 0 {
		a.Relay.Apply(list)
	}
}

func (a *Agent) loadJSON(key string, v any) {
	if raw := a.St.GetSetting(key, ""); raw != "" {
		json.Unmarshal([]byte(raw), v)
	}
}

func (a *Agent) saveJSON(key string, v any) {
	b, _ := json.Marshal(v)
	a.St.SetSetting(key, string(b))
}

// totals are the node's cumulative counters.
func (a *Agent) totals() counters {
	c := counters{Users: map[string]store.Traffic{}, Inbounds: map[int64]store.Traffic{}}
	if users, err := a.St.Users(); err == nil {
		for _, u := range users {
			c.Users[u.Name] = store.Traffic{Up: u.UsedUp, Down: u.UsedDown}
		}
	}
	if list, err := a.St.Inbounds(); err == nil {
		for _, in := range list {
			c.Inbounds[in.ID] = store.Traffic{Up: in.Up, Down: in.Down}
		}
	}
	return c
}

func delta(now, before store.Traffic) store.Traffic {
	d := store.Traffic{Up: now.Up - before.Up, Down: now.Down - before.Down}
	if d.Up < 0 || d.Down < 0 { // the counter started again (the user was removed and came back)
		return now
	}
	return d
}

// Stats hands out the traffic since the last acknowledged batch. The batch is
// kept (on disk) until the panel acknowledges its number, so a reply lost on
// the way is sent again instead of being counted twice or not at all.
func (a *Agent) Stats(ack int64) Batch {
	a.mu.Lock()
	defer a.mu.Unlock()
	var rep counters
	var pend *pending
	a.loadJSON(keyReported, &rep)
	a.loadJSON(keyPending, &pend)
	if rep.Users == nil {
		rep = counters{Users: map[string]store.Traffic{}, Inbounds: map[int64]store.Traffic{}}
	}
	if pend != nil && pend.Seq == ack {
		rep = pend.Snap
		a.saveJSON(keyReported, rep)
		pend = nil
		a.St.SetSetting(keyPending, "")
	}
	if pend == nil {
		seq, _ := strconv.ParseInt(a.St.GetSetting(keySeq, "0"), 10, 64)
		seq++
		a.St.SetSetting(keySeq, strconv.FormatInt(seq, 10))
		pend = &pending{Seq: seq, Snap: a.totals()}
		a.saveJSON(keyPending, pend)
	}
	b := Batch{Epoch: a.St.GetSetting(keyEpoch, ""), Seq: pend.Seq, Users: map[string]store.Traffic{}, Inbounds: map[int64]store.Traffic{}, Devices: a.Mgr.AllDevices()}
	for name, t := range pend.Snap.Users {
		if d := delta(t, rep.Users[name]); d.Up > 0 || d.Down > 0 {
			b.Users[name] = d
		}
	}
	for id, t := range pend.Snap.Inbounds {
		if d := delta(t, rep.Inbounds[id]); d.Up > 0 || d.Down > 0 {
			b.Inbounds[id] = d
		}
	}
	if b.Devices == nil {
		b.Devices = []engine.Seen{}
	}
	return b
}

func (a *Agent) stats(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ack int64 `json:"ack"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in)
	reply(w, http.StatusOK, a.Stats(in.Ack))
}
