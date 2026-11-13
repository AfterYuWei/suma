package compose

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

type ResolvedProjectImages struct {
	Revision string
	Images   map[string]string
}

// ResolvedImages returns only declared image references, never the rendered
// environment or other private configuration. A nil result denotes an external
// project, for which runtime tags can still be checked without granting writes.
func (s *Service) ResolvedImages(ctx context.Context, name string) (*ResolvedProjectImages, error) {
	unlock := s.lockProject(name)
	defer unlock()
	projects, err := s.managedProjects()
	if err != nil {
		return nil, err
	}
	var project *Project
	for i := range projects {
		if projects[i].Name == name {
			project = &projects[i]
			break
		}
	}
	if project == nil {
		return nil, nil
	}
	snapshot, err := readConfiguration(project.Path)
	if err != nil {
		return nil, err
	}
	revision := configurationRevision(snapshot[0], snapshot[1])
	rendered, err := s.runner.Render(ctx, ExecutionSpec{ProjectName: name, ProjectDir: project.Path}, io.Discard)
	if err != nil {
		return nil, errors.New("unable to resolve project image references")
	}
	var config struct {
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if err := json.Unmarshal([]byte(rendered), &config); err != nil {
		return nil, errors.New("invalid rendered project image configuration")
	}
	if err := s.checkRevision(project.Path, revision); err != nil {
		return nil, err
	}
	result := &ResolvedProjectImages{Revision: revision, Images: make(map[string]string, len(config.Services))}
	for service, value := range config.Services {
		result.Images[service] = value.Image
	}
	return result, nil
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
