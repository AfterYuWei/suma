package compose

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/goccy/go-yaml"
)

type TakeoverInput struct {
	Fingerprint      string `json:"fingerprint"`
	ConfirmationName string `json:"confirmation_name"`
	Compose          string `json:"compose"`
	Environment      string `json:"environment"`
}

func (s *Service) Takeover(ctx context.Context, name string, input TakeoverInput) (Project, error) {
	if input.ConfirmationName != name {
		return Project{}, errors.New("type the Compose Project name to confirm takeover")
	}
	if input.Fingerprint == "" || input.Compose == "" {
		return Project{}, errors.New("takeover fingerprint and Compose YAML are required")
	}
	unlock := s.lockProject(name)
	defer unlock()
	draft, err := s.BuildTakeoverDraft(ctx, name)
	if err != nil {
		return Project{}, err
	}
	if draft.Fingerprint != input.Fingerprint {
		return Project{}, errors.New("Project changed while preparing takeover; analyze it again")
	}
	if err := validateManagedTakeoverContent(input.Compose); err != nil {
		return Project{}, err
	}
	if s.runner == nil {
		return Project{}, errors.New("Compose runner is unavailable")
	}
	base := s.nodeRoot()
	if err := os.MkdirAll(base, 0o750); err != nil {
		return Project{}, err
	}
	target, err := s.safePath(name)
	if err != nil {
		return Project{}, err
	}
	if _, err := os.Stat(target); err == nil {
		return Project{}, errors.New("Project is already managed by SUMA")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Project{}, err
	}
	temporary, err := os.MkdirTemp(base, ".takeover-")
	if err != nil {
		return Project{}, err
	}
	defer os.RemoveAll(temporary)
	if err := os.Chmod(temporary, 0o750); err != nil {
		return Project{}, err
	}
	if err := writeAtomic(filepath.Join(temporary, "compose.yml"), input.Compose); err != nil {
		return Project{}, err
	}
	if err := writeAtomic(filepath.Join(temporary, ".env"), input.Environment); err != nil {
		return Project{}, err
	}
	metadata := newManagedProjectMetadata(s.effectiveNodeID(), name, "takeover", draft.Source, time.Now().UTC())
	if err := writeManagedProjectMetadata(temporary, metadata); err != nil {
		return Project{}, err
	}
	if err := s.validateComposeProject(ctx, temporary, input.Environment); err != nil {
		return Project{}, fmt.Errorf("validate takeover Compose Project: %w", err)
	}
	if err := os.Rename(temporary, target); err != nil {
		return Project{}, fmt.Errorf("claim Compose Project: %w", err)
	}
	return s.Get(ctx, name)
}

func validateManagedTakeoverContent(content string) error {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return fmt.Errorf("parse takeover Compose Project: %w", err)
	}
	services, ok := document["services"].(map[string]any)
	if !ok || len(services) == 0 {
		return errors.New("takeover Compose Project must define at least one service")
	}
	for _, section := range []string{"configs", "secrets"} {
		values, _ := document[section].(map[string]any)
		for name, raw := range values {
			entry, _ := raw.(map[string]any)
			if _, ok := entry["file"]; ok {
				return fmt.Errorf("%s %q is file-backed; convert it to an external resource before takeover", section, name)
			}
		}
	}
	return nil
}
