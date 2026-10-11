package node

import (
	"context"
	"errors"

	"github.com/suma/suma/server/internal/docker"
)

type OverviewResourceCounts struct {
	Networks int `json:"networks"`
	Volumes  int `json:"volumes"`
}

func (s *Service) OverviewInfo(ctx context.Context, id string) (docker.Info, error) {
	runtime, err := s.Runtime(ctx, id)
	if err != nil {
		return docker.Info{}, err
	}
	return runtime.Info(ctx)
}

func (s *Service) OverviewResources(ctx context.Context, id string) (OverviewResourceCounts, error) {
	runtime, err := s.Runtime(ctx, id)
	if err != nil {
		return OverviewResourceCounts{}, err
	}
	counts, err := runtime.ResourceCounts(ctx)
	return OverviewResourceCounts{Networks: counts.Networks, Volumes: counts.Volumes}, err
}

func (s *Service) OverviewDiskUsage(ctx context.Context, id string) (int64, error) {
	runtime, err := s.Runtime(ctx, id)
	if err != nil {
		return 0, err
	}
	return runtime.DiskUsage(ctx)
}

// OverviewMetrics returns only the fields rendered at this card size. Small
// cards need aggregate CPU/memory but no container inspection or detail rows.
func (s *Service) OverviewMetrics(ctx context.Context, id, size string) (map[string]any, error) {
	if size != "small" && size != "medium" && size != "large" {
		return nil, errors.New("invalid overview card size")
	}
	runtime, err := s.Runtime(ctx, id)
	if err != nil {
		return nil, err
	}
	samples, err := runtime.MetricsWithOptions(ctx, docker.MetricsOptions{IncludeUptime: size != "small"})
	if err != nil {
		return nil, err
	}
	result := map[string]any{"metrics_available": true}
	var cpu float64
	var memory, rx, tx, read, write uint64
	var longest int64
	containers := make([]map[string]any, 0, len(samples))
	for _, sample := range samples {
		if !sample.Available {
			result["metrics_available"] = false
		} else {
			cpu += sample.CPUPercent
			memory += sample.MemoryBytes
			rx += sample.NetworkRXBytes
			tx += sample.NetworkTXBytes
			read += sample.BlockReadBytes
			write += sample.BlockWriteBytes
			longest = max(longest, sample.UptimeSeconds)
		}
		if size == "small" {
			continue
		}
		row := map[string]any{"id": sample.ID, "name": sample.Name, "image": sample.Image, "state": sample.State,
			"available": sample.Available, "cpu_percent": sample.CPUPercent, "memory_bytes": sample.MemoryBytes}
		if size == "large" {
			row["network_rx_bytes"], row["network_tx_bytes"] = sample.NetworkRXBytes, sample.NetworkTXBytes
			row["block_read_bytes"], row["block_write_bytes"] = sample.BlockReadBytes, sample.BlockWriteBytes
			row["uptime_seconds"] = sample.UptimeSeconds
		}
		containers = append(containers, row)
	}
	result["container_cpu_percent"], result["container_memory_bytes"] = cpu, memory
	if size != "small" {
		result["containers"] = containers
		result["container_network_rx_bytes"], result["container_network_tx_bytes"] = rx, tx
		result["container_block_read_bytes"], result["container_block_write_bytes"] = read, write
		result["longest_container_uptime_seconds"] = longest
	}
	return result, nil
}
