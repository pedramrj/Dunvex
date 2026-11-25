package bridge

import (
	"crypto/rand"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/xtaci/smux"
)

// freeUDP picks a free UDP port on loopback.
func freeUDP(t *testing.T) string {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	addr := c.LocalAddr().String()
	c.Close()
	return addr
}

// bridgePair brings up a relay (server) and a target (client) over one UDP
// carrier and returns a smux session each: the relay opens streams (like the
// reverse tunnel), the target accepts them. It mirrors how the Bridge is wired
// into the relay code.
func bridgePair(t *testing.T) (relay *smux.Session, targetAccept func() (*smux.Stream, error), stop func()) {
	t.Helper()
	psk := []byte("bridge-end-to-end-test-shared-key")
	kp, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr := freeUDP(t)

	ln, err := Listen(Config{PSK: psk, Static: kp, Carriers: []CarrierOpt{{Carrier: NewUDPServer(addr), Priority: 10}}})
	if err != nil {
		t.Fatal(err)
	}

	type accepted struct {
		s   *smux.Session
		err error
	}
	acc := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			acc <- accepted{err: err}
			return
		}
		s, err := smux.Client(c, smuxCfg()) // relay opens streams
		acc <- accepted{s: s, err: err}
	}()

	uc, err := NewUDPClient(addr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := Dial(Config{PSK: psk, ServerPub: kp.Pub, Carriers: []CarrierOpt{{Carrier: uc, Priority: 10}}}, 10*time.Second)
	if err != nil {
		ln.Close()
		t.Fatalf("dial: %v", err)
	}
	ts, err := smux.Server(conn, smuxCfg()) // target accepts streams
	if err != nil {
		t.Fatal(err)
	}
	a := <-acc
	if a.err != nil {
		t.Fatalf("accept: %v", a.err)
	}
	return a.s, ts.AcceptStream, func() { a.s.Close(); ts.Close(); conn.Close(); ln.Close() }
}

func smuxCfg() *smux.Config {
	c := smux.DefaultConfig()
	c.Version = 2
	c.KeepAliveInterval = 5 * time.Second
	c.KeepAliveTimeout = 25 * time.Second
	return c
}

// A full stack: relay opens a stream, the target echoes it, several MB flow.
func TestBridgeEndToEndUDP(t *testing.T) {
	relay, accept, stop := bridgePair(t)
	defer stop()

	// target echoes every stream it accepts
	go func() {
		for {
			st, err := accept()
			if err != nil {
				return
			}
			go func() { io.Copy(st, st); st.Close() }()
		}
	}()

	st, err := relay.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const total = 3 << 20 // 3 MB
	payload := make([]byte, total)
	rand.Read(payload)

	done := make(chan []byte, 1)
	go func() {
		got := make([]byte, 0, total)
		buf := make([]byte, 64<<10)
		for len(got) < total {
			n, err := st.Read(buf)
			got = append(got, buf[:n]...)
			if err != nil {
				break
			}
		}
		done <- got
	}()

	start := time.Now()
	if _, err := st.Write(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if len(got) != total {
			t.Fatalf("got %d of %d bytes", len(got), total)
		}
		mbps := float64(total) / time.Since(start).Seconds() / 1e6
		t.Logf("3 MB echoed over the UDP carrier in %v (%.1f MB/s round trip)", time.Since(start).Round(time.Millisecond), mbps)
	case <-time.After(30 * time.Second):
		t.Fatal("transfer did not finish")
	}
}

// A second carrier added via a loopback pipe lets the test kill the primary
// mid-transfer and prove the stream survives on the backup.
func TestBridgeFailover(t *testing.T) {
	psk := []byte("bridge-failover-test-shared-key!!")
	kp, _ := GenerateKey()
	addr := freeUDP(t)

	// one real UDP carrier plus one in-memory pipe carrier, both sides
	sPipe, cPipe := newPipeCarrierPair()
	ln, err := Listen(Config{PSK: psk, Static: kp, Carriers: []CarrierOpt{
		{Carrier: NewUDPServer(addr), Priority: 10},
		{Carrier: sPipe, Priority: 5},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var relay *smux.Session
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		relay, err = smux.Client(c, smuxCfg())
		done <- err
	}()

	uc, _ := NewUDPClient(addr)
	conn, err := Dial(Config{PSK: psk, ServerPub: kp.Pub, Carriers: []CarrierOpt{
		{Carrier: uc, Priority: 10},
		{Carrier: cPipe, Priority: 5},
	}}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ts, _ := smux.Server(conn, smuxCfg())
	go func() {
		for {
			st, err := ts.AcceptStream()
			if err != nil {
				return
			}
			go func() { io.Copy(st, st); st.Close() }()
		}
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	st, err := relay.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// a long trickle: the writer keeps sending while we kill the UDP carrier
	var wg sync.WaitGroup
	wg.Add(1)
	gotCh := make(chan int, 1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 32<<10)
		got := 0
		for got < (2 << 20) {
			n, err := st.Read(buf)
			got += n
			if err != nil {
				break
			}
		}
		gotCh <- got
	}()

	chunk := make([]byte, 16<<10)
	rand.Read(chunk)
	killed := false
	for sent := 0; sent < (2 << 20); sent += len(chunk) {
		if !killed && sent > (512<<10) {
			uc.Close() // the UDP carrier dies mid-transfer; the pipe carrier must carry on
			killed = true
		}
		if _, err := st.Write(chunk); err != nil {
			t.Fatalf("write failed after %d bytes (failover did not hold): %v", sent, err)
		}
		time.Sleep(3 * time.Millisecond)
	}
	select {
	case got := <-gotCh:
		if got < (2 << 20) {
			t.Fatalf("only %d bytes survived the failover", got)
		}
		t.Logf("stream survived the UDP carrier dying: %d bytes delivered over the backup", got)
	case <-time.After(30 * time.Second):
		t.Fatal("transfer stalled after failover")
	}
	wg.Wait()
}
