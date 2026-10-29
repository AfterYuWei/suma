package imageupdate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/compose"
	containerdomain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/task"
)

type projectContainers struct {
	containerdomain.Service
	runtime *fakeRuntime
}

func (c projectContainers) List(context.Context) ([]containerdomain.Summary, error) {
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	rows := []containerdomain.Summary{}
	for _, u := range c.runtime.inventory.Containers {
		rows = append(rows, containerdomain.Summary{ID: u.ContainerID, Name: u.ContainerName, Image: u.Reference, State: u.State, Labels: map[string]string{compose.ProjectLabel: u.Project, compose.ServiceLabel: u.Service}})
	}
	return rows, nil
}

type projectRunner struct {
	compose.Runner
	pulls, ups atomic.Int32
	onPull     func()
	onUp       func()
}

func (r *projectRunner) Render(context.Context, compose.ExecutionSpec, io.Writer) (string, error) {
	return `{"services":{"web":{"image":"example/app:latest"}}}`, nil
}
func (r *projectRunner) ValidateRelease(context.Context, compose.ExecutionSpec, io.Writer) error {
	return nil
}
func (r *projectRunner) PullRelease(_ context.Context, spec compose.ExecutionSpec, _ io.Writer) error {
	r.pulls.Add(1)
	if len(spec.Services) != 1 || spec.Services[0] != "web" {
		return errors.New("wrong service scope")
	}
	if r.onPull != nil {
		r.onPull()
	}
	return nil
}
func (r *projectRunner) UpRelease(context.Context, compose.ExecutionSpec, int, io.Writer) error {
	r.ups.Add(1)
	if r.onUp != nil {
		r.onUp()
	}
	return nil
}
func projectFixture(t *testing.T) (*Service, *fakeRuntime, *projectRunner, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	version := &atomic.Int32{}
	version.Store(1)
	service, runtime, db := testService(t, resolverFunc(func(context.Context, string, Platform, credential.RegistryMaterial) (Remote, error) {
		digit := "b"
		if version.Load() == 2 {
			digit = "c"
		}
		value := "sha256:" + strings.Repeat(digit, 64)
		return Remote{ManifestDigest: value, ConfigDigest: value}, nil
	}))
	runner := &projectRunner{}
	base, err := compose.NewService(db, t.TempDir(), runner, service.tasks, projectContainers{runtime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.Create(context.Background(), "shop", "services:\n  web:\n    image: example/app:latest\n", ""); err != nil {
		t.Fatal(err)
	}
	service.deps.Compose = func(context.Context, string) (*compose.Service, error) { return base, nil }
	events := &atomic.Int32{}
	service.deps.Emit = func(e event.Event) {
		if e.CandidateID != "" {
			events.Add(1)
		}
	}
	runner.onUp = func() {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		id := "sha256:" + strings.Repeat("b", 64)
		if version.Load() == 2 {
			id = "sha256:" + strings.Repeat("c", 64)
		}
		runtime.inventory.Images = []LocalImage{{ID: id, Tags: []string{"example/app:latest"}, Platform: Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}}}
		for i := range runtime.inventory.Containers {
			runtime.inventory.Containers[i].ImageID = id
		}
	}
	return service, runtime, runner, version, events
}
func checkedCandidate(t *testing.T, s *Service) *Candidate {
	t.Helper()
	row, err := s.Check(context.Background(), "local", CheckInput{ProjectName: "shop"}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitCheck(t, s, row); result.Status != task.StatusSuccess {
		t.Fatal(result.Message)
	}
	candidate, err := s.Candidate(context.Background(), "local", "shop", "")
	if err != nil || candidate == nil {
		t.Fatal(candidate, err)
	}
	return candidate
}
func awaitNewCandidate(t *testing.T, s *Service, previous string) *Candidate {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		row, err := s.Candidate(context.Background(), "local", "shop", "")
		if err == nil && row != nil && row.ID != previous && row.Status == "pending" {
			return row
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("replacement candidate missing")
	return nil
}
func TestProjectCandidateVersionDriftSupersedesWithoutPullOrRebuild(t *testing.T) {
	s, _, runner, version, events := projectFixture(t)
	candidate := checkedCandidate(t, s)
	again := checkedCandidate(t, s)
	if candidate.ID != again.ID || events.Load() != 1 {
		t.Fatal("identical checks duplicated candidate notifications")
	}
	version.Store(2)
	global, err := s.Check(context.Background(), "local", CheckInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	awaitCheck(t, s, global)
	unchanged, _ := s.Candidate(context.Background(), "local", "shop", "")
	if unchanged.ID != candidate.ID || events.Load() != 1 {
		t.Fatal("global check changed the project")
	}
	published, err := s.Publish(context.Background(), "local", "shop", candidate.ID, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitCheck(t, s, published); result.Status != task.StatusCanceled {
		t.Fatal("superseded publication was not canceled", result)
	}
	replacement := awaitNewCandidate(t, s, candidate.ID)
	if replacement.Services[0].ManifestDigest == candidate.Services[0].ManifestDigest || runner.pulls.Load() != 0 || runner.ups.Load() != 0 || events.Load() != 2 {
		t.Fatal("version drift executed or lost its replacement notification")
	}
	old, _ := s.Candidate(context.Background(), "local", "shop", candidate.ID)
	if old.Status != "superseded" {
		t.Fatal(old.Status)
	}
}
func TestProjectCandidateRechecksAfterPullAndKeepsTheSourceConfiguration(t *testing.T) {
	s, _, runner, version, _ := projectFixture(t)
	candidate := checkedCandidate(t, s)
	runner.onPull = func() { version.Store(2) }
	row, err := s.Publish(context.Background(), "local", "shop", candidate.ID, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	awaitCheck(t, s, row)
	awaitNewCandidate(t, s, candidate.ID)
	if runner.pulls.Load() != 1 || runner.ups.Load() != 0 {
		t.Fatal("changed version was recreated")
	}
	base, _ := s.deps.Compose(context.Background(), "local")
	project, err := base.Get(context.Background(), "shop")
	if err != nil || strings.Contains(project.Compose, "@sha256:") {
		t.Fatal("publication modified saved Compose", err)
	}
}
func TestProjectCandidatePublishesOnceAndPreservesEvidence(t *testing.T) {
	s, _, runner, _, _ := projectFixture(t)
	candidate := checkedCandidate(t, s)
	row, err := s.Publish(context.Background(), "local", "shop", candidate.ID, Actor{Source: "telegram", ExternalID: "123", ChatID: "456"})
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitCheck(t, s, row); result.Status != task.StatusSuccess {
		t.Fatal(result.Message)
	}
	saved, err := s.Candidate(context.Background(), "local", "shop", candidate.ID)
	if err != nil || saved.Status != "succeeded" || saved.TaskID != row.ID || runner.ups.Load() != 1 {
		t.Fatal(saved, err)
	}
	var actor Actor
	if err := json.Unmarshal([]byte(saved.ActorJSON), &actor); err != nil || actor.ExternalID != "123" {
		t.Fatal("publisher evidence missing", err)
	}
	if _, err := s.Publish(context.Background(), "local", "shop", candidate.ID, Actor{}); !errors.Is(err, ErrConflict) {
		t.Fatal("completed candidate was published again", err)
	}
}

func TestProjectScheduleCustomIsIndependentAndManualChecksDoNotMoveIt(t *testing.T) {
	s, _, _, _, _ := projectFixture(t)
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	s.deps.Now = func() time.Time { return now }
	s.deps.Nodes = func(context.Context) ([]string, error) { return []string{"local"}, nil }
	s.deps.Projects = func(ctx context.Context, _ string) ([]compose.Project, error) {
		base, _ := s.deps.Compose(ctx, "local")
		return base.List(ctx)
	}
	policy, err := s.SaveProjectPolicy(context.Background(), "local", "shop", ProjectPolicyInput{Mode: "custom", Cron: "*/5 * * * *", Timezone: "Asia/Shanghai"}, Actor{})
	if err != nil || policy.NextRunAt == nil {
		t.Fatal(policy, err)
	}
	next := *policy.NextRunAt
	checkedCandidate(t, s)
	after, err := s.ProjectPolicy(context.Background(), "local", "shop")
	if err != nil || !after.NextRunAt.Equal(next) {
		t.Fatal("manual check moved schedule", err)
	}
	now = next
	s.ScanDue(context.Background())
	s.mu.Lock()
	id := s.running["local"]
	s.mu.Unlock()
	if id == "" {
		t.Fatal("custom project schedule depended on the disabled global switch")
	}
	if err := s.tasks.Wait(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	global, err := s.View(context.Background(), "local", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range global.Results {
		if row.CheckedAt != nil {
			t.Fatal("project schedule changed global results")
		}
	}
	after, err = s.ProjectPolicy(context.Background(), "local", "shop")
	if err != nil || after.NextRunAt == nil || !after.NextRunAt.After(now) {
		t.Fatal("schedule not advanced", err)
	}
	if _, err := s.SaveProjectPolicy(context.Background(), "local", "shop", ProjectPolicyInput{Mode: "disabled", Cron: policy.Cron, Timezone: policy.Timezone}, Actor{}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale policy overwrote current policy", err)
	}
}
