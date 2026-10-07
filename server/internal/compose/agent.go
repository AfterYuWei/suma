package compose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/suma/suma/server/internal/redact"
	"github.com/suma/suma/server/internal/task"
)

const secretRefPrefix = "__SUMA_SECRET_REF_"

// AgentConfig exposes configuration with sensitive leaves replaced by references
// tied to their exact YAML path. Environment files never leave this service.
func AgentConfig(content string) (string, error) {
	var model map[string]any
	if err := yaml.Unmarshal([]byte(content), &model); err != nil {
		return "", err
	}
	protected := protectAgentValue(model, "", false, map[string]any{})
	raw, err := yaml.Marshal(protected)
	return string(raw), err
}
func protectAgentValue(value any, path string, sensitive bool, refs map[string]any) any {
	switch item := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, entry := range item {
			out[key] = protectAgentValue(entry, path+"/"+key, sensitive || sensitiveEnvironmentKey(key), refs)
		}
		return out
	case []any:
		out := make([]any, len(item))
		for i, entry := range item {
			entryPath := fmt.Sprintf("%s/%d", path, i)
			if text, ok := entry.(string); ok && strings.HasSuffix(path, "/environment") {
				key, _, exists := strings.Cut(text, "=")
				if exists && sensitiveEnvironmentKey(key) {
					ref := secretRefPrefix + configHash(entryPath)[:24] + "__"
					refs[entryPath] = text
					out[i] = key + "=" + ref
					continue
				}
			}
			out[i] = protectAgentValue(entry, entryPath, sensitive, refs)
		}
		return out
	case string:
		if sensitive || redact.Text(item) != item {
			ref := secretRefPrefix + configHash(path)[:24] + "__"
			refs[path] = item
			return ref
		}
		return item
	default:
		if sensitive {
			ref := secretRefPrefix + configHash(path)[:24] + "__"
			refs[path] = item
			return ref
		}
		return item
	}
}
func restoreAgentValue(value any, path string, refs map[string]any) (any, error) {
	switch item := value.(type) {
	case map[string]any:
		for key, entry := range item {
			v, err := restoreAgentValue(entry, path+"/"+key, refs)
			if err != nil {
				return nil, err
			}
			item[key] = v
		}
		return item, nil
	case []any:
		for i, entry := range item {
			entryPath := fmt.Sprintf("%s/%d", path, i)
			if text, ok := entry.(string); ok && strings.HasSuffix(path, "/environment") {
				key, v, exists := strings.Cut(text, "=")
				if exists && strings.Contains(v, secretRefPrefix) {
					expected := secretRefPrefix + configHash(entryPath)[:24] + "__"
					original, found := refs[entryPath]
					originalText, _ := original.(string)
					originalKey, _, _ := strings.Cut(originalText, "=")
					if !found || v != expected || key != originalKey {
						return nil, errors.New("secret reference changed or moved")
					}
					item[i] = originalText
					delete(refs, entryPath)
					continue
				}
			}
			v, err := restoreAgentValue(entry, entryPath, refs)
			if err != nil {
				return nil, err
			}
			item[i] = v
		}
		return item, nil
	case string:
		if strings.Contains(item, secretRefPrefix) {
			expected := secretRefPrefix + configHash(path)[:24] + "__"
			original, found := refs[path]
			if !found || item != expected {
				return nil, errors.New("secret reference changed or moved")
			}
			delete(refs, path)
			return original, nil
		}
		if sensitiveEnvironmentKey(path[strings.LastIndex(path, "/")+1:]) || redact.Text(item) != item {
			return nil, errors.New("AI cannot add or change credentials; use the configuration editor")
		}
		return item, nil
	default:
		return item, nil
	}
}
func (s *Service) PrepareAgentDraft(ctx context.Context, name, content, expected string) (string, string, string, error) {
	if len(content) > 128<<10 {
		return "", "", "", errors.New("Compose draft exceeds AI size limit")
	}
	base, environment, revision := "", "", ""
	project, err := s.Get(ctx, name)
	if err == nil {
		if !project.CanManage {
			draft, e := s.BuildTakeoverDraft(ctx, name)
			if e != nil {
				return "", "", "", e
			}
			base, environment, revision = draft.Compose, draft.Environment, draft.Fingerprint
		} else {
			base, environment, revision = project.Compose, project.Environment, project.Revision
		}
		if revision != expected {
			return "", "", "", ErrRevisionConflict
		}
	} else if expected != "" {
		return "", "", "", err
	}
	refs := map[string]any{}
	var prior map[string]any
	if base != "" {
		if err = yaml.Unmarshal([]byte(base), &prior); err != nil {
			return "", "", "", err
		}
		protectAgentValue(prior, "", false, refs)
	}
	var model map[string]any
	if err = yaml.Unmarshal([]byte(content), &model); err != nil {
		return "", "", "", errors.New("invalid Compose draft")
	}
	restored, err := restoreAgentValue(model, "", refs)
	if err != nil {
		return "", "", "", err
	}
	if len(refs) > 0 {
		return "", "", "", errors.New("Compose draft removed protected secret references")
	}
	raw, err := yaml.Marshal(restored)
	if err != nil {
		return "", "", "", err
	}
	if err = s.ValidateAgentSources(name, string(raw)); err != nil {
		return "", "", "", err
	}
	if err = ValidateComposeBindMounts(string(raw), !s.localSources, true); err != nil {
		return "", "", "", err
	}
	if err = s.ValidateConfiguration(ctx, name, string(raw), environment); err != nil {
		return "", "", "", errors.New("Compose draft validation failed; review configuration in the editor")
	}
	before, _ := AgentConfig(base)
	after, _ := AgentConfig(string(raw))
	preview, _ := json.Marshal(map[string]any{"before": before, "after": after, "validated": true, "base_revision": revision, "docker_socket": ValidateComposeBindMounts(string(raw), !s.localSources, false) != nil})
	return string(raw), revision, string(preview), nil
}
func (s *Service) SaveAgentDraft(ctx context.Context, name, content, revision string, create bool) (Project, error) {
	if create {
		if revision != "" {
			return Project{}, ErrRevisionConflict
		}
		return s.Create(ctx, name, content, "")
	}
	project, err := s.Get(ctx, name)
	if err != nil {
		return Project{}, err
	}
	return s.SaveWithRevision(ctx, name, content, project.Environment, revision)
}

// ApplyAgentAction executes inside the already persisted AI Task and uses the
// same project lock, revision guard, runner and output redactor as GUI actions.
func (s *Service) ApplyAgentAction(ctx context.Context, name, action, revision string, confirmedSocket bool, report task.Reporter) error {
	unlock := s.lockProject(name)
	defer unlock()
	if s.operationActive(name) {
		return ErrProjectBusy
	}
	project, err := s.managedProject(name)
	if err != nil {
		return err
	}
	if err = s.checkRevision(project.Path, revision); err != nil {
		return err
	}
	if err = s.ValidateAgentSources(name, project.Compose); err != nil {
		return err
	}
	config, err := readConfiguration(project.Path)
	if err != nil {
		return err
	}
	if err = ValidateComposeBindMounts(config[0], !s.localSources, confirmedSocket); err != nil {
		return err
	}
	s.operations.Store(s.operationKey(name), true)
	defer s.operations.Delete(s.operationKey(name))
	mask := configurationRedactor(config[0], config[1])
	writer := newReportWriter(func(progress int, message string) { report(progress, mask(message)) }, nil, 5, 95)
	defer writer.Flush()
	switch action {
	case "start":
		err = s.runner.Start(ctx, project.Path, writer)
	case "stop":
		err = s.runner.Stop(ctx, project.Path, writer)
	case "restart":
		err = s.runner.Restart(ctx, project.Path, writer)
	case "down":
		err = s.runner.Down(ctx, project.Path, writer)
	case "pull":
		err = s.runner.Pull(ctx, project.Path, writer)
	case "build":
		err = s.runner.Build(ctx, project.Path, writer)
	default:
		return errors.New("unsupported reviewed Compose action")
	}
	if err != nil {
		return errors.New(mask(err.Error()))
	}
	return nil
}
func (s *Service) ApplyAgentCleanup(ctx context.Context, name string, report task.Reporter) error {
	unlock := s.lockProject(name)
	defer unlock()
	current, err := s.findProject(ctx, name)
	if err != nil {
		return err
	}
	if current.CanManage {
		return errors.New("Project became managed")
	}
	cleaner, ok := s.containers.(RuntimeProjectCleaner)
	if !ok {
		return errors.New("runtime cannot clean external Project")
	}
	return cleaner.CleanupComposeProject(ctx, name, false, report)
}

var _ io.Writer = (*reportWriter)(nil)

// AI configuration and build actions may reference local project files only.
// Bind mounts are separately validated against the target host rules.
func (s *Service) ValidateAgentSources(name, content string) error {
	path, err := s.safePath(name)
	if err != nil {
		return err
	}
	var document map[string]any
	if yaml.Unmarshal([]byte(content), &document) != nil {
		return errors.New("invalid Compose configuration")
	}
	if document["include"] != nil {
		return errors.New("AI configuration cannot add Compose include files")
	}
	check := func(value string) error {
		if value == "" {
			return nil
		}
		if strings.Contains(value, "$") || strings.Contains(value, "://") {
			return errors.New("AI file sources must be explicit paths inside the Project")
		}
		candidate := value
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(path, candidate)
		}
		relative, err := filepath.Rel(path, filepath.Clean(candidate))
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("Compose file source escapes the Project root")
		}
		if resolved, e := filepath.EvalSymlinks(candidate); e == nil {
			relative, e = filepath.Rel(path, resolved)
			if e != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return errors.New("Compose source symlink escapes the Project root")
			}
		}
		return nil
	}
	services, _ := document["services"].(map[string]any)
	for _, entry := range services {
		service, _ := entry.(map[string]any)
		if service["extends"] != nil {
			return errors.New("AI configuration cannot add Compose extends files")
		}
		if context, ok := service["build"].(string); ok {
			if err := check(context); err != nil {
				return err
			}
		} else if build, ok := service["build"].(map[string]any); ok {
			context, _ := build["context"].(string)
			if err := check(context); err != nil {
				return err
			}
			dockerfile, _ := build["dockerfile"].(string)
			if dockerfile != "" {
				if err := check(filepath.Join(context, dockerfile)); err != nil {
					return err
				}
			}
			if build["additional_contexts"] != nil || build["secrets"] != nil || build["ssh"] != nil {
				return errors.New("AI build cannot introduce additional contexts, build secrets or SSH forwarding")
			}
		}
		files := []any{}
		switch value := service["env_file"].(type) {
		case string:
			files = []any{value}
		case []any:
			files = value
		case map[string]any:
			files = []any{value}
		}
		for _, entry := range files {
			file, _ := entry.(string)
			if value, ok := entry.(map[string]any); ok {
				file, _ = value["path"].(string)
			}
			if err := check(file); err != nil {
				return err
			}
		}
	}
	return nil
}
