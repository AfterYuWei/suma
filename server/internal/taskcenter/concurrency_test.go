package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

func TestConcurrentScannersAndManualAdmissionStartOnlyOneTask(t *testing.T) {
	center, stub := newTestService(t)
	block := make(chan struct{})
	defer close(block)
	stub.block = block
	rule := createPlan(t, center, nil)
	if err := center.db.Model(&rule).Update("next_run_at", fixedNow).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 8)
	var workers sync.WaitGroup
	for i := range 8 {
		workers.Go(func() {
			<-start
			if i%2 == 0 {
				results <- center.RunDue(ctx)
			} else {
				_, err := center.RunNow(ctx, rule.ID)
				if errors.Is(err, ErrBusy) {
					err = nil
				}
				results <- err
			}
		})
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if stub.count() != 1 {
		t.Fatalf("concurrent admission started %d business operations", stub.count())
	}
	var tasks, occurrences int64
	if err := center.db.Model(&database.Task{}).Count(&tasks).Error; err != nil || tasks != 1 {
		t.Fatal("admission created duplicate tasks", tasks, err)
	}
	if err := center.db.Model(&database.TaskCenterScheduleRun{}).Where("schedule_id = ? AND scheduled_for = ?", rule.ID, fixedNow).Count(&occurrences).Error; err != nil || occurrences != 1 {
		t.Fatal("occurrence was not claimed exactly once", occurrences, err)
	}
	var retained database.TaskCenterSchedule
	if err := center.db.First(&retained, rule.ID).Error; err != nil || retained.Version != rule.Version || retained.NextRunAt == nil || !retained.NextRunAt.After(fixedNow) {
		t.Fatal("tick changed configuration version or failed to advance", err)
	}
}

func TestPauseAfterClaimRechecksBeforeWorkButAllowsManualRun(t *testing.T) {
	center, _ := newTestService(t)
	tasks := task.NewService(center.db)
	block := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(block) })
	var effects atomic.Int32
	if err := center.kinds.Register(KindSpec{Type: "controlled", Start: func(ctx context.Context, request StartRequest) (string, error) {
		row, err := tasks.StartDefinition(ctx, task.Definition{Type: "controlled", Name: "Controlled business work", TriggerRunID: request.RunID}, func(ctx context.Context, _ string, _ task.Reporter) (any, error) {
			select {
			case <-block:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if err := center.ValidateRun(ctx, request.RunID); err != nil {
				return nil, err
			}
			effects.Add(1)
			return nil, nil
		})
		return row.ID, err
	}}); err != nil {
		t.Fatal(err)
	}
	rule, err := center.Create(context.Background(), ScheduleInput{Name: "Gate", Kind: "controlled", Params: json.RawMessage(`{}`), Cron: "0 4 * * *", Timezone: "UTC"}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if err := center.db.Model(&rule).Update("next_run_at", fixedNow).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	run, err := center.Dispatch(ctx, DispatchRequest{ScheduleID: rule.ID, Reason: ReasonScheduled, ScheduledFor: &fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		tasks.Cancel(run.TaskID)
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if err := tasks.Wait(cleanup, run.TaskID); err != nil {
			t.Error(err)
		}
	})
	enabled := false
	if _, err := center.Update(ctx, rule.ID, ScheduleInput{Enabled: &enabled}, rule.Version); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(block) })
	if err := tasks.Wait(ctx, run.TaskID); err != nil {
		t.Fatal(err)
	}
	stopped, err := tasks.Get(ctx, run.TaskID)
	if err != nil || stopped.Status != task.StatusFailed || effects.Load() != 0 {
		t.Fatal("claimed automatic operation bypassed the updated gate", err)
	}
	manual, err := center.RunNow(ctx, rule.ID)
	if err != nil {
		t.Fatal("pause blocked explicit manual work", err)
	}
	if err := tasks.Wait(ctx, manual.TaskID); err != nil {
		t.Fatal(err)
	}
	finished, err := tasks.Get(ctx, manual.TaskID)
	if err != nil || finished.Status != task.StatusSuccess || effects.Load() != 1 {
		t.Fatal("manual run did not retain its ordinary behavior", err)
	}
}
