package awg

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// network gives the interface its address and sends the clients' traffic out
// to the internet (NAT), while keeping them away from each other and from
// private networks.
func (s *Server) network(c *Config) error {
	_, subnet, err := net.ParseCIDR(c.Address)
	if err != nil {
		return fmt.Errorf("bad tunnel address %q", c.Address)
	}
	mtu := c.MTU
	if mtu <= 0 {
		mtu = 1420
	}
	for _, args := range [][]string{
		{"address", "replace", c.Address, "dev", s.Iface},
		{"link", "set", s.Iface, "mtu", strconv.Itoa(mtu), "up"},
	} {
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("ip %s: %v %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0o644)
	rules, err := addNAT(s.Iface, subnet.String())
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.nat = rules
	s.mu.Unlock()
	return nil
}

// natRules remembers what was added, to take it away again.
type natRules struct {
	ipt   [][]string // iptables rules ("-t nat" included where needed), without -A/-I
	nft   bool       // the "dunvex_awg" nft table
	iface string
}

var private = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16"}

func addNAT(iface, subnet string) (natRules, error) {
	if _, err := exec.LookPath("iptables"); err == nil {
		r := natRules{iface: iface}
		tag := []string{"-m", "comment", "--comment", "dunvex-awg"}
		rules := [][]string{
			append([]string{"-t", "nat", "POSTROUTING", "-s", subnet, "!", "-o", iface, "-j", "MASQUERADE"}, tag...),
			append([]string{"FORWARD", "-i", iface, "-o", iface, "-j", "DROP"}, tag...), // clients cannot reach each other
		}
		for _, p := range private { // nor this server's private networks
			rules = append(rules, append([]string{"FORWARD", "-i", iface, "-d", p, "-j", "DROP"}, tag...))
		}
		rules = append(rules,
			append([]string{"FORWARD", "-o", iface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"}, tag...),
			append([]string{"FORWARD", "-i", iface, "-j", "ACCEPT"}, tag...))
		// inserted bottom-up at the top of FORWARD, so they end in this order: drops first
		for i := len(rules) - 1; i >= 0; i-- {
			if err := iptables(rules[i], true); err != nil {
				r.remove()
				return natRules{}, err
			}
			r.ipt = append(r.ipt, rules[i])
		}
		return r, nil
	}
	if _, err := exec.LookPath("nft"); err == nil {
		set := strings.Join(private, ", ")
		script := fmt.Sprintf(`table inet dunvex_awg {
	chain post { type nat hook postrouting priority 100; ip saddr %s oifname != "%s" masquerade; }
	chain fwd { type filter hook forward priority -10;
		iifname "%s" oifname "%s" drop
		iifname "%s" ip daddr { %s } drop
	}
}
`, subnet, iface, iface, iface, iface, set)
		exec.Command("nft", "delete", "table", "inet", "dunvex_awg").Run()
		cmd := exec.Command("nft", "-f", "-")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			return natRules{}, fmt.Errorf("nft: %v %s", err, strings.TrimSpace(string(out)))
		}
		return natRules{nft: true, iface: iface}, nil
	}
	return natRules{}, fmt.Errorf("neither iptables nor nft is installed; AmneziaWG needs one of them for NAT")
}

// iptables adds a rule once (checks first); FORWARD rules go to the top.
func iptables(rule []string, insert bool) error {
	table, chain, rest := []string{}, rule[0], rule[1:]
	if rule[0] == "-t" {
		table, chain, rest = rule[:2], rule[2], rule[3:]
	}
	check := append(append(append([]string{}, table...), "-C", chain), rest...)
	if exec.Command("iptables", check...).Run() == nil {
		return nil
	}
	op := []string{"-A", chain}
	if insert && chain == "FORWARD" {
		op = []string{"-I", chain, "1"}
	}
	args := append(append(append([]string{}, table...), op...), rest...)
	if out, err := exec.Command("iptables", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("iptables %s: %v %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (r natRules) remove() {
	for _, rule := range r.ipt {
		table, chain, rest := []string{}, rule[0], rule[1:]
		if rule[0] == "-t" {
			table, chain, rest = rule[:2], rule[2], rule[3:]
		}
		args := append(append(append([]string{}, table...), "-D", chain), rest...)
		exec.Command("iptables", args...).Run()
	}
	if r.nft {
		exec.Command("nft", "delete", "table", "inet", "dunvex_awg").Run()
	}
}
