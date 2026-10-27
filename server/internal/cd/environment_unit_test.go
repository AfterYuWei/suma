package cd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
)

func TestManualEnvironmentConfigurationWithoutDatabase(t *testing.T) {
	store, err := secret.Open(filepath.Join(t.TempDir(), "test.key"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(nil, nil, nil, &fakeComposeRunner{}, nil, nil, store)
	input := []EnvironmentVariable{{Name: "PASSWORD", Value: environmentValue("test-private-value")}, {Name: "EMPTY", Value: environmentValue("")}}
	ciphertext, err := service.configureEnvironment(nil, input)
	if err != nil || len(ciphertext) == 0 || strings.Contains(string(ciphertext), "test-private-value") {
		t.Fatal("manual environment was not encrypted", err)
	}
	summary, err := service.environmentSummary(ciphertext)
	if err != nil || !reflect.DeepEqual(summary, []EnvironmentVariable{{Name: "EMPTY", HasValue: true}, {Name: "PASSWORD", HasValue: true}}) {
		t.Fatal("summary lost saved names", err)
	}
	raw, _ := json.Marshal(Configuration{Environment: summary})
	if strings.Contains(string(raw), "test-private-value") || strings.Contains(string(raw), `"value"`) {
		t.Fatal("configuration exposed a value")
	}
	for _, input := range [][]EnvironmentVariable{nil, {{Name: "PASSWORD"}, {Name: "EMPTY"}}} {
		unchanged, err := service.configureEnvironment(ciphertext, input)
		if err != nil || string(unchanged) != string(ciphertext) {
			t.Fatal("unchanged/legacy update did not preserve values", err)
		}
	}
	replaced, err := service.configureEnvironment(ciphertext, []EnvironmentVariable{{Name: "PASSWORD", Value: environmentValue("replacement")}})
	if err != nil {
		t.Fatal(err)
	}
	values, _ := service.decryptEnvironment(replaced)
	if len(values) != 1 || values["PASSWORD"] != "replacement" {
		t.Fatal("replace/remove failed")
	}
	empty, err := service.configureEnvironment(replaced, []EnvironmentVariable{{Name: "PASSWORD", Value: environmentValue("")}})
	if err != nil {
		t.Fatal(err)
	}
	values, _ = service.decryptEnvironment(empty)
	if len(values) != 1 || values["PASSWORD"] != "" {
		t.Fatal("explicit empty value failed")
	}
	cleared, err := service.configureEnvironment(empty, []EnvironmentVariable{})
	if err != nil || len(cleared) != 0 {
		t.Fatal("clear failed", err)
	}
	for _, invalid := range [][]EnvironmentVariable{
		{{Name: "NEW"}}, {{Name: "1BAD", Value: environmentValue("test-private-value")}},
		{{Name: "DUP", Value: environmentValue("")}, {Name: "DUP", Value: environmentValue("")}},
		{{Name: "DOCKER_HOST", Value: environmentValue("test-private-value")}},
		{{Name: "COMPOSE_FILE", Value: environmentValue("test-private-value")}},
		{{Name: "NULL", Value: environmentValue("test-private-value\x00")}},
		{{Name: "BIG", Value: environmentValue(strings.Repeat("x", 65537))}},
	} {
		if _, err := service.configureEnvironment(ciphertext, invalid); err == nil || strings.Contains(err.Error(), "test-private-value") {
			t.Fatal("invalid input accepted or leaked")
		}
	}
	if _, err := service.decryptEnvironment([]byte("corrupt")); err == nil {
		t.Fatal("corrupt snapshot was accepted")
	}
}

func TestManualEnvironmentReleaseSnapshotsWithoutDatabase(t *testing.T) {
	store, err := secret.Open(filepath.Join(t.TempDir(), "test.key"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(nil, nil, nil, &fakeComposeRunner{}, nil, nil, store)
	first, err := service.configureEnvironment(nil, []EnvironmentVariable{{Name: "VERSION", Value: environmentValue("first")}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.configureEnvironment(first, []EnvironmentVariable{{Name: "VERSION", Value: environmentValue("second")}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "compose.yml"), []byte("services: {app: {image: 'fixture:${VERSION}'}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	release := database.DeliveryRelease{WorktreePath: root, ComposeFilesJSON: `["compose.yml"]`, EnvironmentCiphertext: first}
	for _, test := range []struct {
		ciphertext []byte
		expected   string
	}{{first, "first"}, {second, "second"}, {first, "first"}, {nil, ""}} {
		release.EnvironmentCiphertext = test.ciphertext
		spec, err := service.releaseSpec("fixture", release)
		if err != nil || spec.Environment["VERSION"] != test.expected {
			t.Fatal("release/rollback snapshot changed", err)
		}
		raw, _ := json.Marshal(release)
		if strings.Contains(string(raw), "environment_ciphertext") {
			t.Fatal("release exposed private material")
		}
	}
}

func TestManualEnvironmentChangeCreatesDistinctConfiguration(t *testing.T) {
	rendered := `{"services":{"app":{"image":"fixture:stable"}}}`
	base := releaseConfigurationHash(rendered, nil)
	if base != releaseConfigurationHash(rendered, map[string]string{}) {
		t.Fatal("empty environment invalidated existing releases")
	}
	first := releaseConfigurationHash(rendered, map[string]string{"UNUSED": "first", "EMPTY": ""})
	if first != releaseConfigurationHash(rendered, map[string]string{"EMPTY": "", "UNUSED": "first"}) {
		t.Fatal("map order invalidated unchanged values")
	}
	if first == base || first == releaseConfigurationHash(rendered, map[string]string{"UNUSED": "second", "EMPTY": ""}) {
		t.Fatal("manual-only change did not create a distinct candidate")
	}
}
