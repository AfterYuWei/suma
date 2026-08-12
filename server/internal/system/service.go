package system

import (
	"context"

	"github.com/dockport/dockport/server/internal/database"
	"github.com/dockport/dockport/server/internal/task"
)

type Adapter interface {
	Prune(context.Context, task.Reporter) error
}
type Service struct {
	adapter Adapter
	tasks   *task.Service
}

func NewService(adapter Adapter, tasks *task.Service) *Service {
	return &Service{adapter: adapter, tasks: tasks}
}
func (s *Service) Prune() (database.Task, error) {
	return s.tasks.Start("system.prune", "Prune unused Docker resources", func(ctx context.Context, report task.Reporter) error { return s.adapter.Prune(ctx, report) })
}
