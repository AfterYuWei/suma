package compose

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	projectdomain "github.com/suma/suma/server/internal/project"
)

const projectMetadataSchema = 1

type ManagedProjectMetadata struct {
	SchemaVersion  int                     `json:"schema_version"`
	Backend        projectdomain.Backend   `json:"backend"`
	ScopeKind      projectdomain.ScopeKind `json:"scope_kind"`
	ScopeID        string                  `json:"scope_id"`
	NativeName     string                  `json:"native_name"`
	Origin         string                  `json:"origin"`
	TakeoverSource string                  `json:"takeover_source,omitempty"`
	ClaimedAt      time.Time               `json:"claimed_at"`
	LastDeployedAt *time.Time              `json:"last_deployed_at,omitempty"`
}

func newManagedProjectMetadata(nodeID, name, origin, takeoverSource string, claimedAt time.Time) ManagedProjectMetadata {
	return ManagedProjectMetadata{
		SchemaVersion: projectMetadataSchema, Backend: projectdomain.BackendCompose,
		ScopeKind: projectdomain.ScopeEngine, ScopeID: nodeID, NativeName: name,
		Origin: origin, TakeoverSource: takeoverSource, ClaimedAt: claimedAt,
	}
}

func metadataPath(projectPath string) string {
	return filepath.Join(projectPath, ".suma", "project.json")
}

func readManagedProjectMetadata(projectPath, nodeID, name string, fallback time.Time) (ManagedProjectMetadata, error) {
	content, err := os.ReadFile(metadataPath(projectPath))
	if errors.Is(err, os.ErrNotExist) {
		return newManagedProjectMetadata(nodeID, name, "legacy", "", fallback), nil
	}
	if err != nil {
		return ManagedProjectMetadata{}, err
	}
	var value ManagedProjectMetadata
	if err := json.Unmarshal(content, &value); err != nil {
		return ManagedProjectMetadata{}, err
	}
	if value.SchemaVersion != projectMetadataSchema || value.Backend != projectdomain.BackendCompose || value.ScopeKind != projectdomain.ScopeEngine || value.ScopeID != nodeID || value.NativeName != name {
		return ManagedProjectMetadata{}, errors.New("managed Project metadata does not match its Compose Project identity")
	}
	return value, nil
}

func writeManagedProjectMetadata(projectPath string, value ManagedProjectMetadata) error {
	directory := filepath.Dir(metadataPath(projectPath))
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(metadataPath(projectPath), string(content)+"\n")
}
