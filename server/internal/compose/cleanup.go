package compose

import (
	"context"
	"errors"
	"fmt"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

// RuntimeProjectCleaner removes only Docker resources that the runtime can
// prove belong to one external Compose Project. Docker adapters implement this
// interface; the Compose domain does not import the Docker SDK.
type RuntimeProjectCleaner interface {
	CleanupComposeProject(context.Context, string, bool, task.Reporter) error
}

// CleanupExternalProject starts a destructive cleanup task for an unmanaged
// Compose Project. It deliberately does not use ComposeRunner because an
// external Project may not have a safe, renderable Compose configuration.
func (s *Service) CleanupExternalProject(ctx context.Context, name, confirmationName string, removeVolumes bool) (database.Task, error) {
	if !validName.MatchString(name) {
		return database.Task{}, fmt.Errorf("invalid Project name")
	}
	if confirmationName != name {
		return database.Task{}, fmt.Errorf("type the complete Project name to confirm cleanup")
	}
	project, err := s.findProject(ctx, name)
	if err != nil {
		return database.Task{}, err
	}
	if project.CanManage {
		return database.Task{}, fmt.Errorf("managed Project must be removed through Project deletion")
	}
	cleaner, ok := s.containers.(RuntimeProjectCleaner)
	if !ok {
		return database.Task{}, errors.New("Docker runtime does not support Compose Project cleanup")
	}
	if s.tasks == nil {
		return database.Task{}, errors.New("Task service is unavailable")
	}
	return s.tasks.StartForNode(s.effectiveNodeID(), s.effectiveNodeName(), "project.cleanup", "Clean "+name, func(taskCtx context.Context, report task.Reporter) error {
		unlock := s.lockProject(name)
		defer unlock()

		current, err := s.findProject(taskCtx, name)
		if err != nil {
			return err
		}
		if current.CanManage {
			return fmt.Errorf("Project became managed before cleanup started")
		}
		return cleaner.CleanupComposeProject(taskCtx, name, removeVolumes, report)
	})
}
