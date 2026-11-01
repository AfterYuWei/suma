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
	values, err := h.service.GetEnvironmentValues(ctx, h.project.Name)
	if err != nil || len(values) != 2 || values["PASSWORD"] != "manual-test-private-value" || values["EMPTY"] != "" {
		t.Fatal("explicit read lost saved or empty values")
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
	values, err = h.service.GetEnvironmentValues(ctx, h.project.Name)
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

func TestDeliveryEnvironmentRecordSnapshotsSurviveChangesAndRollback(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	ctx := context.Background()
	configuration, _ := h.service.GetConfiguration(ctx, h.project.Name)
	input := ConfigureInput{Repository: configuration.Repository, ReconcileMode: ModeManual, SyncIntervalSeconds: 300, DeploymentTimeout: 60, Environment: []EnvironmentVariable{{Name: "APP_VERSION", Value: environmentValue("version-one")}}}
	if _, err := h.service.Configure(ctx, h.project.Name, input); err != nil {
		t.Fatal(err)
	}
	first := syncAndDeploy(t, h)
	firstRecord := *first.LatestRecordID
	input.Environment[0].Value = environmentValue("version-two")
	if _, err := h.service.Configure(ctx, h.project.Name, input); err != nil {
		t.Fatal(err)
	}
	// Publishing needs no extra Git sync and uses the current saved settings.
	operation, err := h.service.Deploy(ctx, h.project.Name, first.ID, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, operation.ID, "success")
	second, err := h.service.GetRelease(ctx, h.project.Name, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || firstRecord == *second.LatestRecordID {
		t.Fatal("Git identity or operation identity was lost")
	}
	for id, want := range map[uint]string{firstRecord: "version-one", *second.LatestRecordID: "version-two"} {
		record, err := h.service.GetRecord(ctx, h.project.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := h.service.releaseSpec("fixture", recordRelease(first, record))
		if err != nil || spec.Environment["APP_VERSION"] != want {
			t.Fatal("snapshot lost environment", err)
		}
		raw, _ := json.Marshal(record)
		if strings.Contains(string(raw), want) || strings.Contains(string(raw), "environment_ciphertext") {
			t.Fatal("record exposed environment")
		}
	}
	rollback, err := h.service.Rollback(ctx, h.project.Name, first.ID, firstRecord, nil, Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, rollback.ID, "success")
	specs := h.runner.UpSpecs()
	if specs[len(specs)-1].Environment["APP_VERSION"] != "version-one" {
		t.Fatal("rollback lost selected environment")
	}
	saved, err := h.service.GetEnvironmentValues(ctx, h.project.Name)
	if err != nil || saved["APP_VERSION"] != "version-two" {
		t.Fatal("rollback mutated settings")
	}
	releases, err := h.service.ListReleases(ctx, h.project.Name)
	if err != nil || len(releases) != 1 {
		t.Fatal("operation created another release", err)
	}
}
