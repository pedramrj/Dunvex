package sshproxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// A small SOCKS5 client (RFC 1928, no sign-in): CONNECT for TCP and UDP
// ASSOCIATE for UDP, against Xray's SOCKS inbound on loopback.

// socksAddr appends a SOCKS address (ATYP, address, port).
func socksAddr(b []byte, host string, port int) ([]byte, error) {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			b = append(append(b, 1), v4...)
		} else {
			b = append(append(b, 4), ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, errors.New("name too long")
		}
		b = append(append(b, 3, byte(len(host))), host...)
	}
	return binary.BigEndian.AppendUint16(b, uint16(port)), nil
}

// readSocksAddr reads a SOCKS address (ATYP, address, port).
func readSocksAddr(r io.Reader) (string, int, error) {
	var t [1]byte
	if _, err := io.ReadFull(r, t[:]); err != nil {
		return "", 0, err
	}
	var host string
	switch t[0] {
	case 1, 4:
		n := 4
		if t[0] == 4 {
			n = 16
		}
		ip := make([]byte, n)
		if _, err := io.ReadFull(r, ip); err != nil {
			return "", 0, err
		}
		host = net.IP(ip).String()
	case 3:
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
		return "", 0, fmt.Errorf("unknown address type %d", t[0])
	}
	var p [2]byte
	if _, err := io.ReadFull(r, p[:]); err != nil {
		return "", 0, err
	}
	return host, int(binary.BigEndian.Uint16(p[:])), nil
}

// socksRequest opens a connection to the SOCKS server and sends one request
// (cmd 1 CONNECT, 3 UDP ASSOCIATE); it returns the connection and the bound address.
func socksRequest(ctx context.Context, server string, cmd byte, host string, port int) (net.Conn, string, int, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", server)
	if err != nil {
		return nil, "", 0, err
	}
	if dl, ok := ctx.Deadline(); ok {
		c.SetDeadline(dl)
	} else {
		c.SetDeadline(time.Now().Add(15 * time.Second))
	}
	fail := func(err error) (net.Conn, string, int, error) { c.Close(); return nil, "", 0, err }
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		return fail(err)
	}
	var hello [2]byte
	if _, err := io.ReadFull(c, hello[:]); err != nil {
		return fail(err)
	}
	if hello[0] != 5 || hello[1] != 0 {
		return fail(errors.New("socks: sign-in refused"))
	}
	req, err := socksAddr([]byte{5, cmd, 0}, host, port)
	if err != nil {
		return fail(err)
	}
	if _, err := c.Write(req); err != nil {
		return fail(err)
	}
	var head [3]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		return fail(err)
	}
	bh, bp, err := readSocksAddr(c)
	if err != nil {
		return fail(err)
	}
	if head[0] != 5 || head[1] != 0 {
		return fail(fmt.Errorf("socks: request failed (%d)", head[1]))
	}
	c.SetDeadline(time.Time{})
	return c, bh, bp, nil
}

// socksConnect opens a TCP connection to host:port through the SOCKS server.
func socksConnect(ctx context.Context, server, host string, port int) (net.Conn, error) {
	c, _, _, err := socksRequest(ctx, server, 1, host, port)
	return c, err
}

// udpRelay is one UDP association: datagrams to any address go out through
// the SOCKS server, and the answers come back.
type udpRelay struct {
	ctrl  net.Conn     // the association lasts as long as this connection
	pc    *net.UDPConn // not connected: Xray answers from another socket than the relay port
	relay *net.UDPAddr // where datagrams are sent
}

func socksAssociate(ctx context.Context, server string) (*udpRelay, error) {
	ctrl, bh, bp, err := socksRequest(ctx, server, 3, "0.0.0.0", 0)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(bh); ip == nil || ip.IsUnspecified() {
		bh, _, _ = net.SplitHostPort(server) // "this address": the server's own
	}
	relay := &net.UDPAddr{IP: net.ParseIP(bh), Port: bp}
	local := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
	if relay.IP.To4() == nil {
		local = &net.UDPAddr{IP: net.IPv6loopback}
	}
	if !relay.IP.IsLoopback() {
		local = nil
	}
	pc, err := net.ListenUDP("udp", local)
	if err != nil {
		ctrl.Close()
		return nil, err
	}
	r := &udpRelay{ctrl: ctrl, pc: pc, relay: relay}
	go func() { // the server ends the association by closing its connection
		io.Copy(io.Discard, ctrl)
		r.Close()
	}()
	return r, nil
}

func (r *udpRelay) Close() error {
	r.ctrl.Close()
	return r.pc.Close()
}

// WriteTo sends one datagram to host:port.
func (r *udpRelay) WriteTo(p []byte, host string, port int) error {
	b, err := socksAddr(make([]byte, 3, 3+1+255+2+len(p)), host, port)
	if err != nil {
		return err
	}
	_, err = r.pc.WriteToUDP(append(b, p...), r.relay)
	return err
}

// ReadFrom reads one datagram into buf: its payload and where it came from.
func (r *udpRelay) ReadFrom(buf []byte) ([]byte, string, int, error) {
	for {
		n, from, err := r.pc.ReadFromUDP(buf)
		if err != nil {
			return nil, "", 0, err
		}
		if !from.IP.Equal(r.relay.IP) { // only the SOCKS server answers here
			continue
		}
		pkt := buf[:n]
		if len(pkt) < 4 || pkt[2] != 0 { // a fragment: never sent by Xray, dropped
			continue
		}
		rd := &sliceReader{b: pkt[3:]}
		host, port, err := readSocksAddr(rd)
		if err != nil {
			continue
		}
		return rd.b, host, port, nil
	}
}

type sliceReader struct{ b []byte }

func (s *sliceReader) Read(p []byte) (int, error) {
	if len(s.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.b)
	s.b = s.b[n:]
	return n, nil
}

func joinHostPort(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }
