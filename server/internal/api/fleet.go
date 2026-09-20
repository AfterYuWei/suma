package api

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	nodeService "github.com/suma/suma/server/internal/node"
)

type fleetContainer struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Image           string  `json:"image"`
	State           string  `json:"state"`
	Available       bool    `json:"available"`
	CPUPercent      float64 `json:"cpu_percent"`
	MemoryBytes     uint64  `json:"memory_bytes"`
	NetworkRXBytes  uint64  `json:"network_rx_bytes"`
	NetworkTXBytes  uint64  `json:"network_tx_bytes"`
	BlockReadBytes  uint64  `json:"block_read_bytes"`
	BlockWriteBytes uint64  `json:"block_write_bytes"`
	UptimeSeconds   int64   `json:"uptime_seconds"`
}

type fleetNode struct {
	ID                   string           `json:"id"`
	Name                 string           `json:"name"`
	ConnectionType       string           `json:"connection_type"`
	TLSMode              string           `json:"tls_mode"`
	Enabled              bool             `json:"enabled"`
	Status               string           `json:"status"`
	EngineVersion        string           `json:"engine_version,omitempty"`
	LastLatencyMS        int64            `json:"last_latency_ms,omitempty"`
	LastCheckedAt        *time.Time       `json:"last_checked_at,omitempty"`
	LastError            string           `json:"last_error,omitempty"`
	Hostname             string           `json:"hostname,omitempty"`
	OS                   string           `json:"os,omitempty"`
	OSVersion            string           `json:"os_version,omitempty"`
	Architecture         string           `json:"architecture,omitempty"`
	KernelVersion        string           `json:"kernel_version,omitempty"`
	ContainersRunning    int              `json:"containers_running"`
	ContainersPaused     int              `json:"containers_paused"`
	ContainersStopped    int              `json:"containers_stopped"`
	Images               int              `json:"images"`
	Networks             *int             `json:"networks,omitempty"`
	Volumes              *int             `json:"volumes,omitempty"`
	CPUs                 int              `json:"cpus"`
	MemoryTotalBytes     int64            `json:"memory_total_bytes"`
	DockerDiskUsageBytes *int64           `json:"docker_disk_usage_bytes,omitempty"`
	StorageDriver        string           `json:"storage_driver,omitempty"`
	LoggingDriver        string           `json:"logging_driver,omitempty"`
	CgroupDriver         string           `json:"cgroup_driver,omitempty"`
	CgroupVersion        string           `json:"cgroup_version,omitempty"`
	DefaultRuntime       string           `json:"default_runtime,omitempty"`
	LiveRestore          bool             `json:"live_restore"`
	SecurityOptions      []string         `json:"security_options,omitempty"`
	MetricsAvailable     bool             `json:"metrics_available"`
	ContainerCPUPercent  float64          `json:"container_cpu_percent"`
	ContainerMemoryBytes uint64           `json:"container_memory_bytes"`
	ContainerNetworkRX   uint64           `json:"container_network_rx_bytes"`
	ContainerNetworkTX   uint64           `json:"container_network_tx_bytes"`
	ContainerBlockRead   uint64           `json:"container_block_read_bytes"`
	ContainerBlockWrite  uint64           `json:"container_block_write_bytes"`
	LongestUptimeSeconds int64            `json:"longest_container_uptime_seconds"`
	Containers           []fleetContainer `json:"containers"`
}

// registerFleetRoutes exposes the control-plane fleet overview. Every node is
// probed in parallel with short timeouts; disabled and unreachable nodes keep
// their recorded state so the overview degrades gracefully.
func registerFleetRoutes(v1 gin.IRouter, deps Dependencies) {
	v1.GET("/fleet/overview", requireAuth(deps.Auth), func(c *gin.Context) {
		nodes, err := deps.Nodes.List(c.Request.Context())
		if err != nil {
			failure(c, http.StatusInternalServerError, 20101, "Unable to list nodes")
			return
		}
		results := make([]fleetNode, len(nodes))
		var wait sync.WaitGroup
		semaphore := make(chan struct{}, 8)
		for index, node := range nodes {
			results[index] = fleetNode{
				ID: node.ID, Name: node.Name, ConnectionType: node.ConnectionType, TLSMode: node.TLSMode,
				Enabled: node.Enabled, Status: node.Status, EngineVersion: node.EngineVersion,
				LastLatencyMS: node.LastLatencyMS, LastCheckedAt: node.LastCheckedAt, LastError: node.LastError,
				Containers: make([]fleetContainer, 0),
			}
			if !node.Enabled {
				continue
			}
			wait.Add(1)
			go func(index int, node nodeService.View) {
				defer wait.Done()
				semaphore <- struct{}{}
				defer func() { <-semaphore }()
				entry := &results[index]
				runtime, err := deps.Nodes.Runtime(c.Request.Context(), node.ID)
				if err != nil {
					entry.Status, entry.LastError = "offline", err.Error()
					return
				}
				infoCtx, cancelInfo := context.WithTimeout(c.Request.Context(), 5*time.Second)
				info, err := runtime.Info(infoCtx)
				cancelInfo()
				if err != nil {
					entry.Status, entry.LastError = "offline", err.Error()
					return
				}
				entry.Status, entry.LastError = "online", ""
				entry.Hostname, entry.OS, entry.OSVersion = info.Name, info.OperatingSystem, info.OSVersion
				entry.Architecture, entry.KernelVersion = info.Architecture, info.KernelVersion
				entry.EngineVersion = info.ServerVersion
				entry.ContainersRunning, entry.ContainersPaused, entry.ContainersStopped = info.Running, info.Paused, info.Stopped
				entry.Images = info.Images
				entry.CPUs, entry.MemoryTotalBytes = info.CPUs, info.MemoryBytes
				entry.StorageDriver, entry.LoggingDriver = info.StorageDriver, info.LoggingDriver
				entry.CgroupDriver, entry.CgroupVersion = info.CgroupDriver, info.CgroupVersion
				entry.DefaultRuntime, entry.LiveRestore = info.DefaultRuntime, info.LiveRestore
				entry.SecurityOptions = append([]string(nil), info.SecurityOptions...)

				var detailWait sync.WaitGroup
				detailWait.Add(3)
				go func() {
					defer detailWait.Done()
					metricsCtx, cancelMetrics := context.WithTimeout(c.Request.Context(), 5*time.Second)
					defer cancelMetrics()
					metrics, metricsErr := runtime.Metrics(metricsCtx)
					if metricsErr != nil {
						return
					}
					entry.MetricsAvailable = true
					for _, sample := range metrics {
						entry.Containers = append(entry.Containers, fleetContainer{
							ID: sample.ID, Name: sample.Name, Image: sample.Image, State: sample.State, Available: sample.Available,
							CPUPercent: sample.CPUPercent, MemoryBytes: sample.MemoryBytes,
							NetworkRXBytes: sample.NetworkRXBytes, NetworkTXBytes: sample.NetworkTXBytes,
							BlockReadBytes: sample.BlockReadBytes, BlockWriteBytes: sample.BlockWriteBytes,
							UptimeSeconds: sample.UptimeSeconds,
						})
						if !sample.Available {
							entry.MetricsAvailable = false
							continue
						}
						entry.ContainerCPUPercent += sample.CPUPercent
						entry.ContainerMemoryBytes += sample.MemoryBytes
						entry.ContainerNetworkRX += sample.NetworkRXBytes
						entry.ContainerNetworkTX += sample.NetworkTXBytes
						entry.ContainerBlockRead += sample.BlockReadBytes
						entry.ContainerBlockWrite += sample.BlockWriteBytes
						if sample.UptimeSeconds > entry.LongestUptimeSeconds {
							entry.LongestUptimeSeconds = sample.UptimeSeconds
						}
					}
					sort.SliceStable(entry.Containers, func(i, j int) bool {
						if entry.Containers[i].MemoryBytes != entry.Containers[j].MemoryBytes {
							return entry.Containers[i].MemoryBytes > entry.Containers[j].MemoryBytes
						}
						return entry.Containers[i].Name < entry.Containers[j].Name
					})
				}()
				go func() {
					defer detailWait.Done()
					diskCtx, cancelDisk := context.WithTimeout(c.Request.Context(), 5*time.Second)
					defer cancelDisk()
					usage, diskErr := runtime.DiskUsage(diskCtx)
					if diskErr == nil {
						entry.DockerDiskUsageBytes = &usage
					}
				}()
				go func() {
					defer detailWait.Done()
					inventoryCtx, cancelInventory := context.WithTimeout(c.Request.Context(), 5*time.Second)
					defer cancelInventory()
					counts, inventoryErr := runtime.ResourceCounts(inventoryCtx)
					if inventoryErr == nil {
						networks, volumes := counts.Networks, counts.Volumes
						entry.Networks, entry.Volumes = &networks, &volumes
					}
				}()
				detailWait.Wait()
			}(index, node)
		}
		wait.Wait()
		success(c, gin.H{"nodes": results})
	})
}
