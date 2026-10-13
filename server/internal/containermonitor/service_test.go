package containermonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	domain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

func fixture(t *testing.T) (*Service, Node, Inventory) {
	t.Helper()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(db, task.NewService(db), Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	return s, Node{ID: "node-a", Name: "Node A", EngineID: "engine-a", Enabled: true}, Inventory{ID: strings.Repeat("a", 64), Name: "app", Image: "app:1", State: "running", Project: "project", Service: "web"}
}
func TestHistoricalSamplesIdentityAggregationAndEvents(t *testing.T) {
	s, n, c := fixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	base := now.Add(-time.Minute)
	if err := s.RecordInstance(ctx, n, c, base); err != nil {
		t.Fatal(err)
	}
	id := InstanceID(n.ID, n.EngineID, c.ID)
	rows := []database.MonitoringSample{}
	for i, value := range []float64{200, 400, 300, 500} {
		v := value
		rows = append(rows, database.MonitoringSample{InstanceID: id, RecordedAt: base.Add(time.Duration(i) * 5 * time.Second), SampledAt: base.Add(time.Duration(i) * 5 * time.Second), StartedAt: "start-a", Session: "one", IntervalSeconds: 5, CPU: &v, CountersJSON: "{}"})
	}
	rows[2].CPU = nil
	rows[3].StartedAt = "start-b"
	if err := s.WriteSamples(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSamples(ctx, rows); err != nil {
		t.Fatal(err)
	}
	var count int64
	s.db.Model(&database.MonitoringSample{}).Count(&count)
	if count != 4 {
		t.Fatal("duplicate samples", count)
	}
	target := Target{NodeID: n.ID, EngineID: n.EngineID, ContainerID: c.ID, Since: base.Add(-time.Second), Until: now}
	result, err := s.Query(ctx, Query{Targets: []Target{target}, MaxPoints: 1440})
	if err != nil {
		t.Fatal(err)
	}
	series := result.Series[0]
	if series.Summary["cpu"].Count != 3 || *series.Summary["cpu"].Max != 500 || len(series.Points) != 4 || series.Points[2].Values["cpu"].Avg != nil || series.Points[2].Segment == series.Points[3].Segment {
		t.Fatalf("lost nullable samples, CPU scale or restart boundary: %#v", series)
	}
	other := target
	other.NodeID = "node-b"
	isolated, err := s.Query(ctx, Query{Targets: []Target{other}})
	if err != nil || len(isolated.Series[0].Points) != 0 {
		t.Fatal("history leaked across nodes", err)
	}
	other = target
	other.EngineID = "engine-b"
	isolated, err = s.Query(ctx, Query{Targets: []Target{other}})
	if err != nil || len(isolated.Series[0].Points) != 0 {
		t.Fatal("history leaked across Engines", err)
	}
	for i := 0; i < 3; i++ {
		e := domain.ContainerEvent{ID: fmt.Sprint(i), ContainerID: c.ID, Time: base.Add(time.Duration(i) * time.Second), TimeNano: fmt.Sprint(i), Action: "oom"}
		if err := s.RecordEvent(ctx, n, e, c); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordEvent(ctx, n, e, c); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.Events(ctx, id, base.Add(-time.Second), now, "abnormal", "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatal("events pagination/deduplication", err, page)
	}
	next, err := s.Events(ctx, id, base.Add(-time.Second), now, "abnormal", page.NextCursor, 2)
	if err != nil || len(next.Items) != 1 {
		t.Fatal("event cursor lost rows", err)
	}
	// Old instances remain readable without any live runtime provider.
	if _, err := s.Instance(ctx, id); err != nil {
		t.Fatal(err)
	}
	result, err = s.Query(ctx, Query{Targets: []Target{target}, MaxPoints: 2})
	if err != nil || len(result.Series[0].Points) > 2 || *result.Series[0].Summary["cpu"].Max != 500 {
		t.Fatal("aggregation cap lost peak", err)
	}
}
func TestRatesMissingRestartRollbackAndExclusions(t *testing.T) {
	started := time.Now().UTC().Add(-time.Hour)
	first := time.Now().UTC().Add(-5 * time.Second)
	second := first.Add(5 * time.Second)
	previous := domain.Metrics{StartedAt: &started, SampledAt: &first, NetworkAvailable: true, BlockAvailable: true, NetworkRXBytes: 100, NetworkTXBytes: 200, BlockReadBytes: 1000, BlockWriteBytes: 2000}
	current := previous
	current.SampledAt = &second
	current.NetworkRXBytes = 150
	current.NetworkTXBytes = 300
	current.BlockReadBytes = 2000
	current.BlockWriteBytes = 3000
	v := latest{sample: current, previous: &previous, session: "a"}
	sample := makeSample("id", v, second, 5)
	if sample.NetworkRX == nil || *sample.NetworkRX != 10 || sample.BlockRead == nil || *sample.BlockRead != 200 {
		t.Fatal("incorrect actual-time rates")
	}
	v.sample.NetworkRXBytes = 1
	if makeSample("id", v, second, 5).NetworkRX != nil {
		t.Fatal("counter rollback created rate")
	}
	v.previous = nil
	if makeSample("id", v, second, 5).NetworkTX != nil {
		t.Fatal("first sample created rate")
	}
	v.previous = &previous
	restart := started.Add(time.Minute)
	v.sample.StartedAt = &restart
	if makeSample("id", v, second, 5).BlockRead != nil {
		t.Fatal("rate crossed restart")
	}
	n := Node{ID: "node", Enabled: true}
	c := Inventory{ID: strings.Repeat("a", 64), Project: "app", Service: "web"}
	p := Policy{Enabled: true, IntervalSeconds: 5, RetentionDays: 3, Exclusions: []Exclusion{{Kind: "compose", NodeID: "node", Project: "app", Service: "web"}}}
	if !excluded(p, n, c) {
		t.Fatal("service exclusion did not match")
	}
	c.ID = strings.Repeat("b", 64)
	if !excluded(p, n, c) {
		t.Fatal("service exclusion lost rebuild")
	}
	c.Service = "database"
	if excluded(p, n, c) {
		t.Fatal("service exclusion matched another service")
	}
	p.Exclusions = []Exclusion{{Kind: "container", NodeID: "node", ContainerID: strings.Repeat("a", 64)}}
	if excluded(p, n, c) {
		t.Fatal("instance exclusion matched replacement")
	}
}
func TestRetentionConfirmationCleanupRecoveryAndLogicalExpiry(t *testing.T) {
	s, n, c := fixture(t)
	ctx := context.Background()
	p := s.Settings()
	p.RetentionDays = 1
	if _, _, err := s.SaveSettings(ctx, SavePolicy{Policy: p}); err != ErrConfirmation {
		t.Fatal("retention reduction lacked confirmation", err)
	}
	old := time.Now().UTC().Add(-4 * 24 * time.Hour)
	if err := s.RecordInstance(ctx, n, c, old); err != nil {
		t.Fatal(err)
	}
	value := 2.0
	id := InstanceID(n.ID, n.EngineID, c.ID)
	if err := s.WriteSamples(ctx, []database.MonitoringSample{{InstanceID: id, RecordedAt: old, SampledAt: old, StartedAt: "a", Session: "a", IntervalSeconds: 5, CPU: &value, CountersJSON: "{}"}}); err != nil {
		t.Fatal(err)
	}
	saved, taskID, err := s.SaveSettings(ctx, SavePolicy{Policy: p, ConfirmRetentionReduction: true})
	if err != nil || taskID == "" {
		t.Fatal("cleanup not launched", err)
	}
	if err := s.tasks.Wait(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	var count int64
	s.db.Model(&database.MonitoringSample{}).Count(&count)
	if count != 0 {
		t.Fatal("expired samples retained")
	}
	if _, _, err := s.SaveSettings(ctx, SavePolicy{Policy: p, ConfirmRetentionReduction: true}); err != ErrConflict {
		t.Fatal("stale policy overwrote settings", err)
	}
	saved.RetentionDays = 3
	if _, _, err := s.SaveSettings(ctx, SavePolicy{Policy: saved}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&database.MonitoringPolicy{}).Where("id=1").Updates(map[string]any{"cleanup_pending": true, "cleanup_task_id": "interrupted"}).Error; err != nil {
		t.Fatal(err)
	}
	taskID, err = s.startCleanup()
	if err != nil || taskID == "" {
		t.Fatal("interrupted checkpoint not recovered", err)
	}
	_ = s.tasks.Wait(ctx, taskID)
	list, err := s.Instances(ctx, InstanceQuery{})
	if err != nil || len(list.Items) != 0 {
		t.Fatal("expired history remained visible", err)
	}
}

type controlledRuntime struct {
	mu     sync.Mutex
	rows   []Inventory
	opened atomic.Int64
	active atomic.Int64
	closed atomic.Int64
}

func (r *controlledRuntime) MonitoringInventory(context.Context) ([]Inventory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Inventory{}, r.rows...), nil
}

type joinedReader struct {
	*io.PipeReader
	done chan struct{}
}

func (r *joinedReader) Close() error { err := r.PipeReader.Close(); <-r.done; return err }

func (r *controlledRuntime) NormalizedStats(ctx context.Context, id string) (io.ReadCloser, error) {
	r.opened.Add(1)
	r.active.Add(1)
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer r.active.Add(-1)
		defer r.closed.Add(1)
		defer writer.Close()
		encoder := json.NewEncoder(writer)
		timer := time.NewTicker(15 * time.Millisecond)
		defer timer.Stop()
		started := time.Now().UTC().Add(-time.Hour)
		for {
			at := time.Now().UTC()
			if encoder.Encode(domain.Metrics{ID: id, Available: true, CPUAvailable: true, MemoryAvailable: true, CPUPercent: 200, MemoryBytes: 1024, SampledAt: &at, StartedAt: &started}) != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	context.AfterFunc(ctx, func() { _ = reader.Close() })
	return &joinedReader{PipeReader: reader, done: done}, nil
}
func (r *controlledRuntime) MonitoringEvents(ctx context.Context, _ time.Time, _ func(domain.ContainerEvent, Inventory) error) error {
	<-ctx.Done()
	return ctx.Err()
}
func TestBackgroundHundredContainersSharedCacheAndCancellation(t *testing.T) {
	s, n, _ := fixture(t)
	n.Agent = true
	runtime := &controlledRuntime{}
	for i := 0; i < 100; i++ {
		runtime.rows = append(runtime.rows, Inventory{ID: fmt.Sprintf("%064x", i+1), Name: fmt.Sprint(i), State: "running"})
	}
	s.deps = Dependencies{Tick: 40 * time.Millisecond, Nodes: func(context.Context) ([]Node, error) { return []Node{n}, nil }, Runtime: func(context.Context, string) (Runtime, error) { return runtime, nil }}
	s.Start()
	defer s.Stop()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if s.Status().ActiveContainers == 100 && runtime.active.Load() == 100 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if runtime.active.Load() != 100 {
		t.Fatal("100-container Agent budget failed", runtime.active.Load())
	}
	id := runtime.rows[0].ID
	before := runtime.opened.Load()
	for i := 0; i < 10; i++ {
		if _, ok := s.Latest(n.ID, n.EngineID, id); !ok {
			t.Fatal("latest sample unavailable")
		}
	}
	if runtime.opened.Load() != before {
		t.Fatal("view reads opened extra stats streams")
	}
	deadline = time.Now().Add(10 * time.Second)
	var samples int64
	for time.Now().Before(deadline) {
		s.db.Model(&database.MonitoringSample{}).Count(&samples)
		if samples >= 100 {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if samples < 100 {
		t.Fatal("no background historical writes", samples)
	}
	s.Stop()
	if runtime.active.Load() != 0 || runtime.closed.Load() != runtime.opened.Load() {
		t.Fatal("streams not closed on shutdown")
	}
}

type stalledRuntime struct{ controlledRuntime }

func (r *stalledRuntime) MonitoringInventory(ctx context.Context) ([]Inventory, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestSlowNodeDoesNotBlockHealthyNodeAndDatabaseFailureRecovers(t *testing.T) {
	s, n, c := fixture(t)
	runtime := &controlledRuntime{rows: []Inventory{c}}
	slow := n
	slow.ID = "slow"
	slow.EngineID = "slow-engine"
	bad := &stalledRuntime{}
	s.deps = Dependencies{Tick: 40 * time.Millisecond, Nodes: func(context.Context) ([]Node, error) { return []Node{slow, n}, nil }, Runtime: func(_ context.Context, id string) (Runtime, error) {
		if id == "slow" {
			return bad, nil
		}
		return runtime, nil
	}}
	s.Start()
	defer s.Stop()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := s.Latest(n.ID, n.EngineID, c.ID); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := s.Latest(n.ID, n.EngineID, c.ID); !ok {
		t.Fatal("slow node blocked healthy collection")
	}
	if err := s.db.Exec("ALTER TABLE monitoring_samples RENAME TO monitoring_samples_unavailable").Error; err != nil {
		t.Fatal(err)
	}
	time.Sleep(900 * time.Millisecond)
	if runtime.active.Load() != 1 {
		t.Fatal("database failure stopped Docker collector")
	}
	if err := s.db.Exec("ALTER TABLE monitoring_samples_unavailable RENAME TO monitoring_samples").Error; err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	var count int64
	for time.Now().Before(deadline) {
		s.db.Model(&database.MonitoringSample{}).Count(&count)
		if count > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if count == 0 {
		t.Fatal("database writes did not recover")
	}
	var gaps int64
	s.db.Model(&database.MonitoringGap{}).Where("reason = 'database_buffer_overflow'").Count(&gaps)
	if gaps == 0 {
		t.Fatal("bounded buffer overflow did not preserve gap evidence")
	}
	s.Stop()
	if runtime.active.Load() != 0 {
		t.Fatal("healthy stream leaked on cancellation")
	}
}

type replayRuntime struct {
	controlledRuntime
	events   []domain.ContainerEvent
	metadata Inventory
}

func (r *replayRuntime) MonitoringEvents(ctx context.Context, _ time.Time, consume func(domain.ContainerEvent, Inventory) error) error {
	for _, event := range r.events {
		if err := consume(event, r.metadata); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return ctx.Err()
}
func TestPolicyActivationDoesNotBackfillDisabledInterval(t *testing.T) {
	s, n, c := fixture(t)
	old := time.Now().UTC().Add(-time.Minute)
	p := s.Settings()
	p.Enabled = false
	saved, _, err := s.SaveSettings(context.Background(), SavePolicy{Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	saved.Enabled = true
	if _, _, err := s.SaveSettings(context.Background(), SavePolicy{Policy: saved}); err != nil {
		t.Fatal(err)
	}
	runtime := &replayRuntime{metadata: c, events: []domain.ContainerEvent{
		{ID: "old", ContainerID: c.ID, Time: old, TimeNano: "1", Action: "start"},
		{ID: "new", ContainerID: c.ID, Time: time.Now().UTC().Add(time.Millisecond), TimeNano: "2", Action: "start"},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	changes := make(chan domain.ContainerEvent, 8)
	done := make(chan struct{})
	go func() { defer close(done); s.watchEvents(ctx, n, runtime, changes) }()
	select {
	case <-changes:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("current event was not recorded")
	}
	cancel()
	<-done
	var events []database.MonitoringEvent
	if err := s.db.Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Time.Before(old.Add(time.Second)) {
		t.Fatal("disabled interval was retroactively collected", events)
	}
}

func TestCleanupDoesNotChangePolicyReplayBoundary(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := context.Background()
	var before, after database.MonitoringPolicy
	if err := s.db.First(&before, 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.markScheduledCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := s.startCleanup()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.tasks.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.db.First(&after, 1).Error; err != nil {
		t.Fatal(err)
	}
	if !before.UpdatedAt.Equal(after.UpdatedAt) || before.Revision != after.Revision {
		t.Fatal("housekeeping changed the event replay policy boundary")
	}
}
