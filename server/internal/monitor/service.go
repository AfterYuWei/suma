package monitor

import (
	"bufio"
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dockport/dockport/server/internal/docker"
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
func readHost(dataPath string) (Host, error) {
	hostname, _ := os.Hostname()
	host := Host{Hostname: hostname, Architecture: runtime.GOARCH, CPUs: runtime.NumCPU()}
	if value, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		host.Kernel = strings.TrimSpace(string(value))
	}
	if value, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(value), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				host.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
			}
		}
	}
	var system syscall.Sysinfo_t
	if syscall.Sysinfo(&system) == nil {
		host.UptimeSeconds = uint64(system.Uptime)
	}
	memory, _ := os.ReadFile("/proc/meminfo")
	values := map[string]int64{}
	for _, line := range strings.Split(string(memory), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			value, _ := strconv.ParseInt(fields[1], 10, 64)
			values[strings.TrimSuffix(fields[0], ":")] = value * 1024
		}
	}
	host.MemoryTotal = values["MemTotal"]
	host.MemoryUsed = host.MemoryTotal - values["MemAvailable"]
	var stat syscall.Statfs_t
	if syscall.Statfs(dataPath, &stat) == nil {
		host.DiskTotal = stat.Blocks * uint64(stat.Bsize)
		host.DiskUsed = (stat.Blocks - stat.Bavail) * uint64(stat.Bsize)
	}
	host.NetworkRX, host.NetworkTX = readNetwork()
	host.CPUPercent = readCPU()
	return host, nil
}
func readNetwork() (uint64, uint64) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	var rx, tx uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.Fields(strings.Replace(line, ":", " ", 1))
		if len(parts) < 10 || parts[0] == "lo" {
			continue
		}
		incoming, _ := strconv.ParseUint(parts[1], 10, 64)
		outgoing, _ := strconv.ParseUint(parts[9], 10, 64)
		rx += incoming
		tx += outgoing
	}
	return rx, tx
}
func readCPU() float64 {
	firstIdle, firstTotal := cpuSample()
	time.Sleep(100 * time.Millisecond)
	idle, total := cpuSample()
	if total <= firstTotal {
		return 0
	}
	return (1 - float64(idle-firstIdle)/float64(total-firstTotal)) * 100
}
func cpuSample() (uint64, uint64) {
	value, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(strings.SplitN(string(value), "\n", 2)[0])
	if len(fields) < 5 {
		return 0, 0
	}
	var total uint64
	for _, field := range fields[1:] {
		number, _ := strconv.ParseUint(field, 10, 64)
		total += number
	}
	idle, err := strconv.ParseUint(fields[4], 10, 64)
	if err != nil {
		return 0, 0
	}
	if len(fields) > 5 {
		wait, _ := strconv.ParseUint(fields[5], 10, 64)
		idle += wait
	}
	return idle, total
}
