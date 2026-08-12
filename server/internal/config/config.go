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
	CookieSecure   bool
	SessionMaxAge  time.Duration
}

func Load() Config {
	dataRoot := env("DOCKPORT_DATA_ROOT", "./data")
	return Config{
		Address:        env("DOCKPORT_ADDRESS", ":8080"),
		DatabasePath:   env("DOCKPORT_DATABASE", filepath.Join(dataRoot, "dockport.db")),
		DockerHost:     env("DOCKPORT_DOCKER_HOST", "unix:///var/run/docker.sock"),
		ComposeRoot:    env("DOCKPORT_COMPOSE_ROOT", filepath.Join(dataRoot, "compose")),
		BackupRoot:     env("DOCKPORT_BACKUP_ROOT", filepath.Join(dataRoot, "backups")),
		ComposeCommand: env("DOCKPORT_COMPOSE_COMMAND", "docker compose"),
		CookieSecure:   env("DOCKPORT_COOKIE_SECURE", "false") == "true",
		SessionMaxAge:  24 * time.Hour,
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
