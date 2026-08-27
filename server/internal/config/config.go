package config

import (
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	Address        string
	DatabasePath   string
	DockerHost     string
	ComposeRoot    string
	BackupRoot     string
	ComposeCommand string
	GitCommand     string
	GitRoot        string
	SecretKeyFile  string
	CookieSecure   bool
	SessionMaxAge  time.Duration
}

func Load() Config {
	dataRoot := env("SUMA_DATA_ROOT", "./data")
	return Config{
		Address:        env("SUMA_ADDRESS", ":8080"),
		DatabasePath:   env("SUMA_DATABASE", filepath.Join(dataRoot, "suma.db")),
		DockerHost:     env("SUMA_DOCKER_HOST", "unix:///var/run/docker.sock"),
		ComposeRoot:    env("SUMA_COMPOSE_ROOT", filepath.Join(dataRoot, "compose")),
		BackupRoot:     env("SUMA_BACKUP_ROOT", filepath.Join(dataRoot, "backups")),
		ComposeCommand: env("SUMA_COMPOSE_COMMAND", "docker compose"),
		GitCommand:     env("SUMA_GIT_COMMAND", "git"),
		GitRoot:        env("SUMA_GIT_ROOT", filepath.Join(dataRoot, "gitops")),
		SecretKeyFile:  env("SUMA_SECRET_KEY_FILE", filepath.Join(dataRoot, "secret.key")),
		CookieSecure:   env("SUMA_COOKIE_SECURE", "false") == "true",
		SessionMaxAge:  24 * time.Hour,
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
