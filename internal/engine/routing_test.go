package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pedramrj/dunvex/internal/geodata"
	"github.com/pedramrj/dunvex/internal/protocol"
	"github.com/pedramrj/dunvex/internal/routing"
	"github.com/pedramrj/dunvex/internal/store"
	"github.com/pedramrj/dunvex/internal/util"
	"github.com/pedramrj/dunvex/internal/warp"
)

// routingSetup makes a manager whose Xray folder holds small site lists.
func routingSetup(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	geodata.WriteSite(filepath.Join(bin, "geosite.dat"), map[string][]geodata.Domain{
		"category-ir":      {{Kind: geodata.Suffix, Value: "aparat.com"}, {Kind: geodata.Full, Value: "www.digikala.com"}},
		"category-ads-all": {{Kind: geodata.Suffix, Value: "doubleclick.net"}, {Kind: geodata.Regex, Value: `^ad\d+\.x\.com$`}},
		"category-ai-!cn":  {{Kind: geodata.Suffix, Value: "chatgpt.com"}, {Kind: geodata.Keyword, Value: "openai"}},
		"netflix":          {{Kind: geodata.Suffix, Value: "netflix.com"}},
	})
	geodata.WriteIP(filepath.Join(bin, "geoip.dat"), map[string][]string{"ir": {"2.144.0.0/14", "5.22.0.0/17"}, "private": {"10.0.0.0/8"}})
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := NewManager(st, bin, filepath.Join(dir, "run"), func() protocol.Env { return protocol.Env{Host: "203.0.113.5"} })
	m.WarpClient = &warp.Client{Base: "http://127.0.0.1:1"} // never Cloudflare from a test
	return m, st
}

func fakeAccount() *warp.Account {
	priv, _ := util.WireGuardKeys()
	_, peer := util.WireGuardKeys()
	return &warp.Account{ID: "d", Token: "t", PrivateKey: priv, PeerKey: peer, AddressV4: "172.16.0.2", Endpoint: "162.159.192.1:2408", Reserved: []int{1, 2, 3}}
}

func js(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestRoutingDefaults(t *testing.T) {
	m, _ := routingSetup(t)
	rp := m.routePlan()
	rules, outs, ins, strategy := rp.xrayRouting(true, true)
	if strategy != "AsIs" || len(outs) != 2 || len(ins) != 0 || len(rules) != 2 {
		t.Fatalf("defaults: %s %s %s %s", strategy, js(rules), js(outs), js(ins))
	}
	if rp.useRouter() {
		t.Error("Mieru and TrustTunnel are sent through Xray without any rule")
	}
	srules, sets, eps, final, resolver, err := m.singboxRouting(rp, true)
	if err != nil || len(sets) != 0 || len(eps) != 0 || final != "direct" || resolver || len(srules) != 3 {
		t.Errorf("sing-box defaults: %v %s %s", err, js(srules), js(sets))
	}
}

func TestRoutingRules(t *testing.T) {
	m, st := routingSetup(t)
	routing.SaveAccount(st, fakeAccount())
	if _, err := routing.Save(st, routing.Settings{BlockAds: true, Iran: routing.IranBlock, Warp: routing.WarpSites,
		WarpGroups: []string{"ai", "netflix"}, WarpDomains: []string{"x.ai"}, BlockDomains: []string{"example.org"}, DirectDomains: []string{"bank.com"}}); err != nil {
		t.Fatal(err)
	}
	rp := m.routePlan()
	rules, outs, ins, strategy := rp.xrayRouting(false, true)
	all := js(rules)
	for _, want := range []string{
		`{"inboundTag":["warp-test"],"outboundTag":"warp"}`,
		`{"ip":["geoip:private"],"outboundTag":"block"}`,
		`{"domain":["geosite:category-ads-all"],"outboundTag":"block"}`,
		`{"domain":["domain:example.org"],"outboundTag":"block"}`,
		`{"domain":["domain:bank.com"],"outboundTag":"direct"}`,
		`{"domain":["geosite:category-ir","domain:ir"],"outboundTag":"block"}`,
		`{"ip":["geoip:ir"],"outboundTag":"block"}`,
		`{"domain":["geosite:category-ai-!cn","geosite:netflix","domain:x.ai"],"outboundTag":"warp"}`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing rule %s in\n%s", want, all)
		}
	}
	if strings.Contains(all, "bittorrent") {
		t.Error("BitTorrent blocked although it was turned off")
	}
	if strategy != "IPIfNonMatch" || outs[0]["tag"] != "direct" || outs[2]["tag"] != "warp" || len(ins) != 2 {
		t.Errorf("strategy %s outbounds %s inbounds %s", strategy, js(outs), js(ins))
	}
	if ins[1]["tag"] != "router" || ins[1]["port"] != m.RouterPort || ins[0]["port"] != m.WarpTestPort {
		t.Errorf("loopback inbounds %s", js(ins))
	}

	srules, sets, eps, final, resolver, err := m.singboxRouting(rp, true)
	if err != nil {
		t.Fatal(err)
	}
	sall := js(srules)
	for _, want := range []string{
		`{"action":"reject","rule_set":["site-category-ads-all"]}`,
		`{"action":"reject","domain_suffix":["example.org"]}`,
		`{"domain_suffix":["bank.com"],"outbound":"direct"}`,
		`{"action":"reject","rule_set":["site-category-ir"]}`,
		`{"domain_suffix":["x.ai"],"outbound":"warp","rule_set":["site-category-ai-not-cn","site-netflix"]}`,
		`{"action":"resolve"},{"action":"reject","rule_set":["ip-ir"]}`,
		`{"action":"reject","protocol":["bittorrent"]}`,
	} {
		if !strings.Contains(sall, want) {
			t.Errorf("sing-box: missing %s in\n%s", want, sall)
		}
	}
	if final != "direct" || !resolver || len(eps) != 1 || eps[0]["tag"] != "warp" || len(sets) != 5 {
		t.Errorf("final %s resolver %v endpoints %s sets %s", final, resolver, js(eps), js(sets))
	}
	files := map[string]string{}
	for _, s := range sets {
		b, err := os.ReadFile(s["path"].(string))
		if err != nil {
			t.Fatal(err)
		}
		files[s["tag"].(string)] = string(b)
	}
	if f := files["site-category-ir"]; !strings.Contains(f, `"domain_suffix":["ir","aparat.com"]`) || !strings.Contains(f, `"domain":["www.digikala.com"]`) {
		t.Errorf("Iran's list: %s", f)
	}
	if f := files["site-category-ads-all"]; !strings.Contains(f, `"domain_regex":["^ad\\d+\\.x\\.com$"]`) {
		t.Errorf("ads list: %s", f)
	}
	if f := files["ip-ir"]; f != `{"rules":[{"ip_cidr":["2.144.0.0/14","5.22.0.0/17"]}],"version":3}` {
		t.Errorf("Iran's addresses: %s", f)
	}
	if f := files["site-category-ai-not-cn"]; !strings.Contains(f, `"domain_keyword":["openai"]`) {
		t.Errorf("AI list: %s", f)
	}
}

func TestRoutingEverythingThroughWarp(t *testing.T) {
	m, st := routingSetup(t)
	routing.SaveAccount(st, fakeAccount())
	routing.Save(st, routing.Settings{Iran: routing.IranDirect, Warp: routing.WarpAll})
	rp := m.routePlan()
	rules, outs, _, strategy := rp.xrayRouting(true, false)
	// what no rule matches leaves through the first outbound: WARP; Iran stays direct
	if outs[0]["tag"] != "warp" || strategy != "IPIfNonMatch" ||
		!strings.Contains(js(rules), `{"domain":["geosite:category-ir","domain:ir"],"outboundTag":"direct"}`) {
		t.Errorf("outbounds %s rules %s", js(outs), js(rules))
	}
	if strings.Contains(js(rules), `"router"`) {
		t.Error("a router inbound without Mieru or TrustTunnel")
	}
	_, _, _, final, _, _ := m.singboxRouting(rp, true)
	if final != "warp" {
		t.Errorf("sing-box final %s", final)
	}
}

func TestWarpDeviceWithoutRules(t *testing.T) {
	m, st := routingSetup(t)
	routing.SaveAccount(st, fakeAccount())
	rp := m.routePlan()
	rules, outs, ins, strategy := rp.xrayRouting(true, true)
	// just turned on: nothing is routed to WARP, but the panel can check it
	if len(outs) != 3 || outs[0]["tag"] != "direct" || outs[2]["tag"] != "warp" || len(ins) != 1 || ins[0]["tag"] != "warp-test" || strategy != "AsIs" {
		t.Errorf("outbounds %s inbounds %s", js(outs), js(ins))
	}
	if r := js(rules); strings.Count(r, `"warp"`) != 1 || !strings.Contains(r, `{"inboundTag":["warp-test"],"outboundTag":"warp"}`) {
		t.Errorf("rules %s", r)
	}
	if rp.useRouter() {
		t.Error("Mieru and TrustTunnel go through Xray although no rule is on")
	}
}

func TestRoutingWithoutWarpDevice(t *testing.T) {
	m, st := routingSetup(t)
	routing.Save(st, routing.Settings{Iran: routing.IranWarp, Warp: routing.WarpAll})
	rp := m.routePlan()
	rules, outs, ins, strategy := rp.xrayRouting(true, true)
	// no device yet: nothing is routed to a WARP that does not exist
	if strings.Contains(js(outs), "wireguard") || strings.Contains(js(rules), `"warp"`) || strategy != "AsIs" || len(ins) != 1 {
		t.Errorf("rules %s outbounds %s", js(rules), js(outs))
	}
}

func TestMieruAndTrustTunnelLeaveThroughXray(t *testing.T) {
	m, st := routingSetup(t)
	u := &store.User{Name: "sara", UUID: util.UUID(), Password: util.RandomString(16), SubToken: util.RandomString(20), Enabled: true, ResetPeriod: "none"}
	st.CreateUser(u)
	for _, preset := range []string{"mieru", "trusttunnel"} {
		p := protocol.Get(preset)
		if err := st.CreateInbound(&store.Inbound{Preset: p.ID, Port: 31000 + len(preset), Enabled: true, Params: protocol.Defaults(p, nil, "")}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := m.build()
	if err != nil {
		t.Fatal(err)
	}
	if b.xray != nil || strings.Contains(string(b.mita), "egress") || strings.Contains(string(b.tt.Main), "socks5") {
		t.Fatal("routing off, yet Mieru or TrustTunnel go through Xray")
	}
	routing.Save(st, routing.Settings{BlockAds: true, Iran: routing.IranDirect, Warp: routing.WarpOff})
	if b, err = m.build(); err != nil {
		t.Fatal(err)
	}
	addr := "127.0.0.1:" + js(m.RouterPort)
	if !strings.Contains(string(b.mita), `"SOCKS5_PROXY_PROTOCOL"`) || !strings.Contains(string(b.mita), js(m.RouterPort)) {
		t.Errorf("mita egress:\n%s", b.mita)
	}
	if !strings.Contains(string(b.tt.Main), "[forward_protocol.socks5]\naddress = \""+addr+"\"") {
		t.Errorf("TrustTunnel forward:\n%s", b.tt.Main)
	}
	if !strings.Contains(string(b.xray), `"tag": "router"`) {
		t.Errorf("Xray runs without its routing inbound:\n%s", b.xray)
	}
}
