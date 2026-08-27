//go:build linux

package monitor

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func applyReleaseInfo(host *Host) {
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
}

func fillPlatformHost(dataPath string, host *Host) {
	var system syscall.Sysinfo_t
	if syscall.Sysinfo(&system) == nil {
		host.UptimeSeconds = uint64(system.Uptime)
	}
	if used, total, ok := readProcMeminfo(); ok {
		host.MemoryTotal = total
		host.MemoryUsed = used
	}
	var stat syscall.Statfs_t
	if syscall.Statfs(dataPath, &stat) == nil {
		host.DiskTotal = stat.Blocks * uint64(stat.Bsize)
		host.DiskUsed = (stat.Blocks - stat.Bavail) * uint64(stat.Bsize)
	}
	host.NetworkRX, host.NetworkTX = readNetwork()
	if percent, ok := cpuPercent(); ok {
		host.CPUPercent = percent
	}
}

func (s *Service) systemStats() SystemStats {
	var stats SystemStats
	stats.CPUs = runtime.NumCPU()
	var sys syscall.Sysinfo_t
	if syscall.Sysinfo(&sys) == nil {
		uptime := uint64(sys.Uptime)
		stats.UptimeSeconds = &uptime
	}
	if used, total, ok := readProcMeminfo(); ok {
		stats.MemoryUsed = &used
		stats.MemoryTotal = &total
	}
	var stat syscall.Statfs_t
	if syscall.Statfs(s.dataPath, &stat) == nil {
		diskTotal := stat.Blocks * uint64(stat.Bsize)
		diskUsed := (stat.Blocks - stat.Bavail) * uint64(stat.Bsize)
		stats.DiskUsed = &diskUsed
		stats.DiskTotal = &diskTotal
	}
	if percent, ok := cpuPercent(); ok {
		stats.CPUPercent = &percent
	}
	return stats
}

func readProcMeminfo() (int64, int64, bool) {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	values := map[string]int64{}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			value, _ := strconv.ParseInt(fields[1], 10, 64)
			values[strings.TrimSuffix(fields[0], ":")] = value * 1024
		}
	}
	total := values["MemTotal"]
	if total <= 0 {
		return 0, 0, false
	}
	return total - values["MemAvailable"], total, true
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

func cpuPercent() (float64, bool) {
	firstIdle, firstTotal := cpuSample()
	time.Sleep(100 * time.Millisecond)
	idle, total := cpuSample()
	if total <= firstTotal {
		return 0, false
	}
	return (1 - float64(idle-firstIdle)/float64(total-firstTotal)) * 100, true
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
