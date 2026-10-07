package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalConfigurationFromNestedGoDirectory(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("SUMA_ENV_FILE", "")
	t.Setenv("SUMA_TEST_DATABASE_DSN", "")
	root := t.TempDir()
	nested := filepath.Join(root, "server", "internal", "testutil")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "server", "go.mod"), []byte("module test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dsn := "postgres://local:quoted$literal@127.0.0.1:5432/suma?sslmode=disable"
	content := "SUMA_DATABASE_DSN='" + dsn + "'\nSUMA_TEST_DATABASE_DSN=${SUMA_DATABASE_DSN}\nSUMA_DATA_ROOT=/local/application-files\n"
	if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	cfg := loadTestConfig(t)
	if cfg.DatabaseDSN != dsn || cfg.ComposeRoot != "/local/application-files/compose" || cfg.SecretKeyFile != "/local/application-files/secret.key" {
		t.Fatal("nested native command did not read the root local configuration")
	}
	values, err := ReadEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if values.Get("SUMA_TEST_DATABASE_DSN", "") != dsn {
		t.Fatal("test DSN did not resolve the configured local database reference")
	}
	if os.Getenv("SUMA_DATABASE_DSN") != "" || os.Getenv("SUMA_TEST_DATABASE_DSN") != "" {
		t.Fatal("local configuration changed process environment")
	}
	deploymentDSN := "postgres://deployment:private@postgres:5432/suma?sslmode=disable"
	t.Setenv("SUMA_DATABASE_DSN", deploymentDSN)
	if loadTestConfig(t).DatabaseDSN != deploymentDSN {
		t.Fatal("deployment environment must override local configuration")
	}
}

func TestLocalConfigurationNeverExecutesShellText(t *testing.T) {
	clearConfigEnv(t)
	root := t.TempDir()
	marker := filepath.Join(root, "must-not-exist")
	path := filepath.Join(root, "local.env")
	value := "$(touch " + marker + ") `touch " + marker + "`"
	if err := os.WriteFile(path, []byte("SUMA_DATABASE_DSN='"+value+"'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUMA_ENV_FILE", path)
	if loadTestConfig(t).DatabaseDSN != value {
		t.Fatal("shell text must remain literal")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("local configuration evaluated shell commands")
	}
}

func TestLocalConfigurationErrorsNeverExposeValues(t *testing.T) {
	clearConfigEnv(t)
	path := filepath.Join(t.TempDir(), "local.env")
	secret := "private-database-password"
	if err := os.WriteFile(path, []byte("INVALID!"+secret+"=value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUMA_ENV_FILE", path)
	if _, err := Load(); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("malformed local configuration must fail without exposing contents")
	}
	t.Setenv("SUMA_ENV_FILE", filepath.Join(t.TempDir(), "missing.env"))
	if _, err := Load(); err == nil {
		t.Fatal("explicit missing configuration must fail")
	}
}

func TestLocalConfigurationOptionalAndDisabled(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("SUMA_ENV_FILE", "")
	t.Chdir(t.TempDir())
	if cfg := loadTestConfig(t); cfg.DatabaseDSN != "" {
		t.Fatal("missing optional local configuration should preserve deployment defaults")
	}
	if err := os.WriteFile(".env.local", []byte("INVALID!private=value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUMA_ENV_FILE", "-")
	t.Setenv("SUMA_DATABASE_DSN", "postgres://deployment/suma")
	if cfg := loadTestConfig(t); cfg.DatabaseDSN != "postgres://deployment/suma" {
		t.Fatal("disabled local configuration must use only deployment environment")
	}
}
