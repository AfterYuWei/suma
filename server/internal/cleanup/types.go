package cleanup

import (
	"context"
	"errors"
	"time"

	"github.com/suma/suma/server/internal/database"
)

var (
	ErrConflict     = errors.New("cleanup configuration changed, preview expired, or cleanup is already running")
	ErrConfirmation = errors.New("confirm the node name and automatic deletion authorization")
	ErrInvalid      = errors.New("invalid cleanup configuration")
	ErrGone         = errors.New("resource no longer exists")
	ErrInUse        = errors.New("resource is now in use")
	ErrUnavailable  = errors.New("node or cleanup capability is unavailable")
)

type Kind string

const (
	Container Kind = "container"
	Cache     Kind = "cache"
	Image     Kind = "image"
	Network   Kind = "network"
	Volume    Kind = "volume"
)

type Rule struct {
	Enabled       bool `json:"enabled"`
	RetentionDays int  `json:"retention_days"`
}
type ImageRule struct {
	Rule
	IncludeTagged bool `json:"include_tagged"`
}
type CacheRule struct {
	Rule
	ReservedBytes int64 `json:"reserved_bytes"`
}
type Schedule struct {
	Frequency string `json:"frequency"`
	Weekday   int    `json:"weekday"`
	Hour      int    `json:"hour"`
	Minute    int    `json:"minute"`
	Timezone  string `json:"timezone"`
}
type Config struct {
	Enabled     bool              `json:"enabled"`
	Schedule    Schedule          `json:"schedule"`
	Images      ImageRule         `json:"images"`
	Cache       CacheRule         `json:"cache"`
	Containers  Rule              `json:"containers"`
	Networks    Rule              `json:"networks"`
	ScanVolumes bool              `json:"scan_volumes"`
	Protected   map[Kind][]string `json:"protected"`
}
type Policy struct {
	Config
	NodeID       string     `json:"node_id"`
	Version      uint64     `json:"version"`
	AuthorizedBy *uint      `json:"authorized_by,omitempty"`
	AuthorizedAt *time.Time `json:"authorized_at,omitempty"`
	NextRunAt    *time.Time `json:"next_run_at,omitempty"`
}
type Update struct {
	Config
	Version          uint64 `json:"version"`
	ConfirmationName string `json:"confirmation_name"`
	Authorize        bool   `json:"authorize"`
}
type Actor struct {
	UserID *uint
	IP     string
}
type Node struct {
	RuntimeKey string
	ID         string
	Name       string
	Enabled    bool
}
type Capabilities struct {
	Available  bool   `json:"available"`
	BuildCache bool   `json:"build_cache"`
	API        string `json:"api_version,omitempty"`
	Reason     string `json:"reason,omitempty"`
}
type Resource struct {
	Kind       Kind              `json:"kind"`
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Aliases    []string          `json:"-"`
	Labels     map[string]string `json:"-"`
	State      string            `json:"-"`
	InUse      bool              `json:"-"`
	CreatedAt  time.Time         `json:"created_at"`
	FinishedAt time.Time         `json:"-"`
	LastUsedAt *time.Time        `json:"last_used_at,omitempty"`
	SizeBytes  *int64            `json:"size_bytes"`
	Tagged     bool              `json:"-"`
	System     bool              `json:"-"`
	Candidate  bool              `json:"candidate"`
	Manual     bool              `json:"manual"`
	Reason     string            `json:"reason"`
}
type Inventory struct {
	Resources    []Resource
	Capabilities Capabilities
	// LayersBytes is Docker's single LayersSize value, not the sum of images.
	LayersBytes *int64
	Usage       Usage
}
type Usage struct {
	ImageLayersBytes *int64 `json:"image_layers_bytes"`
	ContainerBytes   *int64 `json:"container_bytes"`
	VolumeBytes      *int64 `json:"volume_bytes"`
}
type CacheOptions struct {
	Until         time.Time
	RetentionDays int
	ReservedBytes int64
}
type CacheReport struct {
	Deleted        []string
	ReclaimedBytes uint64
}
type Runtime interface {
	CleanupCapabilities(context.Context) (Capabilities, error)
	CleanupInventory(context.Context) (Inventory, error)
	CleanupResource(context.Context, Kind, string) (Resource, error)
	CleanupRemove(context.Context, Kind, string) error
	CleanupPruneCache(context.Context, CacheOptions) (CacheReport, error)
}
type Protection map[Kind][]string
type Preview struct {
	ID               string       `json:"id"`
	NodeID           string       `json:"node_id"`
	RuntimeKey       string       `json:"-"`
	PolicyVersion    uint64       `json:"policy_version"`
	GeneratedAt      time.Time    `json:"generated_at"`
	ExpiresAt        time.Time    `json:"expires_at"`
	Resources        []Resource   `json:"resources"`
	Capabilities     Capabilities `json:"capabilities"`
	ImageLayersBytes *int64       `json:"image_layers_bytes"`
	CacheApproximate bool         `json:"cache_approximate"`
	Policy           Policy       `json:"policy"`
	Usage            Usage        `json:"usage"`
}
type Outcome struct {
	Kind           Kind   `json:"kind"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	EstimatedBytes *int64 `json:"estimated_bytes,omitempty"`
}
type Stats struct {
	Deleted        int     `json:"deleted"`
	Skipped        int     `json:"skipped"`
	Failed         int     `json:"failed"`
	Scanned        int     `json:"scanned"`
	ReclaimedBytes *uint64 `json:"reclaimed_bytes,omitempty"`
}
type Result struct {
	Outcomes          []Outcome       `json:"outcomes"`
	Stats             map[Kind]*Stats `json:"stats"`
	ImageLayersBefore *int64          `json:"image_layers_before"`
	ImageLayersAfter  *int64          `json:"image_layers_after"`
	UsageBefore       Usage           `json:"usage_before"`
	UsageAfter        Usage           `json:"usage_after"`
}
type Run struct {
	database.CleanupRun
	Policy Config `json:"policy"`
	Result Result `json:"result"`
}
type View struct {
	Policy       Policy       `json:"policy"`
	Capabilities Capabilities `json:"capabilities"`
	NextRuns     []time.Time  `json:"next_runs"`
	LatestRun    *Run         `json:"latest_run"`
	ActiveRun    *Run         `json:"active_run"`
}
type RunPage struct {
	Items []Run `json:"items"`
	Total int64 `json:"total"`
	Page  int   `json:"page"`
}
