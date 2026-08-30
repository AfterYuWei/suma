package compose

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

// ValidateRemoteBindMounts rejects unsafe remote bind declarations. Remote host
// paths must be explicit absolute paths because SUMA cannot resolve them locally.
func ValidateRemoteBindMounts(content string) error {
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
			if !bind {
				continue
			}
			if source == "/var/run/docker.sock" || bindTarget(raw) == "/var/run/docker.sock" {
				return fmt.Errorf("service %q cannot mount the Docker socket", serviceName)
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
