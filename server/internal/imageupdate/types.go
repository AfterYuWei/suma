// Package imageupdate implements the image.update Task Center kind: it
// compares local image IDs against remote registry digests and optionally
// recreates the services that moved. All execution runs through the task
// engine; scheduling belongs to the Task Center.
package imageupdate

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
)

// KindImageUpdate is the Task Center task type of this feature.
const KindImageUpdate = "image.update"

var projectIdentifier = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
var serviceIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var ErrUnavailable = errors.New("Docker node is unavailable")

// BusyError reports that a check or update for the project is already running.
type BusyError struct{ TaskID string }

func (e *BusyError) Error() string { return "an image check is already running for this project" }

// Check statuses stored on per-service results.
const (
	StatusCurrent         = "current"
	StatusUpdateAvailable = "update_available"
	StatusBlocked         = "blocked"
	StatusUnavailable     = "unavailable"
)

// Platform describes the OS/architecture a local image was built for.
type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

func (p Platform) String() string { return p.OS + "/" + p.Architecture + "/" + p.Variant }

// LocalImage is one image present on the node.
type LocalImage struct {
	ID       string
	Tags     []string
	Platform Platform
}

// Usage ties one container to its Compose service and image reference.
type Usage struct {
	OneOff          bool   `json:"one_off,omitempty"`
	ContainerID     string `json:"container_id"`
	ContainerName   string `json:"container_name"`
	Project         string `json:"project,omitempty"`
	Service         string `json:"service,omitempty"`
	State           string `json:"state"`
	ImageID         string `json:"image_id"`
	Reference       string `json:"reference"`
	DeliveryProject string `json:"delivery_project,omitempty"`
}

// Inventory is the node-wide image and container snapshot used for checks.
type Inventory struct {
	Images     []LocalImage
	Containers []Usage
}

// Runtime reads the image inventory from a Docker node.
type Runtime interface {
	UpdateInventory(context.Context) (Inventory, error)
}

// Node is the resolved node a run targets.
type Node struct {
	ID, Name, RuntimeKey string
	Enabled              bool
}

// Remote carries the digests observed in the registry.
type Remote struct{ ManifestDigest, ConfigDigest, IndexDigest string }

// LookupError maps registry failures to a stable reason code.
type LookupError struct{ Code string }

func (e *LookupError) Error() string { return e.Code }

// Resolver resolves a reference's remote digests for one platform.
type Resolver interface {
	Resolve(context.Context, string, Platform, credential.RegistryMaterial) (Remote, error)
}

// CredentialStore loads node-authorized registry credentials.
type CredentialStore interface {
	AuthorizedForNode(context.Context, uint, string) error
	Material(context.Context, uint) (credential.RegistryMaterial, error)
	List(context.Context) ([]database.RegistryCredential, error)
}

// Params is the recorded input of every image.update run.
type Params struct {
	Project           string   `json:"project"`
	Services          []string `json:"services,omitempty"`
	AutoApply         bool     `json:"auto_apply,omitempty"`
	AllowDockerSocket bool     `json:"allow_docker_socket,omitempty"`
}

// ServiceCheck is the per-service outcome of a check.
type ServiceCheck struct {
	Service              string `json:"service"`
	Reference            string `json:"reference,omitempty"`
	Registry             string `json:"registry,omitempty"`
	Status               string `json:"status"`
	ReasonCode           string `json:"reason_code,omitempty"`
	Platform             string `json:"platform,omitempty"`
	LocalImageID         string `json:"local_image_id,omitempty"`
	RemoteManifestDigest string `json:"remote_manifest_digest,omitempty"`
	RemoteConfigDigest   string `json:"remote_config_digest,omitempty"`
	Replicas             int    `json:"replicas"`
}

// Summary is both the task result and the stored artifact payload.
type Summary struct {
	Project      string         `json:"project"`
	Mode         string         `json:"mode"`
	Services     []ServiceCheck `json:"services"`
	Updated      []string       `json:"updated,omitempty"`
	UpdateTaskID string         `json:"update_task_id,omitempty"`
	FinishedAt   time.Time      `json:"finished_at"`
}

// Normalize canonicalizes an image reference and reports why it cannot be
// checked: untagged scratch images, bare image IDs and digest-pinned refs are
// never update candidates.
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

// RegistryHost canonicalizes Docker Hub host aliases.
func RegistryHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "index.docker.io" || host == "registry-1.docker.io" {
		return "docker.io"
	}
	return host
}
