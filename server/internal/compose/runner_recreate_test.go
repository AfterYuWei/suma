package compose

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseRecreationStillPullsLatestAndPreservesExecutionScope(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "normal"
		if force {
			name = "recreate"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			command := filepath.Join(dir, "compose-fixture")
			if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> arguments\nprintf '%s\\n' END >> arguments\nprintf '%s' \"$DOCKER_HOST\" > target\n"), 0700); err != nil {
				t.Fatal(err)
			}
			runner, err := NewRunner(command)
			if err != nil {
				t.Fatal(err)
			}
			runner = runner.ForTarget(Target{Host: "unix:///selected-node.sock"})
			spec := ExecutionSpec{ProjectName: "selected-project", ProjectDir: dir, Files: []string{filepath.Join(dir, "historical-compose.yml")}, EnvFiles: []string{filepath.Join(dir, "historical.env")}, ForceRecreate: force}
			if err := runner.PullRelease(context.Background(), spec, io.Discard); err != nil {
				t.Fatal(err)
			}
			if err := runner.UpRelease(context.Background(), spec, 42, io.Discard); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "arguments"))
			if err != nil {
				t.Fatal(err)
			}
			commands := strings.Split(string(raw), "END\n")
			if len(commands) != 3 || !strings.Contains(commands[0], "pull\n--policy\nalways\n") || !strings.Contains(commands[1], "up\n-d\n--remove-orphans\n--wait\n") {
				t.Fatalf("missing pull-before-up sequence: %s", raw)
			}
			if strings.Contains(commands[1], "--force-recreate\n") != force {
				t.Fatalf("wrong recreation policy: %s", raw)
			}
			for _, call := range commands[:2] {
				for _, expected := range []string{"--project-name\nselected-project\n", "--project-directory\n" + dir + "\n", "--file\n" + spec.Files[0] + "\n", "--env-file\n" + spec.EnvFiles[0] + "\n"} {
					if !strings.Contains(call, expected) {
						t.Fatalf("execution scope changed: %s", raw)
					}
				}
			}
			if !strings.Contains(commands[1], "--wait-timeout\n42\n") || strings.Contains(string(raw), "down\n") || strings.Contains(string(raw), "--volumes") {
				t.Fatalf("health gate or storage policy changed: %s", raw)
			}
			target, _ := os.ReadFile(filepath.Join(dir, "target"))
			if string(target) != "unix:///selected-node.sock" {
				t.Fatal("wrong Docker node", string(target))
			}
		})
	}
}
