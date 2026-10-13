package containermonitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
	"time"

	domain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/database"
)

var ErrInput = errors.New("invalid monitoring request")
var ErrConflict = errors.New("monitoring settings changed; reload before saving")
var ErrConfirmation = errors.New("confirm removal of monitoring history before reducing retention")
var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var fullID = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Exclusion struct {
	Kind        string `json:"kind"`
	NodeID      string `json:"node_id"`
	ContainerID string `json:"container_id,omitempty"`
	Project     string `json:"project,omitempty"`
	Service     string `json:"service,omitempty"`
}
type Policy struct {
	Enabled         bool        `json:"enabled"`
	IntervalSeconds int         `json:"interval_seconds"`
	RetentionDays   int         `json:"retention_days"`
	Exclusions      []Exclusion `json:"exclusions"`
	Revision        int64       `json:"revision"`
}
type SavePolicy struct {
	Policy
	ConfirmRetentionReduction bool `json:"confirm_retention_reduction"`
}
type Node struct {
	ID, Name, EngineID, RuntimeKey string
	Enabled                        bool
	Agent                          bool
}
type Inventory struct {
	ID, Name, Image, Project, Service, State string
	MemoryLimit                              *int64
}
type Runtime interface {
	MonitoringInventory(context.Context) ([]Inventory, error)
	NormalizedStats(context.Context, string) (io.ReadCloser, error)
	MonitoringEvents(context.Context, time.Time, func(domain.ContainerEvent, Inventory) error) error
}
type Dependencies struct {
	Nodes   func(context.Context) ([]Node, error)
	Runtime func(context.Context, string) (Runtime, error)
	OnError func(error)
	// Test clocks shorten scheduling without changing production policy validation.
	Tick time.Duration
}
type Target struct {
	NodeID      string    `json:"node_id"`
	EngineID    string    `json:"engine_id"`
	ContainerID string    `json:"container_id"`
	Since       time.Time `json:"since"`
	Until       time.Time `json:"until"`
}
type Query struct {
	Targets   []Target `json:"targets"`
	MaxPoints int      `json:"max_points"`
}
type Aggregate struct {
	Avg   *float64 `json:"avg"`
	Min   *float64 `json:"min"`
	Max   *float64 `json:"max"`
	Count int64    `json:"count"`
}
type Point struct {
	Time      time.Time            `json:"time"`
	StartedAt string               `json:"started_at"`
	Segment   string               `json:"segment"`
	Values    map[string]Aggregate `json:"values"`
}
type Series struct {
	Instance    database.MonitoringInstance `json:"instance"`
	Target      Target                      `json:"target"`
	Points      []Point                     `json:"points"`
	Summary     map[string]Aggregate        `json:"summary"`
	Gaps        []database.MonitoringGap    `json:"gaps"`
	Coverage    string                      `json:"coverage"`
	StepSeconds int                         `json:"step_seconds"`
	Truncated   bool                        `json:"truncated"`
}
type Result struct {
	Series        []Series  `json:"series"`
	RetainedSince time.Time `json:"retained_since"`
}
type InstanceQuery struct {
	Search, NodeID, ContainerID string
	Since, Until                time.Time
	Page, Limit                 int
}
type HistoricalNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Instances struct {
	Nodes []HistoricalNode              `json:"nodes"`
	Items []database.MonitoringInstance `json:"items"`
	Total int64                         `json:"total"`
	Page  int                           `json:"page"`
}
type EventPage struct {
	Items      []database.MonitoringEvent `json:"items"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}
type NodeStatus struct {
	NodeID    string     `json:"node_id"`
	Streams   int        `json:"streams"`
	LastWrite *time.Time `json:"last_write"`
	Error     string     `json:"error,omitempty"`
}
type Status struct {
	Policy                Policy       `json:"policy"`
	Nodes                 []NodeStatus `json:"nodes"`
	ActiveContainers      int          `json:"active_containers"`
	EstimatedDailyRecords int64        `json:"estimated_daily_records"`
	CleanupTaskID         string       `json:"cleanup_task_id,omitempty"`
}

var Fields = []string{"cpu", "memory", "memory_limit", "network_rx", "network_tx", "block_read", "block_write", "pids"}

func InstanceID(node, engine, container string) string {
	sum := sha256.Sum256([]byte(node + "\x00" + engine + "\x00" + container))
	return hex.EncodeToString(sum[:])
}
func excluded(p Policy, n Node, c Inventory) bool {
	if !p.Enabled || !n.Enabled {
		return true
	}
	for _, e := range p.Exclusions {
		if e.NodeID != n.ID {
			continue
		}
		if e.Kind == "node" || e.Kind == "container" && e.ContainerID == c.ID ||
			e.Kind == "compose" && e.Project == c.Project && (e.Service == "" || e.Service == c.Service) {
			return true
		}
	}
	return false
}
func validatePolicy(p Policy) error {
	intervals := map[int]bool{5: true, 10: true, 15: true, 30: true, 60: true}
	days := map[int]bool{1: true, 3: true, 7: true, 14: true, 30: true}
	if !intervals[p.IntervalSeconds] || !days[p.RetentionDays] || len(p.Exclusions) > 200 {
		return ErrInput
	}
	for _, e := range p.Exclusions {
		if !identifier.MatchString(e.NodeID) {
			return ErrInput
		}
		switch e.Kind {
		case "node":
			if e.ContainerID != "" || e.Project != "" || e.Service != "" {
				return ErrInput
			}
		case "container":
			if !fullID.MatchString(e.ContainerID) || e.Project != "" || e.Service != "" {
				return ErrInput
			}
		case "compose":
			if !identifier.MatchString(e.Project) || e.Service != "" && !identifier.MatchString(e.Service) || e.ContainerID != "" {
				return ErrInput
			}
		default:
			return ErrInput
		}
	}
	return nil
}
