package compose

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

const DockerSocketConfirmationHeader = "X-SUMA-Allow-Docker-Socket"

// ValidateComposeBindMounts requires explicit authorization for Docker socket
// access. Remote host paths must additionally be explicit absolute paths
// because SUMA cannot resolve them locally.
func ValidateComposeBindMounts(content string, remote, allowDockerSocket bool) error {
	var document struct {
		Services map[string]struct {
			Volumes []any `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return fmt.Errorf("parse Compose file: %w", err)
	}
	for serviceName, service := range document.Services {
		for _, raw := range service.Volumes {
			source, bind, err := bindSource(raw)
			if err != nil {
				return fmt.Errorf("service %q volume: %w", serviceName, err)
			}
			dockerSocket := source == "/var/run/docker.sock" || bindTarget(raw) == "/var/run/docker.sock"
			if dockerSocket && !allowDockerSocket {
				return fmt.Errorf("service %q Docker socket mount requires explicit confirmation", serviceName)
			}
			// An interpolated short source may not look like a bind until Compose
			// resolves it, but a Docker socket target makes the intent explicit.
			bind = bind || dockerSocket
			if !bind {
				continue
			}
			if !remote {
				continue
			}
			if strings.Contains(source, "$") {
				return fmt.Errorf("service %q bind source cannot be interpolated for a remote node", serviceName)
			}
			if !filepath.IsAbs(source) {
				return fmt.Errorf("service %q remote bind source %q must be absolute", serviceName, source)
			}
		}
	}
	return nil
}

// ValidateRemoteBindMounts preserves the strict, unconfirmed policy used by
// non-interactive callers.
func ValidateRemoteBindMounts(content string) error {
	return ValidateComposeBindMounts(content, true, false)
}

func bindTarget(raw any) string {
	switch value := raw.(type) {
	case string:
		parts := strings.SplitN(value, ":", 3)
		if len(parts) > 1 {
			return strings.TrimSpace(parts[1])
		}
	case map[string]any:
		target, _ := value["target"].(string)
		return strings.TrimSpace(target)
	}
	return ""
}

func bindSource(raw any) (string, bool, error) {
	switch value := raw.(type) {
	case string:
		parts := strings.SplitN(value, ":", 3)
		if len(parts) < 2 {
			return "", false, nil
		}
		source := strings.TrimSpace(parts[0])
		return source, strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/") || strings.Contains(source, "/"), nil
	case map[string]any:
		kind, _ := value["type"].(string)
		if kind != "bind" {
			return "", false, nil
		}
		source, _ := value["source"].(string)
		if source == "" {
			return "", true, errors.New("bind source is required")
		}
		return source, true, nil
	default:
		return "", false, errors.New("unsupported volume declaration")
	}
}
