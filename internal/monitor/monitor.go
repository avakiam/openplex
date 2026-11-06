// Package monitor samples host telemetry on an interval, keeps a short
// in-memory history and fans samples out to live subscribers.
package monitor

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
)

// Sample is one host measurement.
type Sample struct {
	Time       time.Time `json:"time"`
	CPUPercent float64   `json:"cpu_percent"`
	Load1      float64   `json:"load1"`
	Load5      float64   `json:"load5"`
	Load15     float64   `json:"load15"`
	MemTotal   uint64    `json:"mem_total"`
	MemUsed    uint64    `json:"mem_used"`
	SwapTotal  uint64    `json:"swap_total"`
	SwapUsed   uint64    `json:"swap_used"`
	DiskTotal  uint64    `json:"disk_total"`
	DiskUsed   uint64    `json:"disk_used"`
	NetRxRate  float64   `json:"net_rx_rate"` // bytes/s
	NetTxRate  float64   `json:"net_tx_rate"`
	UptimeSecs uint64    `json:"uptime_secs"`
}

// HostInfo is static host information.
type HostInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Platform string `json:"platform"`
	Version  string `json:"platform_version"`
	Kernel   string `json:"kernel"`
	Arch     string `json:"arch"`
	CPUs     int    `json:"cpus"`
	CPUModel string `json:"cpu_model"`
}

// Sampler collects samples.
type Sampler struct {
	interval time.Duration
	diskPath string
	log      *slog.Logger

	mu      sync.RWMutex
	history []Sample // ring buffer
	next    int
	full    bool
	subs    map[chan Sample]struct{}

	lastRx, lastTx uint64
	lastNet        time.Time
}

// New creates a sampler keeping size samples taken every interval.
func New(interval time.Duration, size int, diskPath string, log *slog.Logger) *Sampler {
	return &Sampler{interval: interval, diskPath: diskPath, log: log, history: make([]Sample, size), subs: map[chan Sample]struct{}{}}
}

// Run samples until ctx is done.
func (s *Sampler) Run(ctx context.Context) {
	_, _ = cpu.PercentWithContext(ctx, 0, false) // prime the CPU delta
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			smp := s.collect(ctx)
			s.publish(smp)
		}
	}
}

func (s *Sampler) collect(ctx context.Context) Sample {
	smp := Sample{Time: time.Now().UTC()}
	if p, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(p) > 0 {
		smp.CPUPercent = p[0]
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		smp.Load1, smp.Load5, smp.Load15 = l.Load1, l.Load5, l.Load15
	}
	if m, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		smp.MemTotal, smp.MemUsed = m.Total, m.Total-m.Available
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil {
		smp.SwapTotal, smp.SwapUsed = sw.Total, sw.Used
	}
	if d, err := disk.UsageWithContext(ctx, s.diskPath); err == nil {
		smp.DiskTotal, smp.DiskUsed = d.Total, d.Used
	}
	if up, err := host.UptimeWithContext(ctx); err == nil {
		smp.UptimeSecs = up
	}
	if n, err := psnet.IOCountersWithContext(ctx, false); err == nil && len(n) > 0 {
		if !s.lastNet.IsZero() {
			dt := smp.Time.Sub(s.lastNet).Seconds()
			if dt > 0 && n[0].BytesRecv >= s.lastRx && n[0].BytesSent >= s.lastTx {
				smp.NetRxRate = float64(n[0].BytesRecv-s.lastRx) / dt
				smp.NetTxRate = float64(n[0].BytesSent-s.lastTx) / dt
			}
		}
		s.lastRx, s.lastTx, s.lastNet = n[0].BytesRecv, n[0].BytesSent, smp.Time
	}
	return smp
}

func (s *Sampler) publish(smp Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history[s.next] = smp
	s.next = (s.next + 1) % len(s.history)
	if s.next == 0 {
		s.full = true
	}
	for ch := range s.subs {
		select {
		case ch <- smp:
		default: // slow subscriber: drop sample rather than block sampling
		}
	}
}

// History returns samples oldest first.
func (s *Sampler) History() []Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.full {
		return append([]Sample(nil), s.history[:s.next]...)
	}
	out := make([]Sample, 0, len(s.history))
	out = append(out, s.history[s.next:]...)
	return append(out, s.history[:s.next]...)
}

// Subscribe returns a channel of new samples and a cancel func.
func (s *Sampler) Subscribe() (<-chan Sample, func()) {
	ch := make(chan Sample, 4)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}
}

// Info returns static host information.
func Info(ctx context.Context) HostInfo {
	var hi HostInfo
	if h, err := host.InfoWithContext(ctx); err == nil {
		hi.Hostname, hi.OS, hi.Platform, hi.Version, hi.Kernel, hi.Arch = h.Hostname, h.OS, h.Platform, h.PlatformVersion, h.KernelVersion, h.KernelArch
	}
	if n, err := cpu.CountsWithContext(ctx, true); err == nil {
		hi.CPUs = n
	}
	if c, err := cpu.InfoWithContext(ctx); err == nil && len(c) > 0 {
		hi.CPUModel = c[0].ModelName
	}
	return hi
}
