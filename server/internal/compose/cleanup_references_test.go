package compose

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/suma/suma/server/internal/task"
)

type cleanupRefRunner struct {
	Runner
	rendered string
	err      error
	specs    []ExecutionSpec
}

func (r *cleanupRefRunner) Render(_ context.Context, spec ExecutionSpec, _ io.Writer) (string, error) {
	r.specs = append(r.specs, spec)
	return r.rendered, r.err
}
func TestCleanupReferencesIncludeStoppedProjectsAndAllProfiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "stopped")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services: {}"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &cleanupRefRunner{rendered: `{"services":{"app":{"image":"app:old"}},"networks":{"default":{"name":"external-net"}},"volumes":{"db":{"name":"stored-data"}}}`}
	service, err := NewService(nil, root, runner, (*task.Service)(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := service.CleanupResourceReferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(refs.Images) != 1 || refs.Images[0] != "app:old" || refs.Networks[0] != "external-net" || refs.Volumes[0] != "stored-data" || len(runner.specs[0].Profiles) != 1 || runner.specs[0].Profiles[0] != "*" {
		t.Fatalf("%+v %+v", refs, runner.specs)
	}
	runner.err = errors.New("render rejected secret=value")
	if _, err = service.CleanupResourceReferences(context.Background()); err == nil {
		t.Fatal("broken project silently ignored")
	}
}
func TestCleanupReferencesRejectEscapingSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "compose.yml"), []byte("services: {}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "project")); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(nil, root, &cleanupRefRunner{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.CleanupResourceReferences(context.Background()); err == nil {
		t.Fatal("escaped project accepted")
	}
}

func TestCleanupReferencesFailClosedWhenConfiguredRootDisappears(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	service, err := NewService(nil, root, &cleanupRefRunner{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CleanupResourceReferences(context.Background()); err == nil {
		t.Fatal("missing configured root treated as empty protection")
	}
}
