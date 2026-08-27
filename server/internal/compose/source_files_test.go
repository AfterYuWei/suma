package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateLocalProjectSourcePreservesConfigOrderAndDependencies(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "compose.yml")
	second := filepath.Join(root, "override.yml")
	if err := os.WriteFile(first, []byte("services:\n  web:\n    image: nginx\n    env_file: web.env\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("services:\n  web:\n    label_file: labels.txt\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web.env"), []byte("MODE=prod\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "labels.txt"), []byte("tier=web\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	value, err := ValidateLocalProjectSource(root, []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(value.ConfigFiles) != 2 || value.ConfigFiles[0] != first || value.ConfigFiles[1] != second || len(value.Dependencies) != 2 {
		t.Fatalf("manifest = %#v", value)
	}
}

func TestValidateLocalProjectSourceRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "compose.yml")
	if err := os.WriteFile(target, []byte("services: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "compose.yml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := ValidateLocalProjectSource(root, []string{link})
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("expected escape rejection, got %v", err)
	}
}

func TestValidateLocalProjectSourceRejectsDependencyOutsideWorkingDirectory(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	compose := filepath.Join(root, "compose.yml")
	if err := os.WriteFile(compose, []byte("services:\n  web:\n    env_file: "+filepath.Join(outside, "secret.env")+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.env"), []byte("TOKEN=secret\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := ValidateLocalProjectSource(root, []string{compose})
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("expected dependency rejection, got %v", err)
	}
}
