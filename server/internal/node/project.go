package node

import (
	"context"

	"github.com/suma/suma/server/internal/compose"
)

func (s *Service) InspectComposeNamespace(ctx context.Context, nodeID, name string) (compose.RuntimeProjectSnapshot, error) {
	runtime, err := s.Runtime(ctx, nodeID)
	if err != nil {
		return compose.RuntimeProjectSnapshot{}, err
	}
	return runtime.InspectComposeNamespace(ctx, name)
}
