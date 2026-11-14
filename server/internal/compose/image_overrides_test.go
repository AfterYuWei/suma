package compose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrozenImageOverlayIsPrivateTemporaryAndLeavesGitFilesUnchanged(t *testing.T) {
	file := filepath.Join(t.TempDir(), "compose.yml")
	original := []byte("services: {app: {image: 'example/app:latest'}}\n")
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	spec, cleanup, err := prepareImageOverrides(ExecutionSpec{Files: []string{file}, ImageOverrides: map[string]string{"app": "example/app@sha256:" + strings.Repeat("a", 64)}, PlatformOverrides: map[string]string{"app": "linux/arm64"}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	overlay := spec.Files[1]
	info, err := os.Stat(overlay)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("overlay permissions", err)
	}
	raw, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatal(err)
	}
	var content struct {
		Services map[string]struct {
			Image    string
			Platform string
		}
	}
	if err := json.Unmarshal(raw, &content); err != nil || content.Services["app"].Platform != "linux/arm64" || strings.Contains(content.Services["app"].Image, ":latest") {
		t.Fatal("overlay did not fix the image and platform", err)
	}
	current, err := os.ReadFile(file)
	if err != nil || string(current) != string(original) {
		t.Fatal("Git source changed", err)
	}
	cleanup()
	if _, err := os.Stat(overlay); !os.IsNotExist(err) {
		t.Fatal("overlay survived cleanup", err)
	}
}
