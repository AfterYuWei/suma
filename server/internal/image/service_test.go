package image

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

type stubAdapter struct {
	listResult    []Summary
	detail        Detail
	pullStream    io.ReadCloser
	pullErr       error
	pullCalled    bool
	pullReference string
	inspectedID   string
	removedID     string
	removedForce  bool
	taggedID      string
	taggedRef     string
}

func (s *stubAdapter) ListImages(context.Context) ([]Summary, error) { return s.listResult, nil }

func (s *stubAdapter) InspectImage(_ context.Context, id string) (Detail, error) {
	s.inspectedID = id
	return s.detail, nil
}

func (s *stubAdapter) PullImage(_ context.Context, reference string) (io.ReadCloser, error) {
	s.pullCalled = true
	s.pullReference = reference
	return s.pullStream, s.pullErr
}

func (s *stubAdapter) RemoveImage(_ context.Context, id string, force bool) error {
	s.removedID, s.removedForce = id, force
	return nil
}

func (s *stubAdapter) TagImage(_ context.Context, id, reference string) error {
	s.taggedID, s.taggedRef = id, reference
	return nil
}

type authenticatedStub struct {
	stubAdapter
	authCalled    bool
	authReference string
	authServer    string
	authUsername  string
	authSecret    string
	authToken     bool
	authStream    io.ReadCloser
}

func (a *authenticatedStub) PullImageAuthenticated(_ context.Context, reference, server, username, secret string, token bool) (io.ReadCloser, error) {
	a.authCalled = true
	a.authReference, a.authServer, a.authUsername, a.authSecret, a.authToken = reference, server, username, secret, token
	return a.authStream, nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func stream(lines ...string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(strings.Join(lines, "\n")))
}

func newTestService(t *testing.T, adapter Adapter) (*Service, *gorm.DB) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "image.db"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(adapter, task.NewService(db))
	return service, db
}

func waitTask(t *testing.T, db *gorm.DB, id string) database.Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var row database.Task
		if err := db.First(&row, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if row.Status != task.StatusPending && row.Status != task.StatusRunning {
			return row
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for pull task")
	return database.Task{}
}

func logMessages(t *testing.T, db *gorm.DB, id string) []string {
	t.Helper()
	var logs []database.TaskLog
	if err := db.Where("task_id = ?", id).Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	messages := make([]string, 0, len(logs))
	for _, entry := range logs {
		messages = append(messages, entry.Message)
	}
	return messages
}

func contains(haystack []string, needle string) bool {
	for _, value := range haystack {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func TestPullForNodeWithRegistryStreamParsing(t *testing.T) {
	tests := []struct {
		name        string
		lines       []string
		wantStatus  string
		wantMessage string
		wantLogs    []string
	}{
		{
			name: "progress and status events reach completion",
			lines: []string{
				`{"status":"Downloading","id":"8acdb","progressDetail":{"current":50,"total":100}}`,
				`{"status":"Pull complete","id":"8acdb"}`,
				`{"status":"Digest: sha256:86c0"}`,
			},
			wantStatus:  task.StatusSuccess,
			wantMessage: "Completed",
			wantLogs:    []string{"8acdb: Downloading", "8acdb: Pull complete", "Digest: sha256:86c0"},
		},
		{
			name:        "non-json lines are ignored",
			lines:       []string{"garbage", `not json {`},
			wantStatus:  task.StatusSuccess,
			wantMessage: "Completed",
		},
		{
			name:        "error event fails the pull",
			lines:       []string{`{"status":"Downloading"}`, `{"error":"manifest for alpine:missing not found"}`},
			wantStatus:  task.StatusFailed,
			wantMessage: "manifest for alpine:missing not found",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := &stubAdapter{pullStream: stream(test.lines...)}
			service, db := newTestService(t, adapter)
			row, err := service.PullForNodeWithRegistry("local", "Local", "alpine:3.20", "", "", "", false)
			if err != nil {
				t.Fatal(err)
			}
			finished := waitTask(t, db, row.ID)
			// The work runs asynchronously, so adapter side effects are only
			// guaranteed once the task reaches a terminal state.
			if !adapter.pullCalled || adapter.pullReference != "alpine:3.20" {
				t.Fatalf("unexpected unauthenticated pull call: called=%v ref=%q", adapter.pullCalled, adapter.pullReference)
			}
			if finished.Status != test.wantStatus {
				t.Fatalf("expected status %q, got %q (%s)", test.wantStatus, finished.Status, finished.Message)
			}
			if finished.Message != test.wantMessage {
				t.Fatalf("expected message %q, got %q", test.wantMessage, finished.Message)
			}
			if test.wantStatus == task.StatusSuccess && finished.Progress != 100 {
				t.Fatalf("expected progress 100 on success, got %d", finished.Progress)
			}
			if len(test.wantLogs) > 0 {
				messages := logMessages(t, db, row.ID)
				for _, expected := range test.wantLogs {
					if !contains(messages, expected) {
						t.Fatalf("expected task log containing %q, got %v", expected, messages)
					}
				}
			}
		})
	}
}

func TestPullFailsWhenStreamIOErrors(t *testing.T) {
	adapter := &stubAdapter{pullStream: io.NopCloser(errReader{})}
	service, db := newTestService(t, adapter)
	row, err := service.Pull("nginx:1.27")
	if err != nil {
		t.Fatal(err)
	}
	finished := waitTask(t, db, row.ID)
	if finished.Status != task.StatusFailed || finished.Message != "connection reset" {
		t.Fatalf("expected failed task with reader error, got %q (%s)", finished.Status, finished.Message)
	}
}

func TestPullFailsWhenAdapterRejects(t *testing.T) {
	adapter := &stubAdapter{pullErr: errors.New("dial tcp 127.0.0.1:2375: connect refused")}
	service, db := newTestService(t, adapter)
	row, err := service.Pull("redis:7")
	if err != nil {
		t.Fatal(err)
	}
	finished := waitTask(t, db, row.ID)
	if finished.Status != task.StatusFailed {
		t.Fatalf("expected failed task, got %q", finished.Status)
	}
	var logs []database.TaskLog
	if err := db.Where("task_id = ?", row.ID).Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	foundErr := false
	for _, entry := range logs {
		if entry.Level == "error" && strings.Contains(entry.Message, "connect refused") {
			foundErr = true
		}
	}
	if !foundErr {
		t.Fatalf("expected an error-level task log with the failure reason")
	}
}

func TestPullRegistryAuthenticationBranches(t *testing.T) {
	t.Run("no registry routes through plain pull even when auth is supported", func(t *testing.T) {
		adapter := &authenticatedStub{
			stubAdapter: stubAdapter{pullStream: stream(`{"status":"Loaded image"}`)},
			authStream:  stream(`{"status":"never used"}`),
		}
		service, db := newTestService(t, adapter)
		row, err := service.PullForNodeWithRegistry("local", "Local", "alpine:3.20", "", "", "", false)
		if err != nil {
			t.Fatal(err)
		}
		waitTask(t, db, row.ID)
		if !adapter.pullCalled || adapter.authCalled {
			t.Fatalf("expected plain pull, got plain=%v auth=%v", adapter.pullCalled, adapter.authCalled)
		}
	})

	t.Run("registry routes through authenticated pull with material", func(t *testing.T) {
		adapter := &authenticatedStub{
			stubAdapter: stubAdapter{pullStream: stream(`{"status":"never used"}`)},
			authStream:  stream(`{"status":"Download complete"}`),
		}
		service, db := newTestService(t, adapter)
		row, err := service.PullForNodeWithRegistry("local", "Local", "corp.local/app:1.4", "https://corp.local", "deploy-user", "SECRET-VALUE", true)
		if err != nil {
			t.Fatal(err)
		}
		finished := waitTask(t, db, row.ID)
		if !adapter.authCalled {
			t.Fatal("expected the authenticated pull path to be used")
		}
		if adapter.pullCalled {
			t.Fatal("plain pull must not run when a registry server is set")
		}
		if adapter.authReference != "corp.local/app:1.4" || adapter.authServer != "https://corp.local" ||
			adapter.authUsername != "deploy-user" || adapter.authSecret != "SECRET-VALUE" || !adapter.authToken {
			t.Fatalf("unexpected credential forwarding: %+v", map[string]any{
				"reference": adapter.authReference, "server": adapter.authServer,
				"username_set": adapter.authUsername != "", "secret_set": adapter.authSecret != "", "token": adapter.authToken,
			})
		}
		if finished.Status != task.StatusSuccess {
			t.Fatalf("expected successful pull, got %q (%s)", finished.Status, finished.Message)
		}
	})

	t.Run("unsupported adapter fails closed for registries", func(t *testing.T) {
		adapter := &stubAdapter{pullStream: stream(`{"status":"never reached"}`)}
		service, db := newTestService(t, adapter)
		row, err := service.PullForNodeWithRegistry("local", "Local", "private/app:2", "https://private.local", "", "", false)
		if err != nil {
			t.Fatal(err)
		}
		finished := waitTask(t, db, row.ID)
		if finished.Status != task.StatusFailed || !strings.Contains(finished.Message, "does not support registry authentication") {
			t.Fatalf("expected auth-unsupported failure, got %q (%s)", finished.Status, finished.Message)
		}
		if adapter.pullCalled {
			t.Fatal("unauthenticated pull must not run for registry pulls")
		}
	})
}

func TestPullWrappersTrackNodeMetadata(t *testing.T) {
	adapter := &stubAdapter{pullStream: stream("")}
	service, db := newTestService(t, adapter)

	local, err := service.Pull("grafana:11")
	if err != nil {
		t.Fatal(err)
	}
	if local.NodeID != "local" || local.NodeName != "Local" || local.Type != "image.pull" || local.Name != "Pull grafana:11" {
		t.Fatalf("unexpected local wrapper metadata: %+v", local)
	}

	node, err := service.PullForNode("edge", "Edge Node", "nginx:alpine")
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, db, local.ID)
	finished := waitTask(t, db, node.ID)
	if node.NodeID != "edge" || node.NodeName != "Edge Node" {
		t.Fatalf("unexpected node wrapper metadata: %+v", node)
	}
	if finished.Status != task.StatusSuccess {
		t.Fatalf("expected both pulls to succeed, got %q", finished.Status)
	}
}

func TestServiceOperationsDelegateToAdapter(t *testing.T) {
	ctx := context.Background()
	adapter := &stubAdapter{
		listResult: []Summary{{ID: "sha256:abc", Tags: []string{"nginx:latest"}, Size: 187654321}},
		detail:     Detail{Summary: Summary{ID: "sha256:abc"}, Architecture: "amd64", OS: "linux"},
	}
	service, _ := newTestService(t, adapter)

	rows, err := service.List(ctx)
	if err != nil || len(rows) != 1 || rows[0].ID != "sha256:abc" || rows[0].Tags[0] != "nginx:latest" {
		t.Fatalf("List did not pass through adapter result: %v %v", rows, err)
	}
	detail, err := service.Get(ctx, "sha256:abc")
	if err != nil || detail.Architecture != "amd64" || adapter.inspectedID != "sha256:abc" {
		t.Fatalf("Get did not forward the identifier: %+v %v", detail, err)
	}
	if err := service.Tag(ctx, "sha256:abc", "mirror/nginx:latest"); err != nil ||
		adapter.taggedID != "sha256:abc" || adapter.taggedRef != "mirror/nginx:latest" {
		t.Fatalf("Tag did not forward arguments: %v", err)
	}
	if err := service.Remove(ctx, "sha256:abc", true); err != nil ||
		adapter.removedID != "sha256:abc" || !adapter.removedForce {
		t.Fatalf("Remove did not forward arguments: %v", err)
	}
}
