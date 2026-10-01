package compose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type CleanupReferences struct {
	Images   []string
	Networks []string
	Volumes  []string
}

// Render all managed projects and profiles without returning or logging secrets.
// Invalid/unreadable configuration fails closed rather than hiding a project.
func (s *Service) CleanupResourceReferences(ctx context.Context) (CleanupReferences, error) {
	result := CleanupReferences{}
	base := s.nodeRoot()
	// The configured root must remain readable. Only an absent per-node
	// subdirectory can represent a node with no managed projects yet.
	realRoot, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return result, err
	}
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return result, fmt.Errorf("symlink below managed Compose root")
		}
		if !entry.IsDir() || !nativeProjectName.MatchString(entry.Name()) {
			continue
		}
		dir := filepath.Join(base, entry.Name())
		file := filepath.Join(dir, "compose.yml")
		info, err := os.Lstat(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return result, err
		}
		real, err := filepath.EvalSymlinks(file)
		if err != nil {
			return result, err
		}
		relative, err := filepath.Rel(realRoot, real)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return result, fmt.Errorf("managed project escapes Compose root")
		}
		if _, err := readManagedProjectMetadata(dir, s.effectiveNodeID(), entry.Name(), info.ModTime()); err != nil {
			return result, fmt.Errorf("unable to read managed project metadata")
		}
		rendered, err := s.runner.Render(ctx, ExecutionSpec{ProjectName: entry.Name(), ProjectDir: dir, Files: []string{file}, Profiles: []string{"*"}}, io.Discard)
		if err != nil {
			return result, fmt.Errorf("unable to render managed project for cleanup protection")
		}
		var config struct {
			Services map[string]struct {
				Image string `json:"image"`
			} `json:"services"`
			Networks map[string]struct {
				Name string `json:"name"`
			} `json:"networks"`
			Volumes map[string]struct {
				Name string `json:"name"`
			} `json:"volumes"`
		}
		if err = json.Unmarshal([]byte(rendered), &config); err != nil {
			return result, fmt.Errorf("invalid rendered project for cleanup protection")
		}
		for _, service := range config.Services {
			if service.Image != "" {
				result.Images = append(result.Images, service.Image)
			}
		}
		for key, network := range config.Networks {
			name := network.Name
			if name == "" {
				name = entry.Name() + "_" + key
			}
			result.Networks = append(result.Networks, name)
		}
		for key, volume := range config.Volumes {
			name := volume.Name
			if name == "" {
				name = entry.Name() + "_" + key
			}
			result.Volumes = append(result.Volumes, name)
		}
	}
	return result, nil
}
