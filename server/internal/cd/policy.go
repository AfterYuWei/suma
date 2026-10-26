package cd

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

// Bind sources belong to the target Docker host, including Unix-socket nodes.
// Validate their declared paths without consulting the control-plane filesystem.
func validateDeploymentPolicy(rendered string) error {
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
			source, target := path.Clean(volume.Source), path.Clean(volume.Target)
			if source == "/var/run/docker.sock" || target == "/var/run/docker.sock" || source == "/run/docker.sock" || target == "/run/docker.sock" {
				return fmt.Errorf("%s cannot mount the Docker socket", prefix)
			}
			if volume.Type != "bind" {
				continue
			}
			if !volume.ReadOnly {
				return fmt.Errorf("%s bind mount %q must be read-only", prefix, volume.Source)
			}
			if err := validateHostBindSource(volume.Source); err != nil {
				return fmt.Errorf("%s bind mount %q: %w", prefix, volume.Source, err)
			}
		}
	}
	return nil
}

func validateHostBindSource(source string) error {
	if strings.Contains(source, "$") || !path.IsAbs(source) {
		return errors.New("source must be a non-interpolated absolute path on the Docker host")
	}
	// Clean only lexically: resolving symlinks here would inspect the wrong
	// host. The Engine remains responsible for source existence and access.
	clean := path.Clean(source)
	for _, forbidden := range []string{"/", "/etc", "/proc", "/sys", "/dev", "/var/run", "/run"} {
		if clean == forbidden || strings.HasPrefix(clean, forbidden+"/") {
			return errors.New("source is a protected host path")
		}
	}
	return nil
}
