package cd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/database"
)

func environmentValue(value string) *string { return &value }

func TestDeliveryEnvironmentEncryptedConfigurationAndCompatibility(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	configuration, err := h.service.GetConfiguration(ctx, h.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	input := ConfigureInput{Repository: configuration.Repository, ReconcileMode: ModeManual, SyncIntervalSeconds: 300, DeploymentTimeout: 60,
		Environment: []EnvironmentVariable{{Name: "PASSWORD", Value: environmentValue("manual-test-private-value")}, {Name: "EMPTY", Value: environmentValue("")}}}
	saved, err := h.service.Configure(ctx, h.project.Name, input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(saved)
	if strings.Contains(string(raw), "manual-test-private-value") || len(saved.Environment) != 2 || saved.Environment[0].Value != nil || !saved.Environment[0].HasValue {
		t.Fatal("configuration exposed values or lost names")
	}
	var stored database.DeliveryProject
	if err := h.db.First(&stored, h.project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored.EnvironmentCiphertext) == 0 || strings.Contains(string(stored.EnvironmentCiphertext), "manual-test-private-value") {
		t.Fatal("values were not encrypted")
	}
	before := string(stored.EnvironmentCiphertext)
	for _, variables := range [][]EnvironmentVariable{nil, {{Name: "PASSWORD"}, {Name: "EMPTY"}}} {
		input.Environment = variables
		if _, err := h.service.Configure(ctx, h.project.Name, input); err != nil {
			t.Fatal(err)
		}
		h.db.First(&stored, h.project.ID)
		if string(stored.EnvironmentCiphertext) != before {
			t.Fatal("unchanged configuration rewrote environment")
		}
	}
	input.Environment = []EnvironmentVariable{{Name: "PASSWORD", Value: environmentValue("")}}
	if _, err := h.service.Configure(ctx, h.project.Name, input); err != nil {
		t.Fatal(err)
	}
	h.db.First(&stored, h.project.ID)
	values, err := h.service.decryptEnvironment(stored.EnvironmentCiphertext)
	if err != nil || len(values) != 1 || values["PASSWORD"] != "" {
		t.Fatal("explicit empty or removal was not saved")
	}
	input.Environment = []EnvironmentVariable{}
	if _, err := h.service.Configure(ctx, h.project.Name, input); err != nil {
		t.Fatal(err)
	}
	stored = database.DeliveryProject{}
	h.db.First(&stored, h.project.ID)
	if len(stored.EnvironmentCiphertext) != 0 {
		t.Fatal("clear did not remove stored values")
	}
}

func TestDeliveryEnvironmentValidationDoesNotChangeConfiguration(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	configuration, _ := h.service.GetConfiguration(context.Background(), h.project.Name)
	for _, variables := range [][]EnvironmentVariable{
		{{Name: "1BAD", Value: environmentValue("private")}},
		{{Name: "DUP", Value: environmentValue("one")}, {Name: "DUP", Value: environmentValue("two")}},
		{{Name: "DOCKER_HOST", Value: environmentValue("tcp://private")}},
		{{Name: "COMPOSE_FILE", Value: environmentValue("private")}},
		{{Name: "NEW"}},
		{{Name: "NULL", Value: environmentValue("private\x00value")}},
	} {
		_, err := h.service.Configure(context.Background(), h.project.Name, ConfigureInput{Repository: configuration.Repository, ReconcileMode: ModeManual, SyncIntervalSeconds: 300, DeploymentTimeout: 60, Environment: variables})
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("invalid input accepted or value exposed")
		}
	}
}

func TestDeliveryEnvironmentReleaseSnapshotsSurviveChangesAndRollback(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	configuration, _ := h.service.GetConfiguration(ctx, h.project.Name)
	input := ConfigureInput{Repository: configuration.Repository, ReconcileMode: ModeManual, SyncIntervalSeconds: 300, DeploymentTimeout: 60,
		Environment: []EnvironmentVariable{{Name: "APP_VERSION", Value: environmentValue("version-one")}}}
	if _, err := h.service.Configure(ctx, h.project.Name, input); err != nil {
		t.Fatal(err)
	}
	first := syncAndDeploy(t, h)
	input.Environment[0].Value = environmentValue("version-two")
	if _, err := h.service.Configure(ctx, h.project.Name, input); err != nil {
		t.Fatal(err)
	}
	second := syncAndDeploy(t, h) // Same commit and rendered Compose; manual values still create a new release.
	if first.ID == second.ID || first.ConfigHash == second.ConfigHash {
		t.Fatal("environment-only change reused a release")
	}
	for _, release := range []database.DeliveryRelease{first, second} {
		spec, err := h.service.releaseSpec("fixture", release)
		if err != nil {
			t.Fatal(err)
		}
		expected := "version-one"
		if release.ID == second.ID {
			expected = "version-two"
		}
		if spec.Environment["APP_VERSION"] != expected {
			t.Fatal("release did not retain its environment")
		}
		raw, _ := json.Marshal(release)
		if strings.Contains(string(raw), expected) || strings.Contains(string(raw), "environment_ciphertext") {
			t.Fatal("release exposed environment")
		}
	}
	rollbackTask, err := h.service.Rollback(ctx, h.project.Name, first.ID, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, rollbackTask.ID, "success")
	releases, err := h.service.ListReleases(ctx, h.project.Name)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := h.service.releaseSpec("fixture", releases[0])
	if err != nil || spec.Environment["APP_VERSION"] != "version-one" {
		t.Fatal("rollback lost original environment", err)
	}
}
