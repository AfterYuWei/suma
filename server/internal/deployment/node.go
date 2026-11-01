package deployment

import (
	"context"
	"errors"
	"io"

	"github.com/suma/suma/server/internal/compose"
)

// DeployNode is the single-node delivery business flow. Project CD calls it
// once; Continuous Delivery calls it for each target and aggregates the results.
// Owners persist their own progress through observe; the execution order and
// health acceptance are identical for both entrypoints.
func DeployNode(ctx context.Context, runner compose.Runner, spec compose.ExecutionSpec, timeout int, observe func(string) (io.Writer, error), beforeApply func(context.Context) error) (string, error) {
	health := ""
	for _, stage := range []string{"pulling", "deploying", "verifying"} {
		if err := ctx.Err(); err != nil {
			return health, err
		}
		output, err := observe(stage)
		if err != nil {
			return health, err
		}
		if output == nil {
			output = io.Discard
		}
		switch stage {
		case "pulling":
			err = runner.PullRelease(ctx, spec, output)
		case "deploying":
			if beforeApply != nil {
				err = beforeApply(ctx)
			}
			if err == nil {
				err = runner.UpRelease(ctx, spec, timeout, output)
			}
		case "verifying":
			health, err = runner.PS(ctx, spec, io.Discard)
			if err == nil && !RuntimeIsHealthy(health) {
				err = errors.New("Compose services did not reach a running and healthy state")
			}
		}
		if err != nil {
			return health, err
		}
	}
	return health, nil
}
