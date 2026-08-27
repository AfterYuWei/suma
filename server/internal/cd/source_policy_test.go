package cd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/compose"
)

func TestValidateComposeSourcesAcceptsRepositoryLocalReferences(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "deploy")
	for _, directory := range []string{"env", "labels", "configs", "secrets"} {
		if err := os.MkdirAll(filepath.Join(projectDir, directory), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(projectDir, "env", "common.env"), "APP_ENV=production\n")
	writeTestFile(t, filepath.Join(projectDir, "env", "optional.env"), "OPTIONAL=true\n")
	writeTestFile(t, filepath.Join(projectDir, "labels", "common.labels"), "com.example.owner=platform\n")
	writeTestFile(t, filepath.Join(projectDir, "configs", "app.conf"), "listen=:8080\n")
	writeTestFile(t, filepath.Join(projectDir, "secrets", "public.pem"), "test-public-material\n")
	composeFile := filepath.Join(projectDir, "compose.yml")
	writeTestFile(t, composeFile, `
configs:
  app:
    file: ./configs/app.conf
  external_config:
    external: true
secrets:
  public_key:
    file: ./secrets/public.pem
services:
  app:
    image: example/app:1
    env_file:
      - ./env/common.env
      - path: ./env/optional.env
        required: false
    label_file:
      - ./labels/common.labels
`)
	spec := compose.ExecutionSpec{ProjectName: "production", ProjectDir: projectDir, Files: []string{composeFile}}
	if err := validateComposeSources(spec, root); err != nil {
		t.Fatalf("validateComposeSources(valid repository-local references): %v", err)
	}
}

func TestValidateComposeSourcesRejectsExecutableComposeFeatures(t *testing.T) {
	tests := []struct {
		name    string
		compose string
		want    string
	}{
		{"include", "include: [other.yml]\nservices: {app: {image: example/app:1}}\n", "uses include"},
		{"build string", "services:\n  app:\n    image: example/app:1\n    build: .\n", "uses build"},
		{"build map", "services:\n  app:\n    image: example/app:1\n    build:\n      context: .\n      dockerfile: Dockerfile\n", "uses build"},
		{"extends", "services:\n  app:\n    image: example/app:1\n    extends:\n      file: common.yml\n      service: base\n", "uses extends"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			composeFile := filepath.Join(root, "compose.yml")
			writeTestFile(t, composeFile, test.compose)
			err := validateComposeSources(compose.ExecutionSpec{ProjectName: "production", ProjectDir: root, Files: []string{composeFile}}, root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateComposeSources() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateComposeSourcesRejectsEscapedAndInterpolatedReferences(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		setupPath func(t *testing.T, root, projectDir string)
		want      string
	}{
		{
			name: "relative traversal", path: "../../outside.env", want: "escapes the Git worktree",
			setupPath: func(t *testing.T, root, _ string) {
				writeTestFile(t, filepath.Join(filepath.Dir(root), "outside.env"), "TOKEN=outside\n")
			},
		},
		{
			name: "absolute outside", want: "escapes the Git worktree",
			setupPath: func(t *testing.T, _, projectDir string) {
				outside := filepath.Join(t.TempDir(), "outside.env")
				writeTestFile(t, outside, "TOKEN=outside\n")
				writeTestFile(t, filepath.Join(projectDir, "path.txt"), outside)
			},
		},
		{
			name: "symlink outside", path: "escaped.env", want: "escapes the Git worktree",
			setupPath: func(t *testing.T, _, projectDir string) {
				outside := filepath.Join(t.TempDir(), "outside.env")
				writeTestFile(t, outside, "TOKEN=outside\n")
				if err := os.Symlink(outside, filepath.Join(projectDir, "escaped.env")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "braced interpolation", path: "${ENV_FILE}", want: "interpolated",
			setupPath: func(t *testing.T, _, projectDir string) {
				writeTestFile(t, filepath.Join(projectDir, "${ENV_FILE}"), "TOKEN=literal\n")
			},
		},
		{
			name: "unbraced interpolation", path: "$ENV_FILE", want: "interpolated",
			setupPath: func(t *testing.T, _, projectDir string) {
				writeTestFile(t, filepath.Join(projectDir, "$ENV_FILE"), "TOKEN=literal\n")
			},
		},
		{
			name: "command-like interpolation", path: "$(ENV_FILE)", want: "interpolated",
			setupPath: func(t *testing.T, _, projectDir string) {
				writeTestFile(t, filepath.Join(projectDir, "$(ENV_FILE)"), "TOKEN=literal\n")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rootParent := t.TempDir()
			root := filepath.Join(rootParent, "worktree")
			projectDir := filepath.Join(root, "deploy")
			if err := os.MkdirAll(projectDir, 0o750); err != nil {
				t.Fatal(err)
			}
			if test.setupPath != nil {
				test.setupPath(t, root, projectDir)
			}
			path := test.path
			if test.name == "absolute outside" {
				value, err := os.ReadFile(filepath.Join(projectDir, "path.txt"))
				if err != nil {
					t.Fatal(err)
				}
				path = string(value)
			}
			composeFile := filepath.Join(projectDir, "compose.yml")
			writeTestFile(t, composeFile, "services:\n  app:\n    image: example/app:1\n    env_file: "+quoteYAML(path)+"\n")
			err := validateComposeSources(compose.ExecutionSpec{ProjectName: "production", ProjectDir: projectDir, Files: []string{composeFile}}, root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateComposeSources(%q) error = %v, want %q", path, err, test.want)
			}
		})
	}
}

func TestValidateComposeSourcesRejectsUnsafeConfigSecretAndLabelPaths(t *testing.T) {
	rootParent := t.TempDir()
	root := filepath.Join(rootParent, "worktree")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(rootParent, "outside")
	writeTestFile(t, outside, "outside\n")
	tests := []struct {
		name    string
		compose string
		want    string
	}{
		{"config", "configs:\n  app:\n    file: ../outside\nservices: {app: {image: example/app:1}}\n", "configs \"app\" file"},
		{"secret", "secrets:\n  token:\n    file: ../outside\nservices: {app: {image: example/app:1}}\n", "secrets \"token\" file"},
		{"label mapping", "services:\n  app:\n    image: example/app:1\n    label_file:\n      - path: ../outside\n", "label_file"},
		{"empty env path", "services:\n  app:\n    image: example/app:1\n    env_file: \"\"\n", "is empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			composeFile := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-")+".yml")
			writeTestFile(t, composeFile, test.compose)
			err := validateComposeSources(compose.ExecutionSpec{ProjectName: "production", ProjectDir: root, Files: []string{composeFile}}, root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateComposeSources() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReadDeploymentFileLimitsSizeAndRequiresRegularFile(t *testing.T) {
	root := t.TempDir()
	tooLarge := filepath.Join(root, "too-large.env")
	if err := os.WriteFile(tooLarge, make([]byte, maxDeploymentFileSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDeploymentFile(tooLarge); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("readDeploymentFile(too large) error = %v", err)
	}
	if _, err := readDeploymentFile(root); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("readDeploymentFile(directory) error = %v", err)
	}
	missing := filepath.Join(root, "missing")
	if _, err := readDeploymentFile(missing); err == nil {
		t.Fatal("readDeploymentFile(missing) unexpectedly succeeded")
	}
}

func TestValidateComposeSourcesChecksImplicitEnvironmentFile(t *testing.T) {
	root := t.TempDir()
	composeFile := filepath.Join(root, "compose.yml")
	writeTestFile(t, composeFile, "services: {app: {image: example/app:1}}\n")
	outside := filepath.Join(t.TempDir(), "outside.env")
	writeTestFile(t, outside, "TOKEN=outside\n")
	if err := os.Symlink(outside, filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	}
	err := validateComposeSources(compose.ExecutionSpec{ProjectName: "production", ProjectDir: root, Files: []string{composeFile}}, root)
	if err == nil || !strings.Contains(err.Error(), "implicit Compose environment file") || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("validateComposeSources() error = %v", err)
	}
}

func writeTestFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func quoteYAML(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}
