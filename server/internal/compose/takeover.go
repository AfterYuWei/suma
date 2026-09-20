package compose

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

const (
	// TakeoverModeDraft claims the Project with the reviewed generated draft.
	TakeoverModeDraft = "draft"
	// TakeoverModeManual claims the Project with Compose content the operator
	// wrote directly, skipping the generated draft and environment review.
	TakeoverModeManual = "manual"
)

type TakeoverInput struct {
	Mode             string `json:"mode"`
	Fingerprint      string `json:"fingerprint"`
	ConfirmationName string `json:"confirmation_name"`
	Compose          string `json:"compose"`
	Environment      string `json:"environment"`
}

func takeoverMode(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "", TakeoverModeDraft:
		return TakeoverModeDraft, nil
	case TakeoverModeManual:
		return TakeoverModeManual, nil
	default:
		return "", errors.New("takeover mode must be draft or manual")
	}
}

func (s *Service) Takeover(ctx context.Context, name string, input TakeoverInput) (Project, error) {
	mode, err := takeoverMode(input.Mode)
	if err != nil {
		return Project{}, err
	}
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
	if err := validateTakeoverProjectName(name, input.Compose, input.Environment); err != nil {
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
	source := draft.Source
	if mode == TakeoverModeManual {
		source = TakeoverModeManual
	}
	metadata := newManagedProjectMetadata(s.effectiveNodeID(), name, "takeover", source, time.Now().UTC())
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

// ValidateTakeoverDraft validates unsaved takeover content for one Compose
// Project, including the Project identity the content would run under, without
// requiring a managed directory and without mutating runtime state.
func (s *Service) ValidateTakeoverDraft(ctx context.Context, name, content, environment string) error {
	if err := validateTakeoverProjectName(name, content, environment); err != nil {
		return err
	}
	return s.ValidateDraft(ctx, content, environment)
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

// validateTakeoverProjectName rejects content that would make Compose run the
// managed directory under a different Project name. The managed directory is
// named after the native Project, so a declared top-level name or a
// COMPOSE_PROJECT_NAME entry in .env must agree with it; otherwise the first
// deployment would create a second Project and orphan the existing containers.
func validateTakeoverProjectName(name, content, environment string) error {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return fmt.Errorf("parse takeover Compose Project: %w", err)
	}
	if declared, ok := document["name"]; ok && declared != nil {
		if value := strings.TrimSpace(fmt.Sprint(declared)); value != "" && value != name {
			return fmt.Errorf("top-level name %q must match the Compose Project name %q", value, name)
		}
	}
	if value, ok := dotEnvValue(environment, "COMPOSE_PROJECT_NAME"); ok && value != name {
		return fmt.Errorf("COMPOSE_PROJECT_NAME %q must match the Compose Project name %q", value, name)
	}
	return nil
}

func dotEnvValue(environment, key string) (string, bool) {
	var result string
	found := false
	for _, line := range strings.Split(environment, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export") {
			rest := strings.TrimPrefix(line, "export")
			if len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
				line = strings.TrimSpace(rest)
			}
		}
		current, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(current) != key {
			continue
		}
		result, found = unquoteDotEnv(strings.TrimSpace(value)), true
	}
	return result, found
}

func unquoteDotEnv(value string) string {
	if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
		return value[1 : len(value)-1]
	}
	return value
}
