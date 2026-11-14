package cd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
	"github.com/suma/suma/server/internal/imageupdate"
	"github.com/suma/suma/server/internal/task"
)

type fixtureImageResolver struct{}

func (fixtureImageResolver) Resolve(_ context.Context, value string, platform imageupdate.Platform, _ credential.RegistryMaterial) (imageupdate.Remote, error) {
	sum := sha256.Sum256([]byte(value + platform.String()))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if _, pinned, ok := strings.Cut(value, "@"); ok {
		digest = pinned
	}
	return imageupdate.Remote{ManifestDigest: digest, ConfigDigest: digest, IndexDigest: digest}, nil
}

type movingImageResolver struct {
	mu     sync.Mutex
	digest string
}

func (r *movingImageResolver) Resolve(_ context.Context, value string, _ imageupdate.Platform, _ credential.RegistryMaterial) (imageupdate.Remote, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	digest := r.digest
	if _, pinned, ok := strings.Cut(value, "@"); ok {
		digest = pinned
	}
	return imageupdate.Remote{ManifestDigest: digest, ConfigDigest: digest}, nil
}
func (r *movingImageResolver) move(value string) { r.mu.Lock(); defer r.mu.Unlock(); r.digest = value }

func preparedPlan(t *testing.T, h *cdHarness, input deployment.PrepareReleaseInput) deployment.ReleasePlanView {
	t.Helper()
	row, err := h.service.PrepareRelease(context.Background(), h.project.Name, input, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, row.ID, task.StatusSuccess)
	view, err := h.service.PreparedRelease(context.Background(), h.project.Name, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestPreparedReleaseFreezesImagesAndDeduplicatesConfirmation(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	resolver := &movingImageResolver{digest: "sha256:" + strings.Repeat("a", 64)}
	h.service.SetImageResolver(resolver)
	view := preparedPlan(t, h, deployment.PrepareReleaseInput{})
	if len(h.runner.UpSpecs()) != 0 {
		t.Fatal("preparation changed containers")
	}
	resolver.move("sha256:" + strings.Repeat("b", 64))
	started, err := h.service.ConfirmRelease(ctx, h.project.Name, view.ID, deployment.ConfirmReleaseInput{}, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, started.ID, task.StatusSuccess)
	duplicate, err := h.service.ConfirmRelease(ctx, h.project.Name, view.ID, deployment.ConfirmReleaseInput{}, Actor{Name: "operator"})
	if err != nil || duplicate.ID != started.ID || duplicate.RecordID != started.RecordID {
		t.Fatal("confirmation was not idempotent", err)
	}
	specs := h.runner.UpSpecs()
	if len(specs) != 1 || !strings.HasSuffix(specs[0].ImageOverrides["web"], strings.Repeat("a", 64)) {
		t.Fatal("confirmation followed the moved tag", specs)
	}
	next := preparedPlan(t, h, deployment.PrepareReleaseInput{})
	if next.CommitSHA != view.CommitSHA || !next.Nodes[0].Changed || !strings.HasSuffix(next.Nodes[0].Images[0].Reference, strings.Repeat("b", 64)) {
		t.Fatal("same-commit image update missing")
	}
	var count int64
	if err := h.db.Model(&database.DeliveryRelease{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("image update fabricated a Git release", count, err)
	}
}

func TestPreparedReleaseRejectsConfigChangesAndExpiration(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	view := preparedPlan(t, h, deployment.PrepareReleaseInput{})
	if err := h.db.Model(&h.project).Update("deployment_timeout", 120).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.ConfirmRelease(ctx, h.project.Name, view.ID, deployment.ConfirmReleaseInput{}, Actor{}); !errors.Is(err, deployment.ErrPlanChanged) {
		t.Fatal("changed configuration accepted", err)
	}
	view = preparedPlan(t, h, deployment.PrepareReleaseInput{})
	if err := h.db.Model(&database.ReleasePlan{}).Where("id = ?", view.ID).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.ConfirmRelease(ctx, h.project.Name, view.ID, deployment.ConfirmReleaseInput{}, Actor{}); !errors.Is(err, deployment.ErrPlanChanged) {
		t.Fatal("expired plan accepted", err)
	}
	if len(h.runner.UpSpecs()) != 0 {
		t.Fatal("rejected confirmation changed containers")
	}
}

func TestPreparedRetryKeepsSuccessfulNodesAndFailureEvidence(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	if err := h.db.Create(&database.Node{ID: "other", Name: "Other", Enabled: true, ConnectionType: "unix"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.service.replaceTargets(ctx, h.db, h.project.ID, []string{"local", "other"}); err != nil {
		t.Fatal(err)
	}
	other := &fakeComposeRunner{rendered: h.runner.rendered, health: h.runner.health, upFailures: []error{errors.New("controlled failure")}}
	h.service.compose = &targetedFakeRunner{fakeComposeRunner: h.runner, runners: map[string]compose.Runner{"local": h.runner, "other": other}}
	view := preparedPlan(t, h, deployment.PrepareReleaseInput{})
	started, err := h.service.ConfirmRelease(ctx, h.project.Name, view.ID, deployment.ConfirmReleaseInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, started.ID, task.StatusFailed)
	localCalls := len(h.runner.UpSpecs())
	retry := preparedPlan(t, h, deployment.PrepareReleaseInput{Operation: "retry", SourceRecordID: started.RecordID})
	if len(retry.Nodes) != 1 || retry.Nodes[0].NodeID != "other" {
		t.Fatal("retry included successful nodes", retry.Nodes)
	}
	retried, err := h.service.ConfirmRelease(ctx, h.project.Name, retry.ID, deployment.ConfirmReleaseInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, retried.ID, task.StatusSuccess)
	if len(h.runner.UpSpecs()) != localCalls {
		t.Fatal("retry redeployed successful node")
	}
	original, err := h.service.GetRecord(ctx, h.project.Name, started.RecordID)
	if err != nil || original.Status != StatusPartialFailed {
		t.Fatal("retry rewrote failure evidence", err)
	}
}

func TestPreparedFailedNodeRestoreUsesEachNodesOwnSuccessfulSnapshot(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	if err := h.db.Create(&database.Node{ID: "other", Name: "Other", Enabled: true, ConnectionType: "unix"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.service.replaceTargets(ctx, h.db, h.project.ID, []string{"local", "other"}); err != nil {
		t.Fatal(err)
	}
	other := &fakeComposeRunner{rendered: h.runner.rendered, health: h.runner.health}
	h.service.compose = &targetedFakeRunner{fakeComposeRunner: h.runner, runners: map[string]compose.Runner{"local": h.runner, "other": other}}
	resolver := &movingImageResolver{digest: "sha256:" + strings.Repeat("a", 64)}
	h.service.SetImageResolver(resolver)
	first := preparedPlan(t, h, deployment.PrepareReleaseInput{})
	initial, err := h.service.ConfirmRelease(ctx, h.project.Name, first.ID, deployment.ConfirmReleaseInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, initial.ID, task.StatusSuccess)
	h.git.revision.CommitSHA = strings.Repeat("b", 40)
	resolver.move("sha256:" + strings.Repeat("b", 64))
	other.mu.Lock()
	other.upFailures = []error{errors.New("controlled failure")}
	other.mu.Unlock()
	second := preparedPlan(t, h, deployment.PrepareReleaseInput{})
	failed, err := h.service.ConfirmRelease(ctx, h.project.Name, second.ID, deployment.ConfirmReleaseInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, failed.ID, task.StatusFailed)
	localCalls := len(h.runner.UpSpecs())
	restore := preparedPlan(t, h, deployment.PrepareReleaseInput{Operation: "restore-failed", SourceRecordID: failed.RecordID})
	if len(restore.Nodes) != 1 || restore.Nodes[0].NodeID != "other" || restore.Nodes[0].TargetCommit != first.CommitSHA {
		t.Fatal("recovery selected the wrong node or baseline", restore.Nodes)
	}
	operation, err := h.service.ConfirmRelease(ctx, h.project.Name, restore.ID, deployment.ConfirmReleaseInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, operation.ID, task.StatusSuccess)
	if len(h.runner.UpSpecs()) != localCalls {
		t.Fatal("recovery changed a successful node")
	}
	specs := other.UpSpecs()
	if !strings.HasSuffix(specs[len(specs)-1].ImageOverrides["web"], strings.Repeat("a", 64)) {
		t.Fatal("recovery resolved the latest tag instead of the baseline")
	}
	original, err := h.service.GetRecord(ctx, h.project.Name, failed.RecordID)
	if err != nil || original.Status != StatusPartialFailed {
		t.Fatal("recovery erased original failure", err)
	}
}

func TestAutomaticFirstReleaseStillRequiresExplicitConfirmation(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	input := creationInput()
	input.Repository.ComposeFiles = []string{"compose.yml"}
	input.Repository.EnvironmentFile = ""
	input.ReconcileMode = ModeAuto
	project, _, err := h.service.CreateConfiguredProject(ctx, "first-release", input)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := h.service.Sync(ctx, project.Name, "poll", Actor{Name: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, checked.ID, task.StatusSuccess)
	if len(h.runner.UpSpecs()) != 0 {
		t.Fatal("automatic trigger deployed before first confirmation")
	}
	configuration, err := h.service.GetConfiguration(ctx, project.Name)
	if err != nil || !configuration.InitialApplicationRequired {
		t.Fatal("first confirmation gate missing", err)
	}
}
