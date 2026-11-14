package deployment

import (
	"encoding/json"
	"github.com/suma/suma/server/internal/secret"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptedEnvironmentLiteralAndPreserveSemantics(t *testing.T) {
	store, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	value := "${UNCHANGED}\\n=private"
	encrypted, err := ConfigureEnvironment(store, nil, []EnvironmentVariable{{Name: "TOKEN", Value: &value}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encrypted), "private") {
		t.Fatal("stored plaintext")
	}
	summary, err := EnvironmentSummary(store, encrypted)
	if err != nil || len(summary) != 1 || summary[0].Value != nil {
		t.Fatal("summary revealed values", err)
	}
	preserved, err := ConfigureEnvironment(store, encrypted, summary)
	if err != nil {
		t.Fatal(err)
	}
	values, err := DecryptEnvironment(store, preserved)
	if err != nil || values["TOKEN"] != value {
		t.Fatal("literal changed", err)
	}
	empty := ""
	cleared, err := ConfigureEnvironment(store, preserved, []EnvironmentVariable{{Name: "TOKEN", Value: &empty}})
	if err != nil {
		t.Fatal(err)
	}
	values, err = DecryptEnvironment(store, cleared)
	if err != nil || values["TOKEN"] != "" {
		t.Fatal("empty was not saved", err)
	}
	if _, err := ConfigureEnvironment(store, nil, []EnvironmentVariable{{Name: "DOCKER_HOST", Value: &value}}); err == nil {
		t.Fatal("process variable accepted")
	}
}

func TestPreviewRedactionPreservesJSONStructureWithShortSecrets(t *testing.T) {
	raw := `{"services":{"app":{"image":"image:1","scale":1,"environment":{"API_TOKEN":"a","MULTILINE":"private\nvalue"}}}}`
	redacted, err := RedactConfiguration(raw, map[string]string{"API_TOKEN": "a", "SCALE": "1", "MULTILINE": "private\nvalue"})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Services map[string]struct {
			Scale       int
			Environment map[string]string
		}
	}
	if err := json.Unmarshal([]byte(redacted), &document); err != nil {
		t.Fatal(err)
	}
	if document.Services["app"].Scale != 1 || document.Services["app"].Environment["API_TOKEN"] != "[redacted]" || strings.Contains(redacted, "private") {
		t.Fatal("display redaction corrupted metadata or leaked a value")
	}
}

func TestPreviewRedactsRepositorySecretsWithoutManualOverrides(t *testing.T) {
	raw := `{"services":{"app":{"image":"example/app:latest","environment":{"PASSWORD":"repository-private-value","APP_ENV":"production"},"command":["run","repository-private-value"]}}}`
	redacted, err := RedactConfiguration(raw, nil)
	if err != nil || strings.Contains(redacted, "repository-private-value") || !strings.Contains(redacted, "production") {
		t.Fatal("repository secret escaped preview redaction", err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(redacted), &document); err != nil {
		t.Fatal("redaction damaged configuration structure", err)
	}
}
