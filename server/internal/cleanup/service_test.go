package cleanup

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

type fakeRuntime struct {
	mu             sync.Mutex
	resources      []Resource
	removed        []Resource
	fail           map[string]error
	fresh          func(Resource) Resource
	inventoryError error
	cacheCalls     int
	cacheOptions   CacheOptions
	cacheSupported bool
	block          chan struct{}
}

func (f *fakeRuntime) CleanupCapabilities(context.Context) (Capabilities, error) {
	return Capabilities{Available: true, BuildCache: f.cacheSupported, API: "1.51"}, nil
}
func (f *fakeRuntime) CleanupInventory(context.Context) (Inventory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Inventory{Resources: append([]Resource{}, f.resources...), Capabilities: Capabilities{Available: true, BuildCache: f.cacheSupported}}, f.inventoryError
}
func (f *fakeRuntime) CleanupResource(_ context.Context, k Kind, id string) (Resource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.resources {
		if r.Kind == k && r.ID == id {
			if f.fresh != nil {
				r = f.fresh(r)
			}
			return r, nil
		}
	}
	return Resource{}, ErrGone
}
func (f *fakeRuntime) CleanupRemove(ctx context.Context, k Kind, id string) error {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail[id]; err != nil {
		return err
	}
	f.removed = append(f.removed, Resource{Kind: k, ID: id})
	return nil
}
func (f *fakeRuntime) CleanupPruneCache(_ context.Context, opts CacheOptions) (CacheReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cacheCalls++
	f.cacheOptions = opts
	return CacheReport{Deleted: []string{"cache-old"}, ReclaimedBytes: 1024}, nil
}
func fixture(t *testing.T, f *fakeRuntime) (*Service, Actor) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	userID := uint(1)
	s := NewService(db, task.NewService(db), audit.NewService(db), Dependencies{Node: func(_ context.Context, id string) (Node, error) {
		return Node{ID: id, Name: "Node " + id, Enabled: true}, nil
	}, Runtime: func(context.Context, string) (Runtime, error) { return f, nil }, Protection: func(context.Context, string) (Protection, error) { return Protection{}, nil }, Timezone: func(context.Context) string { return "Asia/Shanghai" }})
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	t.Cleanup(s.Stop)
	return s, Actor{UserID: &userID}
}
func oldResource(kind Kind, id string) Resource {
	return Resource{Kind: kind, ID: id, Name: id, CreatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), State: "exited"}
}
func await(t *testing.T, s *Service, taskID string) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var row database.Task
		if err := s.db.First(&row, "id = ?", taskID).Error; err != nil {
			t.Fatal(err)
		}
		if row.Status == task.StatusSuccess || row.Status == task.StatusFailed || row.Status == task.StatusCanceled {
			var run database.CleanupRun
			if err := s.db.First(&run, "task_id = ?", taskID).Error; err != nil {
				t.Fatal(err)
			}
			return decodeRun(run)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("cleanup did not finish")
	return Run{}
}
func runPreview(t *testing.T, s *Service, actor Actor) Run {
	t.Helper()
	preview, err := s.Preview(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.StartRun(context.Background(), "a", preview.ID, "Node a", actor)
	if err != nil {
		t.Fatal(err)
	}
	return await(t, s, row.ID)
}
func TestPreviewUsesActualStopAgeAndProtectsResources(t *testing.T) {
	f := &fakeRuntime{cacheSupported: true}
	s, _ := fixture(t, f)
	p := Policy{Config: DefaultConfig("UTC")}
	p.Containers.Enabled = true
	p.Networks.Enabled = true
	now := s.now()
	recent := oldResource(Container, "recent-stop")
	recent.FinishedAt = now.Add(-time.Hour)
	compose := oldResource(Container, "compose")
	compose.Labels = map[string]string{"com.docker.compose.project": "external"}
	inUse := oldResource(Image, "stopped-ref")
	inUse.InUse = true
	protectedImage := oldResource(Image, "protected-image")
	protectedImage.Aliases = []string{"app:old"}
	labeled := oldResource(Network, "labeled")
	labeled.Labels = map[string]string{"suma.cleanup.protect": "true"}
	system := oldResource(Network, "bridge")
	system.System = true
	volume := oldResource(Volume, "data")
	for _, test := range []struct {
		resource  Resource
		reason    string
		candidate bool
		manual    bool
	}{{recent, "retention", false, false}, {compose, "compose_project", false, false}, {inUse, "in_use", false, false}, {protectedImage, "protected_reference", false, false}, {labeled, "protection_label", false, false}, {system, "system_resource", false, false}, {volume, "manual_confirmation", false, true}, {oldResource(Container, "old"), "eligible", true, false}} {
		got := evaluate(test.resource, p, Protection{Image: {"app:old"}}, now)
		if got.Reason != test.reason || got.Candidate != test.candidate || got.Manual != test.manual {
			t.Errorf("%s: %+v", test.resource.ID, got)
		}
	}
}
func TestPolicyAuthorizationAndOptimisticVersion(t *testing.T) {
	s, actor := fixture(t, &fakeRuntime{})
	view, err := s.Get(context.Background(), "a", false)
	if err != nil {
		t.Fatal(err)
	}
	if view.Policy.Enabled || view.Policy.Schedule.Timezone != "Asia/Shanghai" {
		t.Fatal("unsafe defaults")
	}
	in := Update{Config: view.Policy.Config}
	in.Enabled = true
	if _, err = s.Update(context.Background(), "a", in, actor); !errors.Is(err, ErrConfirmation) {
		t.Fatalf("missing authorization: %v", err)
	}
	in.ConfirmationName = "Node a"
	in.Authorize = true
	p, err := s.Update(context.Background(), "a", in, actor)
	if err != nil {
		t.Fatal(err)
	}
	if p.AuthorizedAt == nil || p.NextRunAt == nil {
		t.Fatal("missing grant/schedule")
	}
	if _, err = s.Update(context.Background(), "a", in, actor); !errors.Is(err, ErrConflict) {
		t.Fatal("stale version accepted")
	}
	in.Version = p.Version
	in.Authorize = false
	in.Containers.Enabled = true
	if _, err = s.Update(context.Background(), "a", in, actor); !errors.Is(err, ErrConfirmation) {
		t.Fatal("expansion accepted without grant")
	}
	in.Containers.Enabled = false
	in.Images.RetentionDays = 30
	p, err = s.Update(context.Background(), "a", in, actor)
	if err != nil {
		t.Fatal("narrowing should preserve authorization", err)
	}
	in.Config = p.Config
	in.Enabled = false
	in.Version = p.Version
	if _, err = s.Update(context.Background(), "a", in, actor); err != nil {
		t.Fatal("pause needs no grant", err)
	}
}
func TestPreviewExpiryRevisionAndCrossNode(t *testing.T) {
	s, actor := fixture(t, &fakeRuntime{})
	preview, err := s.Preview(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(context.Background(), "b", preview.ID, "Node b", actor); !errors.Is(err, ErrConflict) {
		t.Fatal("cross-node preview accepted")
	}
	s.mu.Lock()
	value := s.previews[preview.ID]
	value.ExpiresAt = s.now()
	s.previews[preview.ID] = value
	s.mu.Unlock()
	if _, err = s.StartRun(context.Background(), "a", preview.ID, "Node a", actor); !errors.Is(err, ErrConflict) {
		t.Fatal("expired preview accepted")
	}
	preview, err = s.Preview(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Update(context.Background(), "a", Update{Config: DefaultConfig("UTC")}, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(context.Background(), "a", preview.ID, "Node a", actor); !errors.Is(err, ErrConflict) {
		t.Fatal("old preview accepted")
	}
}
func TestExecutionFreezesCandidatesRechecksAndNeverDeletesVolumes(t *testing.T) {
	image := oldResource(Image, "unused")
	volume := oldResource(Volume, "unused-volume")
	f := &fakeRuntime{resources: []Resource{image, volume}, cacheSupported: true, fresh: func(r Resource) Resource {
		if r.Kind == Image {
			r.InUse = true
		}
		return r
	}}
	s, actor := fixture(t, f)
	run := runPreview(t, s, actor)
	if run.Status != "success" || len(f.removed) != 0 || f.cacheCalls != 1 || run.Result.Stats[Volume].Scanned != 1 || run.Result.Stats[Image].Skipped != 1 {
		t.Fatalf("unsafe result: %+v removed=%v", run, f.removed)
	}
	if f.cacheOptions.RetentionDays != 7 || f.cacheOptions.ReservedBytes != 10*1024*1024*1024 {
		t.Fatal("cache parameters changed")
	}
	if run.Result.Stats[Cache].ReclaimedBytes == nil || *run.Result.Stats[Cache].ReclaimedBytes != 1024 {
		t.Fatal("missing actual Engine bytes")
	}
}
func TestPartialFailureAndVolumeProtection(t *testing.T) {
	f := &fakeRuntime{resources: []Resource{oldResource(Image, "ok"), oldResource(Image, "fail"), oldResource(Volume, "protected-volume")}, fail: map[string]error{"fail": errors.New("private error detail")}}
	s, actor := fixture(t, f)
	s.deps.Protection = func(context.Context, string) (Protection, error) {
		return Protection{Volume: {"protected-volume"}}, nil
	}
	run := runPreview(t, s, actor)
	if run.Status != "partial_failed" || run.Result.Stats[Image].Deleted != 1 || run.Result.Stats[Image].Failed != 1 {
		t.Fatalf("%+v", run)
	}
	if err := s.CheckVolumeDeletion(context.Background(), "a", "protected-volume"); !errors.Is(err, ErrInUse) {
		t.Fatal("protected volume accepted")
	}
	if _, err := s.Run(context.Background(), "b", run.ID); err == nil {
		t.Fatal("cross-node history accepted")
	}
}
func TestProtectionFailureBlocksAllDeletion(t *testing.T) {
	f := &fakeRuntime{resources: []Resource{oldResource(Image, "old")}}
	s, actor := fixture(t, f)
	preview, err := s.Preview(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	s.deps.Protection = func(context.Context, string) (Protection, error) { return nil, errors.New("unreadable compose") }
	row, err := s.StartRun(context.Background(), "a", preview.ID, "Node a", actor)
	if err != nil {
		t.Fatal(err)
	}
	run := await(t, s, row.ID)
	if run.Status != "failed" || len(f.removed) > 0 {
		t.Fatal("deleted with unreadable protection")
	}
}
func TestConcurrencyAndCancellation(t *testing.T) {
	f := &fakeRuntime{resources: []Resource{oldResource(Image, "old")}, block: make(chan struct{})}
	s, actor := fixture(t, f)
	preview, err := s.Preview(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.StartRun(context.Background(), "a", preview.ID, "Node a", actor)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Preview(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(context.Background(), "a", again.ID, "Node a", actor); !errors.Is(err, ErrConflict) {
		t.Fatal("same-node overlap")
	}
	if _, err = s.Update(context.Background(), "a", Update{Config: DefaultConfig("UTC")}, actor); !errors.Is(err, ErrConflict) {
		t.Fatal("active policy edited")
	}
	s.tasks.Cancel(row.ID)
	run := await(t, s, row.ID)
	if run.Status != "canceled" {
		t.Fatalf("%+v", run)
	}
}
func TestRecoverDoesNotReplayMissedScheduleOrInterruptedWork(t *testing.T) {
	f := &fakeRuntime{}
	s, actor := fixture(t, f)
	in := Update{Config: DefaultConfig("UTC"), ConfirmationName: "Node a", Authorize: true}
	in.Enabled = true
	p, err := s.Update(context.Background(), "a", in, actor)
	if err != nil {
		t.Fatal(err)
	}
	past := s.now().Add(-48 * time.Hour)
	if err = s.db.Model(&database.CleanupPolicy{}).Where("node_id = ?", p.NodeID).Update("next_run_at", past).Error; err != nil {
		t.Fatal(err)
	}
	unfinished := database.CleanupRun{ID: "interrupted", NodeID: "a", Status: "running", PolicyJSON: encode(p.Config), ResultJSON: encode(Result{Outcomes: []Outcome{{Kind: Image, ID: "already-deleted", Status: "deleted"}}, Stats: map[Kind]*Stats{Image: {Deleted: 1}}})}
	if err = s.db.Create(&unfinished).Error; err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := s.Runs(context.Background(), "a", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	interrupted, err := s.Run(context.Background(), "a", "interrupted")
	if err != nil || len(interrupted.Result.Outcomes) != 1 || interrupted.Result.Stats[Image].Deleted != 1 {
		t.Fatal("completed results lost on restart")
	}
	if len(result.Items) != 2 || f.cacheCalls != 0 {
		t.Fatal("replayed missed work")
	}
	p, err = s.policy(context.Background(), "a")
	if err != nil || p.NextRunAt == nil || !p.NextRunAt.After(s.now()) {
		t.Fatal("schedule not advanced")
	}
}
func TestScheduledOccurrenceIsExecutedOnce(t *testing.T) {
	f := &fakeRuntime{}
	s, actor := fixture(t, f)
	in := Update{Config: DefaultConfig("UTC"), ConfirmationName: "Node a", Authorize: true}
	in.Enabled = true
	p, err := s.Update(context.Background(), "a", in, actor)
	if err != nil {
		t.Fatal(err)
	}
	due := s.now().Add(-10 * time.Second)
	if err = s.db.Model(&database.CleanupPolicy{}).Where("node_id = ?", p.NodeID).Update("next_run_at", due).Error; err != nil {
		t.Fatal(err)
	}
	var ticks sync.WaitGroup
	tickErrors := make(chan error, 2)
	for range 2 {
		ticks.Add(1)
		go func() { defer ticks.Done(); tickErrors <- s.Tick(context.Background()) }()
	}
	ticks.Wait()
	close(tickErrors)
	for err := range tickErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var row database.CleanupRun
		s.db.First(&row, "scheduled_for = ?", due)
		if row.TaskID != "" {
			await(t, s, row.TaskID)
			break
		}
		time.Sleep(time.Millisecond)
	}
	result, err := s.Runs(context.Background(), "a", 1, false)
	if err != nil || result.Total != 1 {
		t.Fatalf("duplicate occurrence: %+v %v", result, err)
	}
}
func TestScheduleTimezoneAndDST(t *testing.T) {
	schedule := Schedule{Frequency: "daily", Hour: 2, Minute: 30, Timezone: "America/New_York"}
	before := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)
	next := NextOccurrences(schedule, before, 1)
	want := time.Date(2026, 3, 9, 6, 30, 0, 0, time.UTC)
	if len(next) != 1 || !next[0].Equal(want) {
		t.Fatalf("nonexistent minute: %v", next)
	}
	schedule.Hour = 1
	before = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	next = NextOccurrences(schedule, before, 2)
	if len(next) != 2 || !next[0].Equal(time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)) || !next[1].Equal(time.Date(2026, 11, 2, 6, 30, 0, 0, time.UTC)) {
		t.Fatalf("repeated minute: %v", next)
	}
	weekly := DefaultConfig("Asia/Shanghai").Schedule
	next = NextOccurrences(weekly, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), 3)
	if len(next) != 3 || next[0].In(mustLocation(t, "Asia/Shanghai")).Weekday() != time.Sunday {
		t.Fatal(next)
	}
}
func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestPauseWhileRunningDoesNotCancelCurrentRun(t *testing.T) {
	f := &fakeRuntime{resources: []Resource{oldResource(Image, "old")}, block: make(chan struct{})}
	s, actor := fixture(t, f)
	input := Update{Config: DefaultConfig("UTC"), Authorize: true, ConfirmationName: "Node a"}
	input.Enabled = true
	p, err := s.Update(context.Background(), "a", input, actor)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.Preview(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.StartRun(context.Background(), "a", preview.ID, "Node a", actor)
	if err != nil {
		t.Fatal(err)
	}
	p.Enabled = false
	paused, err := s.Update(context.Background(), "a", Update{Config: p.Config, Version: p.Version}, actor)
	if err != nil || paused.Enabled || paused.NextRunAt != nil {
		t.Fatalf("pause failed: %+v %v", paused, err)
	}
	close(f.block)
	run := await(t, s, row.ID)
	if run.Status != "success" {
		t.Fatal("pausing canceled current work", run.Status)
	}
}
func TestGlobalConcurrencyLimitAndBusyDeployment(t *testing.T) {
	f := &fakeRuntime{resources: []Resource{oldResource(Image, "old")}, block: make(chan struct{})}
	s, actor := fixture(t, f)
	for _, nodeID := range []string{"a", "b"} {
		preview, err := s.Preview(context.Background(), nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.StartRun(context.Background(), nodeID, preview.ID, "Node "+nodeID, actor); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := s.Preview(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(context.Background(), "c", preview.ID, "Node c", actor); !errors.Is(err, ErrConflict) {
		t.Fatal("global limit bypassed")
	}
	close(f.block)
	s.Stop()
	f2 := &fakeRuntime{}
	other, actor := fixture(t, f2)
	if err = other.db.Create(&database.Task{ID: "deployment", NodeID: "a", Scope: task.ScopeNode, Type: "cd.deploy.node", Status: task.StatusRunning}).Error; err != nil {
		t.Fatal(err)
	}
	preview, err = other.Preview(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.StartRun(context.Background(), "a", preview.ID, "Node a", actor); !errors.Is(err, ErrConflict) {
		t.Fatal("deployment ignored")
	}
}

func TestPreviewRejectsChangedNodeRuntime(t *testing.T) {
	s, actor := fixture(t, &fakeRuntime{})
	key := "engine-one"
	s.deps.Node = func(_ context.Context, id string) (Node, error) {
		return Node{ID: id, Name: "Node " + id, Enabled: true, RuntimeKey: key}, nil
	}
	preview, err := s.Preview(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	key = "engine-two"
	if _, err = s.StartRun(context.Background(), "a", preview.ID, "Node a", actor); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed Engine accepted: %v", err)
	}
}

func TestScheduledSkipsOfflineDisabledBusyAndMissedNodes(t *testing.T) {
	for _, reason := range []string{"offline", "disabled", "busy", "missed"} {
		t.Run(reason, func(t *testing.T) {
			f := &fakeRuntime{resources: []Resource{oldResource(Image, "old")}, cacheSupported: true}
			s, actor := fixture(t, f)
			in := Update{Config: DefaultConfig("UTC"), ConfirmationName: "Node a", Authorize: true}
			in.Enabled = true
			if _, err := s.Update(context.Background(), "a", in, actor); err != nil {
				t.Fatal(err)
			}
			due := s.now().Add(-10 * time.Second)
			switch reason {
			case "offline":
				f.inventoryError = ErrUnavailable
			case "disabled":
				s.deps.Node = func(_ context.Context, id string) (Node, error) { return Node{ID: id, Name: "Node " + id}, nil }
			case "busy":
				if err := s.db.Create(&database.Task{ID: "build", NodeID: "a", Scope: task.ScopeNode, Type: "compose.build", Status: task.StatusRunning}).Error; err != nil {
					t.Fatal(err)
				}
			case "missed":
				due = s.now().Add(-2 * time.Minute)
			}
			if err := s.db.Model(&database.CleanupPolicy{}).Where("node_id = ?", "a").Update("next_run_at", due).Error; err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := s.Tick(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			runs, err := s.Runs(context.Background(), "a", 1, false)
			if err != nil || runs.Total != 1 || runs.Items[0].Status != "skipped" || runs.Items[0].TaskID != "" || len(f.removed) != 0 || f.cacheCalls != 0 {
				t.Fatalf("unsafe scheduled skip: %+v %v", runs, err)
			}
		})
	}
}

func TestLostProtectionStopsAfterCompletedDeletion(t *testing.T) {
	f := &fakeRuntime{resources: []Resource{oldResource(Image, "first"), oldResource(Image, "second")}}
	s, actor := fixture(t, f)
	config := DefaultConfig("UTC")
	config.Cache.Enabled = false
	if _, err := s.Update(context.Background(), "a", Update{Config: config}, actor); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.deps.Protection = func(context.Context, string) (Protection, error) {
		calls++
		if calls >= 3 {
			return nil, errors.New("project protection unavailable")
		}
		return Protection{}, nil
	}
	preview, err := s.Preview(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.StartRun(context.Background(), "a", preview.ID, "Node a", actor)
	if err != nil {
		t.Fatal(err)
	}
	run := await(t, s, row.ID)
	if run.Status != "failed" || len(f.removed) != 1 || f.removed[0].ID != "first" || run.Result.Stats[Image].Deleted != 1 {
		t.Fatalf("lost protection did not stop: %+v %+v", f.removed, run)
	}
}

func TestRescheduleBetweenDueScanAndClaimDoesNotRunEarly(t *testing.T) {
	f := &fakeRuntime{}
	s, actor := fixture(t, f)
	input := Update{Config: DefaultConfig("UTC"), ConfirmationName: "Node a", Authorize: true}
	input.Enabled = true
	if _, err := s.Update(context.Background(), "a", input, actor); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&database.CleanupPolicy{}).Where("node_id = ?", "a").Update("next_run_at", s.now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	changed := false
	callback := "cleanup:test-reschedule"
	if err := s.db.Callback().Query().After("gorm:query").Register(callback, func(db *gorm.DB) {
		if _, selected := db.Statement.Dest.(*[]database.CleanupPolicy); selected && !changed {
			changed = true
			db.AddError(db.Session(&gorm.Session{NewDB: true}).Model(&database.CleanupPolicy{}).Where("node_id = ?", "a").Update("next_run_at", s.now().Add(24*time.Hour)).Error)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer s.db.Callback().Query().Remove(callback)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := s.Runs(context.Background(), "a", 1, false)
	if !changed || err != nil || runs.Total != 0 {
		t.Fatalf("future plan claimed from stale due scan: %+v %v", runs, err)
	}
}
