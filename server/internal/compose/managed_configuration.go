package compose

import "context"

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
