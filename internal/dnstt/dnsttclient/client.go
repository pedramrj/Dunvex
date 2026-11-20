// Package dnsttclient is the client end of dnstt (by David Fifield, CC0 1.0),
// cut down to plain UDP DNS: it opens a tunnel session through a resolver (or
// straight to the server) and gives out streams that the server connects to
// its upstream. Dunvex uses it to test its dnstt tunnels end to end.
package dnsttclient

import (
	"fmt"
	"net"
	"time"

	"github.com/xtaci/kcp-go/v5"
	"github.com/xtaci/smux"
	"www.bamsoftware.com/git/dnstt.git/dns"
	"www.bamsoftware.com/git/dnstt.git/noise"
	"www.bamsoftware.com/git/dnstt.git/turbotunnel"
)

// Logf receives the client's log lines (nil: dropped).
var Logf func(format string, v ...any)

func logf(format string, v ...any) {
	if Logf != nil {
		Logf(format, v...)
	}
}

// smux streams are closed after this much time without receiving data.
const idleTimeout = 2 * time.Minute

// HandshakeTimeout bounds Dial's wait for the server's handshake.
var HandshakeTimeout = 30 * time.Second

// Client is one tunnel session.
type Client struct {
	pconn net.PacketConn
	conn  *kcp.UDPSession
	sess  *smux.Session
}

// Dial opens a session for the tunnel domain through resolver (host:port, UDP),
// checking the server against its public key (hex).
func Dial(resolver, domain, pubkey string) (*Client, error) {
	key, err := noise.DecodeKey(pubkey)
	if err != nil {
		return nil, fmt.Errorf("public key: %v", err)
	}
	name, err := dns.ParseName(domain)
	if err != nil {
		return nil, fmt.Errorf("domain %q: %v", domain, err)
	}
	mtu := dnsNameCapacity(name) - 8 - 1 - numPadding - 1 // clientid + padding length prefix + padding + data length prefix
	if mtu < 80 {
		return nil, fmt.Errorf("domain %s leaves only %d bytes for payload", domain, mtu)
	}
	addr, err := net.ResolveUDPAddr("udp", resolver)
	if err != nil {
		return nil, err
	}
	udp, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	pconn := NewDNSPacketConn(udp, addr, name)
	conn, err := kcp.NewConn2(addr, nil, 0, 0, pconn)
	if err != nil {
		pconn.Close()
		return nil, fmt.Errorf("opening KCP conn: %v", err)
	}
	conn.SetStreamMode(true)
	conn.SetNoDelay(0, 0, 0, 1) // congestion window off, as dnstt does
	conn.SetWindowSize(turbotunnel.QueueSize/2, turbotunnel.QueueSize/2)
	if !conn.SetMtu(mtu) {
		conn.Close()
		pconn.Close()
		return nil, fmt.Errorf("cannot set MTU %d", mtu)
	}
	// a server with another key never answers the handshake
	conn.SetReadDeadline(time.Now().Add(HandshakeTimeout))
	rw, err := noise.NewClient(conn, key)
	if err != nil {
		conn.Close()
		pconn.Close()
		return nil, fmt.Errorf("tunnel handshake: %v", err)
	}
	conn.SetReadDeadline(time.Time{})
	cfg := smux.DefaultConfig()
	cfg.Version = 2
	cfg.KeepAliveTimeout = idleTimeout
	cfg.MaxStreamBuffer = 1 * 1024 * 1024
	sess, err := smux.Client(rw, cfg)
	if err != nil {
		conn.Close()
		pconn.Close()
		return nil, fmt.Errorf("opening smux session: %v", err)
	}
	return &Client{pconn: pconn, conn: conn, sess: sess}, nil
}

// Open starts a stream; the server connects it to its upstream.
func (c *Client) Open() (net.Conn, error) { return c.sess.OpenStream() }

// Close ends the session.
func (c *Client) Close() error {
	c.sess.Close()
	c.conn.Close()
	return c.pconn.Close()
}

// dnsNameCapacity returns the number of bytes remaining for encoded data after
// including domain in a DNS name (from dnstt-client/main.go).
func dnsNameCapacity(domain dns.Name) int {
	// Names must be 255 octets or shorter in total length.
	capacity := 255
	// Subtract the length of the null terminator.
	capacity -= 1
	for _, label := range domain {
		// Subtract the length of the label and the length octet.
		capacity -= len(label) + 1
	}
	// Each label may be up to 63 bytes long and requires 64 bytes to encode.
	capacity = capacity * 63 / 64
	// Base32 expands every 5 bytes to 8.
	capacity = capacity * 5 / 8
	return capacity
}
