package cd

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/taskcenter"
	"gorm.io/gorm"
)

func TestPausedAutomationPreservesManualLifecycle(t *testing.T) {
	h := newCDHarness(t, ModeAuto)
	ctx := context.Background()
	registry := taskcenter.NewRegistry()
	center := taskcenter.New(h.db, registry, taskcenter.Dependencies{})
	h.service.SetTaskCenter(center)
	if err := registry.Register(h.service.AutomationKind()); err != nil {
		t.Fatal(err)
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error { return h.service.BackfillAutomation(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	before, err := h.service.GetConfiguration(ctx, h.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	project, err := h.service.SetAutomation(ctx, h.project.Name, false, before.AutomationVersion)
	if err != nil || project.AutomationEnabled {
		t.Fatal("pause failed", err)
	}
	if err := center.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	for _, trigger := range []string{"poll", "webhook"} {
		if _, err := h.service.Sync(ctx, h.project.Name, trigger, Actor{}); !errors.Is(err, ErrAutomationPaused) {
			t.Fatal("automatic trigger accepted while paused", trigger, err)
		}
	}
	var count int64
	h.db.Model(&database.Task{}).Count(&count)
	if count != 0 {
		t.Fatal("paused triggers queued tasks")
	}
	// In Auto policy a paused manual sync must still require an explicit deploy.
	first := syncAndDeploy(t, h)
	if first.Status != StatusSucceeded {
		t.Fatal("manual lifecycle unavailable while paused", first.Status)
	}
	rollback, err := h.service.Rollback(ctx, h.project.Name, first.ID, *first.LatestRecordID, nil, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, rollback.ID, task.StatusSuccess)
	configuration, err := h.service.GetConfiguration(ctx, h.project.Name)
	if err != nil || configuration.AutomationEnabled {
		t.Fatal("manual actions unexpectedly resumed automation", err)
	}
	// Older clients omit the switch; saving another setting must preserve pause.
	configuration, err = h.service.Configure(ctx, h.project.Name, ConfigureInput{
		Repository: configuration.Repository, ReconcileMode: ModeAuto,
		WebhookEnabled:      configuration.WebhookEnabled,
		SyncIntervalSeconds: 300, DeploymentTimeout: 60,
	})
	if err != nil || configuration.AutomationEnabled {
		t.Fatal("legacy configuration save lost pause", err)
	}
	if _, err := h.service.SetAutomation(ctx, h.project.Name, true, configuration.AutomationVersion); err != nil {
		t.Fatal(err)
	}
	if err := h.db.Model(&h.project).Update("last_sync_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Model(&database.TaskCenterSchedule{}).Where("kind = ? AND subject = ?", "cd.sync", h.project.Name).Update("next_run_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := center.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	var latest database.Task
	if err := h.db.Order("created_at DESC").First(&latest).Error; err != nil || latest.Type != "cd.sync" {
		t.Fatal("polling did not resume", err)
	}
	waitTask(t, h.db, latest.ID, task.StatusSuccess)
}

func TestUnifiedAutomationRejectsStaleBusinessSettings(t *testing.T) {
	h := newCDHarness(t, ModeAuto)
	ctx := context.Background()
	registry := taskcenter.NewRegistry()
	center := taskcenter.New(h.db, registry, taskcenter.Dependencies{})
	h.service.SetTaskCenter(center)
	if err := registry.Register(h.service.AutomationKind()); err != nil {
		t.Fatal(err)
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error { return h.service.BackfillAutomation(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
	before, err := h.service.GetConfiguration(ctx, h.project.Name)
	if err != nil || before.AutomationVersion == 0 {
		t.Fatal("missing shared version", err)
	}
	var rule database.TaskCenterSchedule
	if err := h.db.Where("kind = 'cd.sync' AND subject = ?", h.project.Name).First(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := center.Update(ctx, rule.ID, taskcenter.ScheduleInput{IntervalSeconds: 35}, rule.Version); err != nil {
		t.Fatal(err)
	}
	input := ConfigureInput{ExpectedAutomationVersion: &before.AutomationVersion, Repository: before.Repository, ReconcileMode: before.ReconcileMode, SyncIntervalSeconds: before.SyncIntervalSeconds, DeploymentTimeout: before.DeploymentTimeout, WebhookEnabled: before.WebhookEnabled, AutoRollback: before.AutoRollback}
	if _, err := h.service.Configure(ctx, h.project.Name, input); !errors.Is(err, taskcenter.ErrVersionConflict) {
		t.Fatal("stale business form replaced center settings", err)
	}
	if _, err := h.service.SetAutomation(ctx, h.project.Name, false, before.AutomationVersion); !errors.Is(err, taskcenter.ErrVersionConflict) {
		t.Fatal("stale switch replaced center settings", err)
	}
	if _, err := h.service.SetAutomation(ctx, h.project.Name, false); !errors.Is(err, taskcenter.ErrVersionConflict) {
		t.Fatal("unversioned mutation changed shared automation", err)
	}
	current, err := h.service.GetConfiguration(ctx, h.project.Name)
	if err != nil || current.SyncIntervalSeconds != 35 || !current.AutomationEnabled {
		t.Fatal("rejected writes changed business projections", err)
	}
	if _, err := h.service.SetAutomation(ctx, h.project.Name, false, current.AutomationVersion); err != nil {
		t.Fatal(err)
	}
	input.ExpectedAutomationVersion = nil
	input.SyncIntervalSeconds = 35
	saved, err := h.service.Configure(ctx, h.project.Name, input)
	if err != nil || saved.AutomationEnabled || saved.SyncIntervalSeconds != 35 {
		t.Fatal("legacy non-automation save changed pause", err)
	}
}

func TestConfiguredProjectCreatesAutomationInTheSameTransaction(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	registry := taskcenter.NewRegistry()
	center := taskcenter.New(h.db, registry, taskcenter.Dependencies{})
	h.service.SetTaskCenter(center)
	if err := registry.Register(h.service.AutomationKind()); err != nil {
		t.Fatal(err)
	}
	before, err := h.service.GetConfiguration(ctx, h.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	input := ConfigureInput{Repository: before.Repository, NodeIDs: before.NodeIDs, ReconcileMode: ModeManual, SyncIntervalSeconds: 35, DeploymentTimeout: 60, AutomationEnabled: &enabled}
	project, configuration, err := h.service.CreateConfiguredProject(ctx, "created-with-rule", input)
	if err != nil {
		t.Fatal(err)
	}
	var rule database.TaskCenterSchedule
	if err := h.db.Where("kind = 'cd.sync' AND subject = ?", project.Name).First(&rule).Error; err != nil {
		t.Fatal("new project has no rule", err)
	}
	if rule.Enabled || rule.IntervalSeconds != 35 || configuration.AutomationVersion != rule.Version || project.AutomationVersion != rule.Version {
		t.Fatal("creation lost disabled state or shared version")
	}
}

func TestCreateConfiguredProjectWithAutomationPaused(t *testing.T) {
	h := newCDHarness(t, ModeAuto)
	configuration, err := h.service.GetConfiguration(context.Background(), h.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	project, saved, err := h.service.CreateConfiguredProject(context.Background(), "created-paused", ConfigureInput{
		Repository: configuration.Repository, ReconcileMode: ModeAuto, AutomationEnabled: &enabled,
		SyncIntervalSeconds: 300, DeploymentTimeout: 60, NodeIDs: []string{"local"},
	})
	if err != nil || saved.AutomationEnabled || project.AutomationEnabled {
		t.Fatal("paused creation lost switch", err)
	}
	if _, err := h.service.Sync(context.Background(), project.Name, "poll", Actor{}); !errors.Is(err, ErrAutomationPaused) {
		t.Fatal("new paused project accepted poll", err)
	}
}

func TestPausedWebhookDoesNotConsumeDeliveryID(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	configureWebhookProject(t, h, "https://github.com/example/deploy.git", "test-webhook-secret")
	body := []byte(`{"repository":"https://github.com/example/deploy.git","ref":"refs/heads/main"}`)
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer test-webhook-secret")
	headers.Set("Idempotency-Key", "paused-delivery")
	if _, err := h.service.SetAutomation(ctx, h.project.Name, false); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.HandleGitWebhook(ctx, "hook-test", headers, body); !errors.Is(err, ErrAutomationPaused) {
		t.Fatal("paused webhook accepted", err)
	}
	bad := headers.Clone()
	bad.Set("Authorization", "Bearer invalid")
	if _, err := h.service.HandleGitWebhook(ctx, "hook-test", bad, body); !errors.Is(err, ErrWebhookSignature) {
		t.Fatal("paused webhook bypassed authentication", err)
	}
	assertWebhookDeliveryCount(t, h, 0)
	if _, err := h.service.SetAutomation(ctx, h.project.Name, true); err != nil {
		t.Fatal(err)
	}
	row, err := h.service.HandleGitWebhook(ctx, "hook-test", headers, body)
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, row.ID, task.StatusSuccess)
	assertWebhookDelivery(t, h, "paused-delivery", "queued")
}

func TestPauseRechecksQueuedAutomaticSync(t *testing.T) {
	h := newCDHarness(t, ModeAuto)
	ctx := context.Background()
	lock := h.service.projectLock(h.project.ID)
	lock.Lock()
	row, err := h.service.Sync(ctx, h.project.Name, "poll", Actor{})
	if err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	_, err = h.service.SetAutomation(ctx, h.project.Name, false)
	lock.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	result := waitTask(t, h.db, row.ID, task.StatusFailed)
	if result.Message != ErrAutomationPaused.Error() {
		t.Fatal("queued trigger did not observe pause", result.Message)
	}
	h.git.mu.Lock()
	calls := h.git.calls
	h.git.mu.Unlock()
	if calls != 0 {
		t.Fatal("queued trigger fetched Git after pause")
	}
}

func TestPauseAllowsStartedDeliveryToFinish(t *testing.T) {
	h := newCDHarness(t, ModeAuto)
	h.git.delay = 300 * time.Millisecond
	row, err := h.service.Sync(context.Background(), h.project.Name, "poll", Actor{})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.git.mu.Lock()
		started := h.git.active > 0
		h.git.mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delivery did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := h.service.SetAutomation(context.Background(), h.project.Name, false); err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, row.ID, task.StatusSuccess)
	releases, err := h.service.ListReleases(context.Background(), h.project.Name)
	if err != nil || len(releases) != 1 || releases[0].Status != StatusSucceeded {
		t.Fatal("started delivery was interrupted", err)
	}
}
