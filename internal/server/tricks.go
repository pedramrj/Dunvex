package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pedramrj/dunvex/internal/cdn"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/store"
)

// Anti-censorship settings (Settings > Subscription) beyond the fragment
// (server.go): ECH, multiplexing with TCP Brutal, WireGuard noise and XHTTP
// that downloads another way through the CDN. Each reaches only the apps that
// read it (docs/ANTI-CENSORSHIP.md).
const (
	echCDNSetting    = "ech_cdn"    // configs through the CDN carry the CDN's ECH
	echDirectSetting = "ech_direct" // this server's TLS configs carry its own ECH
	echNameSetting   = "ech_name"   // the public name of this server's own ECH
	echKeysSetting   = "ech_keys"   // its keys (made by the panel, never shown)
	echConfigSetting = "ech_config" // its config (made with the keys)

	muxSetting         = "mux"
	muxProtocolSetting = "mux_protocol"
	muxStreamsSetting  = "mux_streams"
	muxPaddingSetting  = "mux_padding"
	brutalSetting      = "mux_brutal"
	brutalUpSetting    = "mux_up"
	brutalDownSetting  = "mux_down"

	noiseSetting      = "wg_noise"
	noiseCountSetting = "wg_noise_count"
	noiseSizeSetting  = "wg_noise_size"
	noiseDelaySetting = "wg_noise_delay"

	splitSetting = "xhttp_split" // "", "down" or "up" (protocol.Split*)
)

// DefaultECHName is the public name of this server's own ECH: the name a
// filter sees. Any hostname works; the server does not need to own it.
const DefaultECHName = protocol.DefaultReality

// trickSettings are the editable ones, fragment included.
var trickSettings = []string{fragmentSetting, fragmentPacketsSetting, fragmentLengthSetting, fragmentDelaySetting,
	echCDNSetting, echDirectSetting, echNameSetting,
	muxSetting, muxProtocolSetting, muxStreamsSetting, muxPaddingSetting, brutalSetting, brutalUpSetting, brutalDownSetting,
	noiseSetting, noiseCountSetting, noiseSizeSetting, noiseDelaySetting, splitSetting}

// trickDefaults are what the panel shows before the owner sets anything.
var trickDefaults = map[string]string{
	fragmentSetting: "false", fragmentPacketsSetting: protocol.FragmentPackets, fragmentLengthSetting: protocol.FragmentLength, fragmentDelaySetting: protocol.FragmentDelay,
	echCDNSetting: "false", echDirectSetting: "false", echNameSetting: DefaultECHName,
	muxSetting: "false", muxProtocolSetting: protocol.MuxProtocol, muxStreamsSetting: strconv.Itoa(protocol.MuxStreams), muxPaddingSetting: "true",
	brutalSetting: "false", brutalUpSetting: strconv.Itoa(protocol.BrutalUp), brutalDownSetting: strconv.Itoa(protocol.BrutalDown),
	noiseSetting: "false", noiseCountSetting: strconv.Itoa(protocol.NoiseCount), noiseSizeSetting: protocol.NoiseSize, noiseDelaySetting: protocol.NoiseDelay,
	splitSetting: protocol.SplitOff,
}

// boolSettings are on or off.
var boolSettings = map[string]bool{fragmentSetting: true, echCDNSetting: true, echDirectSetting: true, muxSetting: true,
	muxPaddingSetting: true, brutalSetting: true, noiseSetting: true}

// muxFrom reads the multiplex settings (nil when off). Brutal is left out
// unless ready says this server can do it.
func muxFrom(get func(k, def string) string, ready bool) *protocol.Mux {
	if get(muxSetting, "false") != "true" {
		return nil
	}
	m := protocol.Mux{Protocol: get(muxProtocolSetting, protocol.MuxProtocol), Padding: get(muxPaddingSetting, "true") == "true", Singbox: true}
	m.Streams, _ = strconv.Atoi(get(muxStreamsSetting, strconv.Itoa(protocol.MuxStreams)))
	if get(brutalSetting, "false") == "true" {
		up, _ := strconv.Atoi(get(brutalUpSetting, strconv.Itoa(protocol.BrutalUp)))
		down, _ := strconv.Atoi(get(brutalDownSetting, strconv.Itoa(protocol.BrutalDown)))
		m.Brutal = &protocol.Brutal{Up: up, Down: down}
	}
	if m.Check() != nil {
		m = protocol.Mux{Protocol: protocol.MuxProtocol, Streams: protocol.MuxStreams, Padding: true, Singbox: true}
	}
	if !ready {
		m.Brutal = nil
	}
	return &m
}

// noiseFrom reads the WireGuard noise settings (nil when off).
func noiseFrom(get func(k, def string) string) *protocol.Noise {
	if get(noiseSetting, "false") != "true" {
		return nil
	}
	n := protocol.Noise{Size: get(noiseSizeSetting, protocol.NoiseSize), Delay: get(noiseDelaySetting, protocol.NoiseDelay)}
	n.Count, _ = strconv.Atoi(get(noiseCountSetting, strconv.Itoa(protocol.NoiseCount)))
	if n.Check() != nil {
		n = protocol.Noise{Count: protocol.NoiseCount, Size: protocol.NoiseSize, Delay: protocol.NoiseDelay}
	}
	return &n
}

func (s *Server) setting(k, def string) string { return s.St.GetSetting(k, def) }

// mux is the multiplexing the apps get, nil when the owner keeps it off.
func (s *Server) mux() *protocol.Mux {
	on := s.setting(muxSetting, "false") == "true" && s.setting(brutalSetting, "false") == "true"
	ready := on && s.brutal().Ready // the module is looked for only when it is wanted
	return muxFrom(s.setting, ready)
}

// noise is the WireGuard noise the apps get, nil when off.
func (s *Server) noise() *protocol.Noise { return noiseFrom(s.setting) }

// split is how XHTTP through the CDN sends its downloads (protocol.Split*).
func (s *Server) split() string {
	switch v := s.setting(splitSetting, ""); v {
	case protocol.SplitDown, protocol.SplitUp:
		return v
	}
	return protocol.SplitOff
}

// ownECH is this server's own ECH, nil when off. Its keys are made when it is
// turned on (see makeECH); a database without them gets new ones here.
func (s *Server) ownECH() *protocol.ECH {
	if s.setting(echDirectSetting, "false") != "true" {
		return nil
	}
	e := &protocol.ECH{Config: s.setting(echConfigSetting, ""), Keys: s.setting(echKeysSetting, "")}
	if e.Config == "" || e.Keys == "" {
		var err error
		if e, err = s.makeECH(s.setting(echNameSetting, DefaultECHName)); err != nil {
			return nil
		}
	}
	return e
}

var echMu sync.Mutex

// makeECH makes and keeps new ECH keys for name.
func (s *Server) makeECH(name string) (*protocol.ECH, error) {
	echMu.Lock()
	defer echMu.Unlock()
	e, err := protocol.NewECHKeys(name)
	if err != nil {
		return nil, err
	}
	s.St.SetSetting(echKeysSetting, e.Keys)
	s.St.SetSetting(echConfigSetting, e.Config)
	return e, nil
}

// directEnv is env for an inbound's own config (not its CDN or relay copies):
// this server's own ECH is left out when the inbound's links point to another
// address (its "Address in links", often a CDN, which could not open it).
// Relay copies keep it: a relay passes the TLS through untouched.
func directEnv(in *store.Inbound, env protocol.Env) protocol.Env {
	if env.ECH != nil && env.ECH.Keys != "" && strings.TrimSpace(in.Str("address")) != "" {
		env.ECH = nil
	}
	return env
}

// checkTricks checks the anti-censorship settings of a change as they will be
// used together: the request's values over the saved ones. It tidies in (on or
// off become "true" or "false") and reports whether anything changed.
func (s *Server) checkTricks(in map[string]string) (changed bool, err error) {
	for _, k := range trickSettings {
		v, ok := in[k]
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if boolSettings[k] && v != "true" {
			v = "false"
		}
		if k == echNameSetting {
			v = strings.ToLower(strings.TrimSuffix(v, "."))
		}
		in[k] = v
		changed = changed || v != s.setting(k, trickDefaults[k])
	}
	if !changed {
		return false, nil
	}
	get := func(k, def string) string {
		if v, ok := in[k]; ok && v != "" {
			return v
		}
		return s.setting(k, def)
	}
	f := protocol.Fragment{Packets: get(fragmentPacketsSetting, protocol.FragmentPackets),
		Length: get(fragmentLengthSetting, protocol.FragmentLength), Delay: get(fragmentDelaySetting, protocol.FragmentDelay)}
	if err := f.Check(); err != nil {
		return true, fmt.Errorf("fragment: %v", err)
	}
	if name := get(echNameSetting, DefaultECHName); !cdn.IsHostname(name) {
		return true, errors.New("ECH: the public name must be a hostname, like www.example.com")
	}
	m := protocol.Mux{Protocol: get(muxProtocolSetting, protocol.MuxProtocol)}
	var e1, e2, e3 error
	m.Streams, e1 = strconv.Atoi(get(muxStreamsSetting, strconv.Itoa(protocol.MuxStreams)))
	b := protocol.Brutal{}
	b.Up, e2 = strconv.Atoi(get(brutalUpSetting, strconv.Itoa(protocol.BrutalUp)))
	b.Down, e3 = strconv.Atoi(get(brutalDownSetting, strconv.Itoa(protocol.BrutalDown)))
	if e1 != nil {
		return true, errors.New("mux: streams per connection must be a number")
	}
	if e2 != nil || e3 != nil {
		return true, errors.New("mux: the Brutal speeds must be numbers (Mbps)")
	}
	m.Brutal = &b
	if err := m.Check(); err != nil {
		return true, fmt.Errorf("mux: %v", err)
	}
	n := protocol.Noise{Size: get(noiseSizeSetting, protocol.NoiseSize), Delay: get(noiseDelaySetting, protocol.NoiseDelay)}
	if n.Count, err = strconv.Atoi(get(noiseCountSetting, strconv.Itoa(protocol.NoiseCount))); err != nil {
		return true, errors.New("WireGuard noise: packets must be a number")
	}
	if err := n.Check(); err != nil {
		return true, fmt.Errorf("WireGuard noise: %v", err)
	}
	switch v := get(splitSetting, ""); v {
	case protocol.SplitOff, protocol.SplitDown, protocol.SplitUp:
	default:
		return true, errors.New(`XHTTP: downloads go "down" (straight), "up" (uploads straight) or "" (the same way)`)
	}
	return true, nil
}

// afterTricks makes this server's ECH keys when its ECH is on and has none
// for its public name yet. A new name means new keys: configs the apps still
// hold stop hiding the name until they update their subscription.
func (s *Server) afterTricks() {
	if s.setting(echDirectSetting, "false") != "true" {
		return
	}
	name := s.setting(echNameSetting, DefaultECHName)
	if cfg, keys := s.setting(echConfigSetting, ""), s.setting(echKeysSetting, ""); cfg != "" && keys != "" {
		if n, err := protocol.ECHPublicName(cfg); err == nil && n == name {
			return // the keys the apps already have keep working
		}
	}
	s.makeECH(name)
}

// trickInfo is what the panel shows next to the settings.
func (s *Server) trickInfo() map[string]any {
	info := map[string]any{}
	if s.setting(muxSetting, "false") == "true" && s.setting(brutalSetting, "false") == "true" {
		b := s.brutal()
		info["brutal"] = map[string]any{"ready": b.Ready, "why": b.Why}
	}
	if e := s.ownECH(); e != nil {
		name, _ := protocol.ECHPublicName(e.Config)
		info["ech"] = map[string]any{"publicName": name}
	}
	if s.setting(echCDNSetting, "false") == "true" {
		cl := s.cdnLinks(s.Env())
		v := map[string]any{"domain": cl.domain, "on": cl.set.Enabled}
		if cl.ech != nil {
			v["publicName"], _ = protocol.ECHPublicName(cl.ech.Config)
			v["source"] = cl.echFrom
		}
		info["echCdn"] = v
	}
	return info
}

// ---------------------------------------------------------------- TCP Brutal

type brutalState struct {
	Ready bool
	Why   string // why not: brutalNotLinux or brutalNoModule
}

// Why TCP Brutal cannot run (the panel says it in the owner's language).
const (
	brutalNotLinux = "not-linux" // only Linux kernels have it
	brutalNoModule = "no-module" // the tcp-brutal module is not installed: bash <(curl -fsSL https://tcp.hy2.sh/)
)

var (
	brutalMu   sync.Mutex
	brutalAt   time.Time
	brutalLast brutalState
)

// brutalCheck finds whether the kernel has TCP Brutal; tests replace it.
var brutalCheck = func() brutalState {
	if runtime.GOOS != "linux" {
		return brutalState{Why: brutalNotLinux}
	}
	const cc = "/proc/sys/net/ipv4/tcp_available_congestion_control"
	has := func() bool {
		b, err := os.ReadFile(cc)
		return err == nil && strings.Contains(" "+strings.TrimSpace(string(b))+" ", " brutal ")
	}
	if has() {
		return brutalState{Ready: true}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec.CommandContext(ctx, "modprobe", "brutal").Run() // installed but not loaded yet
	if has() {
		return brutalState{Ready: true}
	}
	return brutalState{Why: brutalNoModule}
}

// brutal is whether this server can do TCP Brutal, looked up at most every
// few minutes. Without it a sing-box server refuses the apps' Brutal request
// and their connection fails, so Brutal reaches no config then.
func (s *Server) brutal() brutalState {
	brutalMu.Lock()
	defer brutalMu.Unlock()
	if time.Since(brutalAt) > 5*time.Minute {
		brutalLast, brutalAt = brutalCheck(), time.Now()
	}
	return brutalLast
}

// forgetBrutal makes the next brutal() look again (after the owner saves).
func forgetBrutal() {
	brutalMu.Lock()
	brutalAt = time.Time{}
	brutalMu.Unlock()
}
