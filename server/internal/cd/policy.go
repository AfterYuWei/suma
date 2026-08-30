package cd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func validateDeploymentPolicy(worktreeRoot string, rendered string) error {
	var document struct {
		Services map[string]struct {
			Image       string           `json:"image"`
			Build       json.RawMessage  `json:"build"`
			Privileged  bool             `json:"privileged"`
			NetworkMode string           `json:"network_mode"`
			Pid         string           `json:"pid"`
			IPC         string           `json:"ipc"`
			Devices     []map[string]any `json:"devices"`
			VolumesFrom []string         `json:"volumes_from"`
			CapAdd      []string         `json:"cap_add"`
			SecurityOpt []string         `json:"security_opt"`
			Cgroup      string           `json:"cgroup"`
			Volumes     []struct {
				Type     string `json:"type"`
				Source   string `json:"source"`
				Target   string `json:"target"`
				ReadOnly bool   `json:"read_only"`
			} `json:"volumes"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(rendered), &document); err != nil {
		return fmt.Errorf("parse rendered Compose configuration: %w", err)
	}
	if len(document.Services) == 0 {
		return errors.New("Compose configuration must define at least one service")
	}
	root, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		return fmt.Errorf("resolve Git worktree: %w", err)
	}
	for name, service := range document.Services {
		prefix := fmt.Sprintf("service %q", name)
		if service.Image == "" {
			return fmt.Errorf("%s must reference a prebuilt image; SUMA CD does not build images", prefix)
		}
		if len(service.Build) > 0 && string(service.Build) != "null" && string(service.Build) != "{}" {
			return fmt.Errorf("%s uses build; SUMA CD only deploys prebuilt images", prefix)
		}
		if service.Privileged {
			return fmt.Errorf("%s cannot use privileged mode", prefix)
		}
		if service.NetworkMode == "host" || service.Pid == "host" || service.IPC == "host" {
			return fmt.Errorf("%s cannot share a host namespace", prefix)
		}
		if len(service.Devices) > 0 {
			return fmt.Errorf("%s cannot expose host devices", prefix)
		}
		if len(service.VolumesFrom) > 0 {
			return fmt.Errorf("%s cannot inherit another container's volumes", prefix)
		}
		for _, capability := range service.CapAdd {
			if strings.EqualFold(capability, "ALL") || strings.EqualFold(capability, "SYS_ADMIN") {
				return fmt.Errorf("%s cannot add high-risk capability %q", prefix, capability)
			}
		}
		for _, option := range service.SecurityOpt {
			lower := strings.ToLower(option)
			if strings.Contains(lower, "unconfined") || strings.HasPrefix(lower, "label:disable") {
				return fmt.Errorf("%s cannot disable its security confinement", prefix)
			}
		}
		if strings.EqualFold(service.Cgroup, "host") {
			return fmt.Errorf("%s cannot join the host cgroup namespace", prefix)
		}
		for _, volume := range service.Volumes {
			if volume.Source == "/var/run/docker.sock" || volume.Target == "/var/run/docker.sock" {
				return fmt.Errorf("%s cannot mount the Docker socket", prefix)
			}
			if volume.Type != "bind" {
				continue
			}
			if !volume.ReadOnly {
				return fmt.Errorf("%s bind mount %q must be read-only", prefix, volume.Source)
			}
			source, err := existingPath(volume.Source)
			if err != nil {
				return fmt.Errorf("%s bind mount %q: %w", prefix, volume.Source, err)
			}
			if err := below(root, source); err != nil {
				return fmt.Errorf("%s bind mount %q escapes the Git worktree", prefix, volume.Source)
			}
		}
	}
	return nil
}

func validateRemoteDeploymentPolicy(rendered string) error {
	var document struct {
		Services map[string]struct {
			Image       string           `json:"image"`
			Build       json.RawMessage  `json:"build"`
			Privileged  bool             `json:"privileged"`
			NetworkMode string           `json:"network_mode"`
			Pid         string           `json:"pid"`
			IPC         string           `json:"ipc"`
			Devices     []map[string]any `json:"devices"`
			VolumesFrom []string         `json:"volumes_from"`
			CapAdd      []string         `json:"cap_add"`
			SecurityOpt []string         `json:"security_opt"`
			Cgroup      string           `json:"cgroup"`
			Volumes     []struct {
				Type     string `json:"type"`
				Source   string `json:"source"`
				Target   string `json:"target"`
				ReadOnly bool   `json:"read_only"`
			} `json:"volumes"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(rendered), &document); err != nil {
		return fmt.Errorf("parse rendered Compose configuration: %w", err)
	}
	if len(document.Services) == 0 {
		return errors.New("Compose configuration must define at least one service")
	}
	for name, service := range document.Services {
		prefix := fmt.Sprintf("service %q", name)
		if service.Image == "" || (len(service.Build) > 0 && string(service.Build) != "null" && string(service.Build) != "{}") {
			return fmt.Errorf("%s must use a prebuilt image", prefix)
		}
		if service.Privileged || service.NetworkMode == "host" || service.Pid == "host" || service.IPC == "host" || len(service.Devices) > 0 || len(service.VolumesFrom) > 0 || strings.EqualFold(service.Cgroup, "host") {
			return fmt.Errorf("%s uses a protected host capability", prefix)
		}
		for _, capability := range service.CapAdd {
			if strings.EqualFold(capability, "ALL") || strings.EqualFold(capability, "SYS_ADMIN") {
				return fmt.Errorf("%s cannot add high-risk capability %q", prefix, capability)
			}
		}
		for _, option := range service.SecurityOpt {
			lower := strings.ToLower(option)
			if strings.Contains(lower, "unconfined") || strings.HasPrefix(lower, "label:disable") {
				return fmt.Errorf("%s cannot disable security confinement", prefix)
			}
		}
		for _, volume := range service.Volumes {
			if volume.Source == "/var/run/docker.sock" || volume.Target == "/var/run/docker.sock" {
				return fmt.Errorf("%s cannot mount the Docker socket", prefix)
			}
			if volume.Type != "bind" {
				continue
			}
			if !volume.ReadOnly {
				return fmt.Errorf("%s bind mount %q must be read-only", prefix, volume.Source)
			}
			if strings.Contains(volume.Source, "$") || !filepath.IsAbs(volume.Source) {
				return fmt.Errorf("%s bind mount %q must be a non-interpolated absolute path", prefix, volume.Source)
			}
		}
	}
	return nil
}

func existingPath(value string) (string, error) {
	if value == "" {
		return "", errors.New("source is empty")
	}
	clean := filepath.Clean(value)
	if !filepath.IsAbs(clean) {
		return "", errors.New("rendered source must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("source does not exist")
		}
		return "", err
	}
	for _, forbidden := range []string{"/", "/etc", "/proc", "/sys", "/dev", "/var/run"} {
		if resolved == forbidden || strings.HasPrefix(resolved, forbidden+string(filepath.Separator)) {
			return "", errors.New("source is a protected host path")
		}
	}
	return resolved, nil
}
