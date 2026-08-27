package monitor

import (
	"context"
	"errors"
	"os"
	"runtime"

	"github.com/suma/suma/server/internal/docker"
)

type Host struct {
	Hostname      string  `json:"hostname"`
	OS            string  `json:"os"`
	Kernel        string  `json:"kernel"`
	Architecture  string  `json:"architecture"`
	UptimeSeconds uint64  `json:"uptime_seconds"`
	CPUPercent    float64 `json:"cpu_percent"`
	CPUs          int     `json:"cpus"`
	MemoryUsed    int64   `json:"memory_used"`
	MemoryTotal   int64   `json:"memory_total"`
	DiskUsed      uint64  `json:"disk_used"`
	DiskTotal     uint64  `json:"disk_total"`
	NetworkRX     uint64  `json:"network_rx"`
	NetworkTX     uint64  `json:"network_tx"`
}
type Overview struct {
	Host   Host        `json:"host"`
	Docker docker.Info `json:"docker"`
}
type Service struct {
	engine   docker.Engine
	dataPath string
}

func NewService(engine docker.Engine, dataPath string) *Service {
	return &Service{engine: engine, dataPath: dataPath}
}
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	info, err := s.engine.Info(ctx)
	if err != nil {
		return Overview{}, err
	}
	host, err := readHost(s.dataPath)
	if err != nil {
		return Overview{}, err
	}
	if host.OS == "" {
		host.OS = info.OperatingSystem
	}
	return Overview{Host: host, Docker: info}, nil
}

// SystemStats carries host-wide metrics. Pointer fields distinguish "not
// measurable on this platform" (nil) from a genuine zero value.
type SystemStats struct {
	CPUs          int      `json:"cpus,omitempty"`
	UptimeSeconds *uint64  `json:"uptime_seconds,omitempty"`
	CPUPercent    *float64 `json:"cpu_percent,omitempty"`
	MemoryUsed    *int64   `json:"memory_used,omitempty"`
	MemoryTotal   *int64   `json:"memory_total,omitempty"`
	DiskUsed      *uint64  `json:"disk_used,omitempty"`
	DiskTotal     *uint64  `json:"disk_total,omitempty"`
}

// Snapshot reads host-wide metrics for the machine this process runs on.
// Availability depends on the OS: Linux exposes CPU, memory, disk and uptime;
// other Unix variants expose disk usage only; Windows currently exposes none.
func (s *Service) Snapshot() (SystemStats, error) {
	stats := s.systemStats()
	if stats.CPUs == 0 && stats.DiskTotal == nil && stats.MemoryTotal == nil {
		return stats, errors.New("host metrics unavailable on this platform")
	}
	return stats, nil
}

func readHost(dataPath string) (Host, error) {
	hostname, _ := os.Hostname()
	host := Host{Hostname: hostname, Architecture: runtime.GOARCH, CPUs: runtime.NumCPU()}
	applyReleaseInfo(&host)
	fillPlatformHost(dataPath, &host)
	return host, nil
}
