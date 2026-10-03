package imageupdate

import (
	"context"
	"errors"
	"github.com/distribution/reference"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/event"
	"regexp"
	"strings"
	"time"
)

var projectIdentifier = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
var imageIdentifier = regexp.MustCompile(`^[a-zA-Z0-9:_-]+$`)
var ErrConflict = errors.New("image detection policy changed; reload before saving")
var ErrInvalid = errors.New("invalid image detection request")
var ErrUnavailable = errors.New("Docker node is unavailable")

type BusyError struct{ TaskID string }

func (e *BusyError) Error() string { return "an image check is already running" }

type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

func (p Platform) String() string { return p.OS + "/" + p.Architecture + "/" + p.Variant }

type LocalImage struct {
	ID       string
	Tags     []string
	Platform Platform
}
type Usage struct {
	ContainerID     string `json:"container_id"`
	ContainerName   string `json:"container_name"`
	Project         string `json:"project,omitempty"`
	Service         string `json:"service,omitempty"`
	State           string `json:"state"`
	ImageID         string `json:"image_id"`
	Reference       string `json:"reference"`
	DeliveryProject string `json:"delivery_project,omitempty"`
}
type Inventory struct {
	Images     []LocalImage
	Containers []Usage
}
type Runtime interface {
	UpdateInventory(context.Context) (Inventory, error)
}
type Node struct {
	ID, Name, RuntimeKey string
	Enabled              bool
	Available            bool
}
type Remote struct{ ManifestDigest, ConfigDigest string }
type LookupError struct{ Code string }

func (e *LookupError) Error() string { return e.Code }

type Resolver interface {
	Resolve(context.Context, string, Platform, credential.RegistryMaterial) (Remote, error)
}
type CredentialStore interface {
	AuthorizedForNode(context.Context, uint, string) error
	Material(context.Context, uint) (credential.RegistryMaterial, error)
}
type Dependencies struct {
	Emit        event.Sink
	Node        func(context.Context, string) (Node, error)
	Runtime     func(context.Context, string) (Runtime, error)
	Resolver    Resolver
	Credentials CredentialStore
	Now         func() time.Time
}
type Actor struct {
	UserID *uint
	IP     string
}
type CheckInput struct {
	ImageIDs            []string        `json:"image_ids,omitempty"`
	ProjectName         string          `json:"project_name,omitempty"`
	RegistryCredentials map[string]uint `json:"registry_credentials,omitempty"`
}
type Policy struct {
	Version             uint64          `json:"version"`
	Enabled             bool            `json:"enabled"`
	IntervalHours       int             `json:"interval_hours"`
	NextRunAt           *time.Time      `json:"next_run_at,omitempty"`
	RegistryCredentials map[string]uint `json:"registry_credentials"`
}
type PolicyInput struct {
	ExpectedVersion     uint64          `json:"expected_version"`
	Enabled             bool            `json:"enabled"`
	IntervalHours       int             `json:"interval_hours"`
	RegistryCredentials map[string]uint `json:"registry_credentials"`
}
type Result struct {
	Reference            string     `json:"reference"`
	Registry             string     `json:"registry,omitempty"`
	Platform             Platform   `json:"platform"`
	LocalImageID         string     `json:"local_image_id"`
	RemoteManifestDigest string     `json:"remote_manifest_digest,omitempty"`
	RemoteConfigDigest   string     `json:"remote_config_digest,omitempty"`
	Status               string     `json:"status"`
	ReasonCode           string     `json:"reason_code,omitempty"`
	CheckedAt            *time.Time `json:"checked_at,omitempty"`
	Stale                bool       `json:"stale"`
	PullRequired         bool       `json:"pull_required"`
	RecreateRequired     bool       `json:"recreate_required"`
	Containers           []Usage    `json:"containers"`
}
type View struct {
	Results       []Result `json:"results"`
	RunningTaskID string   `json:"running_task_id,omitempty"`
}
type target struct {
	result Result
	tagID  string
}

func key(r Result) string {
	return r.Reference + "\x00" + r.Platform.String() + "\x00" + r.LocalImageID
}

func Normalize(value string) (normalized, registry, reason string) {
	if value == "" || value == "<none>:<none>" {
		return value, "", "untagged"
	}
	if strings.HasPrefix(value, "sha256:") || (len(value) == 64 && !strings.ContainsAny(value, "/:")) {
		return value, "", "image_id"
	}
	parsed, err := reference.ParseNormalizedNamed(value)
	if err != nil {
		return value, "", "invalid_reference"
	}
	if _, ok := parsed.(reference.Digested); ok {
		return parsed.String(), reference.Domain(parsed), "digest_pinned"
	}
	parsed = reference.TagNameOnly(parsed)
	return parsed.String(), reference.Domain(parsed), ""
}
func RegistryHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "index.docker.io" || host == "registry-1.docker.io" {
		return "docker.io"
	}
	return host
}
func targets(inv Inventory, input CheckInput) []target {
	images := map[string]LocalImage{}
	tags := map[string]string{}
	rows := map[string]*target{}
	selected := map[string]bool{}
	for _, id := range input.ImageIDs {
		selected[id] = true
	}
	for _, img := range inv.Images {
		images[img.ID] = img
		for _, ref := range img.Tags {
			normalized, _, reason := Normalize(ref)
			if reason == "" {
				tags[normalized] = img.ID
			}
		}
	}
	add := func(id, ref string, u *Usage) {
		img, ok := images[id]
		if !ok {
			img = LocalImage{ID: id}
		}
		normalized, host, reason := Normalize(ref)
		r := Result{Reference: normalized, Registry: host, Platform: img.Platform, LocalImageID: id, Status: "unchecked", Containers: []Usage{}}
		if reason != "" {
			r.Status = "unavailable"
			r.ReasonCode = reason
			if reason == "digest_pinned" {
				r.Status = "pinned"
			}
		}
		k := key(r)
		t := rows[k]
		if t == nil {
			t = &target{result: r, tagID: tags[normalized]}
			rows[k] = t
		}
		if u != nil {
			t.result.Containers = append(t.result.Containers, *u)
		}
	}
	if input.ProjectName == "" {
		for _, img := range inv.Images {
			if len(selected) > 0 && !selected[img.ID] {
				continue
			}
			if len(img.Tags) == 0 {
				add(img.ID, "", nil)
			}
			for _, ref := range img.Tags {
				add(img.ID, ref, nil)
			}
		}
	}
	for _, u := range inv.Containers {
		if input.ProjectName != "" && u.Project != input.ProjectName {
			continue
		}
		if len(selected) > 0 && !selected[u.ImageID] {
			continue
		}
		add(u.ImageID, u.Reference, &u)
	}
	result := make([]target, 0, len(rows))
	for _, row := range rows {
		result = append(result, *row)
	}
	return result
}
