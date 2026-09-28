package containerfiles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
)

type fakeRuntime struct {
	files                         map[string]string
	mount                         container.Mount
	state                         string
	failWrite                     bool
	failOncePartial               bool
	externalChangeBeforeReplace   bool
	externalChangeBeforeBindWrite bool
	listing                       []byte
}

func (r *fakeRuntime) Get(_ context.Context, _ string) (container.Detail, error) {
	return container.Detail{State: r.state, Mounts: []container.Mount{r.mount}}, nil
}

func (r *fakeRuntime) RunFileCommand(_ context.Context, _ string, script string, args []string, input []byte, _ int) ([]byte, error) {
	switch script {
	case noSymlinkScript:
		return nil, nil
	case readScript:
		value, ok := r.files[args[0]]
		if !ok {
			return nil, errors.New("missing file")
		}
		return []byte(value), nil
	case createTempScript:
		r.files[args[0]] = ""
		return nil, nil
	case writeScript:
		if r.failOncePartial {
			r.failOncePartial = false
			r.files[args[0]] = "partial"
			return nil, errors.New("interrupted write")
		}
		if r.failWrite {
			return nil, errors.New("write denied")
		}
		r.files[args[0]] = string(input)
		return nil, nil
	case writeCheckedScript:
		if r.externalChangeBeforeBindWrite {
			r.externalChangeBeforeBindWrite = false
			r.files[args[0]] = "external change"
		}
		if hash(r.files[args[0]]) != args[1] {
			return nil, ErrConflict
		}
		if r.failOncePartial {
			r.failOncePartial = false
			r.files[args[0]] = "partial"
			return nil, errors.New("interrupted write")
		}
		if r.failWrite {
			return nil, errors.New("write denied")
		}
		r.files[args[0]] = string(input)
		return nil, nil
	case replaceScript:
		if r.externalChangeBeforeReplace {
			r.externalChangeBeforeReplace = false
			r.files[args[0]] = "external change"
		}
		if hash(r.files[args[0]]) != args[2] {
			return nil, ErrConflict
		}
		r.files[args[0]] = r.files[args[1]]
		delete(r.files, args[1])
		return nil, nil
	case `rm -f -- "$1"`:
		delete(r.files, args[0])
		return nil, nil
	case listScript:
		return r.listing, nil
	case `[ -d "$1" ] && printf 1 || printf 0`:
		return []byte("1"), nil
	default:
		return nil, errors.New("unexpected command")
	}
}

func newHarness(t *testing.T) (*Service, *fakeRuntime) {
	t.Helper()
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(db, secrets), &fakeRuntime{files: map[string]string{"/data/app.yaml": "token: original\n"}, mount: container.Mount{Type: "volume", Destination: "/data", ReadWrite: true}, state: "running"}
}

func TestSaveConflictEncryptedHistoryAndRestore(t *testing.T) {
	s, runtime := newHarness(t)
	ctx := context.Background()
	opened, err := s.Read(ctx, runtime, "container", "/data/app.yaml")
	if err != nil || opened.ReadOnly || !opened.Persistent {
		t.Fatalf("read: %+v, %v", opened, err)
	}
	saved, err := s.Save(ctx, runtime, "node-a", "container", "/data/app.yaml", opened.ETag, "token: changed\n", nil)
	if err != nil || saved.Content != "token: changed\n" {
		t.Fatalf("save: %+v, %v", saved, err)
	}
	if _, err := s.Save(ctx, runtime, "node-a", "container", "/data/app.yaml", opened.ETag, "stale", nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	history, err := s.History(ctx, runtime, "node-a", "container", "/data/app.yaml")
	if err != nil || len(history) != 2 || !history[1].Baseline {
		t.Fatalf("history: %+v, %v", history, err)
	}
	var rows []database.FileRevision
	if err := s.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if bytes.Contains(row.Ciphertext, []byte("token:")) {
			t.Fatal("revision plaintext stored in database")
		}
	}
	if _, err := s.RevisionContent(ctx, "node-b", "container", "/data/app.yaml", history[1].ID); err == nil {
		t.Fatal("cross-node revision access succeeded")
	}
	restored, err := s.Restore(ctx, runtime, "node-a", "container", "/data/app.yaml", history[1].ID, saved.ETag, nil)
	if err != nil || restored.Content != "token: original\n" {
		t.Fatalf("restore: %+v, %v", restored, err)
	}
	history, err = s.History(ctx, runtime, "node-a", "container", "/data/app.yaml")
	if err != nil || len(history) != 3 {
		t.Fatalf("restored history: %+v, %v", history, err)
	}
}

func TestReadOnlyAndFailedWritePreserveContents(t *testing.T) {
	s, runtime := newHarness(t)
	ctx := context.Background()
	opened, _ := s.Read(ctx, runtime, "container", "/data/app.yaml")
	runtime.mount.ReadWrite = false
	if _, err := s.Save(ctx, runtime, "node-a", "container", "/data/app.yaml", opened.ETag, "new", nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected read-only error, got %v", err)
	}
	runtime.mount.ReadWrite = true
	runtime.failWrite = true
	if _, err := s.Save(ctx, runtime, "node-a", "container", "/data/app.yaml", opened.ETag, "new", nil); err == nil {
		t.Fatal("expected write failure")
	}
	if runtime.files["/data/app.yaml"] != opened.Content {
		t.Fatal("failed write changed source")
	}
}

func TestSingleFileBindFailureRestoresPreviousContent(t *testing.T) {
	s, runtime := newHarness(t)
	runtime.mount = container.Mount{Type: "bind", Destination: "/data/app.yaml", ReadWrite: true}
	ctx := context.Background()
	opened, err := s.Read(ctx, runtime, "container", "/data/app.yaml")
	if err != nil || !opened.SingleFileBind {
		t.Fatalf("read bind: %+v, %v", opened, err)
	}
	runtime.failOncePartial = true
	if _, err := s.Save(ctx, runtime, "node-a", "container", opened.Path, opened.ETag, "new content", nil); err == nil {
		t.Fatal("expected failed write")
	}
	if runtime.files[opened.Path] != opened.Content {
		t.Fatalf("failed bind save left %q", runtime.files[opened.Path])
	}
}

func TestExternalChangeDuringSaveIsNotOverwritten(t *testing.T) {
	for _, singleFileBind := range []bool{false, true} {
		s, runtime := newHarness(t)
		if singleFileBind {
			runtime.mount = container.Mount{Type: "bind", Destination: "/data/app.yaml", ReadWrite: true}
			runtime.externalChangeBeforeBindWrite = true
		} else {
			runtime.externalChangeBeforeReplace = true
		}
		opened, err := s.Read(context.Background(), runtime, "container", "/data/app.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Save(context.Background(), runtime, "node-a", "container", opened.Path, opened.ETag, "new content", nil); !errors.Is(err, ErrConflict) {
			t.Fatalf("singleFileBind=%v: expected conflict, got %v", singleFileBind, err)
		}
		if runtime.files[opened.Path] != "external change" {
			t.Fatalf("singleFileBind=%v: external change was overwritten", singleFileBind)
		}
	}
}

func TestPathValidationAndMountClassification(t *testing.T) {
	for _, invalid := range []string{"", "relative", "/a/../b", "/a//b", "/a\x00b", "/a\n"} {
		if _, err := CleanPath(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	all := []MountInfo{{Type: "bind", Destination: "/data", ReadWrite: true}, {Type: "volume", Destination: "/data/nested", ReadWrite: false}}
	if got := effectiveMount(all, "/data/nested/a"); got == nil || got.Destination != "/data/nested" || got.ReadWrite {
		t.Fatalf("wrong nested mount: %+v", got)
	}
	if got := effectiveMount(all, "/database"); got != nil {
		t.Fatalf("prefix overlap: %+v", got)
	}
}

func TestPendingRevisionReconcilesFromObservedContent(t *testing.T) {
	s, runtime := newHarness(t)
	ctx := context.Background()
	if _, err := s.prepareRevision(ctx, "node-a", "container", "/data/app.yaml", "finished", nil, false, "pending"); err != nil {
		t.Fatal(err)
	}
	runtime.files["/data/app.yaml"] = "finished"
	rows, err := s.History(ctx, runtime, "node-a", "container", "/data/app.yaml")
	if err != nil || len(rows) != 1 || rows[0].Hash != hash("finished") {
		t.Fatalf("pending recovery: %+v, %v", rows, err)
	}
}

func TestInterruptedSingleFileWriteWarnsOnUnexpectedContent(t *testing.T) {
	s, runtime := newHarness(t)
	ctx := context.Background()
	if err := s.insertRevision(ctx, "node-a", "container", "/data/app.yaml", "token: original\n", nil, true, "committed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.prepareRevision(ctx, "node-a", "container", "/data/app.yaml", "token: expected\n", nil, false, "pending"); err != nil {
		t.Fatal(err)
	}
	runtime.files["/data/app.yaml"] = "token: partial"
	opened, err := s.ReadForNode(ctx, runtime, "node-a", "container", "/data/app.yaml")
	if err != nil || opened.Warning == "" {
		t.Fatalf("missing interruption warning: %+v, %v", opened, err)
	}
	var count int64
	if err := s.db.Model(&database.FileRevision{}).Where("state = ?", "pending").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("pending not reconciled: %d %v", count, err)
	}
}

func TestHistoryMovesWithFileAndDeletedPathDoesNotInherit(t *testing.T) {
	s, runtime := newHarness(t)
	ctx := context.Background()
	opened, _ := s.Read(ctx, runtime, "container", "/data/app.yaml")
	if _, err := s.Save(ctx, runtime, "node-a", "container", "/data/app.yaml", opened.ETag, "next", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AfterAction(ctx, "node-a", "container", Action{Action: "rename", Path: "/data/app.yaml", Target: "/data/new.yaml"}); err != nil {
		t.Fatal(err)
	}
	var count int64
	s.db.Model(&database.FileRevision{}).Where("path = ?", "/data/new.yaml").Count(&count)
	if count != 2 {
		t.Fatalf("moved history count = %d", count)
	}
	if err := s.AfterAction(ctx, "node-a", "container", Action{Action: "delete", Path: "/data/new.yaml"}); err != nil {
		t.Fatal(err)
	}
	s.db.Model(&database.FileRevision{}).Where("path = ?", "/data/new.yaml").Count(&count)
	if count != 0 {
		t.Fatalf("deleted path retained %d versions", count)
	}
	if strings.Contains(runtime.files["/data/app.yaml"], "original") {
		t.Fatal("fake runtime did not reflect save")
	}
}

func TestListSortsHiddenEntriesBeforeCursorPagination(t *testing.T) {
	s, runtime := newHarness(t)
	var output strings.Builder
	for i := 0; i < 201; i++ {
		fmt.Fprintf(&output, "file-%03d\x00file\x001\x001\x00", i)
	}
	output.WriteString(".env\x00file\x001\x001\x00")
	runtime.listing = []byte(output.String())
	first, err := s.List(context.Background(), runtime, "container", "/data", "")
	if err != nil || len(first.Entries) != 200 || first.Entries[0].Name != ".env" || first.NextCursor == "" {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	second, err := s.List(context.Background(), runtime, "container", "/data", first.NextCursor)
	if err != nil || len(second.Entries) != 2 {
		t.Fatalf("second page: %+v, %v", second, err)
	}
}

func TestPruneAllEnforcesAgeAndVersionCount(t *testing.T) {
	s, _ := newHarness(t)
	ctx := context.Background()
	for i := 0; i < 23; i++ {
		row, err := s.prepareRevision(ctx, "node-a", "container", "/data/app.yaml", fmt.Sprintf("v%d", i), nil, false, "committed")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := s.db.Model(&row).Update("created_at", time.Now().Add(-31*24*time.Hour)).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.PruneAll(ctx); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := s.db.Model(&database.FileRevision{}).Count(&count).Error; err != nil || count != 20 {
		t.Fatalf("retention: %d, %v", count, err)
	}
}
