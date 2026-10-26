package api

import (
	"context"

	projectdomain "github.com/suma/suma/server/internal/project"
)

func annotateDeliveryProjectNames(ctx context.Context, deps Dependencies, nodeID string, rows ...*projectdomain.Summary) error {
	if deps.CD == nil || len(rows) == 0 {
		return nil
	}
	names, err := deps.CD.RuntimeProjectNames(ctx, nodeID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !row.Managed && row.Backend == projectdomain.BackendCompose {
			row.DeliveryProjectName = names[row.NativeName]
		}
	}
	return nil
}
