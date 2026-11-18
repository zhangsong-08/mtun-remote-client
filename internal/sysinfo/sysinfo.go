// Package sysinfo 定期采集本机运行指标，供管理面板首页展示。
package sysinfo

import (
	"context"
	"log/slog"
	"net"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

type Snapshot struct {
	CPUPercent    float64   `json:"cpu_percent"`
	MemPercent    float64   `json:"mem_percent"`
	MemUsed       uint64    `json:"mem_used"`
	MemTotal      uint64    `json:"mem_total"`
	SwapPercent   float64   `json:"swap_percent"`
	SwapUsed      uint64    `json:"swap_used"`
	SwapTotal     uint64    `json:"swap_total"`
	DiskPercent   float64   `json:"disk_percent"`
	DiskUsed      uint64    `json:"disk_used"`
	DiskTotal     uint64    `json:"disk_total"`
	UptimeSeconds float64   `json:"uptime_seconds"`
	ProcessRSS    uint64    `json:"process_rss"`
	Threads       int       `json:"threads"`
	Goroutines    int       `json:"goroutines"`
	NetSent       uint64    `json:"net_sent"`
	NetRecv       uint64    `json:"net_recv"`
	NetUpBps      float64   `json:"net_up_bps"`
	NetDownBps    float64   `json:"net_down_bps"`
	IPv4          []string  `json:"ipv4"`
	IPv6          []string  `json:"ipv6"`
	TCPCount      int       `json:"tcp_count"`
	UDPCount      int       `json:"udp_count"`
	CollectedAt   time.Time `json:"-"`
}

type Collector struct {
	log *slog.Logger

	mu   sync.RWMutex
	snap Snapshot

	prevSent uint64
	prevRecv uint64
	prevAt   time.Time
}

func NewCollector(log *slog.Logger) *Collector {
	return &Collector{log: log}
}

// Run 每 3 秒采集一次，直到 ctx 结束。
func (c *Collector) Run(ctx context.Context) {
	c.collect()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect()
		}
	}
}

func (c *Collector) Get() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap
}

func (c *Collector) collect() {
	var s Snapshot

	if v, err := cpu.Percent(0, false); err == nil && len(v) > 0 {
		s.CPUPercent = round2(v[0])
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		s.MemPercent = round2(vm.UsedPercent)
		s.MemUsed = vm.Used
		s.MemTotal = vm.Total
	}
	if sw, err := mem.SwapMemory(); err == nil {
		s.SwapPercent = round2(sw.UsedPercent)
		s.SwapUsed = sw.Used
		s.SwapTotal = sw.Total
	}
	diskPath := "/"
	if runtime.GOOS == "windows" {
		diskPath = `C:\`
	}
	if du, err := disk.Usage(diskPath); err == nil {
		s.DiskPercent = round2(du.UsedPercent)
		s.DiskUsed = du.Used
		s.DiskTotal = du.Total
	}
	if up, err := host.Uptime(); err == nil {
		s.UptimeSeconds = float64(up)
	}

	if p, err := process.NewProcess(int32(os.Getpid())); err == nil {
		if mi, err := p.MemoryInfo(); err == nil && mi != nil {
			s.ProcessRSS = mi.RSS
		}
		if n, err := p.NumThreads(); err == nil {
			s.Threads = int(n)
		}
	}
	s.Goroutines = runtime.NumGoroutine()

	var sent, recv uint64
	if counters, err := gnet.IOCounters(false); err == nil && len(counters) > 0 {
		sent = counters[0].BytesSent
		recv = counters[0].BytesRecv
	}
	s.NetSent = sent
	s.NetRecv = recv
	now := time.Now()
	if !c.prevAt.IsZero() && now.After(c.prevAt) {
		secs := now.Sub(c.prevAt).Seconds()
		if sent >= c.prevSent {
			s.NetUpBps = round2(float64(sent-c.prevSent) / secs)
		}
		if recv >= c.prevRecv {
			s.NetDownBps = round2(float64(recv-c.prevRecv) / secs)
		}
	}
	c.prevSent, c.prevRecv, c.prevAt = sent, recv, now

	s.IPv4, s.IPv6 = localAddresses()

	if conns, err := gnet.Connections("tcp"); err == nil {
		s.TCPCount = len(conns)
	}
	if conns, err := gnet.Connections("udp"); err == nil {
		s.UDPCount = len(conns)
	}

	s.CollectedAt = now
	c.mu.Lock()
	c.snap = s
	c.mu.Unlock()
}

func localAddresses() ([]string, []string) {
	seen4 := map[string]bool{}
	seen6 := map[string]bool{}
	var v4, v6 []string
	ifaces, err := gnet.Interfaces()
	if err != nil {
		return nil, nil
	}
	for _, iface := range ifaces {
		if !hasFlag(iface.Flags, "up") || hasFlag(iface.Flags, "loopback") {
			continue
		}
		for _, addr := range iface.Addrs {
			ip, _, err := net.ParseCIDR(addr.Addr)
			if err != nil {
				continue
			}
			if ip.To4() != nil {
				if !seen4[ip.String()] {
					seen4[ip.String()] = true
					v4 = append(v4, ip.String())
				}
				continue
			}
			if ip.IsLinkLocalUnicast() {
				continue
			}
			if !seen6[ip.String()] {
				seen6[ip.String()] = true
				v6 = append(v6, ip.String())
			}
		}
	}
	sort.Strings(v4)
	sort.Strings(v6)
	return v4, v6
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
