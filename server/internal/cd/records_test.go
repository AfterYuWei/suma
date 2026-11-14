package cd

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/suma/suma/server/internal/secret"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

func fixtureRecord(t *testing.T, h *cdHarness, release *database.DeliveryRelease, nodes []string, status string) database.DeliveryRecord {
	t.Helper()
	raw, _ := json.Marshal(nodes)
	record := database.DeliveryRecord{ProjectID: h.project.ID, ReleaseID: release.ID, Operation: AttemptDeploy, ActorName: "fixture", Status: status, SnapshotComplete: true, ComposeFilesJSON: `["compose.yml"]`, ImageReferences: "[]", WorktreePath: release.WorktreePath, NodeIDsJSON: string(raw), RegistryCredentialIDsJSON: "[]", DeploymentTimeout: 60}
	if _, err := h.service.freezeRecord(context.Background(), h.project, *release, &record); err != nil {
		t.Fatal(err)
	}
	if err := h.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Model(release).Update("latest_record_id", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	release.LatestRecordID = &record.ID
	return record
}
func TestFailedSnapshotCanRollbackAndPreservesFailureEvidence(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	first := syncAndDeploy(t, h)
	h.runner.mu.Lock()
	h.runner.upFailures = []error{errors.New("controlled failure")}
	h.runner.mu.Unlock()
	failed, err := h.service.Deploy(ctx, h.project.Name, first.ID, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, failed.ID, task.StatusFailed)
	before, err := h.service.GetRecord(ctx, h.project.Name, failed.RecordID)
	if err != nil || !before.SnapshotComplete || before.Status != StatusFailed {
		t.Fatal("failed snapshot missing", err)
	}
	restore, err := h.service.Rollback(ctx, h.project.Name, first.ID, failed.RecordID, nil, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, restore.ID, task.StatusSuccess)
	after, err := h.service.GetRecord(ctx, h.project.Name, failed.RecordID)
	if err != nil || after.Status != before.Status || after.FailureReason != before.FailureReason {
		t.Fatal("rollback rewrote failed record", err)
	}
	records, err := h.service.ListRecords(ctx, h.project.Name, RecordQuery{Operation: AttemptManualRollback, PageSize: 10})
	if err != nil || records.Total != 1 || records.Counts[AttemptDeploy] != 2 {
		t.Fatal("record filters or totals", err)
	}
	if records.Items[0].SourceRecordID == nil || *records.Items[0].SourceRecordID != failed.RecordID {
		t.Fatal("rollback source missing")
	}
}
func TestRetryUsesFailureConfigurationAndKeepsOneGitRelease(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	config, _ := h.service.GetConfiguration(ctx, h.project.Name)
	input := ConfigureInput{Repository: config.Repository, ReconcileMode: ModeManual, SyncIntervalSeconds: 300, DeploymentTimeout: 60, Environment: []EnvironmentVariable{{Name: "VERSION", Value: environmentValue("one")}}}
	if _, err := h.service.Configure(ctx, h.project.Name, input); err != nil {
		t.Fatal(err)
	}
	first := syncAndDeploy(t, h)
	h.runner.mu.Lock()
	h.runner.upFailures = []error{errors.New("controlled failure")}
	h.runner.mu.Unlock()
	failed, err := h.service.Deploy(ctx, h.project.Name, first.ID, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, failed.ID, task.StatusFailed)
	input.Environment[0].Value = environmentValue("two")
	if _, err := h.service.Configure(ctx, h.project.Name, input); err != nil {
		t.Fatal(err)
	}
	retried, err := h.service.RetryFailedNodes(ctx, h.project.Name, first.ID, failed.RecordID, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, retried.Task.ID, task.StatusSuccess)
	specs := h.runner.UpSpecs()
	if specs[len(specs)-1].Environment["VERSION"] != "one" || !specs[len(specs)-1].ForceRecreate {
		t.Fatal("retry used changed settings or skipped recreate")
	}
	original, err := h.service.GetRecord(ctx, h.project.Name, failed.RecordID)
	if err != nil || original.Status != StatusFailed {
		t.Fatal("retry erased failure", err)
	}
	if _, err := h.service.RetryFailedNodes(ctx, h.project.Name, first.ID, failed.RecordID, Actor{Name: "operator"}); err == nil {
		t.Fatal("stale retry was accepted")
	}
	releases, err := h.service.ListReleases(ctx, h.project.Name)
	if err != nil || len(releases) != 1 {
		t.Fatal("retry created a release", err)
	}
}
func TestAutomaticSyncAfterTargetedRetryDoesNotRepublishHealthyNodes(t *testing.T) {
	h := newCDHarness(t, ModeAuto)
	ctx := context.Background()
	for _, id := range []string{"node-a", "node-b"} {
		if err := h.db.Create(&database.Node{ID: id, Name: id, ConnectionType: "unix", Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
		if err := h.db.Create(&database.DeliveryProjectNode{ProjectID: h.project.ID, NodeID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	other := &fakeComposeRunner{rendered: h.runner.rendered, health: h.runner.health}
	h.service.compose = &targetedFakeRunner{fakeComposeRunner: h.runner, runners: map[string]compose.Runner{"node-a": h.runner, "node-b": other}}
	h.service.SetTargetResolver(fakeTargetResolver{})
	initial, err := h.service.Sync(ctx, h.project.Name, "poll", Actor{Name: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, initial.ID, task.StatusSuccess)
	releases, err := h.service.ListReleases(ctx, h.project.Name)
	if err != nil || len(releases) != 1 {
		t.Fatal(err)
	}
	first := releases[0]
	other.mu.Lock()
	other.upFailures = []error{errors.New("controlled failure")}
	other.mu.Unlock()
	failed, err := h.service.Deploy(ctx, h.project.Name, first.ID, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, failed.ID, task.StatusFailed)
	retry, err := h.service.RetryFailedNodes(ctx, h.project.Name, first.ID, failed.RecordID, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, retry.Task.ID, task.StatusSuccess)
	if !reflect.DeepEqual(retry.NodeIDs, []string{"node-b"}) {
		t.Fatal("retry touched a successful node")
	}
	var before int64
	if err := h.db.Model(&database.DeliveryRecord{}).Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	sync, err := h.service.Sync(ctx, h.project.Name, "poll", Actor{Name: "scheduler"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, sync.ID, task.StatusSuccess)
	var after int64
	if err := h.db.Model(&database.DeliveryRecord{}).Count(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("unchanged active configuration was republished after targeted retry")
	}
}

func TestRollbackRequiresRecordedTargetsAndRejectsIncompleteCrossProjectSnapshots(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	if err := h.db.Save(&[]database.Node{{ID: "local", Name: "Local", ConnectionType: "unix", Enabled: true}, {ID: "other", Name: "Other", ConnectionType: "unix", Enabled: true}}).Error; err != nil {
		t.Fatal(err)
	}
	first := syncAndDeploy(t, h)
	if _, err := h.service.Rollback(ctx, h.project.Name, first.ID, *first.LatestRecordID, []string{"other"}, Actor{Name: "operator"}); err == nil {
		t.Fatal("restore accepted a node outside the fixed snapshot")
	}
	result, err := h.service.Rollback(ctx, h.project.Name, first.ID, *first.LatestRecordID, []string{"local"}, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, result.ID, task.StatusSuccess)
	record, err := h.service.GetRecord(ctx, h.project.Name, result.RecordID)
	if err != nil || !reflect.DeepEqual(record.NodeIDs, []string{"local"}) {
		t.Fatal("override not recorded", err)
	}
	config, err := h.service.GetConfiguration(ctx, h.project.Name)
	if err != nil || !reflect.DeepEqual(config.NodeIDs, []string{"local"}) {
		t.Fatal("temporary override changed settings", err)
	}
	if _, err := h.service.Rollback(ctx, h.project.Name, first.ID, result.RecordID, []string{}, Actor{}); err == nil {
		t.Fatal("empty override accepted")
	}
	if _, err := h.service.Rollback(ctx, h.project.Name, first.ID, result.RecordID, []string{"missing"}, Actor{}); err == nil {
		t.Fatal("missing node accepted")
	}
	if err := h.db.Model(&database.DeliveryRecord{}).Where("id = ?", result.RecordID).Update("snapshot_complete", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Rollback(ctx, h.project.Name, first.ID, result.RecordID, nil, Actor{}); err == nil {
		t.Fatal("incomplete snapshot accepted")
	}
	if _, err := h.service.Rollback(ctx, h.project.Name, first.ID, result.RecordID+10000, nil, Actor{}); err == nil {
		t.Fatal("unknown snapshot accepted")
	}
}

func TestRecordSnapshotRetainsEncryptedInputAndTemporaryOverrides(t *testing.T) {
	store, err := secret.Open(filepath.Join(t.TempDir(), "test.key"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(nil, nil, nil, nil, nil, nil, store)
	encrypted, err := service.configureEnvironment(nil, []EnvironmentVariable{{Name: "TOKEN", Value: environmentValue("private-record-value")}})
	if err != nil {
		t.Fatal(err)
	}
	tree := makeWorktree(t, "example/app:latest")
	source := database.DeliveryRecord{ID: 11, ProjectID: 1, ReleaseID: 2, Operation: AttemptDeploy, Status: StatusFailed, SnapshotComplete: true, ComposeFilesJSON: `["compose.yml"]`, EnvironmentCiphertext: encrypted, WorktreePath: tree, NodeIDsJSON: `["first","second"]`, RegistryCredentialIDsJSON: `[4]`, DeploymentTimeout: 90, AutoRollback: true}
	original := append([]byte(nil), source.EnvironmentCiphertext...)
	retry := copyRecordSnapshot(source, AttemptRetry, Actor{Name: "operator"})
	rollback := copyRecordSnapshot(source, AttemptManualRollback, Actor{Name: "operator"})
	rollback.NodeIDsJSON = `["override"]`
	for _, record := range []database.DeliveryRecord{retry, rollback} {
		spec, err := service.releaseSpec("fixture", recordRelease(database.DeliveryRelease{}, record))
		if err != nil || spec.Environment["TOKEN"] != "private-record-value" {
			t.Fatal("operation lost the encrypted source", err)
		}
		raw, err := json.Marshal(record)
		if err != nil || strings.Contains(string(raw), "private-record-value") || strings.Contains(string(raw), "environment_ciphertext") {
			t.Fatal("record response exposed private configuration", err)
		}
		if record.SourceRecordID == nil || *record.SourceRecordID != source.ID || record.DeploymentTimeout != 90 || !record.AutoRollback || record.Status != task.StatusPending {
			t.Fatal("operation did not retain source configuration")
		}
	}
	rollback.EnvironmentCiphertext[0] ^= 1
	if !reflect.DeepEqual(source.EnvironmentCiphertext, original) || source.NodeIDsJSON != `["first","second"]` || source.Status != StatusFailed {
		t.Fatal("new operation mutated the original evidence")
	}
}
