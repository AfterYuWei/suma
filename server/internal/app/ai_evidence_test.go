package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/cleanup"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/imageupdate"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

type cleanupEvidenceRuntime struct {
	cleanup.Runtime
	inventory cleanup.Inventory
	err       error
}

type imageEvidenceInventory struct{ value imageupdate.Inventory }

func (r imageEvidenceInventory) UpdateInventory(context.Context) (imageupdate.Inventory, error) {
	return r.value, nil
}

type imageEvidenceResolver struct{ calls atomic.Int32 }

func (r *imageEvidenceResolver) Resolve(context.Context, string, imageupdate.Platform, credential.RegistryMaterial) (imageupdate.Remote, error) {
	r.calls.Add(1)
	return imageupdate.Remote{ManifestDigest: "sha256:" + strings.Repeat("b", 64), ConfigDigest: "sha256:" + strings.Repeat("c", 64)}, nil
}

func TestAIImageEvidenceReusesCheckAndAffectedServices(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	imageID := "sha256:" + strings.Repeat("a", 64)
	version := regexp.MustCompile(`^/v[0-9.]+`)
	var mutation atomic.Bool
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mutation.Store(true)
		}
		switch version.ReplaceAllString(r.URL.Path, "") {
		case "/_ping":
			w.Header().Set("Api-Version", "1.44")
			w.Write([]byte("OK"))
		case "/info":
			json.NewEncoder(w).Encode(map[string]any{"ID": "evidence-engine", "ServerVersion": "27.0", "OSType": "linux", "Architecture": "amd64"})
		case "/images/json":
			json.NewEncoder(w).Encode([]map[string]any{{"Id": imageID, "RepoTags": []string{"example/app:latest"}, "Labels": map[string]string{"secret": "label-private-value"}}})
		case "/containers/json":
			json.NewEncoder(w).Encode([]map[string]any{{"Id": "container-private-id", "Names": []string{"/container-private-name"}, "Image": "image-private-reference", "ImageID": imageID, "Command": "command-private-value", "State": "running", "Status": "Up (unhealthy)", "Labels": map[string]string{"token": "label-private-value"}}})
		case "/containers/container/json":
			json.NewEncoder(w).Encode(map[string]any{"Id": "container", "Name": "/shop-web", "Image": imageID, "Created": "2026-09-01T00:00:00Z", "RestartCount": 8,
				"Config":     map[string]any{"Image": "example/app:latest", "Cmd": []string{"private-command-value"}, "Env": []string{"SECRET=environment-private-value"}, "Labels": map[string]string{"com.docker.compose.project": "shop"}},
				"State":      map[string]any{"Status": "exited", "ExitCode": 137, "OOMKilled": true, "Health": map[string]any{"Status": "unhealthy", "FailingStreak": 3, "Log": []any{map[string]any{"Output": "health-private-value"}}}},
				"HostConfig": map[string]any{"RestartPolicy": map[string]any{"Name": "always"}}, "NetworkSettings": map[string]any{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()
	db, err := database.Open(filepath.Join(root, "image.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(db, store, "unix:///tmp/suma-unopened-image-evidence.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.Close()
	if err := db.Model(&database.Node{}).Where("id = ?", "local").Updates(map[string]any{"endpoint": strings.Replace(engine.URL, "http://", "tcp://", 1), "connection_type": node.ConnectionTCP, "tls_mode": node.TLSDisabled, "status": "online"}).Error; err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	resolver := &imageEvidenceResolver{}
	inventory := imageEvidenceInventory{value: imageupdate.Inventory{
		Images:     []imageupdate.LocalImage{{ID: imageID, Tags: []string{"example/app:latest"}, Platform: imageupdate.Platform{OS: "linux", Architecture: "amd64"}}},
		Containers: []imageupdate.Usage{{ContainerID: "container", ContainerName: "shop-web", Project: "shop", Service: "web", ImageID: imageID, Reference: "example/app:latest"}},
	}}
	updates := imageupdate.NewService(db, tasks, audit.NewService(db), imageupdate.Dependencies{
		Node: func(_ context.Context, id string) (imageupdate.Node, error) {
			return imageupdate.Node{ID: id, Enabled: true, Available: true, RuntimeKey: "engine"}, nil
		},
		Runtime:  func(context.Context, string) (imageupdate.Runtime, error) { return inventory, nil },
		Resolver: resolver,
	})
	defer updates.Stop()
	check, err := updates.Check(ctx, "local", imageupdate.CheckInput{}, imageupdate.Actor{})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		row, _ := tasks.Get(ctx, check.ID)
		if row.Status == task.StatusSuccess {
			break
		}
		if row.Status == task.StatusFailed || time.Now().After(deadline) {
			t.Fatal("test check did not complete", row.Status, row.Message)
		}
		time.Sleep(10 * time.Millisecond)
	}
	controlled := aiRuntime{db: db, nodes: nodes, tasks: tasks, imageUpdates: updates}
	safeQuery, err := controlled.Query(ctx, "local")
	if err != nil || safeQuery.Containers.Total != 1 || safeQuery.Containers.Running != 1 || safeQuery.Containers.Unhealthy != 1 {
		t.Fatal("safe query counts missing", err)
	}
	for _, value := range []string{"container-private-id", "container-private-name", "image-private-reference", "command-private-value", "label-private-value", "example/app", "127.0.0.1", "shop-web", "sha256:"} {
		if strings.Contains(jsonText(safeQuery), value) {
			t.Fatal("safe query contained private resource or transport data")
		}
	}
	evidence, err := controlled.Read(ctx, "local", "read_status", ai.ToolArgs{Kind: "image", ID: "all"}, 500, 65536)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(evidence.Content, "update_available") || !strings.Contains(evidence.Content, "shop-web") || !strings.Contains(evidence.Content, strings.Repeat("b", 64)) {
		t.Fatal("existing checks or affected services missing", evidence.Content)
	}
	if strings.Contains(evidence.Content, "label-private-value") || mutation.Load() || resolver.calls.Load() != 1 {
		t.Fatal("read exposed labels or started another registry/Docker operation")
	}
	evidence, err = controlled.Read(ctx, "local", "read_status", ai.ToolArgs{Kind: "container", ID: "container"}, 500, 65536)
	if err != nil || !strings.Contains(evidence.Content, `"oom_killed":true`) || !strings.Contains(evidence.Content, `"exit_code":137`) || !strings.Contains(evidence.Content, `"restart_count":8`) || !strings.Contains(evidence.Content, "unhealthy") {
		t.Fatal("container failure evidence missing", evidence.Content, err)
	}
	for _, secret := range []string{"private-command-value", "environment-private-value", "health-private-value"} {
		if strings.Contains(evidence.Content, secret) {
			t.Fatal("container evidence exposed private values")
		}
	}
}

func (r *cleanupEvidenceRuntime) CleanupInventory(context.Context) (cleanup.Inventory, error) {
	return r.inventory, r.err
}

func TestAICleanupEvidenceExplainsProtectionWithoutDeleting(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "evidence.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(db, store, "unix:///tmp/suma-unopened-evidence.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.Close()
	tasks := task.NewService(db)
	old := time.Now().Add(-60 * 24 * time.Hour)
	runtime := &cleanupEvidenceRuntime{inventory: cleanup.Inventory{Resources: []cleanup.Resource{
		{Kind: cleanup.Container, ID: "stopped", Name: "stopped", State: "exited", CreatedAt: old, FinishedAt: old},
		{Kind: cleanup.Image, ID: "protected-image", Name: "protected-image", CreatedAt: old},
		{Kind: cleanup.Volume, ID: "volume-resource", Name: "volume-resource", CreatedAt: old},
		{Kind: cleanup.Cache, ID: "cache-resource", Name: "cache-resource", CreatedAt: old},
	}}}
	s := cleanup.NewService(db, tasks, nil, cleanup.Dependencies{
		Node: func(_ context.Context, id string) (cleanup.Node, error) {
			return cleanup.Node{ID: id, Name: id, Enabled: true}, nil
		},
		Runtime: func(context.Context, string) (cleanup.Runtime, error) { return runtime, nil },
		Protection: func(context.Context, string) (cleanup.Protection, error) {
			return cleanup.Protection{cleanup.Image: {"protected-image"}}, nil
		},
	})
	defer s.Stop()
	controlled := aiRuntime{db: db, nodes: nodes, tasks: tasks, cleanup: s}
	evidence, err := controlled.Read(ctx, "local", "read_status", ai.ToolArgs{Kind: "cleanup"}, 500, 65536)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Resources []cleanup.Resource `json:"resources"`
	}
	if err := json.Unmarshal([]byte(evidence.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Resources) != 2 || decoded.Resources[0].ID != "stopped" || decoded.Resources[1].Candidate || decoded.Resources[1].Reason == "eligible" {
		t.Fatal("cleanup evidence lost protection or included excluded resources", evidence.Content)
	}
	if strings.Contains(evidence.Content, "volume-resource") || strings.Contains(evidence.Content, "cache-resource") {
		t.Fatal("non-reviewed candidates disclosed")
	}
	runtime.err = errors.New("unavailable")
	evidence, err = controlled.Read(ctx, "local", "read_status", ai.ToolArgs{Kind: "cleanup"}, 500, 65536)
	if err != nil || !strings.Contains(evidence.Content, "missing") {
		t.Fatal("missing inventory was concealed", err)
	}
	if _, err = controlled.Read(ctx, "local", "read_logs", ai.ToolArgs{Kind: "cleanup"}, 500, 65536); err == nil {
		t.Fatal("invalid log target accepted")
	}
	var count int64
	db.Model(&database.Task{}).Count(&count)
	if count != 0 {
		t.Fatal("evidence read started a mutation task")
	}
}
