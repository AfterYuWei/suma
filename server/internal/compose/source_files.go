package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

const (
	maxSourceFiles     = 32
	maxSourceFileSize  = 2 << 20
	maxSourceTotalSize = 8 << 20
)

type SourceManifest struct {
	WorkingDirectory string
	ConfigFiles      []string
	Dependencies     []string
}

// ValidateLocalProjectSource proves that every file Compose may read while
// rendering is already visible to the SUMA process and stays below the
// reported Compose working directory. It never follows bind mount sources.
func ValidateLocalProjectSource(workingDirectory string, configFiles []string) (SourceManifest, error) {
	if !filepath.IsAbs(workingDirectory) || len(configFiles) == 0 {
		return SourceManifest{}, errors.New("Compose working directory and config files must be absolute")
	}
	base, err := filepath.EvalSymlinks(workingDirectory)
	if err != nil {
		return SourceManifest{}, errors.New("Compose working directory is not mapped into SUMA")
	}
	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return SourceManifest{}, errors.New("Compose working directory is unavailable")
	}
	manifest := SourceManifest{WorkingDirectory: base}
	seen, total := map[string]bool{}, int64(0)
	var inspectFile func(string, bool) error
	inspectFile = func(name string, config bool) error {
		if strings.Contains(name, "$") {
			return fmt.Errorf("interpolated file path %q cannot be proven safe", name)
		}
		if !filepath.IsAbs(name) {
			name = filepath.Join(base, name)
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(name))
		if err != nil {
			return fmt.Errorf("file %q is not mapped into SUMA", name)
		}
		if !belowDirectory(resolved, base) {
			return fmt.Errorf("file %q is outside the Compose working directory", name)
		}
		if seen[resolved] {
			return nil
		}
		if len(seen) >= maxSourceFiles {
			return fmt.Errorf("Compose source exceeds %d files", maxSourceFiles)
		}
		stat, err := os.Stat(resolved)
		if err != nil || !stat.Mode().IsRegular() {
			return fmt.Errorf("file %q is not a regular file", name)
		}
		if stat.Size() > maxSourceFileSize {
			return fmt.Errorf("file %q exceeds 2 MiB", name)
		}
		total += stat.Size()
		if total > maxSourceTotalSize {
			return errors.New("Compose source exceeds 8 MiB in total")
		}
		content, err := os.ReadFile(resolved)
		if err != nil {
			return err
		}
		seen[resolved] = true
		if config {
			manifest.ConfigFiles = append(manifest.ConfigFiles, resolved)
		} else {
			manifest.Dependencies = append(manifest.Dependencies, resolved)
		}
		if !config || !isYAMLFile(resolved) {
			return nil
		}
		var document map[string]any
		if err := yaml.Unmarshal(content, &document); err != nil {
			return fmt.Errorf("parse Compose source %q: %w", name, err)
		}
		for _, dependency := range composeFileDependencies(document) {
			if err := inspectFile(dependency.path, dependency.compose); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range configFiles {
		if !filepath.IsAbs(name) {
			return SourceManifest{}, fmt.Errorf("Compose config file %q must be absolute", name)
		}
		if err := inspectFile(name, true); err != nil {
			return SourceManifest{}, err
		}
	}
	defaultEnvironment := filepath.Join(base, ".env")
	if _, err := os.Lstat(defaultEnvironment); err == nil {
		if err := inspectFile(defaultEnvironment, false); err != nil {
			return SourceManifest{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return SourceManifest{}, err
	}
	return manifest, nil
}

type fileDependency struct {
	path    string
	compose bool
}

func composeFileDependencies(document map[string]any) []fileDependency {
	result := []fileDependency{}
	appendValues := func(value any, compose bool) {
		for _, path := range filePaths(value) {
			if strings.TrimSpace(path) != "" {
				result = append(result, fileDependency{path: path, compose: compose})
			}
		}
	}
	appendValues(document["include"], true)
	services, _ := document["services"].(map[string]any)
	for _, raw := range services {
		service, _ := raw.(map[string]any)
		appendValues(service["env_file"], false)
		appendValues(service["label_file"], false)
	}
	for _, section := range []string{"configs", "secrets"} {
		values, _ := document[section].(map[string]any)
		for _, raw := range values {
			entry, _ := raw.(map[string]any)
			if path, _ := entry["file"].(string); path != "" {
				result = append(result, fileDependency{path: path})
			}
		}
	}
	return result
}

func filePaths(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		result := []string{}
		for _, item := range typed {
			result = append(result, filePaths(item)...)
		}
		return result
	case map[string]any:
		result := []string{}
		if path, ok := typed["path"]; ok {
			result = append(result, filePaths(path)...)
		}
		if files, ok := typed["env_file"]; ok {
			result = append(result, filePaths(files)...)
		}
		return result
	default:
		return nil
	}
}

func belowDirectory(path, directory string) bool {
	relative, err := filepath.Rel(directory, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func isYAMLFile(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".yml" || extension == ".yaml"
}
