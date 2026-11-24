package sshproxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// UDP over TCP, as sing-box speaks it (github.com/sagernet/sing, common/uot):
// the app forwards a connection to a magic name, and UDP datagrams travel
// inside it, each with a length (and, unless the stream is "connected" to one
// destination, an address). The Hiddify app turns this on for SSH, so voice
// calls, games and QUIC work through the SSH server. The datagrams leave
// through Xray's SOCKS inbound (UDP ASSOCIATE), so the routing applies.
const (
	uotMagic  = "sp.v2.udp-over-tcp.arpa" // version 2: a request header first
	uotLegacy = "sp.udp-over-tcp.arpa"    // version 1: every datagram has its address
	uotIdle   = 3 * time.Minute           // a UDP stream with no datagram either way for this long is closed
)

func uotVersion(host string) int {
	switch strings.ToLower(host) {
	case uotMagic:
		return 2
	case uotLegacy:
		return 1
	}
	return 0
}

// readUoTAddr reads a datagram's address in UoT's own form: family 0 IPv4,
// 1 IPv6, 2 a name (length first), then the port.
func readUoTAddr(r io.Reader) (string, int, error) {
	var t [1]byte
	if _, err := io.ReadFull(r, t[:]); err != nil {
		return "", 0, err
	}
	var host string
	switch t[0] {
	case 0, 1:
		n := 4
		if t[0] == 1 {
			n = 16
		}
		ip := make([]byte, n)
		if _, err := io.ReadFull(r, ip); err != nil {
			return "", 0, err
		}
		host = net.IP(ip).String()
	case 2:
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return "", 0, err
		}
		name := make([]byte, l[0])
		if _, err := io.ReadFull(r, name); err != nil {
			return "", 0, err
		}
		host = string(name)
	default:
		return "", 0, fmt.Errorf("uot: unknown address family %d", t[0])
	}
	var p [2]byte
	if _, err := io.ReadFull(r, p[:]); err != nil {
		return "", 0, err
	}
	return host, int(binary.BigEndian.Uint16(p[:])), nil
}

// appendUoTAddr appends a datagram's address in UoT's own form.
func appendUoTAddr(b []byte, host string, port int) []byte {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			b = append(append(b, 0), v4...)
		} else {
			b = append(append(b, 1), ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			host = host[:255]
		}
		b = append(append(b, 2, byte(len(host))), host...)
	}
	return binary.BigEndian.AppendUint16(b, uint16(port))
}

func (s *Server) uot(c *conn, nch ssh.NewChannel, version int) {
	ctx, cancel := context.WithTimeout(context.Background(), s.dialTTL)
	relay, err := socksAssociate(ctx, s.cfg.Upstream)
	cancel()
	if err != nil {
		nch.Reject(ssh.ConnectionFailed, "connect failed")
		return
	}
	ch, reqs, err := nch.Accept()
	if err != nil {
		relay.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	defer ch.Close()
	defer relay.Close()

	br := bufio.NewReaderSize(ch, 64<<10)
	connect, dHost, dPort := false, "", 0
	if version == 2 {
		var b [1]byte
		if _, err := io.ReadFull(br, b[:]); err != nil {
			return
		}
		connect = b[0] != 0
		if dHost, dPort, err = readSocksAddr(br); err != nil {
			return
		}
	}
	var last atomic.Int64
	active := func() { last.Store(time.Now().Unix()); c.touch() }
	active()
	done := make(chan struct{})
	defer close(done)
	go func() { // closes a stream nobody uses any more
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-c.gone:
				relay.Close()
				ch.Close()
				return
			case <-t.C:
				if time.Since(time.Unix(last.Load(), 0)) > uotIdle {
					relay.Close()
					ch.Close()
					return
				}
			}
		}
	}()

	go func() { // app → internet
		defer relay.Close()
		buf := make([]byte, 65535)
		for {
			host, port := dHost, dPort
			if !connect {
				if host, port, err = readUoTAddr(br); err != nil {
					return
				}
			}
			var l [2]byte
			if _, err := io.ReadFull(br, l[:]); err != nil {
				return
			}
			p := buf[:binary.BigEndian.Uint16(l[:])]
			if _, err := io.ReadFull(br, p); err != nil {
				return
			}
			active()
			if port == 0 || forbidden(host) {
				continue
			}
			c.cnt.up.Add(int64(len(p)))
			relay.WriteTo(p, host, port)
		}
	}()

	buf := make([]byte, 65535+262)
	out := make([]byte, 0, 65535+262)
	for { // internet → app
		p, host, port, err := relay.ReadFrom(buf)
		if err != nil {
			return
		}
		out = out[:0]
		if !connect {
			out = appendUoTAddr(out, host, port)
		}
		out = binary.BigEndian.AppendUint16(out, uint16(len(p)))
		out = append(out, p...)
		if _, err := ch.Write(out); err != nil {
			return
		}
		c.cnt.down.Add(int64(len(p)))
		active()
	}
}
