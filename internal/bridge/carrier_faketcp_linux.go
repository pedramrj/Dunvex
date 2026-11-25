//go:build linux

package bridge

// The fake-TCP carrier, paqet-style (net4people #570): we build our own TCP
// packets on a raw socket instead of using the kernel's stack, so stateful DPI
// that only reassembles real TCP flows sees ordinary-looking PSH/ACK segments
// and nothing to lock onto. The kernel, which never opened these "connections",
// would answer every segment with a RST and tear them down, so a firewall rule
// drops the kernel's RSTs on the carrier port (the carrier adds it on start,
// best effort; the installer/test sets it authoritatively).
//
// Prior art read for the packet shape: github.com/hanselime/paqet (read-only).
// Reliability is not our concern here — kcp above handles loss and ordering —
// so the sequence numbers only have to look monotonic and plausible.
//
// Raw sockets need root (CAP_NET_RAW).

import (
	"encoding/binary"
	"math/rand"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

type faketcpCarrier struct {
	client   bool
	port     int // the carrier port (both sides)
	targetIP net.IP

	sendFD int
	recvFD int

	srcIP  atomic.Pointer[net.IP]
	peerIP atomic.Pointer[net.IP]
	srcPrt int // our tcp source port (client); server uses port
	dstPrt int // peer tcp port

	seq  atomic.Uint32
	ack  atomic.Uint32
	up   atomic.Bool
	recv func([]byte)

	ruleArgs [][]string // firewall rules we added, to remove on close
	closed   chan struct{}
	once     sync.Once
}

// NewFakeTCPClient makes the target side; targetIP is the relay's IP.
func NewFakeTCPClient(targetIP string, port int) (*faketcpCarrier, error) {
	ip := net.ParseIP(targetIP)
	if ip == nil {
		return nil, &net.AddrError{Err: "bad relay IP for the fake-TCP carrier", Addr: targetIP}
	}
	c := &faketcpCarrier{client: true, port: port, targetIP: ip.To4(), srcPrt: 1024 + rand.Intn(60000), dstPrt: port, closed: make(chan struct{})}
	p := ip.To4()
	c.peerIP.Store(&p)
	c.seq.Store(rand.Uint32())
	return c, nil
}

// NewFakeTCPServer makes the Iran relay side, bound to port.
func NewFakeTCPServer(port int) *faketcpCarrier {
	c := &faketcpCarrier{port: port, srcPrt: port, closed: make(chan struct{})}
	c.seq.Store(rand.Uint32())
	return c
}

func (c *faketcpCarrier) Name() string { return "faketcp" }
func (c *faketcpCarrier) Up() bool     { return c.up.Load() }

func (c *faketcpCarrier) Start(recv func([]byte)) error {
	c.recv = recv
	sfd, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_RAW)
	if err != nil {
		return err
	}
	if err := unix.SetsockoptInt(sfd, unix.IPPROTO_IP, unix.IP_HDRINCL, 1); err != nil {
		unix.Close(sfd)
		return err
	}
	rfd, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_TCP)
	if err != nil {
		unix.Close(sfd)
		return err
	}
	c.sendFD, c.recvFD = sfd, rfd
	c.dropKernelRST()
	c.up.Store(true)
	if c.client {
		c.handshake()
	}
	go c.read()
	return nil
}

// dropKernelRST stops the kernel from RSTing the carrier's segments. It tries
// nft, then iptables; failures are ignored (the installer/test also sets it).
func (c *faketcpCarrier) dropKernelRST() {
	port := itoa(c.port)
	try := func(name string, args ...string) bool {
		if _, err := exec.LookPath(name); err != nil {
			return false
		}
		return exec.Command(name, args...).Run() == nil
	}
	if try("nft", "add", "table", "ip", "dvxbridge") {
		try("nft", "add", "chain", "ip", "dvxbridge", "out", "{ type filter hook output priority 0; }")
		if exec.Command("nft", "add", "rule", "ip", "dvxbridge", "out", "tcp", "sport", port, "tcp", "flags", "rst", "drop").Run() == nil {
			c.ruleArgs = append(c.ruleArgs, []string{"nft"})
		}
		exec.Command("nft", "add", "rule", "ip", "dvxbridge", "out", "tcp", "dport", port, "tcp", "flags", "rst", "drop").Run()
		return
	}
	if exec.Command("iptables", "-I", "OUTPUT", "-p", "tcp", "--sport", port, "--tcp-flags", "RST", "RST", "-j", "DROP").Run() == nil {
		c.ruleArgs = append(c.ruleArgs, []string{"iptables", "-D", "OUTPUT", "-p", "tcp", "--sport", port, "--tcp-flags", "RST", "RST", "-j", "DROP"})
	}
	if exec.Command("iptables", "-I", "OUTPUT", "-p", "tcp", "--dport", port, "--tcp-flags", "RST", "RST", "-j", "DROP").Run() == nil {
		c.ruleArgs = append(c.ruleArgs, []string{"iptables", "-D", "OUTPUT", "-p", "tcp", "--dport", port, "--tcp-flags", "RST", "RST", "-j", "DROP"})
	}
}

// handshake sends a token SYN/ACK dance so the flow looks established.
func (c *faketcpCarrier) handshake() {
	c.sendSeg(flagSYN, nil)
}

const (
	flagFIN = 0x01
	flagSYN = 0x02
	flagRST = 0x04
	flagPSH = 0x08
	flagACK = 0x10
)

func (c *faketcpCarrier) read() {
	buf := make([]byte, 1<<16)
	for {
		n, from, err := unix.Recvfrom(c.recvFD, buf, 0)
		if err != nil {
			select {
			case <-c.closed:
				return
			default:
			}
			if err == unix.EINTR {
				continue
			}
			c.up.Store(false)
			return
		}
		if n < 40 {
			continue
		}
		ihl := int(buf[0]&0x0f) * 4
		if ihl < 20 || n < ihl+20 {
			continue
		}
		tcp := buf[ihl:n]
		srcPort := int(binary.BigEndian.Uint16(tcp[0:2]))
		dstPort := int(binary.BigEndian.Uint16(tcp[2:4]))
		flags := tcp[13]
		if !c.mine(srcPort, dstPort) {
			continue
		}
		var peer net.IP
		if sa, ok := from.(*unix.SockaddrInet4); ok {
			ip := net.IPv4(sa.Addr[0], sa.Addr[1], sa.Addr[2], sa.Addr[3])
			peer = ip.To4()
			c.peerIP.Store(&peer)
		}
		dataOff := int(tcp[12]>>4) * 4
		if dataOff < 20 || dataOff > len(tcp) {
			continue
		}
		seq := binary.BigEndian.Uint32(tcp[4:8])
		c.ack.Store(seq + uint32(len(tcp)-dataOff))
		if flags&flagSYN != 0 { // SYN or SYN-ACK: answer, learn the peer
			if !c.client {
				c.srcPrt = c.port
				c.dstPrt = srcPort
				c.sendSeg(flagSYN|flagACK, nil)
			} else {
				c.sendSeg(flagACK, nil)
			}
			continue
		}
		payload := tcp[dataOff:]
		if len(payload) <= 1 {
			continue
		}
		if c.recv != nil {
			b := make([]byte, len(payload))
			copy(b, payload)
			c.recv(b)
		}
	}
}

// mine reports whether a segment's ports belong to this carrier.
func (c *faketcpCarrier) mine(srcPort, dstPort int) bool {
	if c.client {
		return dstPort == c.srcPrt && srcPort == c.port
	}
	return dstPort == c.port
}

func (c *faketcpCarrier) Send(frame []byte) error {
	if !c.up.Load() {
		return net.ErrClosed
	}
	c.sendSeg(flagPSH|flagACK, frame)
	return nil
}

func (c *faketcpCarrier) sendSeg(flags byte, payload []byte) {
	peer := c.peerIP.Load()
	if peer == nil {
		return
	}
	dst := *peer
	src := c.localIP(dst)
	sp, dp := c.srcPrt, c.dstPrt
	if !c.client && dp == 0 {
		return // no peer port learned yet
	}
	seq := c.seq.Add(uint32(len(payload)))
	pkt := buildIPv4TCP(src, dst, sp, dp, seq-uint32(len(payload)), c.ack.Load(), flags, payload)
	var a [4]byte
	copy(a[:], dst.To4())
	_ = unix.Sendto(c.sendFD, pkt, 0, &unix.SockaddrInet4{Addr: a})
}

// localIP finds our source IP toward dst (cached once learned).
func (c *faketcpCarrier) localIP(dst net.IP) net.IP {
	if p := c.srcIP.Load(); p != nil {
		return *p
	}
	ip := routeSrc(dst)
	c.srcIP.Store(&ip)
	return ip
}

func (c *faketcpCarrier) Close() error {
	c.once.Do(func() {
		c.up.Store(false)
		close(c.closed)
		for _, r := range c.ruleArgs {
			if r[0] == "nft" {
				exec.Command("nft", "delete", "table", "ip", "dvxbridge").Run()
			} else {
				exec.Command(r[0], r[1:]...).Run()
			}
		}
	})
	if c.sendFD != 0 {
		unix.Close(c.sendFD)
	}
	if c.recvFD != 0 {
		unix.Close(c.recvFD)
	}
	return nil
}

// routeSrc asks the kernel which source IP it would use for dst.
func routeSrc(dst net.IP) net.IP {
	c, err := net.Dial("udp", net.JoinHostPort(dst.String(), "9"))
	if err != nil {
		return net.IPv4zero
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.To4()
}

func itoa(n int) string { return strconv.Itoa(n) }

// buildIPv4TCP crafts a complete IPv4+TCP packet (IP_HDRINCL) with both
// checksums filled.
func buildIPv4TCP(src, dst net.IP, srcPort, dstPort int, seq, ack uint32, flags byte, payload []byte) []byte {
	src4, dst4 := src.To4(), dst.To4()
	ihl, thl := 20, 20
	total := ihl + thl + len(payload)
	pkt := make([]byte, total)

	// IP header
	pkt[0] = 0x45 // version 4, IHL 5
	binary.BigEndian.PutUint16(pkt[2:4], uint16(total))
	binary.BigEndian.PutUint16(pkt[4:6], uint16(rand.Uint32())) // id
	pkt[6] = 0x40                                               // don't fragment
	pkt[8] = 64                                                 // ttl
	pkt[9] = unix.IPPROTO_TCP
	copy(pkt[12:16], src4)
	copy(pkt[16:20], dst4)
	binary.BigEndian.PutUint16(pkt[10:12], checksum(pkt[:ihl]))

	// TCP header
	t := pkt[ihl:]
	binary.BigEndian.PutUint16(t[0:2], uint16(srcPort))
	binary.BigEndian.PutUint16(t[2:4], uint16(dstPort))
	binary.BigEndian.PutUint32(t[4:8], seq)
	binary.BigEndian.PutUint32(t[8:12], ack)
	t[12] = byte(thl/4) << 4 // data offset
	t[13] = flags
	binary.BigEndian.PutUint16(t[14:16], 0xffff) // window
	copy(t[ihl:], payload)

	// TCP checksum over the pseudo-header + segment
	pseudo := make([]byte, 12+thl+len(payload))
	copy(pseudo[0:4], src4)
	copy(pseudo[4:8], dst4)
	pseudo[9] = unix.IPPROTO_TCP
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(thl+len(payload)))
	copy(pseudo[12:], t)
	binary.BigEndian.PutUint16(t[16:18], checksum(pseudo))
	return pkt
}

// checksum is the Internet checksum (RFC 1071).
func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
