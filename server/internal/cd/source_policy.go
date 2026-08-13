package cd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dockport/dockport/server/internal/compose"
	"github.com/goccy/go-yaml"
)

const maxDeploymentFileSize = 2 << 20

func validateComposeSources(spec compose.ExecutionSpec, worktreeRoot string) error {
	root, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		return fmt.Errorf("resolve Git worktree: %w", err)
	}
	for _, file := range spec.Files {
		if err := validateComposeSourceFile(file, spec.ProjectDir, root); err != nil {
			return err
		}
	}
	// Compose implicitly loads ProjectDir/.env when no explicit --env-file is
	// configured. Apply the same containment and size rules to that input.
	if len(spec.EnvFiles) == 0 {
		implicit := filepath.Join(spec.ProjectDir, ".env")
		if _, err := os.Lstat(implicit); err == nil {
			if _, err := safeSourceReference(spec.ProjectDir, root, ".env"); err != nil {
				return fmt.Errorf("implicit Compose environment file: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect implicit Compose environment file: %w", err)
		}
	}
	return nil
}

func validateComposeSourceFile(file, projectDir, root string) error {
	content, err := readDeploymentFile(file)
	if err != nil {
		return fmt.Errorf("read Compose file %q: %w", file, err)
	}
	document := map[string]any{}
	if err := yaml.Unmarshal(content, &document); err != nil {
		return fmt.Errorf("parse Compose file %q: %w", file, err)
	}
	if _, exists := document["include"]; exists {
		return fmt.Errorf("Compose file %q uses include, which is not allowed for Git delivery", file)
	}
	for _, section := range []string{"configs", "secrets"} {
		entries, _ := document[section].(map[string]any)
		for name, raw := range entries {
			entry, _ := raw.(map[string]any)
			if value, ok := entry["file"].(string); ok {
				if _, err := safeSourceReference(projectDir, root, value); err != nil {
					return fmt.Errorf("%s %q file: %w", section, name, err)
				}
			}
		}
	}
	services, _ := document["services"].(map[string]any)
	for name, raw := range services {
		service, _ := raw.(map[string]any)
		if value, exists := service["build"]; exists && value != nil {
			return fmt.Errorf("service %q uses build; DockPort CD only deploys prebuilt images", name)
		}
		if _, exists := service["extends"]; exists {
			return fmt.Errorf("service %q uses extends, which is not allowed for Git delivery", name)
		}
		for _, key := range []string{"env_file", "label_file"} {
			for _, value := range composePathValues(service[key]) {
				if _, err := safeSourceReference(projectDir, root, value); err != nil {
					return fmt.Errorf("service %q %s: %w", name, key, err)
				}
			}
		}
	}
	return nil
}

func composePathValues(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		var values []string
		for _, item := range typed {
			switch entry := item.(type) {
			case string:
				values = append(values, entry)
			case map[string]any:
				if path, ok := entry["path"].(string); ok {
					values = append(values, path)
				}
			}
		}
		return values
	default:
		return nil
	}
}

func safeSourceReference(projectDir, root, value string) (string, error) {
	// Compose supports both $VARIABLE and ${VARIABLE} interpolation. Resolve no
	// source path whose meaning can change after this policy check.
	if value == "" || strings.Contains(value, "$") {
		return "", fmt.Errorf("path %q is empty or interpolated", value)
	}
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectDir, filepath.FromSlash(value))
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", value, err)
	}
	if err := below(root, resolved); err != nil {
		return "", fmt.Errorf("path %q escapes the Git worktree", value)
	}
	if _, err := readDeploymentFile(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func readDeploymentFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if info.Size() > maxDeploymentFileSize {
		return nil, fmt.Errorf("file exceeds the %d-byte limit", maxDeploymentFileSize)
	}
	return os.ReadFile(path)
}
