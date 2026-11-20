// Package sysinfo samples host metrics for the dashboard.
package sysinfo

import (
	"context"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
)

// Snapshot is the live state of the host.
type Snapshot struct {
	Hostname  string    `json:"hostname"`
	OS        string    `json:"os"`
	Arch      string    `json:"arch"`
	Cores     int       `json:"cores"`
	CPU       float64   `json:"cpu"`
	Load      []float64 `json:"load"`
	MemTotal  uint64    `json:"memTotal"`
	MemUsed   uint64    `json:"memUsed"`
	SwapTotal uint64    `json:"swapTotal"`
	SwapUsed  uint64    `json:"swapUsed"`
	DiskTotal uint64    `json:"diskTotal"`
	DiskUsed  uint64    `json:"diskUsed"`
	Uptime    uint64    `json:"uptime"`
	NetUp     uint64    `json:"netUp"`   // bytes/s
	NetDown   uint64    `json:"netDown"` // bytes/s
	NetSent   uint64    `json:"netSent"` // total since boot
	NetRecv   uint64    `json:"netRecv"`
	TCP       int       `json:"tcp"`
	UDP       int       `json:"udp"`
	PublicIP  string    `json:"publicIp"`
}

// Sampler keeps a fresh snapshot updated every few seconds.
type Sampler struct {
	mu       sync.RWMutex
	snap     Snapshot
	history  []Point
	publicIP string
}

// Point is a CPU/RAM/network history sample.
type Point struct {
	T    int64   `json:"t"`
	CPU  float64 `json:"cpu"`
	Mem  float64 `json:"mem"`
	Up   uint64  `json:"up"`
	Down uint64  `json:"down"`
}

// NewSampler starts sampling in the background.
func NewSampler() *Sampler {
	s := &Sampler{}
	go s.loop()
	go func() { s.publicIP = DetectPublicIP() }()
	return s
}

func (s *Sampler) loop() {
	var lastSent, lastRecv uint64
	last := time.Now()
	for {
		snap := Snapshot{OS: runtime.GOOS, Arch: runtime.GOARCH, Cores: runtime.NumCPU()}
		if hi, err := host.Info(); err == nil {
			snap.Hostname, snap.Uptime = hi.Hostname, hi.Uptime
			if hi.Platform != "" {
				snap.OS = hi.Platform + " " + hi.PlatformVersion
			}
		}
		if p, err := cpu.Percent(time.Second, false); err == nil && len(p) > 0 {
			snap.CPU = p[0]
		}
		if l, err := load.Avg(); err == nil {
			snap.Load = []float64{l.Load1, l.Load5, l.Load15}
		}
		if vm, err := mem.VirtualMemory(); err == nil {
			snap.MemTotal, snap.MemUsed = vm.Total, vm.Used
		}
		if sw, err := mem.SwapMemory(); err == nil {
			snap.SwapTotal, snap.SwapUsed = sw.Total, sw.Used
		}
		root := "/"
		if runtime.GOOS == "windows" {
			root = "C:"
		}
		if du, err := disk.Usage(root); err == nil {
			snap.DiskTotal, snap.DiskUsed = du.Total, du.Used
		}
		if io, err := psnet.IOCounters(false); err == nil && len(io) > 0 {
			now := time.Now()
			dt := now.Sub(last).Seconds()
			if lastSent > 0 && dt > 0 && io[0].BytesSent >= lastSent && io[0].BytesRecv >= lastRecv {
				snap.NetUp = uint64(float64(io[0].BytesSent-lastSent) / dt)
				snap.NetDown = uint64(float64(io[0].BytesRecv-lastRecv) / dt)
			}
			lastSent, lastRecv, last = io[0].BytesSent, io[0].BytesRecv, now
			snap.NetSent, snap.NetRecv = io[0].BytesSent, io[0].BytesRecv
		}
		if runtime.GOOS == "linux" {
			if c, err := psnet.Connections("tcp"); err == nil {
				snap.TCP = len(c)
			}
			if c, err := psnet.Connections("udp"); err == nil {
				snap.UDP = len(c)
			}
		}
		s.mu.Lock()
		snap.PublicIP = s.publicIP
		s.snap = snap
		var memPct float64
		if snap.MemTotal > 0 {
			memPct = float64(snap.MemUsed) * 100 / float64(snap.MemTotal)
		}
		s.history = append(s.history, Point{T: time.Now().Unix(), CPU: snap.CPU, Mem: memPct, Up: snap.NetUp, Down: snap.NetDown})
		if len(s.history) > 720 {
			s.history = s.history[len(s.history)-720:]
		}
		s.mu.Unlock()
		time.Sleep(4 * time.Second)
	}
}

// Get returns the latest snapshot.
func (s *Sampler) Get() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// History returns recent samples (5 s apart, up to 1 hour).
func (s *Sampler) History() []Point {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Point(nil), s.history...)
}

// PublicIP returns the detected public IPv4 (may be empty).
func (s *Sampler) PublicIP() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.publicIP
}

// DetectPublicIP asks a few public services, falling back to the outbound interface address.
func DetectPublicIP() string {
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
		},
	}}
	for _, u := range []string{"https://api.ipify.org", "https://ipv4.icanhazip.com", "https://ifconfig.me/ip", "https://checkip.amazonaws.com"} {
		resp, err := client.Get(u)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		ip := strings.TrimSpace(string(b))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	if c, err := net.Dial("udp4", "1.1.1.1:53"); err == nil {
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).IP.String()
	}
	return ""
}
