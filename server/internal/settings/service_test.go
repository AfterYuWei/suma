package settings

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/config"
	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
)

func testConfig() config.Config {
	return config.Config{
		Address:        ":8080",
		DatabasePath:   "/var/lib/suma/suma.db",
		DockerHost:     "unix:///var/run/docker.sock",
		ComposeRoot:    "/srv/compose",
		BackupRoot:     "/srv/backups",
		ComposeCommand: "docker compose --profile prod",
		CookieSecure:   true,
	}
}

func openDatabase(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func requireSetting(t *testing.T, values map[string]string, key, want string) {
	t.Helper()
	got, ok := values[key]
	if !ok {
		t.Fatalf("setting %q missing from result", key)
	}
	if got != want {
		t.Fatalf("setting %q = %q, want %q", key, got, want)
	}
}

func TestGetMergesDefaultsWithConfig(t *testing.T) {
	db := openDatabase(t, filepath.Join(t.TempDir(), "settings.db"))
	service := NewService(db, testConfig())
	values, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct{ key, want string }{
		{"general.server_name", "SUMA"},
		{"general.language", "en"},
		{"general.timezone", "UTC"},
		{"docker.compose_command", "docker compose --profile prod"},
		{"storage.compose_root", "/srv/compose"},
		{"storage.data_root", "/var/lib/suma"},
		{"storage.backup_root", "/srv/backups"},
		{"security.cookie_secure", "true"},
		{"appearance.theme", "system"},
		{"registry.default", ""},
	}
	if len(values) != len(rows) {
		t.Fatalf("expected %d settings, got %d: %#v", len(rows), len(values), values)
	}
	for _, row := range rows {
		requireSetting(t, values, row.key, row.want)
	}
}

func TestDefaultsFromZeroConfig(t *testing.T) {
	db := openDatabase(t, filepath.Join(t.TempDir(), "settings.db"))
	service := NewService(db, config.Config{})
	values, err := service.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct{ key, want string }{
		{"docker.compose_command", ""},
		{"storage.compose_root", ""},
		{"storage.data_root", ""},
		{"storage.backup_root", ""},
		{"security.cookie_secure", "false"},
	}
	for _, row := range rows {
		requireSetting(t, values, row.key, row.want)
	}
}

func TestUpdatePersistsUpsertsAndReturnsMerged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.db")
	ctx := context.Background()
	db := openDatabase(t, path)
	service := NewService(db, testConfig())

	first, err := service.Update(ctx, map[string]string{"general.server_name": "Ops Hub"})
	if err != nil {
		t.Fatal(err)
	}
	requireSetting(t, first, "general.server_name", "Ops Hub")
	// Untouched keys keep their defaults in the returned map.
	requireSetting(t, first, "general.language", "en")

	second, err := service.Update(ctx, map[string]string{
		"general.server_name": "Renamed",
		"general.timezone":    "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("upsert over existing key must not conflict: %v", err)
	}
	requireSetting(t, second, "general.server_name", "Renamed")
	requireSetting(t, second, "general.timezone", "Asia/Shanghai")

	reopened := NewService(openDatabase(t, path), testConfig())
	stored, err := reopened.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	requireSetting(t, stored, "general.server_name", "Renamed")
	requireSetting(t, stored, "general.timezone", "Asia/Shanghai")

	var count int64
	if err := db.Model(&database.Setting{}).Count(&count).Error; err == nil && count > 2 {
		t.Fatalf("expected at most 2 stored rows after upserts, got %d", count)
	}
}

func TestUpdateRejectsUnsupportedKeysWithoutStoringThem(t *testing.T) {
	db := openDatabase(t, filepath.Join(t.TempDir(), "settings.db"))
	ctx := context.Background()
	service := NewService(db, testConfig())
	cases := []struct {
		name string
		key  string
	}{
		{"unknown namespace", "networking.proxy"},
		{"injection attempt", "general.server_name'; DROP TABLE settings; --"},
		{"empty key", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			before, err := service.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Update(ctx, map[string]string{testCase.key: "x"})
			if err == nil || !strings.Contains(err.Error(), "unsupported setting") {
				t.Fatalf("expected unsupported setting error, got %v", err)
			}
			after, err := service.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := after[testCase.key]; exists {
				t.Fatalf("rejected key %q must not appear in settings", testCase.key)
			}
			for key, value := range before {
				if after[key] != value {
					t.Fatalf("setting %q changed from %q to %q during failed update", key, value, after[key])
				}
			}
		})
	}
}

func TestUpdateAfterRestartUsesRecomputedDataRootDefault(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "settings.db")
	ctx := context.Background()

	cfg := config.Config{}
	cfg.DatabasePath = "/var/lib/suma/suma.db"
	first := NewService(openDatabase(t, path), cfg)
	if _, err := first.Update(ctx, map[string]string{"storage.compose_root": "/custom/compose"}); err != nil {
		t.Fatal(err)
	}

	cfg2 := config.Config{}
	cfg2.DatabasePath = "/mnt/newdata/suma.db"
	reopened := NewService(openDatabase(t, path), cfg2)
	values, err := reopened.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Stored override survives while config-derived defaults follow the new deployment.
	requireSetting(t, values, "storage.compose_root", "/custom/compose")
	requireSetting(t, values, "storage.data_root", "/mnt/newdata")
}
