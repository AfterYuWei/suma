//go:build dockersmoke

package containermonitor_test

import (
	"context"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	monitor "github.com/suma/suma/server/internal/containermonitor"
	"github.com/suma/suma/server/internal/docker"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

type trackedRuntime struct {
	monitor.Runtime
	active atomic.Int64
}
type trackedReader struct {
	io.ReadCloser
	once   sync.Once
	active *atomic.Int64
}

func (r *trackedReader) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(func() { r.active.Add(-1) })
	return err
}
func (r *trackedRuntime) NormalizedStats(ctx context.Context, id string) (io.ReadCloser, error) {
	source, err := r.Runtime.NormalizedStats(ctx, id)
	if err != nil {
		return nil, err
	}
	r.active.Add(1)
	return &trackedReader{ReadCloser: source, active: &r.active}, nil
}
func TestRealDockerMonitoringPersistenceAndClose(t *testing.T) {
	if os.Getenv("SUMA_RUN_DOCKER_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_DOCKER_SMOKE=1 for read-only real Docker monitoring")
	}
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := docker.New("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	info, err := engine.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &trackedRuntime{Runtime: engine}
	node := monitor.Node{ID: "smoke", Name: "Read-only smoke", EngineID: info.ID, Enabled: true, RuntimeKey: info.ID}
	service, err := monitor.New(db, task.NewService(db), monitor.Dependencies{Nodes: func(context.Context) ([]monitor.Node, error) { return []monitor.Node{node}, nil }, Runtime: func(context.Context, string) (monitor.Runtime, error) { return runtime, nil }})
	if err != nil {
		t.Fatal(err)
	}
	service.Start()
	defer service.Stop()
	var instanceID string
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		list, err := service.Instances(ctx, monitor.InstanceQuery{})
		if err != nil {
			t.Fatal(err)
		}
		for _, instance := range list.Items {
			result, err := service.Query(ctx, monitor.Query{Targets: []monitor.Target{{NodeID: instance.NodeID, EngineID: instance.EngineID, ContainerID: instance.ContainerID, Since: time.Now().Add(-time.Hour), Until: time.Now()}}})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Series[0].Points) >= 2 {
				instanceID = instance.ID
				break
			}
		}
		if instanceID != "" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if instanceID == "" {
		t.Fatal("no real Docker container produced two persisted observations")
	}
	service.Stop()
	if runtime.active.Load() != 0 {
		t.Fatal("real Docker stats stream not closed", runtime.active.Load())
	}
	if _, err := service.Instance(ctx, instanceID); err != nil {
		t.Fatal("history unavailable after collector shutdown")
	}
	t.Log("Verified real Docker background collection, PostgreSQL persistence, historical query and complete stream closure; Engine resources were read only.")
}
