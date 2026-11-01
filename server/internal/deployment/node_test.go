package deployment

import (
	"context"
	"errors"
	"github.com/suma/suma/server/internal/compose"
	"io"
	"reflect"
	"testing"
)

type nodeRunner struct {
	compose.Runner
	calls   []string
	pullErr error
	health  string
}

func (r *nodeRunner) PullRelease(context.Context, compose.ExecutionSpec, io.Writer) error {
	r.calls = append(r.calls, "pull")
	return r.pullErr
}
func (r *nodeRunner) UpRelease(context.Context, compose.ExecutionSpec, int, io.Writer) error {
	r.calls = append(r.calls, "up")
	return nil
}
func (r *nodeRunner) PS(context.Context, compose.ExecutionSpec, io.Writer) (string, error) {
	r.calls = append(r.calls, "health")
	return r.health, nil
}
func TestSingleAndBatchUseIdenticalNodeDeliverySemantics(t *testing.T) {
	for _, name := range []string{"single-project", "batch-target"} {
		t.Run(name, func(t *testing.T) {
			r := &nodeRunner{health: `[{"State":"running","Health":"healthy"}]`}
			var stages []string
			_, err := DeployNode(context.Background(), r, compose.ExecutionSpec{ProjectName: name}, 120, func(stage string) (io.Writer, error) { stages = append(stages, stage); return io.Discard, nil }, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(r.calls, []string{"pull", "up", "health"}) || !reflect.DeepEqual(stages, []string{"pulling", "deploying", "verifying"}) {
				t.Fatal(r.calls, stages)
			}
		})
	}
}
func TestNodeDeliveryNeverAppliesAfterPullFailureOrChangedRuntime(t *testing.T) {
	r := &nodeRunner{pullErr: errors.New("pull failed")}
	observe := func(string) (io.Writer, error) { return io.Discard, nil }
	if _, err := DeployNode(context.Background(), r, compose.ExecutionSpec{}, 120, observe, nil); err == nil || len(r.calls) != 1 {
		t.Fatal("pull failure executed up")
	}
	r = &nodeRunner{}
	if _, err := DeployNode(context.Background(), r, compose.ExecutionSpec{}, 120, observe, func(context.Context) error { return errors.New("node changed") }); err == nil || len(r.calls) != 1 {
		t.Fatal("changed target executed up")
	}
	r = &nodeRunner{health: `[{"State":"running","Health":"unhealthy"}]`}
	if _, err := DeployNode(context.Background(), r, compose.ExecutionSpec{}, 120, observe, nil); err == nil {
		t.Fatal("unhealthy deployment succeeded")
	}
}
