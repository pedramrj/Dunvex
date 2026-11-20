package dnstt_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"net"
	"testing"
	"time"

	"github.com/pedramrj/dunvex/internal/dnstt"
	"github.com/pedramrj/dunvex/internal/dnstt/dnsttclient"
)

// echo is a TCP service that sends back what it gets.
func echo(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln.Addr().String()
}

// A real dnstt client reaches the upstream through the in-process server, the
// wrong public key gets nothing, and closing the socket stops the server.
func TestTunnelEndToEnd(t *testing.T) {
	priv, pub, err := dnstt.NewKey()
	if err != nil || len(priv) != 64 || dnstt.PublicKey(priv) != pub {
		t.Fatalf("keys: %q %q %v", priv, pub, err)
	}
	key, _ := dnstt.DecodeKey(priv)
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- dnstt.Run(key, "t.example.com", echo(t), conn) }()

	c, err := dnsttclient.Dial(conn.LocalAddr().String(), "t.example.com", pub)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.Open()
	if err != nil {
		t.Fatal(err)
	}
	msg := make([]byte, 20000) // several DNS answers' worth
	rand.Read(msg)
	go s.Write(msg)
	s.SetReadDeadline(time.Now().Add(30 * time.Second))
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(s, got); err != nil {
		t.Fatalf("read through the tunnel: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatal("the data came back changed")
	}
	if n := dnstt.Sessions.Load(); n < 1 {
		t.Errorf("sessions: %d", n)
	}
	s.Close()

	// another key: the Noise handshake fails, so no session at all
	_, other, _ := dnstt.NewKey()
	dnsttclient.HandshakeTimeout = 4 * time.Second
	if bad, err := dnsttclient.Dial(conn.LocalAddr().String(), "t.example.com", other); err == nil {
		bad.Close()
		t.Fatal("a client with the wrong public key got a session")
	}

	conn.Close()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its socket was closed")
	}
}
