package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/database"
)

func TestModelSelectionPersistenceCompatibilityAndCapabilities(t *testing.T) {
	s, _, _ := aiFixture(t)
	cfg := s.Settings()
	if !reflect.DeepEqual(cfg.Models, []string{"test"}) || !cfg.ToolCapable {
		t.Fatal("single-model configuration was not retained")
	}
	cfg.Models = []string{" test ", "other", "test"}
	saved, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1})
	if err != nil || !reflect.DeepEqual(saved.Models, []string{"test", "other"}) || !saved.ToolCapable {
		t.Fatal("adding alternatives changed default capability", saved, err)
	}
	saved.Models[0] = "tampered"
	if s.Settings().Models[0] != "test" {
		t.Fatal("returned list changed active settings")
	}
	cfg = s.Settings()
	cfg.Model = "other"
	saved, err = s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1})
	if err != nil || saved.ToolCapable {
		t.Fatal("switching default must invalidate tool verification", err)
	}
	var row database.Setting
	if err = s.db.First(&row, "key = ?", "internal.ai.settings").Error; err != nil {
		t.Fatal(err)
	}
	var stored Settings
	if json.Unmarshal([]byte(row.Value), &stored) != nil || !reflect.DeepEqual(stored.Models, saved.Models) || stored.Model != "other" {
		t.Fatal("multiple models were not persisted")
	}
	for _, mutate := range []func(*Settings){
		func(c *Settings) { c.Protocol = "chat_completions" },
		func(c *Settings) { c.Protocol = "unknown" },
		func(c *Settings) { c.Protocol = "" },
		func(c *Settings) { c.Model = "unselected" },
		func(c *Settings) { c.Models = []string{"bad\nmodel"} },
		func(c *Settings) { c.Models = []string{strings.Repeat("x", 257)} },
		func(c *Settings) { c.Models = make([]string, 201) },
		func(c *Settings) { c.Endpoint = "https://example.com/v1/chat/completions" },
		func(c *Settings) { c.Endpoint = "https://example.com/v1?key=secret" },
		func(c *Settings) { c.Endpoint = "http://example.com/v1" },
	} {
		cfg = s.Settings()
		mutate(&cfg)
		if _, err = s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid settings accepted", err)
		}
	}
}

func TestDiscoverModelsUsesDraftAndWriteOnlySavedKey(t *testing.T) {
	s, _, _ := aiFixture(t)
	var path, authorization string
	s.deps.Model = HTTPModel{Client: func(bool) *http.Client {
		return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			path, authorization = req.URL.Path, req.Header.Get("Authorization")
			if req.Method != http.MethodGet || req.Header.Get("Origin") != "" {
				t.Fatal("discovery must be a server-side GET")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"z-model"},{"id":"a-model"},{"id":"z-model"},{"id":""}]}`))}, nil
		})}
	}}
	before := s.Settings()
	in := ModelsInput{Version: before.Version, Endpoint: "https://gateway.example.com/custom/v1/"}
	models, err := s.DiscoverModels(context.Background(), in)
	if err != nil || !reflect.DeepEqual(models, []string{"a-model", "z-model"}) || path != "/custom/v1/models" || authorization != "Bearer model-secret" {
		t.Fatal(models, path, authorization, err)
	}
	in.Endpoint = "https://gateway.example.com"
	in.APIKey = "draft-secret"
	if _, err = s.DiscoverModels(context.Background(), in); err != nil || authorization != "Bearer draft-secret" || path != "/models" {
		t.Fatal("draft key/address ignored", err)
	}
	if !reflect.DeepEqual(before, s.Settings()) || s.key != "model-secret" {
		t.Fatal("discovery saved settings or key")
	}
	in.Version++
	if _, err = s.DiscoverModels(context.Background(), in); !errors.Is(err, ErrConflict) {
		t.Fatal("stale key configuration accepted", err)
	}
}

func TestDiscoverModelsRejectsUnsafeAndMalformedResponsesWithoutLeakingSecrets(t *testing.T) {
	for _, response := range []struct {
		status int
		body   string
	}{
		{403, "Request origin is not allowed; key=PRIVATE-MODEL-SECRET"},
		{404, "PRIVATE-MODEL-SECRET"},
		{200, `{"models":["unsupported-shape"]}`},
		{200, `not-json PRIVATE-MODEL-SECRET`},
		{200, strings.Repeat("x", (1<<20)+1)},
	} {
		m := HTTPModel{Client: func(bool) *http.Client {
			return &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: response.status, Body: io.NopCloser(strings.NewReader(response.body))}, nil
			})}
		}}
		if _, err := m.ListModels(context.Background(), Settings{Endpoint: "https://example.com/v1"}, "PRIVATE-MODEL-SECRET"); err == nil || strings.Contains(err.Error(), "PRIVATE-MODEL-SECRET") {
			t.Fatal("response accepted or secret exposed", err)
		}
	}
	m := HTTPModel{Client: func(bool) *http.Client { t.Fatal("unsafe endpoint reached adapter"); return nil }}
	if _, err := m.ListModels(context.Background(), Settings{Endpoint: "http://example.com/v1", AllowPrivate: true}, "key"); !errors.Is(err, ErrInvalid) {
		t.Fatal("private permission implicitly allowed HTTP", err)
	}
}

func TestLegacyStoredModelAndPrivateHTTPAreMigrated(t *testing.T) {
	s, _, _ := aiFixture(t)
	cfg := s.Settings()
	cfg.Endpoint = "http://127.0.0.1:11434/v1"
	cfg.AllowPrivate = true
	var legacy map[string]any
	if json.Unmarshal([]byte(marshal(cfg)), &legacy) != nil {
		t.Fatal("encode failed")
	}
	delete(legacy, "models")
	delete(legacy, "allow_insecure")
	if err := s.db.Model(&database.Setting{}).Where("key = ?", "internal.ai.settings").Update("value", marshal(legacy)).Error; err != nil {
		t.Fatal(err)
	}
	s.Stop()
	restarted, err := NewService(s.db, s.secrets, s.tasks, Dependencies{Model: &scriptedModel{}})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	loaded := restarted.Settings()
	if !reflect.DeepEqual(loaded.Models, []string{"test"}) || !loaded.AllowInsecure || !loaded.AllowPrivate || !loaded.HasSecret || !loaded.Enabled || !loaded.ToolCapable || loaded.Protocol != ProtocolResponses || loaded.Version != cfg.Version {
		t.Fatal("legacy configuration was lost", loaded)
	}
}

func TestRemovedProtocolMigrationDisablesAIAndInvalidatesPendingOperations(t *testing.T) {
	s, executions, _ := aiFixture(t)
	cfg := s.Settings()
	cfg.Protocol = "chat_completions"
	if err := s.db.Model(&database.Setting{}).Where("key = ?", "internal.ai.settings").Update("value", marshal(cfg)).Error; err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"awaiting_approval", "queued", "completed"} {
		if err := s.db.Create(&database.AIOperation{ID: "legacy-" + status, Status: status}).Error; err != nil {
			t.Fatal(err)
		}
	}
	s.Stop()
	restarted, err := NewService(s.db, s.secrets, s.tasks, Dependencies{Model: &scriptedModel{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Stop)
	loaded := restarted.Settings()
	if loaded.Protocol != ProtocolResponses || loaded.Enabled || loaded.ToolCapable || loaded.Version != cfg.Version+1 || !loaded.HasSecret || loaded.Endpoint != cfg.Endpoint || !reflect.DeepEqual(loaded.Models, cfg.Models) || restarted.key != "model-secret" {
		t.Fatal("removed protocol was not safely migrated", loaded)
	}
	var stored database.Setting
	if err := s.db.First(&stored, "key = ?", "internal.ai.settings").Error; err != nil {
		t.Fatal(err)
	}
	var persisted Settings
	if json.Unmarshal([]byte(stored.Value), &persisted) != nil || persisted.Protocol != ProtocolResponses || persisted.Enabled || persisted.ToolCapable || persisted.Version != loaded.Version {
		t.Fatal("migration was not persisted")
	}
	for _, status := range []string{"awaiting_approval", "queued", "completed"} {
		var operation database.AIOperation
		if err := s.db.First(&operation, "id = ?", "legacy-"+status).Error; err != nil {
			t.Fatal(err)
		}
		if status != "completed" && operation.Status != "invalidated" || status == "completed" && operation.Status != "completed" {
			t.Fatal("operation state was not preserved", operation)
		}
	}
	if _, err = restarted.Start(context.Background(), RunInput{NodeID: "local", Question: "diagnose"}, Actor{UserID: 1, Source: "site"}); !errors.Is(err, ErrDisabled) {
		t.Fatal("migrated AI remained active", err)
	}
	restarted.Stop()
	again, err := NewService(s.db, s.secrets, s.tasks, Dependencies{Model: &scriptedModel{}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Stop()
	if again.Settings().Version != loaded.Version || executions.Load() != 0 {
		t.Fatal("migration repeated or executed operations")
	}
}

func TestRemovedProtocolMigrationRollsBackOnInvalidationFailure(t *testing.T) {
	s, _, _ := aiFixture(t)
	cfg := s.Settings()
	cfg.Protocol = "chat_completions"
	if err := s.db.Model(&database.Setting{}).Where("key = ?", "internal.ai.settings").Update("value", marshal(cfg)).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Create(&database.AIOperation{ID: "pending-migration", Status: "awaiting_approval"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Exec("CREATE TRIGGER reject_protocol_migration BEFORE UPDATE ON ai_operations WHEN NEW.status = 'invalidated' BEGIN SELECT RAISE(ABORT, 'invalidation failed'); END").Error; err != nil {
		t.Fatal(err)
	}
	s.Stop()
	if restarted, err := NewService(s.db, s.secrets, s.tasks, Dependencies{Model: &scriptedModel{}}); err == nil {
		restarted.Stop()
		t.Fatal("failed migration allowed startup")
	}
	var stored database.Setting
	if err := s.db.First(&stored, "key = ?", "internal.ai.settings").Error; err != nil {
		t.Fatal(err)
	}
	var persisted Settings
	if json.Unmarshal([]byte(stored.Value), &persisted) != nil || persisted.Protocol != "chat_completions" || persisted.Version != cfg.Version || !persisted.Enabled {
		t.Fatal("failed migration changed settings")
	}
}
