package config

import (
	"path/filepath"
	"testing"
	"time"
)

const dataRootKey = "SUMA_DATA_ROOT"

var configEnvKeys = []string{
	"SUMA_ADDRESS", "SUMA_DATABASE", "SUMA_DOCKER_HOST", "SUMA_COMPOSE_ROOT",
	"SUMA_BACKUP_ROOT", "SUMA_COMPOSE_COMMAND", "SUMA_GIT_COMMAND",
	"SUMA_GIT_ROOT", "SUMA_SECRET_KEY_FILE", "SUMA_COOKIE_SECURE", dataRootKey,
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range configEnvKeys {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearConfigEnv(t)
	cfg := Load()
	dataRoot := "./data"
	rows := []struct {
		name, got, want string
	}{
		{"address", cfg.Address, ":8080"},
		{"database path", cfg.DatabasePath, filepath.Join(dataRoot, "suma.db")},
		{"docker host", cfg.DockerHost, "unix:///var/run/docker.sock"},
		{"compose root", cfg.ComposeRoot, filepath.Join(dataRoot, "compose")},
		{"backup root", cfg.BackupRoot, filepath.Join(dataRoot, "backups")},
		{"compose command", cfg.ComposeCommand, "docker compose"},
		{"git command", cfg.GitCommand, "git"},
		{"git root", cfg.GitRoot, filepath.Join(dataRoot, "gitops")},
		{"secret key file", cfg.SecretKeyFile, filepath.Join(dataRoot, "secret.key")},
	}
	for _, row := range rows {
		if row.got != row.want {
			t.Errorf("%s = %q, want %q", row.name, row.got, row.want)
		}
	}
	if cfg.CookieSecure {
		t.Error("SUMA_COOKIE_SECURE default must be false")
	}
	if cfg.SessionMaxAge != 24*time.Hour {
		t.Errorf("session max age = %v, want 24h", cfg.SessionMaxAge)
	}
}

func TestLoadEnvironmentOverrides(t *testing.T) {
	rows := []struct {
		name, key, value string
		pick             func(Config) string
		want             string
	}{
		{"listen address", "SUMA_ADDRESS", "127.0.0.1:9099", func(c Config) string { return c.Address }, "127.0.0.1:9099"},
		{"database override ignores data root", "SUMA_DATABASE", "/tmp/other.sqlite", func(c Config) string { return c.DatabasePath }, "/tmp/other.sqlite"},
		{"docker host", "SUMA_DOCKER_HOST", "tcp://10.0.0.5:2376", func(c Config) string { return c.DockerHost }, "tcp://10.0.0.5:2376"},
		{"compose root", "SUMA_COMPOSE_ROOT", "/srv/compose", func(c Config) string { return c.ComposeRoot }, "/srv/compose"},
		{"backup root", "SUMA_BACKUP_ROOT", "/srv/backups", func(c Config) string { return c.BackupRoot }, "/srv/backups"},
		{"compose command", "SUMA_COMPOSE_COMMAND", "docker compose --project-directory /x", func(c Config) string { return c.ComposeCommand }, "docker compose --project-directory /x"},
		{"git command", "SUMA_GIT_COMMAND", "git --no-pager", func(c Config) string { return c.GitCommand }, "git --no-pager"},
		{"git root", "SUMA_GIT_ROOT", "/srv/gitops", func(c Config) string { return c.GitRoot }, "/srv/gitops"},
		{"secret key file", "SUMA_SECRET_KEY_FILE", "/etc/suma/secret.key", func(c Config) string { return c.SecretKeyFile }, "/etc/suma/secret.key"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv(row.key, row.value)
			cfg := Load()
			if got := row.pick(cfg); got != row.want {
				t.Fatalf("%s = %q, want %q", row.key, got, row.want)
			}
		})
	}
}

func TestLoadCookieSecure(t *testing.T) {
	rows := []struct {
		name, value string
		want        bool
	}{
		{"unset defaults to false", "", false},
		{"true enables it", "true", true},
		{"false keeps plaintext cookies", "false", false},
		{"truthy lookalike is strict", "yes", false},
		{"numeric one is rejected", "1", false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("SUMA_COOKIE_SECURE", row.value)
			cfg := Load()
			if cfg.CookieSecure != row.want {
				t.Fatalf("CookieSecure = %v, want %v (input %q)", cfg.CookieSecure, row.want, row.value)
			}
		})
	}
}

func TestLoadDataRootDerivedPaths(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv(dataRootKey, "/opt/suma-data")
	cfg := Load()
	root := "/opt/suma-data"
	rows := []struct {
		name, got, want string
	}{
		{"database", cfg.DatabasePath, filepath.Join(root, "suma.db")},
		{"compose root", cfg.ComposeRoot, filepath.Join(root, "compose")},
		{"backup root", cfg.BackupRoot, filepath.Join(root, "backups")},
		{"git root", cfg.GitRoot, filepath.Join(root, "gitops")},
		{"secret key file", cfg.SecretKeyFile, filepath.Join(root, "secret.key")},
	}
	for _, row := range rows {
		if row.got != row.want {
			t.Errorf("derived %s from SUMA_DATA_ROOT = %q, want %q", row.name, row.got, row.want)
		}
	}
}
