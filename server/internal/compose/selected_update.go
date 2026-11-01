package compose

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/distribution/reference"
)

type ServiceImageUpdate struct {
	Name           string `json:"name"`
	Reference      string `json:"reference"`
	ManifestDigest string `json:"manifest_digest"`
	ConfigDigest   string `json:"config_digest"`
	OS             string `json:"os"`
	Architecture   string `json:"architecture"`
	Variant        string `json:"variant,omitempty"`
	Replicas       int    `json:"replicas"`
}

// SelectedUpdate runs inside the ordinary Compose task, lock and credential pipeline.
type SelectedUpdate struct {
	Services          []ServiceImageUpdate
	AllowDockerSocket bool
	Start             func(context.Context, string) error
	Check             func(context.Context) error
	Complete          func(string, error)
}

// ManagedConfiguration reads SUMA-owned files without querying a Docker engine.
// Runtime validation belongs to the asynchronous operation.
func (s *Service) ManagedConfiguration(name string) (Project, error) {
	unlock := s.lockProject(name)
	defer unlock()
	project, err := s.managedProject(name)
	if err != nil {
		return Project{}, err
	}
	snapshot, err := readConfiguration(project.Path)
	if err != nil {
		return Project{}, err
	}
	project.Compose, project.Environment = snapshot[0], snapshot[1]
	project.Revision = configurationRevision(project.Compose, project.Environment)
	if err := s.decorateDeployment(context.Background(), &project); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (s *Service) validateSelectedRuntime(ctx context.Context, project string, services []ServiceImageUpdate) error {
	rows, err := s.containers.List(ctx)
	if err != nil {
		return err
	}
	for _, service := range services {
		count := 0
		for _, row := range rows {
			if row.Labels[ProjectLabel] != project || row.Labels[ServiceLabel] != service.Name || strings.EqualFold(row.Labels["com.docker.compose.oneoff"], "true") {
				continue
			}
			if row.State != "running" {
				return errors.New("service has stopped or paused replicas; check the project again")
			}
			count++
		}
		if count != service.Replicas || count == 0 {
			return errors.New("service replicas changed; check the project again")
		}
	}
	return nil
}

func (s *Service) selectedSpec(ctx context.Context, project Project, update SelectedUpdate) (ExecutionSpec, func(), error) {
	spec := ExecutionSpec{ProjectName: project.Name, ProjectDir: project.Path, Files: []string{filepath.Join(project.Path, "compose.yml")}, EnvFiles: []string{filepath.Join(project.Path, ".env")}, Replicas: map[string]int{}}
	if _, err := os.Stat(filepath.Join(project.Path, ".env")); errors.Is(err, os.ErrNotExist) {
		spec.EnvFiles = nil
	} else if err != nil {
		return spec, nil, err
	}
	if len(update.Services) == 0 {
		return spec, nil, errors.New("no running services to publish")
	}
	rendered, err := s.runner.Render(ctx, ExecutionSpec{ProjectName: project.Name, ProjectDir: project.Path}, io.Discard)
	if err != nil {
		return spec, nil, errors.New("unable to resolve project image references")
	}
	var configuration struct {
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(rendered), &configuration); err != nil {
		return spec, nil, err
	}
	overrides := map[string]map[string]string{}
	for _, service := range update.Services {
		if service.Name == "" || strings.HasPrefix(service.Name, "-") || service.Replicas < 1 || overrides[service.Name] != nil {
			return spec, nil, errors.New("invalid selected service")
		}
		parsed, err := reference.ParseNormalizedNamed(configuration.Services[service.Name].Image)
		if err != nil || reference.TagNameOnly(parsed).String() != service.Reference {
			return spec, nil, errors.New("project image reference changed; check the project again")
		}
		pinned, err := reference.ParseNormalizedNamed(reference.TrimNamed(parsed).String() + "@" + service.ManifestDigest)
		if err != nil {
			return spec, nil, errors.New("invalid image manifest digest")
		}
		platform := service.OS + "/" + service.Architecture
		if service.Variant != "" {
			platform += "/" + service.Variant
		}
		overrides[service.Name] = map[string]string{"image": pinned.String(), "platform": platform}
		spec.Services = append(spec.Services, service.Name)
		spec.Replicas[service.Name] = service.Replicas
	}
	if err := s.validateSelectedRuntime(ctx, project.Name, update.Services); err != nil {
		return spec, nil, err
	}
	dir, err := os.MkdirTemp(project.Path, ".suma-image-update-")
	if err != nil {
		return spec, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	content, err := json.Marshal(map[string]any{"services": overrides})
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "images.json"), content, 0600)
	}
	if err != nil {
		cleanup()
		return spec, nil, err
	}
	spec.Files = append(spec.Files, filepath.Join(dir, "images.json"))
	return spec, cleanup, nil
}
