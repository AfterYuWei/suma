package system

import (
	"context"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
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
	return s.PruneForNode("local", "Local")
}
func (s *Service) PruneForNode(nodeID, nodeName string) (database.Task, error) {
	return s.tasks.StartDefinition(context.Background(), task.Definition{
		Type: "system.prune", Name: "Prune unused Docker resources",
		NodeID: nodeID, NodeName: nodeName,
	}, func(ctx context.Context, _ string, report task.Reporter) (any, error) {
		return nil, s.adapter.Prune(ctx, report)
	})
}
