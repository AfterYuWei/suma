package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/imageupdate"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
	"gorm.io/gorm"
)

type planImageResolver struct{ references []string }

func (r *planImageResolver) Resolve(_ context.Context, reference string, platform imageupdate.Platform, _ credential.RegistryMaterial) (imageupdate.Remote, error) {
	r.references = append(r.references, reference+" "+platform.String())
	return imageupdate.Remote{ManifestDigest: "sha256:" + strings.Repeat("a", 64), ConfigDigest: "sha256:" + strings.Repeat("b", 64), IndexDigest: "sha256:" + strings.Repeat("c", 64)}, nil
}

func TestReleasePlanClaimsWithTaskAndEncryptsInputs(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.Create(&database.Task{ID: "prepare-task", Type: "cd.prepare", Name: "Prepare", Status: "success"}).Error; err != nil {
		t.Fatal(err)
	}
	view, err := SaveReleasePlan(ctx, db, secrets, "batch", 42, "configuration-one", "prepare-task", map[string]string{"TOKEN": "private-release-value"}, ReleasePlanView{Operation: "deploy", Nodes: []ReleaseNodeView{{NodeID: "local"}}})
	if err != nil {
		t.Fatal(err)
	}
	var row database.ReleasePlan
	if err := db.First(&row, "id = ?", view.ID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(row.PayloadCiphertext), "private-release-value") || strings.Contains(row.PreviewJSON, "private-release-value") {
		t.Fatal("private input exposed")
	}
	var decoded map[string]string
	if _, _, err := LoadReleasePlan(ctx, db, secrets, "project", 42, view.ID, &decoded); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("plan crossed owner kind", err)
	}
	if _, _, err := LoadReleasePlan(ctx, db, secrets, "batch", 43, view.ID, &decoded); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("plan crossed owner identity", err)
	}
	if _, _, err := LoadReleasePlan(ctx, db, secrets, "batch", 42, view.ID, &decoded); err != nil || decoded["TOKEN"] != "private-release-value" {
		t.Fatal("cannot decrypt input", err)
	}
	tasks := task.NewService(db)
	definition := task.Definition{Type: "cd.deploy", Name: "Confirm plan", Admit: func(tx *gorm.DB, row database.Task) error {
		return ClaimReleasePlan(tx, view.ID, "configuration-one", row.ID)
	}}
	started, err := tasks.StartDefinition(ctx, definition, func(context.Context, string, task.Reporter) (any, error) {
		return map[string]string{"operation": "deploy"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.StartDefinition(ctx, definition, func(context.Context, string, task.Reporter) (any, error) {
		t.Error("duplicate work executed")
		return nil, nil
	}); !errors.Is(err, ErrPlanChanged) {
		t.Fatal("duplicate confirmation accepted", err)
	}
	if err := tasks.Wait(ctx, started.ID); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&database.Task{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatal("rejected confirmation left another task", count, err)
	}
}

func TestReleasePlanRejectsExpirationVersionAndUnreviewedTargets(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	view, err := SaveReleasePlan(ctx, db, secrets, "project", 1, "v1", "prepare", nil, ReleasePlanView{Nodes: []ReleaseNodeView{{NodeID: "a"}, {NodeID: "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return ClaimReleasePlan(tx, view.ID, "v2", "task") }); !errors.Is(err, ErrPlanChanged) {
		t.Fatal("stale version claimed", err)
	}
	if err := db.Model(&database.ReleasePlan{}).Where("id = ?", view.ID).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return ClaimReleasePlan(tx, view.ID, "v1", "task") }); !errors.Is(err, ErrPlanChanged) {
		t.Fatal("expired plan claimed", err)
	}
	for _, ids := range [][]string{{}, {"a", "a"}, {"unreviewed"}} {
		if _, err := SelectedReleaseNodes(view, ids); err == nil {
			t.Fatal("invalid target selection accepted", ids)
		}
	}
	if ids, err := SelectedReleaseNodes(view, []string{"b"}); err != nil || len(ids) != 1 || ids[0] != "b" {
		t.Fatal("valid subset rejected", err)
	}
}

func TestFreezeNodePinsPlatformAndMasksPreview(t *testing.T) {
	resolver := &planImageResolver{}
	environment := map[string]string{"TOKEN": "private-value"}
	rendered := `{"services":{"app":{"image":"example/app:latest","environment":{"TOKEN":"private-value"}}}}`
	frozen, err := FreezeNode(context.Background(), resolver, nil, nil, compose.Target{NodeID: "arm", RuntimeIdentity: "engine-arm", OS: "linux", Architecture: "arm64"}, compose.ExecutionSpec{Environment: environment}, rendered)
	if err != nil {
		t.Fatal(err)
	}
	if len(frozen.Images) != 1 || !strings.HasSuffix(resolver.references[0], "linux/arm64/") {
		t.Fatal("node platform was not resolved", resolver.references)
	}
	spec := frozen.Apply(compose.ExecutionSpec{})
	if spec.ImageOverrides["app"] != "docker.io/example/app@sha256:"+strings.Repeat("a", 64) || spec.PlatformOverrides["app"] != "linux/arm64" {
		t.Fatal("image was not pinned", spec.ImageOverrides)
	}
	view, err := NodeChanges(compose.Target{NodeID: "arm"}, frozen, nil, environment, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(view)
	if err != nil || strings.Contains(string(raw), "private-value") {
		t.Fatal("preview exposed environment", err)
	}
}
