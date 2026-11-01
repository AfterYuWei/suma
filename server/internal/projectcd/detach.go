package projectcd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
	"github.com/suma/suma/server/internal/task"
)

func (s *Service) Detach(ctx context.Context, node, name string, revision uint64, actor Actor) (database.Task, error) {
	return s.submit(ctx, node, name, "detach", "manual", actor, func(ctx context.Context, record *database.ProjectCDRecord, row database.ProjectCD, report task.Reporter) error {
		if row.Revision != revision {
			return ErrConflict
		}
		if err := s.db.WithContext(ctx).Model(&row).Update("automation_enabled", false).Error; err != nil {
			return err
		}
		project, err := s.deps.Project(ctx, node)
		if err != nil {
			return err
		}
		if row.AppliedRecordID != nil {
			applied, err := s.record(ctx, row, *row.AppliedRecordID)
			if err != nil {
				return err
			}
			value, err := s.decode(applied)
			if err != nil {
				return err
			}
			record.CommitSHA = applied.CommitSHA
			record.ConfigHash = applied.ConfigHash
			record.SnapshotCiphertext = applied.SnapshotCiphertext
			if err := s.db.WithContext(ctx).Save(record).Error; err != nil {
				return err
			}
			if err := s.git.Verify(ctx, value.Worktree, applied.CommitSHA); err != nil {
				return err
			}
			spec, err := deployment.Spec(name, value.Worktree, value.Repository)
			if err != nil {
				return err
			}
			spec.Environment = value.Environment
			target, err := s.deps.Target(ctx, node)
			if err != nil {
				return err
			}
			if target.RuntimeIdentity != value.RuntimeIdentity {
				return errors.New("node runtime changed")
			}
			runner := s.runner.ForTarget(target)
			report(20, "Preparing standalone manual configuration")
			raw, err := runner.ExportConfiguration(ctx, spec)
			if err != nil {
				return errors.New("cannot export Compose while preserving interpolation; CD mode remains enabled")
			}
			ordinary, err := project.ManagedConfiguration(name)
			if err != nil {
				return err
			}
			dependencyRoot := filepath.Join(ordinary.Path, ".suma", "gitops-export", fmt.Sprint(applied.ID))
			configuration, expected, err := exportDependencies(raw, value.Rendered, spec.ProjectDir, value.Worktree, dependencyRoot)
			if err != nil {
				return err
			}
			environment := ""
			envFile := filepath.Join(spec.ProjectDir, ".env")
			if len(spec.EnvFiles) > 0 {
				envFile = spec.EnvFiles[0]
			}
			data, err := os.ReadFile(envFile)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			environment = string(data)
			// Use the same literal escaping as the execution environment. Existing
			// repository expressions are evaluated by Compose, followed by local values.
			names := make([]string, 0, len(value.Environment))
			for key := range value.Environment {
				names = append(names, key)
			}
			sort.Strings(names)
			escape := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "$", "$$", "\n", "\\n", "\r", "\\r", "\t", "\\t")
			environment += "\n"
			for _, key := range names {
				environment += key + "=\"" + escape.Replace(value.Environment[key]) + "\"\n"
			}
			staging, err := os.MkdirTemp(ordinary.Path, ".gitops-detach-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(staging)
			if err := os.WriteFile(filepath.Join(staging, "compose.yml"), []byte(configuration), 0600); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(staging, ".env"), []byte(environment), 0600); err != nil {
				return err
			}
			verify := compose.ExecutionSpec{ProjectName: name, ProjectDir: ordinary.Path, Files: []string{filepath.Join(staging, "compose.yml")}, EnvFiles: []string{filepath.Join(staging, ".env")}}
			if err := runner.ValidateRelease(ctx, verify, io.Discard); err != nil {
				return errors.New("manual export validation failed; CD mode remains enabled")
			}
			rendered, err := runner.Render(ctx, verify, io.Discard)
			if err != nil {
				return errors.New("manual export rendering failed")
			}
			var got, want any
			if err := json.Unmarshal([]byte(rendered), &got); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(expected), &want); err != nil {
				return err
			}
			if !reflect.DeepEqual(got, want) {
				return errors.New("manual export differs from the successful snapshot; CD mode remains enabled")
			}
			report(80, "Publishing verified manual configuration")
			if err := project.PublishCDExport(ctx, name, configuration, environment, applied.FinishedAt); err != nil {
				return err
			}
		}
		// Ownership is released last. Interrupted publication remains protected and
		// can be retried from the retained immutable applied snapshot after restart.
		if err := s.db.WithContext(ctx).Model(&row).Updates(map[string]any{"enabled": false, "automation_enabled": false, "initial_application_required": true, "revision": row.Revision + 1}).Error; err != nil {
			return err
		}
		report(100, "Manual mode restored; running containers and Git history retained")
		return nil
	})
}

// exportDependencies copies the validated file closure, leaving immutable
// worktrees intact for containers that still mount their original files.
func exportDependencies(raw, rendered, projectDir, worktree, destination string) (string, string, error) {
	var document map[string]any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return "", "", err
	}
	root, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return "", "", err
	}
	replacements := map[string]string{}
	copyFile := func(source string) (string, error) {
		path := source
		if !filepath.IsAbs(path) {
			path = filepath.Join(projectDir, path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return "", errors.New("export dependency escapes worktree")
		}
		content, err := deployment.ReadDeploymentFile(resolved)
		if err != nil {
			return "", err
		}
		target := filepath.Join(destination, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, content, 0600); err != nil {
			return "", err
		}
		replacements[resolved] = target
		replacements[filepath.Clean(path)] = target
		return target, nil
	}
	for _, section := range []string{"configs", "secrets"} {
		entries, _ := document[section].(map[string]any)
		for _, entry := range entries {
			object, _ := entry.(map[string]any)
			if path, ok := object["file"].(string); ok {
				next, err := copyFile(path)
				if err != nil {
					return "", "", err
				}
				object["file"] = next
			}
		}
	}
	services, _ := document["services"].(map[string]any)
	for _, entry := range services {
		service, _ := entry.(map[string]any)
		for _, key := range []string{"env_file", "label_file"} {
			switch files := service[key].(type) {
			case string:
				next, err := copyFile(files)
				if err != nil {
					return "", "", err
				}
				service[key] = next
			case []any:
				for i, file := range files {
					switch item := file.(type) {
					case string:
						next, err := copyFile(item)
						if err != nil {
							return "", "", err
						}
						files[i] = next
					case map[string]any:
						path, ok := item["path"].(string)
						if !ok {
							return "", "", errors.New("unsupported file reference")
						}
						next, err := copyFile(path)
						if err != nil {
							return "", "", err
						}
						item["path"] = next
					}
				}
			}
		}
	}
	var expected any
	if err := json.Unmarshal([]byte(rendered), &expected); err != nil {
		return "", "", err
	}
	var replace func(any) any
	replace = func(value any) any {
		switch typed := value.(type) {
		case string:
			if next, ok := replacements[typed]; ok {
				return next
			}
		case map[string]any:
			for key, entry := range typed {
				typed[key] = replace(entry)
			}
		case []any:
			for i, entry := range typed {
				typed[i] = replace(entry)
			}
		}
		return value
	}
	desired, err := json.Marshal(replace(expected))
	if err != nil {
		return "", "", err
	}
	result, err := json.MarshalIndent(document, "", "  ")
	return string(result) + "\n", string(desired), err
}
