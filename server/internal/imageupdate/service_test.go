package imageupdate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/taskcenter"
	"github.com/suma/suma/server/internal/testutil"
	"gorm.io/gorm"
)

func newTaskService(db *gorm.DB) *task.Service { return task.NewService(db) }

func TestValidateParamsRejectsBadInput(t *testing.T) {
	if err := ValidateParams([]byte(`{"project":"blog"}`)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateParams([]byte(`{"project":"blog","services":["web"]}`)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{}`,
		`{"project":"Blog"}`,
		`{"project":"blog","services":["web","web"]}`,
		`{"project":"blog","services":["bad name"]}`,
		`not json`,
	} {
		if err := ValidateParams([]byte(raw)); err == nil {
			t.Fatalf("params must be rejected: %s", raw)
		}
	}
}

func TestNormalizeClassifiesReferences(t *testing.T) {
	cases := map[string]string{
		"nginx:latest":                   "",
		"docker.io/library/nginx:latest": "",
		"<none>:<none>":                  "untagged",
		"sha256:abc":                     "image_id",
		"nginx:latest@sha256:0000000000000000000000000000000000000000000000000000000000000000": "digest_pinned",
		"nginx::bad": "invalid_reference",
	}
	for value, reason := range cases {
		if _, _, got := Normalize(value); got != reason {
			t.Fatalf("Normalize(%q) reason = %q, want %q", value, got, reason)
		}
	}
	if host := RegistryHost("registry-1.docker.io"); host != "docker.io" {
		t.Fatalf("docker hub alias must canonicalize, got %q", host)
	}
}

func TestPlanChecksFiltersEligibleServices(t *testing.T) {
	inventory := Inventory{
		Images: []LocalImage{{ID: "sha256:aaa", Platform: Platform{OS: "linux", Architecture: "amd64"}}},
		Containers: []Usage{
			{Project: "blog", Service: "web", State: "running", ImageID: "sha256:aaa", Reference: "nginx:1.27", ContainerID: "c1"},
			{Project: "blog", Service: "web", State: "running", ImageID: "sha256:aaa", Reference: "nginx:1.27", ContainerID: "c2"},
			{Project: "blog", Service: "api", State: "exited", ImageID: "sha256:aaa", Reference: "api:2.0", ContainerID: "c3"},
			{Project: "blog", Service: "migrate", State: "running", ImageID: "sha256:aaa", Reference: "api:2.0", OneOff: true, ContainerID: "c4"},
			{Project: "shop", Service: "pay", State: "running", ImageID: "sha256:aaa", Reference: "pay:9", DeliveryProject: "shop-cd", ContainerID: "c5"},
		},
	}
	targets, blocked := planChecks(inventory, Params{Project: "blog"})
	if len(targets) != 1 || targets[0].service != "web" || targets[0].replicas != 2 || targets[0].registry != "docker.io" {
		t.Fatalf("unexpected targets: %+v", targets)
	}
	if len(blocked) != 1 || blocked[0].Service != "api" || blocked[0].ReasonCode != "not_running" {
		t.Fatalf("unexpected blocked entries: %+v", blocked)
	}
	if targets, _ := planChecks(inventory, Params{Project: "shop"}); len(targets) != 0 {
		t.Fatalf("CD-managed services must not be targets: %+v", targets)
	}
	// A service whose containers drifted onto different images stays blocked.
	drifted := Inventory{Images: inventory.Images, Containers: []Usage{
		{Project: "blog", Service: "web", State: "running", ImageID: "sha256:aaa", Reference: "nginx:1.27", ContainerID: "c1"},
		{Project: "blog", Service: "web", State: "running", ImageID: "sha256:bbb", Reference: "nginx:1.27", ContainerID: "c2"},
	}}
	if targets, blocked := planChecks(drifted, Params{Project: "blog"}); len(targets) != 0 || len(blocked) != 1 || blocked[0].ReasonCode != "image_mismatch" {
		t.Fatalf("image drift must block the service: %+v %+v", targets, blocked)
	}
}

func TestPlanChecksContinuesFollowingDeclaredTagAfterPinnedUpdate(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	inventory := Inventory{Images: []LocalImage{{ID: "sha256:current", Platform: Platform{OS: "linux", Architecture: "amd64"}}}, Containers: []Usage{{Project: "blog", Service: "web", State: "running", ImageID: "sha256:current", Reference: "docker.io/library/nginx@" + digest}}}
	targets, blocked := planChecks(inventory, Params{Project: "blog"}, map[string]string{"web": "nginx:latest"})
	if len(targets) != 1 || len(blocked) != 0 || targets[0].reference != "docker.io/library/nginx:latest" {
		t.Fatalf("tag following stopped after an update: %+v %+v", targets, blocked)
	}
	targets, blocked = planChecks(inventory, Params{Project: "blog"}, map[string]string{"web": "nginx@" + digest})
	if len(targets) != 0 || len(blocked) != 1 || blocked[0].ReasonCode != "digest_pinned" {
		t.Fatalf("explicit digest must remain pinned: %+v %+v", targets, blocked)
	}
	targets, blocked = planChecks(inventory, Params{Project: "blog"}, map[string]string{"web": "redis:latest"})
	if len(targets) != 0 || len(blocked) != 1 || blocked[0].ReasonCode != "image_mismatch" {
		t.Fatalf("repository drift must be blocked: %+v %+v", targets, blocked)
	}
}

type fakeResolver struct {
	mu     sync.Mutex
	digest map[string]string // reference -> config digest
}

func (r *fakeResolver) Resolve(ctx context.Context, reference string, platform Platform, material credential.RegistryMaterial) (Remote, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	digest, ok := r.digest[reference]
	if !ok {
		return Remote{}, &LookupError{Code: "reference_not_found"}
	}
	return Remote{ManifestDigest: digest, ConfigDigest: digest}, nil
}

type fakeRuntime struct{ inventory Inventory }

func (r fakeRuntime) UpdateInventory(ctx context.Context) (Inventory, error) { return r.inventory, nil }

type fakeCredentials struct{}

func (fakeCredentials) AuthorizedForNode(ctx context.Context, id uint, nodeID string) error {
	return nil
}
func (fakeCredentials) Material(ctx context.Context, id uint) (credential.RegistryMaterial, error) {
	return credential.RegistryMaterial{}, nil
}
func (fakeCredentials) List(ctx context.Context) ([]database.RegistryCredential, error) {
	return nil, nil
}

func TestStartKindRecordsParamsResultAndArtifact(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &fakeResolver{digest: map[string]string{"docker.io/library/nginx:1.27": "sha256:new", "docker.io/library/redis:7": "sha256:redis"}}
	tasks := task.NewService(db)
	service := NewService(db, tasks, Dependencies{
		Node: func(ctx context.Context, id string) (Node, error) {
			return Node{ID: "edge", Name: "Edge", Enabled: true}, nil
		},
		Runtime: func(ctx context.Context, id string) (Runtime, error) {
			return fakeRuntime{inventory: Inventory{
				Images: []LocalImage{
					{ID: "sha256:old", Platform: Platform{OS: "linux", Architecture: "amd64"}},
					{ID: "sha256:redis", Platform: Platform{OS: "linux", Architecture: "amd64"}},
				},
				Containers: []Usage{
					{Project: "blog", Service: "web", State: "running", ImageID: "sha256:old", Reference: "nginx:1.27", ContainerID: "c1"},
					{Project: "blog", Service: "cache", State: "running", ImageID: "sha256:redis", Reference: "redis:7", ContainerID: "c2"},
				},
			}}, nil
		},
		Resolver:    resolver,
		Credentials: fakeCredentials{},
	})
	row, err := service.StartKind(context.Background(), taskcenter.StartRequest{
		NodeID: "edge", NodeName: "Edge",
		Params: json.RawMessage(`{"project":"blog","auto_apply":false}`), Reason: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.Wait(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	var stored database.Task
	if err := db.First(&stored, "id = ?", row).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "success" {
		t.Fatalf("check must succeed: %s %s", stored.Status, stored.Message)
	}
	var params Params
	if json.Unmarshal(stored.Params, &params) != nil || params.Project != "blog" || params.AutoApply {
		t.Fatalf("input params must be recorded verbatim: %s", string(stored.Params))
	}
	var summary Summary
	if json.Unmarshal(stored.Result, &summary) != nil || summary.Project != "blog" || len(summary.Services) != 2 {
		t.Fatalf("structured result must be recorded: %s", string(stored.Result))
	}
	byService := map[string]ServiceCheck{}
	for _, item := range summary.Services {
		byService[item.Service] = item
	}
	if item := byService["web"]; item.Status != StatusUpdateAvailable || item.RemoteConfigDigest != "sha256:new" {
		t.Fatalf("unexpected web result: %+v", item)
	}
	if item := byService["cache"]; item.Status != StatusCurrent {
		t.Fatalf("unexpected cache result: %+v", item)
	}
	var artifact database.TaskCenterArtifact
	if err := db.First(&artifact, "task_id = ?", row).Error; err != nil {
		t.Fatal("check outcome must be stored as artifact", err)
	}
	if artifact.Subject != "blog" || !strings.Contains(string(artifact.Data), "update_available") {
		t.Fatalf("unexpected artifact: %+v", artifact)
	}
	views, err := service.View(context.Background(), "edge", "")
	if err != nil || len(views) != 1 || views[0].Project != "blog" || views[0].LastTaskStatus != "success" || views[0].LastResult == nil {
		t.Fatalf("view must assemble the latest outcome: %+v %v", views, err)
	}
}

func TestStartKindRejectsConcurrentRunsForSameProject(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	service := NewService(db, tasks, Dependencies{
		Node: func(ctx context.Context, id string) (Node, error) {
			return Node{ID: "edge", Name: "Edge", Enabled: true}, nil
		},
	})
	release := make(chan struct{})
	blocked := &fakeRuntime{}
	service.deps.Runtime = func(ctx context.Context, id string) (Runtime, error) {
		<-release
		return blocked, nil
	}
	first, err := service.StartKind(context.Background(), taskcenter.StartRequest{NodeID: "edge", Params: json.RawMessage(`{"project":"blog"}`), Reason: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.StartKind(context.Background(), taskcenter.StartRequest{NodeID: "edge", Params: json.RawMessage(`{"project":"blog"}`), Reason: "manual"})
	var busy *BusyError
	if !errors.As(err, &busy) {
		t.Fatalf("second run must be rejected as busy, got %v", err)
	}
	close(release)
	tasks.Wait(context.Background(), first)
}

func TestFailingLookupsFailTheTaskWithPartialEvidence(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	service := NewService(db, tasks, Dependencies{
		Node: func(ctx context.Context, id string) (Node, error) {
			return Node{ID: "edge", Name: "Edge", Enabled: true}, nil
		},
		Runtime: func(ctx context.Context, id string) (Runtime, error) {
			return fakeRuntime{inventory: Inventory{
				Images: []LocalImage{{ID: "sha256:old", Platform: Platform{OS: "linux", Architecture: "amd64"}}},
				Containers: []Usage{
					{Project: "blog", Service: "web", State: "running", ImageID: "sha256:old", Reference: "nginx:1.27", ContainerID: "c1"},
				},
			}}, nil
		},
		Resolver:    &fakeResolver{digest: map[string]string{}},
		Credentials: fakeCredentials{},
	})
	row, err := service.StartKind(context.Background(), taskcenter.StartRequest{NodeID: "edge", Params: json.RawMessage(`{"project":"blog"}`), Reason: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.Wait(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	stored, err := tasks.Get(context.Background(), row)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "failed" {
		t.Fatalf("unavailable registry must fail the task: %+v", stored)
	}
	var summary Summary
	if json.Unmarshal(stored.Result, &summary) != nil || len(summary.Services) != 1 || summary.Services[0].ReasonCode != "reference_not_found" {
		t.Fatalf("partial evidence must survive: %s", string(stored.Result))
	}
}
