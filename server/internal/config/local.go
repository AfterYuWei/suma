package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// Environment reads local configuration without changing process environment or
// evaluating shell commands. Deployment environment values take precedence.
type Environment map[string]string

func ReadEnvironment() (Environment, error) {
	path := os.Getenv("SUMA_ENV_FILE")
	if path == "-" {
		return Environment{}, nil
	}
	explicit := path != ""
	if !explicit {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, errors.New("locate local configuration failed")
		}
		path = localEnvironmentPath(cwd)
	}
	values, err := godotenv.Read(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return Environment{}, nil
	}
	if err != nil {
		// Parser errors may contain secret values; never return the original error.
		return nil, errors.New("read local configuration failed; check .env.local or SUMA_ENV_FILE")
	}
	return Environment(values), nil
}

func (values Environment) Get(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	if value := values[key]; value != "" {
		return value
	}
	return fallback
}

func localEnvironmentPath(cwd string) string {
	for directory := cwd; ; directory = filepath.Dir(directory) {
		// Native Go commands and tests run from different directories in the repo.
		if info, err := os.Stat(filepath.Join(directory, "server", "go.mod")); err == nil && !info.IsDir() {
			return filepath.Join(directory, ".env.local")
		}
		if filepath.Dir(directory) == directory {
			return filepath.Join(cwd, ".env.local")
		}
	}
}
