//go:build bridgelab

// Command bridgelab drives one side of a Dunvex Bridge for the hostile-network
// test. It is built only with -tags bridgelab (never shipped). The relay side
// (inside Iran) opens a smux stream and reads a bulk download the target abroad
// pushes into it — the easy, client->server direction — and prints, every
// second, the throughput and every carrier's live state, so the test script
// can read failover time and per-scenario throughput off the log.
package main

import (
	"crypto/tls"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pedramrj/dunvex/internal/bridge"
	"github.com/xtaci/smux"
)

func main() {
	role := flag.String("role", "", "server (Iran relay) or client (target abroad)")
	psk := flag.String("psk", "bridge-lab-shared-key-0123456789ab", "pre-shared key")
	priv := flag.String("priv", "", "server: static X25519 private key (base64)")
	pub := flag.String("pub", "", "client: the relay's static public key (base64)")
	relay := flag.String("relay", "10.10.0.2", "client: the relay's IP")
	carriers := flag.String("carriers", "faketcp:8443,udp:8444,icmp", "comma list: udp:port,icmp,faketcp:port,tls-cdn:port")
	tlsCert := flag.String("tls-cert", "", "server: tls-cdn certificate (PEM)")
	tlsKey := flag.String("tls-key", "", "server: tls-cdn key (PEM)")
	tlsPin := flag.String("tls-pin", "", "client: tls-cdn certificate SHA-256 (hex) to pin")
	seconds := flag.Int("seconds", 20, "server: how long to read the download")
	genkey := flag.Bool("genkey", false, "print a fresh static keypair (priv,pub base64) and exit")
	flag.Parse()

	if *genkey {
		kp, err := bridge.GenerateKey()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s %s\n", base64.StdEncoding.EncodeToString(kp.Priv[:]), base64.StdEncoding.EncodeToString(kp.Pub[:]))
		return
	}

	switch *role {
	case "server":
		runServer(*psk, *priv, *carriers, *tlsCert, *tlsKey, *seconds)
	case "client":
		runClient(*psk, *pub, *relay, *carriers, *tlsPin)
	default:
		log.Fatal("set -role server or client")
	}
}

type carrierSpec struct {
	kind string
	port int
}

func parseCarriers(s string) []carrierSpec {
	var out []carrierSpec
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		cs := carrierSpec{kind: p}
		if i := strings.IndexByte(p, ':'); i >= 0 {
			cs.kind = p[:i]
			cs.port, _ = strconv.Atoi(p[i+1:])
		}
		out = append(out, cs)
	}
	return out
}

func prio(kind string) int {
	switch kind {
	case "faketcp":
		return 30
	case "udp":
		return 25
	case "tls-cdn":
		return 20
	case "icmp":
		return 15
	default:
		return 5
	}
}

func carrierCap(kind string) int {
	if kind == "icmp" {
		return 22
	}
	return 0
}

func runServer(psk, privB64, carriers, certFile, keyFile string, seconds int) {
	raw, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil || len(raw) != 32 {
		log.Fatal("bad -priv")
	}
	var priv [32]byte
	copy(priv[:], raw)
	pub, err := bridge.PubFromPriv(priv)
	if err != nil {
		log.Fatal(err)
	}
	var cert tls.Certificate
	if certFile != "" {
		if cert, err = tls.LoadX509KeyPair(certFile, keyFile); err != nil {
			log.Fatalf("tls-cdn cert: %v", err)
		}
	}
	var opts []bridge.CarrierOpt
	for _, cs := range parseCarriers(carriers) {
		var c bridge.Carrier
		switch cs.kind {
		case "udp":
			c = bridge.NewUDPServer(":" + strconv.Itoa(cs.port))
		case "icmp":
			c = bridge.NewICMPServer(false)
		case "faketcp":
			c = bridge.NewFakeTCPServer(cs.port)
		case "tls-cdn":
			c = bridge.NewTLSCDNServer(":"+strconv.Itoa(cs.port), "/dvxb", cert)
		default:
			continue
		}
		opts = append(opts, bridge.CarrierOpt{Carrier: c, Priority: prio(cs.kind), CapMBps: carrierCap(cs.kind)})
	}
	ln, err := bridge.Listen(bridge.Config{PSK: []byte(psk), Static: bridge.KeyPair{Priv: priv, Pub: pub}, Carriers: opts})
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()
	log.Printf("server up on carriers %s; waiting for the target", carriers)

	conn, err := ln.Accept()
	if err != nil {
		log.Fatal(err)
	}
	sess, err := smux.Client(conn, smuxCfg())
	if err != nil {
		log.Fatal(err)
	}
	st, err := sess.OpenStream()
	if err != nil {
		log.Fatal(err)
	}

	// read the download for `seconds`, printing throughput and carriers each second
	var total int64
	last := int64(0)
	done := time.After(time.Duration(seconds) * time.Second)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	go func() {
		buf := make([]byte, 128<<10)
		for {
			n, err := st.Read(buf)
			total += int64(n)
			if err != nil {
				return
			}
		}
	}()
	start := time.Now()
	for {
		select {
		case <-done:
			mbps := float64(total) / time.Since(start).Seconds() / 1e6
			fmt.Printf("RESULT total=%d bytes seconds=%.1f avg=%.2f MB/s\n", total, time.Since(start).Seconds(), mbps)
			st.Close()
			return
		case <-tick.C:
			now := total
			rate := float64(now-last) / 1e6
			last = now
			fmt.Printf("t=%2d rate=%6.2f MB/s %s\n", int(time.Since(start).Seconds()), rate, fmtCarriers(ln.Stats()))
		}
	}
}

func fmtCarriers(cs []bridge.CarrierStat) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		up := "down"
		if c.Up {
			up = "up"
		}
		parts = append(parts, fmt.Sprintf("%s:%s rtt=%dms loss=%d%% dn=%.1fMB/s", c.Name, up, c.RTTMs, c.LossPct, float64(c.RateDown)/1e6))
	}
	return strings.Join(parts, " | ")
}

func runClient(psk, pubB64, relay, carriers, tlsPin string) {
	raw, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(raw) != 32 {
		log.Fatal("bad -pub")
	}
	var pub [32]byte
	copy(pub[:], raw)
	var opts []bridge.CarrierOpt
	for _, cs := range parseCarriers(carriers) {
		var c bridge.Carrier
		switch cs.kind {
		case "udp":
			c, err = bridge.NewUDPClient(relay + ":" + strconv.Itoa(cs.port))
		case "icmp":
			c, err = bridge.NewICMPClient(relay, false)
		case "faketcp":
			c, err = bridge.NewFakeTCPClient(relay, cs.port)
		case "tls-cdn":
			url := "wss://" + relay + ":" + strconv.Itoa(cs.port) + "/dvxb"
			c, err = bridge.NewTLSCDNClient(url, relay, "", tlsPin)
		default:
			continue
		}
		if err != nil {
			log.Fatalf("%s carrier: %v", cs.kind, err)
		}
		opts = append(opts, bridge.CarrierOpt{Carrier: c, Priority: prio(cs.kind), CapMBps: carrierCap(cs.kind)})
	}
	conn, err := bridge.Dial(bridge.Config{PSK: []byte(psk), ServerPub: pub, Carriers: opts}, 30*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	sess, err := smux.Server(conn, smuxCfg())
	if err != nil {
		log.Fatal(err)
	}
	st, err := sess.AcceptStream()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("client connected; pushing the download")
	// push a steady stream (the download); stop when the relay closes.
	buf := make([]byte, 128<<10)
	for i := range buf {
		buf[i] = byte(i)
	}
	for {
		if _, err := st.Write(buf); err != nil {
			os.Exit(0)
		}
	}
}

func smuxCfg() *smux.Config {
	c := smux.DefaultConfig()
	c.Version = 2
	c.KeepAliveInterval = 5 * time.Second
	c.KeepAliveTimeout = 25 * time.Second
	c.MaxReceiveBuffer = 16 << 20
	c.MaxStreamBuffer = 4 << 20
	return c
}
