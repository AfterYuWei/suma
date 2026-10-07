package config

import (
	"path/filepath"
	"time"
)

type Config struct {
	Address        string
	DatabaseDSN    string
	DataRoot       string
	DockerHost     string
	ComposeRoot    string
	BackupRoot     string
	ComposeCommand string
	GitCommand     string
	GitRoot        string
	SecretKeyFile  string
	CookieSecure   bool
	BrowserOrigin  string
	TrustedProxies string
	SessionMaxAge  time.Duration
	AgentPublicURL string
}

func Load() (Config, error) {
	values, err := ReadEnvironment()
	if err != nil {
		return Config{}, err
	}
	env := values.Get
	dataRoot := env("SUMA_DATA_ROOT", "./data")
	return Config{
		Address:        env("SUMA_ADDRESS", ":8080"),
		DatabaseDSN:    env("SUMA_DATABASE_DSN", ""),
		DataRoot:       dataRoot,
		DockerHost:     env("SUMA_DOCKER_HOST", "unix:///var/run/docker.sock"),
		ComposeRoot:    env("SUMA_COMPOSE_ROOT", filepath.Join(dataRoot, "compose")),
		BackupRoot:     env("SUMA_BACKUP_ROOT", filepath.Join(dataRoot, "backups")),
		ComposeCommand: env("SUMA_COMPOSE_COMMAND", "docker compose"),
		GitCommand:     env("SUMA_GIT_COMMAND", "git"),
		GitRoot:        env("SUMA_GIT_ROOT", filepath.Join(dataRoot, "gitops")),
		SecretKeyFile:  env("SUMA_SECRET_KEY_FILE", filepath.Join(dataRoot, "secret.key")),
		CookieSecure:   env("SUMA_COOKIE_SECURE", "false") == "true",
		BrowserOrigin:  env("SUMA_BROWSER_ORIGIN", ""),
		TrustedProxies: env("SUMA_TRUSTED_PROXIES", ""),
		SessionMaxAge:  24 * time.Hour,
		AgentPublicURL: env("SUMA_AGENT_PUBLIC_URL", ""),
	}, nil
}
