package engine

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

type obj = map[string]any

// Sample is one point of live proxy throughput (bytes per second).
type Sample struct {
	T    int64 `json:"t"`
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// Manager owns both cores and keeps them in sync with the database.
type Manager struct {
	Store   *store.Store
	Xray    *Core
	SingBox *Core
	Env     func() protocol.Env

	applyCh chan struct{}
	applyMu sync.Mutex

	mu          sync.Mutex
	inboundErrs map[int64]string
	coreErrs    map[string]string
	activeNames map[string]bool
	live        []Sample
	online      map[int64]int64 // user id → last unix second with traffic
	userRate    map[int64]Sample
	inRate      map[int64]Sample
	badPorts    map[int]string // ports another program holds: those inbounds are skipped
}

// NewManager wires a manager for the given home directory.
func NewManager(st *store.Store, binDir, runDir string, env func() protocol.Env) *Manager {
	os.MkdirAll(runDir, 0o700)
	return &Manager{
		Store:       st,
		Xray:        &Core{Name: "xray", BinDir: binDir, ConfigPath: filepath.Join(runDir, "xray.json"), APIPort: 62789},
		SingBox:     &Core{Name: "sing-box", BinDir: binDir, ConfigPath: filepath.Join(runDir, "sing-box.json"), APIPort: 62790},
		Env:         env,
		applyCh:     make(chan struct{}, 1),
		inboundErrs: map[int64]string{},
		coreErrs:    map[string]string{},
		online:      map[int64]int64{},
		userRate:    map[int64]Sample{},
		inRate:      map[int64]Sample{},
		badPorts:    map[int]string{},
	}
}

// RequestApply schedules a config rebuild (debounced).
func (m *Manager) RequestApply() {
	select {
	case m.applyCh <- struct{}{}:
	default:
	}
}

// Run starts the apply and collector loops. It blocks.
func (m *Manager) Run(stop <-chan struct{}) {
	if err := m.Apply(); err != nil {
		log.Printf("initial apply: %v", err)
	}
	collect := time.NewTicker(5 * time.Second)
	maint := time.NewTicker(time.Minute)
	defer collect.Stop()
	defer maint.Stop()
	for {
		select {
		case <-stop:
			m.collect()
			m.Xray.Stop()
			m.SingBox.Stop()
			return
		case <-m.applyCh:
			time.Sleep(1500 * time.Millisecond) // coalesce bursts of edits
			select {
			case <-m.applyCh:
			default:
			}
			if err := m.Apply(); err != nil {
				log.Printf("apply: %v", err)
			}
		case <-collect.C:
			if m.collect() {
				m.Apply()
			}
		case <-maint.C:
			if m.maintenance() {
				m.Apply()
			}
		}
	}
}

// Configs builds both core configs from the database.
// A nil config means the core has nothing to serve.
func (m *Manager) Configs() (xray, singbox []byte, inboundErrs map[int64]string, active map[string]bool, err error) {
	users, err := m.Store.Users()
	if err != nil {
		return
	}
	inbounds, err := m.Store.Inbounds()
	if err != nil {
		return
	}
	env := m.Env()
	now := time.Now().Unix()
	inboundErrs = map[int64]string{}
	active = map[string]bool{}
	var xin, sin []obj
	var sTags, sUsers []string
	seenUser := map[string]bool{}
	for _, in := range inbounds {
		if !in.Enabled {
			continue
		}
		p := protocol.Get(in.Preset)
		if p == nil {
			inboundErrs[in.ID] = "unknown preset " + in.Preset
			continue
		}
		m.mu.Lock()
		bad, isBad := m.badPorts[in.Port]
		m.mu.Unlock()
		if isBad {
			inboundErrs[in.ID] = bad
			continue
		}
		var us []*store.User
		for _, u := range users {
			if u.CanConnect(now) && u.AllowsInbound(in.ID) {
				us = append(us, u)
				active[u.Name] = true
			}
		}
		switch p.Engine {
		case "xray":
			ib, e := protocol.XrayInbound(p, in, us, env)
			if e != nil {
				inboundErrs[in.ID] = e.Error()
				continue
			}
			xin = append(xin, ib)
		case "singbox":
			if len(us) == 0 {
				continue // sing-box refuses user-based inbounds without users
			}
			ibs, e := protocol.SingboxInbounds(p, in, us, env)
			if e != nil {
				inboundErrs[in.ID] = e.Error()
				continue
			}
			sin = append(sin, ibs...)
			sTags = append(sTags, in.Tag())
			for _, u := range us {
				if !seenUser[u.Name] {
					seenUser[u.Name] = true
					sUsers = append(sUsers, u.Name)
				}
			}
		}
	}
	if len(xin) > 0 {
		xray, _ = json.MarshalIndent(m.xrayConfig(xin), "", "  ")
	}
	if len(sin) > 0 {
		sort.Strings(sUsers)
		singbox, _ = json.MarshalIndent(m.singboxConfig(sin, sTags, sUsers), "", "  ")
	}
	return
}

func (m *Manager) xrayConfig(inbounds []obj) obj {
	rules := []obj{}
	if _, err := os.Stat(filepath.Join(m.Xray.BinDir, "geoip.dat")); err == nil {
		rules = append(rules, obj{"ip": []string{"geoip:private"}, "outboundTag": "block"})
	}
	if m.Store.GetSetting("block_torrent", "true") == "true" {
		rules = append(rules, obj{"protocol": []string{"bittorrent"}, "outboundTag": "block"})
	}
	return obj{
		"log": obj{"loglevel": "warning", "access": "none"},
		"api": obj{"tag": "api", "listen": "127.0.0.1:" + strconv.Itoa(m.Xray.APIPort), "services": []string{"StatsService", "HandlerService"}},
		"stats": obj{},
		"policy": obj{
			"levels": obj{"0": obj{"statsUserUplink": true, "statsUserDownlink": true, "handshake": 4, "connIdle": 300}},
			"system": obj{"statsInboundUplink": true, "statsInboundDownlink": true},
		},
		"inbounds": inbounds,
		"outbounds": []obj{
			{"protocol": "freedom", "tag": "direct"},
			{"protocol": "blackhole", "tag": "block"},
		},
		"routing": obj{"domainStrategy": "AsIs", "rules": rules},
	}
}

func (m *Manager) singboxConfig(inbounds []obj, tags, users []string) obj {
	rules := []obj{{"action": "sniff"}, {"ip_is_private": true, "action": "reject"}}
	if m.Store.GetSetting("block_torrent", "true") == "true" {
		rules = append(rules, obj{"protocol": []string{"bittorrent"}, "action": "reject"})
	}
	cfg := obj{
		"log":       obj{"level": "warn", "timestamp": true},
		"inbounds":  inbounds,
		"outbounds": []obj{{"type": "direct", "tag": "direct"}},
		"route":     obj{"rules": rules, "final": "direct"},
	}
	if m.SingBox.HasTag("with_v2ray_api") {
		cfg["experimental"] = obj{"v2ray_api": obj{
			"listen": "127.0.0.1:" + strconv.Itoa(m.SingBox.APIPort),
			"stats":  obj{"enabled": true, "inbounds": tags, "users": users},
		}}
	}
	return cfg
}

// Apply rebuilds configs and restarts cores whose config changed.
func (m *Manager) Apply() error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	xcfg, scfg, ierrs, active, err := m.Configs()
	if err != nil {
		return err
	}
	m.collect() // save counters before a restart wipes them
	cerrs := map[string]string{}
	for attempt := 0; attempt < 6; attempt++ {
		cerrs = map[string]string{}
		retry := false
		for _, c := range []struct {
			core *Core
			cfg  []byte
		}{{m.Xray, xcfg}, {m.SingBox, scfg}} {
			if e := c.core.Apply(c.cfg); e != nil {
				cerrs[c.core.Name] = e.Error()
				continue
			}
			// a port held by another program makes the whole core exit: find it,
			// park only that inbound and start again without it
			if port := c.core.BindFailure(c.cfg != nil); port > 0 {
				m.mu.Lock()
				m.badPorts[port] = fmt.Sprintf("port %d is used by another program on this server; choose another port", port)
				m.mu.Unlock()
				m.Store.Log("core", "%s: port %d is busy, that protocol was paused", c.core.Name, port)
				retry = true
			}
		}
		if !retry {
			break
		}
		xcfg, scfg, ierrs, active, err = m.Configs()
		if err != nil {
			return err
		}
	}
	m.mu.Lock()
	prev := m.activeNames
	m.inboundErrs, m.coreErrs, m.activeNames = ierrs, cerrs, active
	m.mu.Unlock()
	if prev != nil {
		now := time.Now().Unix()
		if users, err := m.Store.Users(); err == nil {
			for _, u := range users {
				if prev[u.Name] && !active[u.Name] && !u.CanConnect(now) {
					m.Store.Log("user", "%s is now %s and was disconnected", u.Name, u.Status(now))
				}
			}
		}
	}
	if len(cerrs) > 0 {
		var parts []string
		for k, v := range cerrs {
			parts = append(parts, k+": "+v)
		}
		sort.Strings(parts)
		return fmt.Errorf("%s", strings.Join(parts, "; "))
	}
	return nil
}

// ClearPortErrors forgets busy ports so the next apply tries them again.
func (m *Manager) ClearPortErrors() {
	m.mu.Lock()
	m.badPorts = map[int]string{}
	m.mu.Unlock()
}

// Errors returns the latest per-inbound and per-core problems.
func (m *Manager) Errors() (map[int64]string, map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ie := map[int64]string{}
	for k, v := range m.inboundErrs {
		ie[k] = v
	}
	ce := map[string]string{}
	for k, v := range m.coreErrs {
		ce[k] = v
	}
	return ie, ce
}

// collect reads and resets the cores' counters and stores them.
// It returns true when a user's status changed and configs must be rebuilt.
func (m *Manager) collect() bool {
	raw := map[string]int64{}
	if m.Xray.Running() {
		if s, err := QueryStats("127.0.0.1:"+strconv.Itoa(m.Xray.APIPort), "xray.app.stats.command.StatsService", true); err == nil {
			for k, v := range s {
				raw[k] += v
			}
		}
	}
	if m.SingBox.Running() && m.SingBox.HasTag("with_v2ray_api") {
		if s, err := QueryStats("127.0.0.1:"+strconv.Itoa(m.SingBox.APIPort), "v2ray.core.app.stats.command.StatsService", true); err == nil {
			for k, v := range s {
				raw[k] += v
			}
		}
	}
	now := time.Now()
	users, err := m.Store.Users()
	if err != nil {
		return false
	}
	byName := map[string]*store.User{}
	for _, u := range users {
		byName[u.Name] = u
	}
	ud := map[int64]store.Traffic{}
	id := map[int64]store.Traffic{}
	var totUp, totDown int64
	for k, v := range raw {
		if v <= 0 {
			continue
		}
		c, ok := ParseCounter(k)
		if !ok {
			continue
		}
		switch c.Kind {
		case "user":
			u := byName[c.Name]
			if u == nil {
				continue
			}
			t := ud[u.ID]
			if c.Dir == "uplink" {
				t.Up += v
			} else {
				t.Down += v
			}
			ud[u.ID] = t
		case "inbound":
			if !strings.HasPrefix(c.Name, "in-") {
				continue
			}
			n, err := strconv.ParseInt(strings.TrimPrefix(c.Name, "in-"), 10, 64)
			if err != nil {
				continue // e.g. the internal "in-5-ss" ShadowTLS hop
			}
			t := id[n]
			if c.Dir == "uplink" {
				t.Up += v
				totUp += v
			} else {
				t.Down += v
				totDown += v
			}
			id[n] = t
		}
	}
	if err := m.Store.AddTraffic(now, ud, id); err != nil {
		log.Printf("store traffic: %v", err)
	}

	m.mu.Lock()
	interval := int64(5)
	if n := len(m.live); n > 0 {
		if d := now.Unix() - m.live[n-1].T; d > 0 {
			interval = d
		}
	}
	m.live = append(m.live, Sample{T: now.Unix(), Up: totUp / interval, Down: totDown / interval})
	if len(m.live) > 360 {
		m.live = m.live[len(m.live)-360:]
	}
	m.userRate = map[int64]Sample{}
	m.inRate = map[int64]Sample{}
	for iid, t := range id {
		m.inRate[iid] = Sample{T: now.Unix(), Up: t.Up / interval, Down: t.Down / interval}
	}
	for uid, t := range ud {
		m.online[uid] = now.Unix()
		m.userRate[uid] = Sample{T: now.Unix(), Up: t.Up / interval, Down: t.Down / interval}
	}
	active := m.activeNames
	m.mu.Unlock()

	// first connection starts the timer of "pending" users
	changed := false
	for _, u := range users {
		t := ud[u.ID]
		if (t.Up > 0 || t.Down > 0) && u.ExpireAt == 0 && u.DurationDays > 0 && u.FirstUseAt == 0 {
			u.FirstUseAt = now.Unix()
			u.ExpireAt = now.Unix() + int64(u.DurationDays)*86400
			m.Store.UpdateUser(u)
			m.Store.Log("user", "%s connected for the first time; plan of %d days started", u.Name, u.DurationDays)
		}
	}
	// detect users that crossed their limits
	fresh, _ := m.Store.Users()
	for _, u := range fresh {
		if u.CanConnect(now.Unix()) != active[u.Name] {
			changed = true
		}
	}
	return changed
}

// maintenance performs periodic resets and pruning; returns true if configs must change.
func (m *Manager) maintenance() bool {
	users, err := m.Store.Users()
	if err != nil {
		return false
	}
	now := time.Now()
	changed := false
	for _, u := range users {
		if u.ResetPeriod == "" || u.ResetPeriod == "none" {
			continue
		}
		last := u.LastResetAt
		if last == 0 {
			last = u.CreatedAt
		}
		lt := time.Unix(last, 0)
		var next time.Time
		switch u.ResetPeriod {
		case "daily":
			next = lt.AddDate(0, 0, 1)
		case "weekly":
			next = lt.AddDate(0, 0, 7)
		case "monthly":
			next = lt.AddDate(0, 1, 0)
		default:
			continue
		}
		if now.After(next) {
			wasLimited := u.Status(now.Unix()) == store.StatusLimited
			u.UsedUp, u.UsedDown, u.LastResetAt = 0, 0, now.Unix()
			m.Store.UpdateUser(u)
			m.Store.Log("user", "%s usage reset (%s)", u.Name, u.ResetPeriod)
			if wasLimited {
				changed = true
			}
		}
	}
	if now.Minute() == 0 {
		m.Store.PruneUsage(now.AddDate(0, 0, -400).Unix())
	}
	return changed
}

// Live returns recent throughput samples.
func (m *Manager) Live() []Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Sample(nil), m.live...)
}

// OnlineUsers returns user ids that moved traffic within the last `within` seconds.
func (m *Manager) OnlineUsers(within int64) map[int64]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().Unix()
	out := map[int64]bool{}
	for id, t := range m.online {
		if now-t <= within {
			out[id] = true
		}
	}
	return out
}

// InboundRate returns the latest per-inbound speed (bytes/s).
func (m *Manager) InboundRate(id int64) Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inRate[id]
}

// UserRate returns the latest per-user speed (bytes/s).
func (m *Manager) UserRate(id int64) Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.userRate[id]
}
