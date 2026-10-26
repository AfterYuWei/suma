package compose

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/suma/suma/server/internal/credential"
)

func (s *Service) SetRegistryCredentials(service *credential.RegistryService) { s.registries = service }

func (s *Service) registryRunner(ctx context.Context, project Project) (Runner, string, error) {
	if s.registries == nil {
		return s.runner, "", nil
	}
	has, err := s.registries.HasNodeCredentials(ctx, s.effectiveNodeID())
	if err != nil || !has {
		return s.runner, "", err
	}
	rendered, err := s.runner.Render(ctx, ExecutionSpec{ProjectName: project.Name, ProjectDir: project.Path}, io.Discard)
	if err != nil {
		return nil, "", errors.New("unable to resolve Compose images for registry authentication")
	}
	var configuration struct {
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if json.Unmarshal([]byte(rendered), &configuration) != nil {
		return nil, "", errors.New("unable to read rendered Compose images")
	}
	images := []string{}
	for _, service := range configuration.Services {
		if service.Image != "" {
			images = append(images, service.Image)
		}
	}
	config, err := s.registries.DockerConfigForNode(ctx, s.effectiveNodeID(), images)
	if err != nil || config == "" {
		return s.runner, "", err
	}
	configured, ok := s.runner.(interface{ WithDockerConfig(string) Runner })
	if !ok {
		return nil, "", errors.New("Compose runner does not support registry authentication")
	}
	return configured.WithDockerConfig(config), config, nil
}

// ReportWriter assembles complete lines before this function runs, so split
// process writes cannot expose credentials to live progress or persisted logs.
func registryRedactor(config string, previous func(string) string) func(string) string {
	var parsed struct {
		Auths map[string]struct {
			Auth          string `json:"auth"`
			IdentityToken string `json:"identitytoken"`
		} `json:"auths"`
	}
	if json.Unmarshal([]byte(config), &parsed) != nil {
		return func(string) string { return "Compose registry diagnostic hidden" }
	}
	values := []string{}
	for _, entry := range parsed.Auths {
		if entry.IdentityToken != "" {
			values = append(values, entry.IdentityToken)
		}
		if entry.Auth != "" {
			values = append(values, entry.Auth)
			if raw, err := base64.StdEncoding.DecodeString(entry.Auth); err == nil {
				if _, password, ok := strings.Cut(string(raw), ":"); ok && password != "" {
					values = append(values, password)
				}
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return func(message string) string {
		for _, value := range values {
			if len(value) < 4 && strings.Contains(message, value) {
				return "Compose diagnostic hidden because it contains a short registry secret"
			}
			message = strings.ReplaceAll(message, value, "[REDACTED]")
		}
		return previous(message)
	}
}
