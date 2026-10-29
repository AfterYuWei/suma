package compose

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectedRunnerRestrictsServicesAndPreservesReplicas(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "compose-fixture")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> arguments\nprintf '%s\\n' END >> arguments\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(command)
	if err != nil {
		t.Fatal(err)
	}
	spec := ExecutionSpec{ProjectName: "app", ProjectDir: dir, Services: []string{"web"}, Replicas: map[string]int{"web": 3}}
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
	calls := strings.Split(string(raw), "END\n")
	for _, expected := range []string{"--no-deps\n", "--no-build\n", "--pull\nnever\n", "--scale\nweb=3\n", "--wait-timeout\n42\nweb\n"} {
		if !strings.Contains(calls[1], expected) {
			t.Fatal("missing service restriction", expected, string(raw))
		}
	}
	if !strings.Contains(calls[0], "pull\n--policy\nalways\nweb\n") || strings.Contains(string(raw), "--remove-orphans") || strings.Contains(string(raw), "--volumes") {
		t.Fatal("unsafe selective update", string(raw))
	}
}
