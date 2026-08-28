package compose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

const shadowPreviewTTL = 30 * time.Minute

var shadowSessionID = regexp.MustCompile(`^[a-f0-9-]{36}$`)

type ShadowAssessment struct {
	Eligible bool     `json:"eligible"`
	Reasons  []string `json:"reasons"`
	Warnings []string `json:"warnings"`
}

type ShadowPreviewSession struct {
	SessionID      string        `json:"session_id"`
	PreviewProject string        `json:"preview_project"`
	ExpiresAt      time.Time     `json:"expires_at"`
	Task           database.Task `json:"task"`
}

type ShadowPreviewStatus struct {
	SessionID      string    `json:"session_id"`
	PreviewProject string    `json:"preview_project"`
	ExpiresAt      time.Time `json:"expires_at"`
	Containers     string    `json:"containers"`
	Logs           string    `json:"logs"`
}

type shadowMetadata struct {
	SessionID      string    `json:"session_id"`
	NativeName     string    `json:"native_name"`
	PreviewProject string    `json:"preview_project"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	InstanceID     string    `json:"instance_id"`
}

func AssessShadowPreview(content string) (ShadowAssessment, error) {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return ShadowAssessment{}, fmt.Errorf("parse Compose Project: %w", err)
	}
	services, ok := document["services"].(map[string]any)
	if !ok || len(services) == 0 {
		return ShadowAssessment{}, errors.New("Compose Project must define at least one service")
	}
	reasons := []string{}
	warnings := []string{}
	if values, _ := document["volumes"].(map[string]any); len(values) > 0 {
		reasons = append(reasons, "top-level volumes are not isolated for shadow preview")
	}
	for _, section := range []string{"configs", "secrets"} {
		if values, _ := document[section].(map[string]any); len(values) > 0 {
			reasons = append(reasons, "top-level "+section+" are not supported by shadow preview")
		}
	}
	if networks, _ := document["networks"].(map[string]any); len(networks) > 0 {
		for name, raw := range networks {
			value, _ := raw.(map[string]any)
			if truthy(value["external"]) {
				reasons = append(reasons, fmt.Sprintf("network %q is external", name))
			} else if strings.TrimSpace(stringValue(value["name"])) != "" {
				warnings = append(warnings, fmt.Sprintf("network %q has an explicit name that will be replaced by an isolated preview name", name))
			}
		}
	}
	for _, name := range sortedMapKeys(services) {
		service, _ := services[name].(map[string]any)
		prefix := fmt.Sprintf("service %q ", name)
		if len(anySlice(service["ports"])) > 0 {
			reasons = append(reasons, prefix+"publishes ports")
		}
		if len(anySlice(service["volumes"])) > 0 {
			reasons = append(reasons, prefix+"mounts production data")
		}
		if strings.TrimSpace(stringValue(service["container_name"])) != "" {
			reasons = append(reasons, prefix+"sets container_name")
		}
		for _, key := range []string{"network_mode", "pid", "ipc", "uts"} {
			if strings.TrimSpace(stringValue(service[key])) != "" {
				reasons = append(reasons, prefix+"shares an explicit "+key)
			}
		}
		if truthy(service["privileged"]) || len(anySlice(service["devices"])) > 0 {
			reasons = append(reasons, prefix+"uses privileged mode or devices")
		}
		if service["build"] != nil {
			reasons = append(reasons, prefix+"requires a build context")
		}
		for _, key := range []string{"configs", "secrets", "env_file", "label_file", "extends", "develop", "links", "external_links"} {
			if hasComposeValue(service[key]) {
				reasons = append(reasons, prefix+"uses "+key)
			}
		}
		deploy, _ := service["deploy"].(map[string]any)
		if replicas := intValue(deploy["replicas"]); replicas > 3 {
			reasons = append(reasons, prefix+"requests more than three preview replicas")
		}
		if service["healthcheck"] == nil {
			warnings = append(warnings, prefix+"has no healthcheck; preview can only verify that its containers remain running")
		}
	}
	sort.Strings(reasons)
	sort.Strings(warnings)
	return ShadowAssessment{Eligible: len(reasons) == 0, Reasons: reasons, Warnings: warnings}, nil
}

func (s *Service) StartShadowPreview(ctx context.Context, name, fingerprint, content, environment string) (ShadowPreviewSession, error) {
	if s.runner == nil || s.tasks == nil {
		return ShadowPreviewSession{}, errors.New("shadow preview runtime is unavailable")
	}
	draft, err := s.BuildTakeoverDraft(ctx, name)
	if err != nil {
		return ShadowPreviewSession{}, err
	}
	if fingerprint == "" || fingerprint != draft.Fingerprint {
		return ShadowPreviewSession{}, errors.New("Project changed while preparing shadow preview; analyze it again")
	}
	if err := validateManagedTakeoverContent(content); err != nil {
		return ShadowPreviewSession{}, err
	}
	assessment, err := AssessShadowPreview(content)
	if err != nil {
		return ShadowPreviewSession{}, err
	}
	if !assessment.Eligible {
		return ShadowPreviewSession{}, fmt.Errorf("Project is not eligible for shadow preview: %s", strings.Join(assessment.Reasons, "; "))
	}
	isolatedContent, err := prepareShadowCompose(content)
	if err != nil {
		return ShadowPreviewSession{}, err
	}
	_ = s.cleanupExpiredShadowPreviews(ctx)
	createdAt := time.Now().UTC()
	expiresAt := createdAt.Add(shadowPreviewTTL)
	previewProject := shadowProjectName(name, createdAt.UnixNano())
	row, err := s.tasks.StartWithIDForNode(s.effectiveNodeID(), s.effectiveNodeName(), "project.shadow_preview", "Preview "+name, func(taskContext context.Context, taskID string, report task.Reporter) error {
		metadata := shadowMetadata{SessionID: taskID, NativeName: name, PreviewProject: previewProject, CreatedAt: createdAt, ExpiresAt: expiresAt, InstanceID: s.instanceID}
		directory := s.shadowSessionPath(taskID)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
		cleanup := true
		defer func() {
			if cleanup {
				_ = s.removeShadowResources(context.Background(), metadata)
			}
		}()
		if err := writeAtomic(filepath.Join(directory, "compose.yml"), isolatedContent); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(directory, ".env"), environment); err != nil {
			return err
		}
		_ = os.Chmod(filepath.Join(directory, ".env"), 0o600)
		if err := writeShadowMetadata(directory, metadata); err != nil {
			return err
		}
		spec := shadowExecutionSpec(metadata, directory)
		report(10, "Validating isolated Compose Project")
		if err := s.runner.ValidateRelease(taskContext, spec, &reportWriter{report: report}); err != nil {
			return err
		}
		report(25, "Starting isolated Compose Project")
		if err := s.runner.UpRelease(taskContext, spec, 60, &reportWriter{report: report}); err != nil {
			return err
		}
		report(100, "Shadow preview is ready")
		cleanup = false
		time.AfterFunc(time.Until(expiresAt), func() { _ = s.removeShadowResources(context.Background(), metadata) })
		return nil
	})
	if err != nil {
		return ShadowPreviewSession{}, err
	}
	return ShadowPreviewSession{SessionID: row.ID, PreviewProject: previewProject, ExpiresAt: expiresAt, Task: row}, nil
}

func (s *Service) ShadowPreviewStatus(ctx context.Context, sessionID string) (ShadowPreviewStatus, error) {
	metadata, directory, err := s.readShadowSession(sessionID)
	if err != nil {
		return ShadowPreviewStatus{}, err
	}
	spec := shadowExecutionSpec(metadata, directory)
	containers, err := s.runner.PS(ctx, spec, io.Discard)
	if err != nil {
		return ShadowPreviewStatus{}, err
	}
	var logs strings.Builder
	if err := s.runner.LogsRelease(ctx, spec, &logs); err != nil {
		logs.WriteString("\n[logs unavailable: " + err.Error() + "]")
	}
	return ShadowPreviewStatus{SessionID: sessionID, PreviewProject: metadata.PreviewProject, ExpiresAt: metadata.ExpiresAt, Containers: containers, Logs: logs.String()}, nil
}

func (s *Service) StopShadowPreview(sessionID string) (database.Task, error) {
	if s.tasks == nil {
		return database.Task{}, errors.New("Task service is unavailable")
	}
	metadata, _, err := s.readShadowSession(sessionID)
	if err != nil {
		return database.Task{}, err
	}
	return s.tasks.StartForNode(s.effectiveNodeID(), s.effectiveNodeName(), "project.shadow_cleanup", "Clean preview "+metadata.NativeName, func(ctx context.Context, report task.Reporter) error {
		report(10, "Stopping isolated Compose Project")
		if err := s.removeShadowResources(ctx, metadata); err != nil {
			return err
		}
		report(100, "Shadow preview removed")
		return nil
	})
}

func (s *Service) removeShadowResources(ctx context.Context, metadata shadowMetadata) error {
	unlock := s.lockProject("shadow:" + metadata.SessionID)
	defer unlock()
	directory := s.shadowSessionPath(metadata.SessionID)
	if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	spec := shadowExecutionSpec(metadata, directory)
	err := s.runner.ForceDownRelease(ctx, spec, false, io.Discard)
	removeErr := os.RemoveAll(directory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return removeErr
}

func (s *Service) cleanupExpiredShadowPreviews(ctx context.Context) error {
	root := filepath.Join(s.nodeRoot(), ".suma-previews")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !shadowSessionID.MatchString(entry.Name()) {
			continue
		}
		metadata, _, readErr := s.readShadowSession(entry.Name())
		if readErr == nil && (time.Now().UTC().After(metadata.ExpiresAt) || metadata.InstanceID != s.instanceID) {
			_ = s.removeShadowResources(ctx, metadata)
		}
	}
	return nil
}

// RecoverShadowPreviews removes previews left by an earlier SUMA process and
// previews whose TTL elapsed while SUMA was stopped. The application invokes
// this once for every enabled node during startup; ordinary API construction
// does not hide recovery work behind a first request.
func (s *Service) RecoverShadowPreviews(ctx context.Context) error {
	if s.runner == nil {
		return errors.New("shadow preview runtime is unavailable")
	}
	return s.cleanupExpiredShadowPreviews(ctx)
}

func (s *Service) readShadowSession(sessionID string) (shadowMetadata, string, error) {
	if !shadowSessionID.MatchString(sessionID) {
		return shadowMetadata{}, "", errors.New("invalid shadow preview session")
	}
	directory := s.shadowSessionPath(sessionID)
	content, err := os.ReadFile(filepath.Join(directory, "session.json"))
	if err != nil {
		return shadowMetadata{}, "", err
	}
	var metadata shadowMetadata
	if err := json.Unmarshal(content, &metadata); err != nil {
		return shadowMetadata{}, "", err
	}
	if metadata.SessionID != sessionID || metadata.NativeName == "" || metadata.PreviewProject == "" {
		return shadowMetadata{}, "", errors.New("shadow preview metadata is invalid")
	}
	return metadata, directory, nil
}

func (s *Service) shadowSessionPath(sessionID string) string {
	return filepath.Join(s.nodeRoot(), ".suma-previews", sessionID)
}
func shadowExecutionSpec(metadata shadowMetadata, directory string) ExecutionSpec {
	return ExecutionSpec{ProjectName: metadata.PreviewProject, ProjectDir: directory, Files: []string{filepath.Join(directory, "compose.yml")}, EnvFiles: []string{filepath.Join(directory, ".env")}, Profiles: []string{"*"}}
}
func writeShadowMetadata(directory string, metadata shadowMetadata) error {
	content, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(directory, "session.json"), string(content)+"\n")
}
func shadowProjectName(name string, unique int64) string {
	name = strings.ToLower(name)
	name = regexp.MustCompile(`[^a-z0-9_-]+`).ReplaceAllString(name, "-")
	name = strings.Trim(name, "-_")
	if name == "" {
		name = "project"
	}
	if len(name) > 30 {
		name = name[:30]
	}
	return fmt.Sprintf("suma-preview-%s-%x", name, uint64(unique)&0xffffff)
}
func truthy(value any) bool        { result, _ := value.(bool); return result }
func stringValue(value any) string { result, _ := value.(string); return result }
func anySlice(value any) []any     { result, _ := value.([]any); return result }
func intValue(value any) int {
	switch current := value.(type) {
	case int:
		return current
	case int64:
		return int(current)
	case uint64:
		return int(current)
	case float64:
		return int(current)
	}
	return 0
}
func hasComposeValue(value any) bool {
	if value == nil {
		return false
	}
	switch current := value.(type) {
	case string:
		return strings.TrimSpace(current) != ""
	case []any:
		return len(current) > 0
	case map[string]any:
		return len(current) > 0
	default:
		return true
	}
}

func prepareShadowCompose(content string) (string, error) {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return "", fmt.Errorf("parse Compose Project for isolation: %w", err)
	}
	delete(document, "name")
	if networks, _ := document["networks"].(map[string]any); len(networks) > 0 {
		for _, raw := range networks {
			if value, ok := raw.(map[string]any); ok && !truthy(value["external"]) {
				delete(value, "name")
			}
		}
	}
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode isolated Compose Project: %w", err)
	}
	return string(encoded), nil
}
