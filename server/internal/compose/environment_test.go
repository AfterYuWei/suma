package compose

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManualEnvironmentUsesPrivateLiteralFileAndPreservesDotenv(t *testing.T) {
	dir := t.TempDir()
	implicit := filepath.Join(dir, ".env")
	if err := os.WriteFile(implicit, []byte("BASE=repository\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value := "quote\" backslash\\ dollar${BASE} # literal\nnext\tline\r"
	original := ExecutionSpec{ProjectName: "fixture", ProjectDir: dir, Environment: map[string]string{"EMPTY": "", "VALUE": value}}
	spec, cleanup, err := prepareEnvironment(original)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(original.EnvFiles) != 0 || len(spec.EnvFiles) != 2 || spec.EnvFiles[0] != implicit {
		t.Fatal("implicit environment or original spec changed")
	}
	private := spec.EnvFiles[1]
	info, err := os.Stat(private)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("environment file is not private", err)
	}
	raw, _ := os.ReadFile(private)
	if string(raw) != "EMPTY=\"\"\nVALUE=\"quote\\\" backslash\\\\ dollar$${BASE} # literal\\nnext\\tline\\r\"\n" {
		t.Fatal("literal values were not escaped correctly")
	}
	cleanup()
	if _, err := os.Stat(private); !os.IsNotExist(err) {
		t.Fatal("private environment file was retained")
	}
	original.EnvFiles = []string{filepath.Join(dir, "production.env")}
	spec, cleanup, err = prepareEnvironment(original)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(spec.EnvFiles) != 2 || spec.EnvFiles[0] != original.EnvFiles[0] {
		t.Fatal("explicit source was not preserved")
	}
}

func TestManualEnvironmentRunnerHidesOutputErrorsAndCleansFile(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "compose-fixture")
	// Emulate an adapter printing private input over several writes and failing.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nlast=''\nfor arg in \"$@\"; do\n if [ \"$last\" = '--env-file' ]; then printf '%s' \"$arg\" > private-path; cat \"$arg\" >&2; fi\n last=\"$arg\"\ndone\nprintf private-; printf diagnostic\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runner, _ := NewRunner(script)
	spec := ExecutionSpec{ProjectName: "fixture", ProjectDir: dir, Environment: map[string]string{"PASSWORD": "private-diagnostic"}}
	for _, capture := range []bool{false, true} {
		var output bytes.Buffer
		var err error
		if capture {
			_, err = runner.Render(context.Background(), spec, &output)
		} else {
			err = runner.UpRelease(context.Background(), spec, 10, &output)
		}
		if err == nil || strings.Contains(err.Error(), "private-diagnostic") || output.Len() != 0 {
			t.Fatal("private output or diagnostic was exposed")
		}
		path, _ := os.ReadFile(filepath.Join(dir, "private-path"))
		if _, err := os.Stat(string(path)); !os.IsNotExist(err) {
			t.Fatal("failed command retained environment file")
		}
	}
}

func TestManualEnvironmentCannotOverrideRuntimeControls(t *testing.T) {
	for _, key := range []string{"DOCKER_HOST", "COMPOSE_FILE", "compose_project_name", "HOME", "PATH", "TMPDIR", "1INVALID"} {
		if ValidateEnvironment(map[string]string{key: "value"}) == nil {
			t.Fatal("unsafe name accepted", key)
		}
	}
	if err := ValidateEnvironment(map[string]string{"APP_ENV": "production", "EMPTY": "", "HTTP_PROXY": "for-container-only"}); err != nil {
		t.Fatal(err)
	}
	filtered := interpolationProcessEnvironment([]string{"HTTP_PROXY=control-plane", "PATH=/fixture"}, map[string]string{"HTTP_PROXY": "container-value"})
	if len(filtered) != 1 || filtered[0] != "PATH=/fixture" {
		t.Fatal("process environment overrode manual interpolation")
	}
	result := redactRuntimeEnvironment(`[{"State":"running","Health":"healthy","Command":"echo secret-value","Name":"app-1"}]`, map[string]string{"PASSWORD": "secret-value", "COUNT": "1"})
	if strings.Contains(result, "secret-value") || !strings.Contains(result, `"State":"running"`) {
		t.Fatal("runtime output was not safely redacted")
	}
	result = redactRuntimeEnvironment("{\"State\":\"running\",\"Command\":\"secret-value\"}\n{\"State\":\"running\",\"Health\":\"healthy\"}\n", map[string]string{"PASSWORD": "secret-value"})
	if !strings.HasPrefix(result, "[") || strings.Contains(result, "secret-value") || strings.Count(result, `"State":"running"`) != 2 {
		t.Fatal("newline-delimited runtime rows were lost")
	}
}
