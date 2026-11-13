package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

var fixedNow = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

type stubKind struct {
	mu     sync.Mutex
	starts []StartRequest
	ids    []string
	fail   error
	tasks  *task.Service
	block  <-chan struct{}
}

func (s *stubKind) spec() KindSpec {
	return KindSpec{
		Type: "image.update", RequiresNode: true,
		ValidateParams: func(params json.RawMessage) error {
			var parsed map[string]any
			return json.Unmarshal(params, &parsed)
		},
		Describe: func(params json.RawMessage) string { return "stub" },
		Start: func(ctx context.Context, request StartRequest) (string, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.starts = append(s.starts, request)
			if s.fail != nil {
				return "", s.fail
			}
			block := s.block
			row, err := s.tasks.StartDefinition(ctx, task.Definition{Type: "image.update", Name: "Controlled check", NodeID: request.NodeID, NodeName: request.NodeName, Params: request.Params, TriggerRunID: request.RunID}, func(ctx context.Context, _ string, _ task.Reporter) (any, error) {
				if block != nil {
					select {
					case <-block:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return nil, nil
			})
			if err != nil {
				return "", err
			}
			s.ids = append(s.ids, row.ID)
			return row.ID, nil
		},
	}
}

func (s *stubKind) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.starts)
}

func newTestService(t *testing.T, register ...KindSpec) (*Service, *stubKind) {
	t.Helper()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	stub := &stubKind{tasks: task.NewService(db)}
	t.Cleanup(func() {
		stub.mu.Lock()
		ids := append([]string(nil), stub.ids...)
		stub.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, id := range ids {
			if err := stub.tasks.Wait(ctx, id); err != nil {
				t.Error(err)
			}
		}
	})
	for _, spec := range append([]KindSpec{stub.spec()}, register...) {
		if err := registry.Register(spec); err != nil {
			t.Fatal(err)
		}
	}
	service := New(db, registry, Dependencies{
		Now: func() time.Time { return fixedNow },
		NodeName: func(ctx context.Context, nodeID string) (string, error) {
			if nodeID != "edge" {
				return "", errors.New("node missing")
			}
			return "Edge", nil
		},
	})
	return service, stub
}

func createPlan(t *testing.T, service *Service, overrides func(*ScheduleInput)) database.TaskCenterSchedule {
	t.Helper()
	enabled := true
	input := ScheduleInput{
		Name: "blog updates", Kind: "image.update", NodeID: "edge", Subject: "blog",
		Params: json.RawMessage(`{"project":"blog"}`), Cron: "0 4 * * *", Timezone: "UTC", Enabled: &enabled,
	}
	if overrides != nil {
		overrides(&input)
	}
	row, err := service.Create(context.Background(), input, "admin")
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestNextRunsResolvesCronInTimezone(t *testing.T) {
	base := time.Date(2026, 11, 7, 9, 2, 0, 0, time.UTC)
	runs, err := NextRuns("*/15 * * * *", "UTC", base, 3)
	if err != nil || len(runs) != 3 {
		t.Fatalf("unexpected next runs: %v %v", runs, err)
	}
	for i, run := range runs {
		if run.Minute()%15 != 0 || !run.After(base) || run.Location() != time.UTC {
			t.Fatalf("unexpected occurrence %d: %v", i, run)
		}
		if i > 0 && !run.After(runs[i-1]) {
			t.Fatal("occurrences are not ascending")
		}
	}
	if _, err := NextRuns("* * * * *", "Local", base, 1); err == nil {
		t.Fatal("Local timezone must be rejected")
	}
	if _, err := NextRuns("not a cron", "UTC", base, 1); err == nil {
		t.Fatal("invalid cron must be rejected")
	}
	// February 31 never fires; plans like this must fail validation instead of
	// silently skipping.
	if runs, err := NextRuns("0 0 31 2 *", "UTC", base, 1); err != nil || len(runs) != 0 {
		t.Fatalf("never-firing plan should return no occurrences: %v %v", runs, err)
	}
}

func TestCreateValidatesKindNodeCronAndUniqueness(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.Create(context.Background(), ScheduleInput{Name: "x", Kind: "ghost", NodeID: "edge", Cron: "* * * * *", Timezone: "UTC"}, ""); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("expected unknown kind, got %v", err)
	}
	if _, err := service.Create(context.Background(), ScheduleInput{Name: "x", Kind: "image.update", Cron: "* * * * *", Timezone: "UTC"}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("node kind without node must be rejected, got %v", err)
	}
	if _, err := service.Create(context.Background(), ScheduleInput{Name: "x", Kind: "image.update", NodeID: "missing", Cron: "* * * * *", Timezone: "UTC"}, ""); err == nil || err.Error() != "node missing" {
		t.Fatalf("unresolved node must surface, got %v", err)
	}
	if _, err := service.Create(context.Background(), ScheduleInput{Name: "x", Kind: "image.update", NodeID: "edge", Cron: "bad cron", Timezone: "UTC"}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid cron must be rejected, got %v", err)
	}
	if _, err := service.Create(context.Background(), ScheduleInput{Name: "x", Kind: "image.update", NodeID: "edge", Cron: "0 0 31 2 *", Timezone: "UTC"}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("never-firing plan must be rejected, got %v", err)
	}
	row := createPlan(t, service, nil)
	if row.NodeName != "Edge" || row.NextRunAt == nil || row.Version != 1 {
		t.Fatalf("unexpected created plan: %+v", row)
	}
	if _, err := service.Create(context.Background(), ScheduleInput{Name: "dup", Kind: "image.update", NodeID: "edge", Subject: "blog", Params: json.RawMessage(`{}`), Cron: "* * * * *", Timezone: "UTC"}, ""); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate plan must be rejected, got %v", err)
	}
}

func TestFireDueStartsTaskAndAdvancesOccurrence(t *testing.T) {
	service, stub := newTestService(t)
	row := createPlan(t, service, nil)
	past := fixedNow.Add(-15 * time.Second)
	if err := service.db.Model(&database.TaskCenterSchedule{}).Where("id = ?", row.ID).Update("next_run_at", past).Error; err != nil {
		t.Fatal(err)
	}
	service.fireDue(context.Background())
	if stub.count() != 1 {
		t.Fatalf("due plan must fire once, got %d", stub.count())
	}
	var updated database.TaskCenterSchedule
	if err := service.db.First(&updated, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(fixedNow) {
		t.Fatalf("occurrence must advance past now: %v", updated.NextRunAt)
	}
	if updated.LastRunAt == nil {
		t.Fatal("last_run_at must be recorded")
	}
	var runs []database.TaskCenterScheduleRun
	if err := service.db.Where("schedule_id = ?", row.ID).Find(&runs).Error; err != nil || len(runs) != 1 {
		t.Fatalf("expected one run row: %v %v", runs, err)
	}
	if runs[0].TaskID == "" || runs[0].Status != "started" || runs[0].TriggeredBy != ReasonScheduled {
		t.Fatalf("unexpected run row: %+v", runs[0])
	}
	// A second pass must not re-fire the same occurrence.
	service.fireDue(context.Background())
	if stub.count() != 1 {
		t.Fatalf("claimed occurrence re-fired: %d", stub.count())
	}
}

func TestFireDueSkipsWhilePreviousRunActive(t *testing.T) {
	service, stub := newTestService(t)
	row := createPlan(t, service, nil)
	if err := service.db.Create(&database.Task{ID: "active-task", Scope: "node", NodeID: "edge", Type: "image.update", Name: "active", Status: "running"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.db.Create(&database.TaskCenterScheduleRun{ScheduleID: &row.ID, TaskID: "active-task", TriggeredBy: ReasonScheduled, Status: "started"}).Error; err != nil {
		t.Fatal(err)
	}
	past := fixedNow.Add(-15 * time.Second)
	if err := service.db.Model(&database.TaskCenterSchedule{}).Where("id = ?", row.ID).Update("next_run_at", past).Error; err != nil {
		t.Fatal(err)
	}
	service.fireDue(context.Background())
	if stub.count() != 0 {
		t.Fatal("active previous run must skip the occurrence")
	}
	var updated database.TaskCenterSchedule
	if err := service.db.First(&updated, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(fixedNow) {
		t.Fatalf("busy occurrence must advance: %v", updated.NextRunAt)
	}
	var last database.TaskCenterScheduleRun
	if err := service.db.Order("id DESC").First(&last).Error; err != nil {
		t.Fatal(err)
	}
	if last.Status != "skipped" || last.ReasonCode != "busy" {
		t.Fatalf("skip evidence missing: %+v", last)
	}
}

func TestFireDueRecordsStartFailureAndParksUnknownKind(t *testing.T) {
	service, stub := newTestService(t)
	stub.mu.Lock()
	stub.fail = errors.New("node offline")
	stub.mu.Unlock()
	row := createPlan(t, service, nil)
	if err := service.db.Model(&database.TaskCenterSchedule{}).Where("id = ?", row.ID).Update("next_run_at", fixedNow.Add(-15*time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	service.fireDue(context.Background())
	var updated database.TaskCenterSchedule
	if err := service.db.First(&updated, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.LastError != "node offline" {
		t.Fatalf("start failure must be recorded: %q", updated.LastError)
	}
	// An unregistered kind must be parked, not retried forever.
	if err := service.db.Model(&database.TaskCenterSchedule{}).Where("id = ?", row.ID).Updates(map[string]any{"kind": "ghost", "next_run_at": fixedNow.Add(-15 * time.Second), "last_error": ""}).Error; err != nil {
		t.Fatal(err)
	}
	service.fireDue(context.Background())
	var parked database.TaskCenterSchedule
	if err := service.db.First(&parked, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if parked.NextRunAt != nil || parked.LastError == "" {
		t.Fatalf("unknown kind must park the plan: %+v", updated)
	}
}

func TestRunNowIsManualAndLeavesScheduleAlone(t *testing.T) {
	service, stub := newTestService(t)
	row := createPlan(t, service, nil)
	release := make(chan struct{})
	stub.block = release
	defer close(release)
	run, err := service.RunNow(context.Background(), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.TriggeredBy != ReasonManual || run.TaskID == "" {
		t.Fatalf("unexpected manual run: %+v", run)
	}
	if stub.count() != 1 {
		t.Fatal("manual run must start the kind")
	}
	var updated database.TaskCenterSchedule
	if err := service.db.First(&updated, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.Equal(*row.NextRunAt) {
		t.Fatal("manual run must not touch the next occurrence")
	}
	if _, err := service.RunNow(context.Background(), row.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("second manual run while active must be busy, got %v", err)
	}
}

func TestRecoverSkipsMissedOccurrences(t *testing.T) {
	service, stub := newTestService(t)
	row := createPlan(t, service, nil)
	if err := service.db.Model(&database.TaskCenterSchedule{}).Where("id = ?", row.ID).Update("next_run_at", fixedNow.Add(-48*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stub.count() != 0 {
		t.Fatal("recovery must not fire missed plans")
	}
	var updated database.TaskCenterSchedule
	if err := service.db.First(&updated, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(fixedNow) {
		t.Fatalf("recovery must re-arm from now: %v", updated.NextRunAt)
	}
}

func TestUpdateEnforcesVersionAndRecomputesOccurrence(t *testing.T) {
	service, _ := newTestService(t)
	row := createPlan(t, service, nil)
	disabled := false
	if _, err := service.Update(context.Background(), row.ID, ScheduleInput{Cron: "30 5 * * *", Timezone: "UTC", Enabled: &disabled}, row.Version+7); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version must conflict, got %v", err)
	}
	updated, err := service.Update(context.Background(), row.ID, ScheduleInput{Name: "renamed", Cron: "30 5 * * *", Timezone: "UTC", Enabled: &disabled}, row.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "renamed" || updated.Enabled || updated.NextRunAt != nil || updated.Version != row.Version+1 {
		t.Fatalf("unexpected updated plan: %+v", updated)
	}
}

func TestArchiveSchedulesRetainsTriggerEvidence(t *testing.T) {
	service, _ := newTestService(t)
	row := createPlan(t, service, nil)
	run, err := service.RunNow(context.Background(), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteSchedules(context.Background(), "image.update", "edge", "blog"); err != nil {
		t.Fatal(err)
	}
	view, err := service.Schedule(context.Background(), row.ID)
	if err != nil || view.ArchivedAt == nil || view.Enabled {
		t.Fatalf("archive state invalid: %+v %v", view, err)
	}
	visible, err := service.Schedules(context.Background(), "", "")
	if err != nil || len(visible) != 0 {
		t.Fatalf("archived rules must be hidden: %v %v", visible, err)
	}
	var retained database.TaskCenterScheduleRun
	if err := service.db.First(&retained, run.ID).Error; err != nil || retained.TaskID != run.TaskID {
		t.Fatalf("trigger evidence was lost: %+v %v", retained, err)
	}
}
