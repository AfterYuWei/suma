package monitor

import (
	"context"
	"runtime"
	"testing"

	"github.com/suma/suma/server/internal/docker"
)

type stubEngine struct{}

func (stubEngine) Ping(context.Context) error            { return nil }
func (stubEngine) Info(context.Context) (docker.Info, error) {
	return docker.Info{
		ID: "engine-1",
		Name: "suma-smoke",
		ServerVersion: "27.5.1",
		OperatingSystem: "Ubuntu 24.04 LTS",
		Containers: 5,
		Running: 3,
		Stopped: 2,
		Images: 12,
		CPUs: runtime.NumCPU(),
		MemoryBytes: 8 << 30,
	}, nil
}
func (stubEngine) Close() error { return nil }

func TestOverviewCombinesHostAndDocker(t *testing.T) {
	service := NewService(stubEngine{}, t.TempDir())
	overview, err := service.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if overview.Docker.ServerVersion != "27.5.1" || overview.Docker.Running != 3 || overview.Docker.Name != "suma-smoke" {
		t.Fatalf("docker info = %#v", overview.Docker)
	}
	if overview.Host.Architecture != runtime.GOARCH {
		t.Fatalf("architecture = %s, want %s", overview.Host.Architecture, runtime.GOARCH)
	}
	if overview.Host.CPUs != runtime.NumCPU() {
		t.Fatalf("cpus = %d, want %d", overview.Host.CPUs, runtime.NumCPU())
	}
	if overview.Host.Hostname == "" {
		t.Fatal("hostname is empty")
	}
}

// On Linux the release/os fields should be populated from /proc and
// /etc/os-release; on other platforms readHost leaves OS empty and Overview
// falls back to the Docker-reported operating system.
func TestOverviewFallsBackToDockerOSWhenHostOSEmpty(t *testing.T) {
	host, err := readHost(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if host.OS != "" {
		t.Skipf("host OS is available on this platform (%s); fallback not reachable", host.OS)
	}
	service := NewService(stubEngine{}, t.TempDir())
	overview, err := service.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if overview.Host.OS == "" {
		t.Fatal("overview did not fall back to Docker operating system")
	}
}

func TestSnapshotReportsLinuxMetrics(t *testing.T) {
	services := NewService(stubEngine{}, t.TempDir())
	stats, err := services.Snapshot()
	if err != nil && runtime.GOOS == "linux" {
		t.Fatalf("snapshot failed on linux: %v", err)
	}
	if err != nil {
		t.Skipf("host metrics unavailable on %s: %v", runtime.GOOS, err)
	}
	if stats.CPUs <= 0 {
		t.Fatalf("cpus = %d", stats.CPUs)
	}
	if stats.MemoryTotal == nil || *stats.MemoryTotal <= 0 {
		t.Fatalf("memory total = %v", stats.MemoryTotal)
	}
	if stats.UptimeSeconds == nil {
		t.Fatal("uptime seconds missing")
	}
}
