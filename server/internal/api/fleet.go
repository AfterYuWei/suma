package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	nodeService "github.com/suma/suma/server/internal/node"
	"github.com/gin-gonic/gin"
)

type fleetNode struct {
	ID                   string     `json:"id"`
	Name                 string     `json:"name"`
	ConnectionType       string     `json:"connection_type"`
	Enabled              bool       `json:"enabled"`
	Status               string     `json:"status"`
	EngineVersion        string     `json:"engine_version,omitempty"`
	LastLatencyMS        int64      `json:"last_latency_ms,omitempty"`
	LastCheckedAt        *time.Time `json:"last_checked_at,omitempty"`
	LastError            string     `json:"last_error,omitempty"`
	Hostname             string     `json:"hostname,omitempty"`
	OS                   string     `json:"os,omitempty"`
	Architecture         string     `json:"architecture,omitempty"`
	ContainersRunning    int        `json:"containers_running"`
	ContainersStopped    int        `json:"containers_stopped"`
	Images               int        `json:"images"`
	CPUs                 int        `json:"cpus"`
	MemoryTotalBytes     int64      `json:"memory_total_bytes"`
	ContainerCPUPercent  float64    `json:"container_cpu_percent"`
	ContainerMemoryBytes uint64     `json:"container_memory_bytes"`
}

type fleetTotals struct {
	NodesTotal           int     `json:"nodes_total"`
	NodesOnline          int     `json:"nodes_online"`
	NodesOffline         int     `json:"nodes_offline"`
	NodesDisabled        int     `json:"nodes_disabled"`
	ContainersRunning    int     `json:"containers_running"`
	ContainersStopped    int     `json:"containers_stopped"`
	Images               int     `json:"images"`
	ContainerCPUPercent  float64 `json:"container_cpu_percent"`
	ContainerMemoryBytes uint64  `json:"container_memory_bytes"`
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
				ID: node.ID, Name: node.Name, ConnectionType: node.ConnectionType,
				Enabled: node.Enabled, Status: node.Status, EngineVersion: node.EngineVersion,
				LastLatencyMS: node.LastLatencyMS, LastCheckedAt: node.LastCheckedAt, LastError: node.LastError,
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
				entry.Hostname, entry.OS, entry.Architecture = info.Name, info.OperatingSystem, info.Architecture
				entry.EngineVersion = info.ServerVersion
				entry.ContainersRunning, entry.ContainersStopped, entry.Images = info.Running, info.Stopped, info.Images
				entry.CPUs, entry.MemoryTotalBytes = info.CPUs, info.MemoryBytes
				metricsCtx, cancelMetrics := context.WithTimeout(c.Request.Context(), 5*time.Second)
				metrics, metricsErr := runtime.Metrics(metricsCtx)
				cancelMetrics()
				if metricsErr == nil {
					for _, sample := range metrics {
						entry.ContainerCPUPercent += sample.CPUPercent
						entry.ContainerMemoryBytes += sample.MemoryBytes
					}
				}
			}(index, node)
		}
		wait.Wait()
		totals := fleetTotals{NodesTotal: len(results)}
		for _, node := range results {
			switch {
			case !node.Enabled:
				totals.NodesDisabled++
			case node.Status == "online":
				totals.NodesOnline++
			default:
				totals.NodesOffline++
			}
			totals.ContainersRunning += node.ContainersRunning
			totals.ContainersStopped += node.ContainersStopped
			totals.Images += node.Images
			totals.ContainerCPUPercent += node.ContainerCPUPercent
			totals.ContainerMemoryBytes += node.ContainerMemoryBytes
		}
		success(c, gin.H{"nodes": results, "totals": totals})
	})
}
