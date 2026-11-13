package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
)

func TestUnifiedPreviewUsesBusinessClockAndSeconds(t *testing.T) {
	registry := NewRegistry()
	for _, spec := range []KindSpec{
		{Type: "interval", ScheduleTypes: []string{"interval"}, Start: func(context.Context, StartRequest) (string, error) { return "", nil }},
		{Type: "civil", NextRuns: func(row database.TaskCenterSchedule, after time.Time, count int) ([]time.Time, error) {
			if row.Cron != "30 2 * * *" || row.Timezone != "Europe/Berlin" || count != 3 {
				t.Fatalf("preview lost business clock input: %+v, count %d", row, count)
			}
			return []time.Time{after.Add(48 * time.Hour)}, nil
		}, Start: func(context.Context, StartRequest) (string, error) { return "", nil }},
	} {
		if err := registry.Register(spec); err != nil {
			t.Fatal(err)
		}
	}
	center := New(nil, registry, Dependencies{Now: func() time.Time { return fixedNow }})
	runs, err := center.Preview(ScheduleInput{Kind: "interval", ScheduleType: "interval", IntervalSeconds: 30}, 3)
	if err != nil || len(runs) != 3 {
		t.Fatal(runs, err)
	}
	for i, at := range runs {
		if !at.Equal(fixedNow.Add(time.Duration(i+1) * 30 * time.Second)) {
			t.Fatal("seconds were rounded", runs)
		}
	}
	if _, err := center.Preview(ScheduleInput{Kind: "interval", ScheduleType: "interval", IntervalSeconds: 29}, 3); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	runs, err = center.Preview(ScheduleInput{Kind: "civil", Cron: "30 2 * * *", Timezone: "Europe/Berlin"}, 3)
	if err != nil || len(runs) != 1 || !runs[0].Equal(fixedNow.Add(48*time.Hour)) {
		t.Fatal(runs, err)
	}
}

func TestStoppedCenterRejectsAdmissionBeforeDatabaseAccess(t *testing.T) {
	center := New(nil, NewRegistry(), Dependencies{})
	center.Stop()
	if _, err := center.Dispatch(context.Background(), DispatchRequest{}); !errors.Is(err, ErrStopped) {
		t.Fatal(err)
	}
}

func TestTriggerFiltersIncludeAdHocFailuresAndArchivedRules(t *testing.T) {
	center, _ := newTestService(t)
	rule := createPlan(t, center, nil)
	now := fixedNow
	if err := center.db.Model(&rule).Updates(map[string]any{"archived_at": now, "enabled": false}).Error; err != nil {
		t.Fatal(err)
	}
	runs := []database.TaskCenterScheduleRun{
		{ScheduleID: &rule.ID, TriggeredBy: "scheduled", Status: "skipped", ReasonCode: "busy"},
		{TriggeredBy: "manual", Status: "start_failed", ReasonCode: "configuration_unavailable", RuleSnapshot: database.JSONB(`{"kind":"image.update","node_id":"removed-node","params":{"project":"blog"}}`)},
		{TriggeredBy: "system", Status: "interrupted", RuleSnapshot: database.JSONB(`{"kind":"cd.sync","node_id":""}`)},
	}
	if err := center.db.Create(&runs).Error; err != nil {
		t.Fatal(err)
	}
	page, err := center.TriggerRecords(context.Background(), RunFilter{Scope: "node", NodeID: "removed-node", Source: "manual", Status: "start_failed", Summary: true}, 20, 0)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != runs[1].ID || page.Items[0].Subject != "blog" || len(page.Items[0].RuleSnapshot) != 0 {
		t.Fatalf("ad-hoc failure is inaccessible: %+v %v", page, err)
	}
	detail, err := center.TriggerRecord(context.Background(), runs[1].ID)
	if err != nil || !json.Valid(detail.RuleSnapshot) || detail.NodeID != "removed-node" {
		t.Fatalf("original snapshot is inaccessible: %+v %v", detail, err)
	}
	page, err = center.TriggerRecords(context.Background(), RunFilter{ScheduleID: rule.ID, Source: "scheduled", Status: "skipped"}, 20, 0)
	if err != nil || page.Total != 1 || page.Items[0].RuleName != rule.Name {
		t.Fatalf("archived rule evidence lost: %+v %v", page, err)
	}
	page, err = center.TriggerRecords(context.Background(), RunFilter{Scope: "control_plane", Source: "system"}, 20, 0)
	if err != nil || page.Total != 1 || page.Items[0].ID != runs[2].ID {
		t.Fatalf("control-plane filter is incorrect: %+v %v", page, err)
	}
}

func TestConfigurationFailureRecordsAndParksOnlyInvalidBinding(t *testing.T) {
	for _, permanent := range []bool{true, false} {
		t.Run(map[bool]string{true: "deleted owner", false: "temporary failure"}[permanent], func(t *testing.T) {
			center, stub := newTestService(t)
			rule := createPlan(t, center, nil)
			spec, _ := center.kinds.Get(rule.Kind)
			spec.ConfigVersion = func(context.Context, *gorm.DB, database.TaskCenterSchedule) (string, error) {
				if permanent {
					return "", gorm.ErrRecordNotFound
				}
				return "", errors.New("temporary configuration read failure")
			}
			var projected *time.Time
			spec.Advance = func(_ context.Context, _ *gorm.DB, _ database.TaskCenterSchedule, next *time.Time) error {
				projected = next
				return nil
			}
			center.kinds.kinds[rule.Kind] = spec
			past := fixedNow.Add(-15 * time.Second)
			if err := center.db.Model(&rule).Update("next_run_at", past).Error; err != nil {
				t.Fatal(err)
			}
			if err := center.RunDue(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := center.RunDue(context.Background()); err != nil {
				t.Fatal(err)
			}
			var saved database.TaskCenterSchedule
			if err := center.db.First(&saved, rule.ID).Error; err != nil {
				t.Fatal(err)
			}
			page, err := center.RunsPage(context.Background(), rule.ID, 20, 0)
			if err != nil || page.Total != 1 || stub.count() != 0 {
				t.Fatalf("repeated or unrecorded failed admission: %+v, %v", page, err)
			}
			run := page.Items[0]
			if run.Status != "start_failed" || run.ReasonCode != "configuration_unavailable" || run.TaskID != "" || saved.Version != rule.Version {
				t.Fatalf("bad failure evidence: %+v / %+v", run, saved)
			}
			if permanent != (saved.NextRunAt == nil) || permanent != (projected == nil) || saved.LastError == "" {
				t.Fatal("wrong parking or projection", saved.NextRunAt, projected)
			}
		})
	}
}

func TestRecoveryAdvancesBusinessProjectionWithoutChangingVersion(t *testing.T) {
	center, stub := newTestService(t)
	rule := createPlan(t, center, nil)
	var projected *time.Time
	spec, _ := center.kinds.Get(rule.Kind)
	spec.Advance = func(_ context.Context, _ *gorm.DB, _ database.TaskCenterSchedule, next *time.Time) error {
		projected = next
		return nil
	}
	center.kinds.kinds[rule.Kind] = spec
	if err := center.db.Model(&rule).Update("next_run_at", fixedNow.Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := center.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	var saved database.TaskCenterSchedule
	if err := center.db.First(&saved, rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if projected == nil || saved.NextRunAt == nil || !projected.Equal(*saved.NextRunAt) || saved.Version != rule.Version || stub.count() != 0 {
		t.Fatal("recovery did not preserve cadence projection and version", saved)
	}
}

func TestBusinessBackfillIsAtomicAndRunsOnce(t *testing.T) {
	center, _ := newTestService(t)
	first := func(_ context.Context, tx *gorm.DB) error {
		return tx.Create(&database.Setting{Key: "backfill.fixture", Value: "preserved"}).Error
	}
	failed := errors.New("conversion rejected")
	if err := center.BackfillBusinessRules(context.Background(), first, func(context.Context, *gorm.DB) error { return failed }); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	var count int64
	if err := center.db.Model(&database.Setting{}).Where("key = ?", "backfill.fixture").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("partial conversion committed", count, err)
	}
	if err := center.BackfillBusinessRules(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := center.BackfillBusinessRules(context.Background(), func(context.Context, *gorm.DB) error { t.Fatal("conversion replayed"); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestManualBusinessEntryLinksExistingRule(t *testing.T) {
	center, stub := newTestService(t)
	rule := createPlan(t, center, nil)
	spec, _ := center.kinds.Get(rule.Kind)
	spec.Subject = func(_ json.RawMessage) string { return "blog" }
	center.kinds.kinds[rule.Kind] = spec
	taskID, err := center.StartManual(context.Background(), rule.Kind, rule.NodeID, json.RawMessage(`{"project":"blog","auto_apply":false}`))
	if err != nil || taskID == "" {
		t.Fatal(taskID, err)
	}
	if err := stub.tasks.Wait(context.Background(), taskID); err != nil {
		t.Fatal(err)
	}
	page, err := center.RunsPage(context.Background(), rule.ID, 20, 0)
	if err != nil || page.Total != 1 || page.Items[0].TaskID != taskID || page.Items[0].TriggeredBy != ReasonManual {
		t.Fatal("manual check disappeared from rule history", page, err)
	}
}

func TestBusinessBackfillRejectsConflictingProjection(t *testing.T) {
	center, _ := newTestService(t)
	if err := center.kinds.Register(KindSpec{Type: "owned", BusinessOwned: true, ScheduleTypes: []string{"interval"}, Start: func(context.Context, StartRequest) (string, error) { return "", nil }}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	input := ScheduleInput{Name: "Retained", Kind: "owned", Subject: "blog", Params: json.RawMessage(`{}`), ScheduleType: "interval", IntervalSeconds: 30, Enabled: &enabled}
	var original database.TaskCenterSchedule
	if err := center.db.Transaction(func(tx *gorm.DB) error {
		var err error
		original, err = center.PutOwned(tx, input)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	convert := func(_ context.Context, tx *gorm.DB) error {
		if err := tx.Create(&database.Setting{Key: "conversion.fixture", Value: "preserved"}).Error; err != nil {
			return err
		}
		_, err := center.PutOwned(tx, input)
		return err
	}
	input.IntervalSeconds = 35
	if err := center.BackfillBusinessRules(context.Background(), convert); err == nil {
		t.Fatal("conflicting conversion silently replaced persisted intent")
	}
	var retained database.TaskCenterSchedule
	if err := center.db.First(&retained, original.ID).Error; err != nil || retained.Version != original.Version || retained.IntervalSeconds != 30 {
		t.Fatal("conflicting conversion did not roll back", retained, err)
	}
	var count int64
	if err := center.db.Model(&database.Setting{}).Where("key = ?", "conversion.fixture").Count(&count).Error; err != nil || count != 0 {
		t.Fatal("partial conversion survived", err)
	}
	input.IntervalSeconds = 30
	if err := center.BackfillBusinessRules(context.Background(), convert); err != nil {
		t.Fatal("matching conversion could not resume", err)
	}
}

func TestScheduleListUsesFixedThreeQueries(t *testing.T) {
	center, _ := newTestService(t)
	var queries atomic.Int32
	if err := center.db.Callback().Query().Before("gorm:query").Register("count_task_center_queries", func(*gorm.DB) { queries.Add(1) }); err != nil {
		t.Fatal(err)
	}
	// Raw(...).Scan uses GORM's Row callback, rather than its Query callback.
	if err := center.db.Callback().Row().Before("gorm:row").Register("count_task_center_rows", func(*gorm.DB) { queries.Add(1) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = center.db.Callback().Query().Remove("count_task_center_queries")
		_ = center.db.Callback().Row().Remove("count_task_center_rows")
	})
	for i := 0; i < 100; i++ {
		rule := database.TaskCenterSchedule{Name: fmt.Sprint(i), Kind: "image.update", NodeID: "edge", Subject: fmt.Sprint(i), Params: database.JSONB(`{}`), Cron: "0 4 * * *", Timezone: "UTC", ScheduleType: "cron"}
		if err := center.db.Create(&rule).Error; err != nil {
			t.Fatal(err)
		}
		task := database.Task{ID: fmt.Sprintf("finished-%d", i), Type: "image.update", Name: "Result", Status: "success"}
		if err := center.db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
		if err := center.db.Create(&database.TaskCenterScheduleRun{ScheduleID: &rule.ID, TaskID: task.ID, Status: "started"}).Error; err != nil {
			t.Fatal(err)
		}
		if i == 0 || i == 99 {
			queries.Store(0)
			rows, err := center.Schedules(context.Background(), "image.update", "edge")
			if err != nil || len(rows) != i+1 || queries.Load() != 3 {
				t.Fatalf("expected three queries for %d rules, got %d: %v", i+1, queries.Load(), err)
			}
			if rows[0].LastTaskID != task.ID || rows[0].LastTaskStatus != "success" {
				t.Fatal("latest task summary is missing", rows[0])
			}
		}
	}
}
