package projectcd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
	"github.com/suma/suma/server/internal/imageupdate"
)

type fixtureImageResolver struct{}

func (fixtureImageResolver) Resolve(_ context.Context, value string, platform imageupdate.Platform, _ credential.RegistryMaterial) (imageupdate.Remote, error) {
	sum := sha256.Sum256([]byte(value + platform.String()))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if _, pinned, ok := strings.Cut(value, "@"); ok {
		digest = pinned
	}
	return imageupdate.Remote{ManifestDigest: digest, ConfigDigest: digest}, nil
}

func projectPreparedPlan(t *testing.T, f *projectCDFixture, input deployment.PrepareReleaseInput) deployment.ReleasePlanView {
	t.Helper()
	operation, err := f.service.PrepareRelease(context.Background(), "local", "app", input, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Wait(context.Background(), operation.ID); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.PreparedRelease(context.Background(), "local", "app", operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestProjectPreparedReleaseConfirmsOnceAndRetainsPinnedHistory(t *testing.T) {
	f := newProjectCDFixture(t)
	ctx := context.Background()
	if _, err := f.service.Configure(ctx, "local", "app", f.input); err != nil {
		t.Fatal(err)
	}
	view := projectPreparedPlan(t, f, deployment.PrepareReleaseInput{})
	raw, err := os.ReadFile(f.calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), " up ") {
		t.Fatal("preparation deployed containers")
	}
	if len(view.Nodes) != 1 || view.Nodes[0].NodeID != "local" || !view.ImagesPinned {
		t.Fatal("invalid single-node plan", view)
	}
	operation, err := f.service.ConfirmRelease(ctx, "local", "app", view.ID, deployment.ConfirmReleaseInput{}, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Wait(ctx, operation.ID); err != nil {
		t.Fatal(err)
	}
	duplicate, err := f.service.ConfirmRelease(ctx, "local", "app", view.ID, deployment.ConfirmReleaseInput{}, Actor{Name: "operator"})
	if err != nil || duplicate.ID != operation.ID {
		t.Fatal("duplicate publication", err)
	}
	config, err := f.service.Get(ctx, "local", "app")
	if err != nil || config.Applied == nil || !config.Applied.ImagesPinned || config.InitialApplicationRequired {
		t.Fatal("successful fixed snapshot missing", err)
	}
	view = projectPreparedPlan(t, f, deployment.PrepareReleaseInput{Operation: "restore", SourceRecordID: config.Applied.ID})
	if view.Operation != "restore" || view.SourceRecordID != config.Applied.ID {
		t.Fatal("restore source changed")
	}
}

func TestProjectPreparedReleaseRejectsChangedSettingsAndExpiredPlans(t *testing.T) {
	f := newProjectCDFixture(t)
	ctx := context.Background()
	config, err := f.service.Configure(ctx, "local", "app", f.input)
	if err != nil {
		t.Fatal(err)
	}
	view := projectPreparedPlan(t, f, deployment.PrepareReleaseInput{})
	variables := []deployment.EnvironmentVariable{{Name: "TOKEN", Value: func() *string { value := "private-updated-value"; return &value }()}}
	if _, err := f.service.SetEnvironment(ctx, "local", "app", config.Revision, variables); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ConfirmRelease(ctx, "local", "app", view.ID, deployment.ConfirmReleaseInput{}, Actor{}); !errors.Is(err, deployment.ErrPlanChanged) {
		t.Fatal("stale preview accepted", err)
	}
	view = projectPreparedPlan(t, f, deployment.PrepareReleaseInput{})
	if err := f.db.Model(&database.ReleasePlan{}).Where("id = ?", view.ID).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ConfirmRelease(ctx, "local", "app", view.ID, deployment.ConfirmReleaseInput{}, Actor{}); !errors.Is(err, deployment.ErrPlanChanged) {
		t.Fatal("expired preview accepted", err)
	}
}

func TestProjectPreparedRetryPreservesOriginalFailureAndConfiguration(t *testing.T) {
	f := newProjectCDFixture(t)
	ctx := context.Background()
	if _, err := f.service.Configure(ctx, "local", "app", f.input); err != nil {
		t.Fatal(err)
	}
	view := projectPreparedPlan(t, f, deployment.PrepareReleaseInput{})
	if err := os.WriteFile(filepath.Join(f.root, "up-fail"), []byte("fail"), 0600); err != nil {
		t.Fatal(err)
	}
	operation, err := f.service.ConfirmRelease(ctx, "local", "app", view.ID, deployment.ConfirmReleaseInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Wait(ctx, operation.ID); err != nil {
		t.Fatal(err)
	}
	var failed database.ProjectCDRecord
	if err := f.db.Where("task_id = ?", operation.ID).First(&failed).Error; err != nil || failed.Status != "failed" {
		t.Fatal("failure evidence missing", err)
	}
	if err := os.Remove(filepath.Join(f.root, "up-fail")); err != nil {
		t.Fatal(err)
	}
	retry := projectPreparedPlan(t, f, deployment.PrepareReleaseInput{Operation: "retry", SourceRecordID: failed.ID})
	operation, err = f.service.ConfirmRelease(ctx, "local", "app", retry.ID, deployment.ConfirmReleaseInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Wait(ctx, operation.ID); err != nil {
		t.Fatal(err)
	}
	var original database.ProjectCDRecord
	if err := f.db.First(&original, failed.ID).Error; err != nil || original.Status != "failed" {
		t.Fatal("retry erased original failure", err)
	}
}

func TestProjectNoChangeRequiresAndHonorsForceRecreation(t *testing.T) {
	f := newProjectCDFixture(t)
	ctx := context.Background()
	if _, err := f.service.Configure(ctx, "local", "app", f.input); err != nil {
		t.Fatal(err)
	}
	first := projectPreparedPlan(t, f, deployment.PrepareReleaseInput{})
	operation, err := f.service.ConfirmRelease(ctx, "local", "app", first.ID, deployment.ConfirmReleaseInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Wait(ctx, operation.ID); err != nil {
		t.Fatal(err)
	}
	next := projectPreparedPlan(t, f, deployment.PrepareReleaseInput{})
	if next.Nodes[0].Changed {
		t.Fatal("unchanged configuration was marked as changed")
	}
	if _, err := f.service.ConfirmRelease(ctx, "local", "app", next.ID, deployment.ConfirmReleaseInput{}, Actor{}); err == nil {
		t.Fatal("implicit recreation was accepted")
	}
	operation, err = f.service.ConfirmRelease(ctx, "local", "app", next.ID, deployment.ConfirmReleaseInput{ForceRecreate: true}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.Wait(ctx, operation.ID); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(f.calls)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "--force-recreate") {
		t.Fatal("explicit recreation was not passed to Compose")
	}
}
