package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewedSourcesFreezeContentsAndRejectEscapes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(path, []byte("private-original"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"secrets": map[string]any{"app": map[string]any{"file": path}}}
	first, body, err := reviewedSources(root, cfg)
	if err != nil || string(body["secrets/app"]) != "private-original" {
		t.Fatal(err)
	}
	if strings.Contains(jsonTextForReview(first), "private-original") {
		t.Fatal("secret stored in preview")
	}
	os.WriteFile(path, []byte("changed"), 0600)
	second, _, err := reviewedSources(root, cfg)
	if err != nil || first["secrets/app"] == second["secrets/app"] {
		t.Fatal("changed source has same fingerprint")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("external"), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	cfg["secrets"].(map[string]any)["app"].(map[string]any)["file"] = "escape"
	if _, _, err := reviewedSources(root, cfg); err == nil {
		t.Fatal("symlink escape allowed")
	}
	cfg["secrets"].(map[string]any)["app"] = map[string]any{"external": true}
	if _, _, err := reviewedSources(root, cfg); err == nil {
		t.Fatal("unfrozen external secret allowed")
	}
}

func TestReviewedRunnerPinsImagesDisablesBuildPullAndRejectsChangedSource(t *testing.T) {
	root := t.TempDir()
	image := "sha256:" + strings.Repeat("a", 64)
	secretPath := filepath.Join(root, "secret")
	os.WriteFile(secretPath, []byte("original"), 0600)
	raw := fmt.Sprintf(`{"services":{"web":{"image":"web:latest","build":{"context":"."},"pull_policy":"always","environment":{"PASSWORD":"not-for-preview"},"ports":[{"target":80,"published":"8080"}]}},"secrets":{"app":{"file":%q}}}`, secretPath)
	renderPath, argsPath, snapshotPath := filepath.Join(root, "render.json"), filepath.Join(root, "arguments"), filepath.Join(root, "snapshot")
	os.WriteFile(renderPath, []byte(raw), 0600)
	os.WriteFile(filepath.Join(root, "compose.yml"), []byte(raw), 0600)
	script := fmt.Sprintf("#!/bin/sh\nfor arg in \"$@\"; do if [ \"$arg\" = config ]; then cat %q; exit 0; fi; done\nprintf '%%s\\n' \"$@\" > %q\nprevious=''\nfor arg in \"$@\"; do if [ \"$previous\" = '--file' ]; then cp \"$arg\" %q; fi; previous=\"$arg\"; done\n", renderPath, argsPath, snapshotPath)
	command := filepath.Join(root, "compose-test")
	os.WriteFile(command, []byte(script), 0700)
	runner, _ := NewRunner(command)
	spec := ExecutionSpec{ProjectName: "review-test", ProjectDir: root}
	review, err := runner.Review(context.Background(), spec, func(context.Context, string) (string, error) { return image, nil })
	if err != nil {
		t.Fatal(err)
	}
	preview, _ := json.Marshal(review)
	if strings.Contains(string(preview), "not-for-preview") || strings.Contains(string(preview), "original") {
		t.Fatal("secret leaked into review")
	}
	if err := runner.ApplyReviewed(context.Background(), spec, review, 30, io.Discard); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(argsPath)
	if !strings.Contains(string(args), "--no-build\n--pull\nnever\n") || strings.Contains(string(args), "rollback") {
		t.Fatalf("unsafe arguments: %s", args)
	}
	frozen, _ := os.ReadFile(snapshotPath)
	if !strings.Contains(string(frozen), image) || strings.Contains(string(frozen), `"build"`) || strings.Contains(string(frozen), `"pull_policy"`) {
		t.Fatal("images/build not frozen")
	}
	os.Remove(argsPath)
	os.WriteFile(secretPath, []byte("changed"), 0600)
	if err := runner.ApplyReviewed(context.Background(), spec, review, 30, io.Discard); err == nil {
		t.Fatal("changed secret source accepted")
	}
	if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
		t.Fatal("up executed with stale approval")
	}
}

func TestReviewedAgentSourceRejectsInterpolationBeforeRendering(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "compose.yml"), []byte("services:\n  app:\n    image: alpine:3.24\n    volumes:\n      - ${DATA_DIR}:/data\n"), 0600)
	runner, _ := NewRunner("must-never-run")
	runner = runner.ForTarget(Target{NodeID: "remote", Host: "unix:///private/agent-proxy.sock", RemoteSources: true})
	_, err := runner.Review(context.Background(), ExecutionSpec{ProjectName: "remote", ProjectDir: root}, func(context.Context, string) (string, error) { t.Fatal("source validation bypassed"); return "", nil })
	if err == nil || !strings.Contains(err.Error(), "interpolat") {
		t.Fatal("Agent interpolation did not fail before rendering", err)
	}
}
