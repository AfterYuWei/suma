package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrollmentTokenEnvironmentAndFileFallback(t *testing.T) {
	t.Setenv("SUMA_AGENT_TOKEN", "")
	t.Setenv("SUMA_AGENT_TOKEN_FILE", "")
	if _, err := enrollmentToken(); err == nil || !strings.Contains(err.Error(), "SUMA_AGENT_TOKEN") {
		t.Fatalf("missing token source: %v", err)
	}
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("legacy-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUMA_AGENT_TOKEN_FILE", path)
	if token, err := enrollmentToken(); err != nil || token != "legacy-token" {
		t.Fatalf("file token = %q, %v", token, err)
	}
	t.Setenv("SUMA_AGENT_TOKEN", " environment-token \n")
	if token, err := enrollmentToken(); err != nil || token != "environment-token" {
		t.Fatalf("environment token = %q, %v", token, err)
	}
	t.Setenv("SUMA_AGENT_TOKEN_FILE", "")
	if token, err := enrollmentToken(); err != nil || token != "environment-token" {
		t.Fatalf("environment-only token = %q, %v", token, err)
	}
}
