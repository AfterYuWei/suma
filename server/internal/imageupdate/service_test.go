package imageupdate

import (
	"context"
	"errors"
	"fmt"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRuntime struct {
	mu        sync.Mutex
	inventory Inventory
}

func (r *fakeRuntime) UpdateInventory(context.Context) (Inventory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inventory, nil
}

type resolverFunc func(context.Context, string, Platform, credential.RegistryMaterial) (Remote, error)

func (f resolverFunc) Resolve(ctx context.Context, r string, p Platform, m credential.RegistryMaterial) (Remote, error) {
	return f(ctx, r, p, m)
}
func testService(t *testing.T, resolver Resolver) (*Service, *fakeRuntime, *gorm.DB) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "checks.db"))
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	sql.SetMaxOpenConns(1)
	runtime := &fakeRuntime{inventory: Inventory{Images: []LocalImage{{ID: "old", Tags: []string{"example/app:latest", "example/app:stable"}, Platform: Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}}}, Containers: []Usage{{ContainerID: "container", ContainerName: "web", Project: "shop", Service: "web", ImageID: "old", Reference: "example/app:latest", State: "running"}}}}
	service := NewService(db, task.NewService(db), audit.NewService(db), Dependencies{Node: func(_ context.Context, id string) (Node, error) {
		if id == "missing" {
			return Node{}, gorm.ErrRecordNotFound
		}
		return Node{ID: id, Name: id, Enabled: true, Available: true, RuntimeKey: "engine"}, nil
	}, Runtime: func(context.Context, string) (Runtime, error) { return runtime, nil }, Resolver: resolver})
	t.Cleanup(service.Stop)
	return service, runtime, db
}
func awaitCheck(t *testing.T, s *Service, row database.Task) database.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.tasks.GetForNode(context.Background(), row.NodeID, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status == task.StatusSuccess || r.Status == task.StatusFailed || r.Status == task.StatusCanceled {
			return r
		}
		time.Sleep(time.Millisecond * 10)
	}
	t.Fatal("task did not finish")
	return database.Task{}
}
func TestPlatformChecksOldContainersAndPull(t *testing.T) {
	var calls atomic.Int32
	s, r, _ := testService(t, resolverFunc(func(_ context.Context, ref string, p Platform, _ credential.RegistryMaterial) (Remote, error) {
		calls.Add(1)
		if p.String() != "linux/arm64/v8" {
			t.Errorf("wrong platform: %v", p)
		}
		return Remote{ConfigDigest: "new", ManifestDigest: "manifest"}, nil
	}))
	row, err := s.Check(context.Background(), "local", CheckInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if task := awaitCheck(t, s, row); task.Status != "success" {
		t.Fatal(task)
	}
	view, err := s.View(context.Background(), "local", "shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Results) != 1 || !view.Results[0].PullRequired || !view.Results[0].RecreateRequired || calls.Load() != 2 {
		t.Fatalf("initial: %+v calls=%d", view, calls.Load())
	}
	r.mu.Lock()
	r.inventory.Images[0].Tags = nil
	r.inventory.Images = append(r.inventory.Images, LocalImage{ID: "new", Tags: []string{"example/app:latest"}, Platform: Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}})
	r.mu.Unlock()
	view, err = s.View(context.Background(), "local", "shop")
	if err != nil {
		t.Fatal(err)
	}
	if view.Results[0].PullRequired || !view.Results[0].RecreateRequired {
		t.Fatalf("pulled but container old: %+v", view)
	}
	r.mu.Lock()
	r.inventory.Containers[0].ImageID = "new"
	r.mu.Unlock()
	view, _ = s.View(context.Background(), "local", "shop")
	if view.Results[0].Status != "unchecked" {
		t.Fatalf("old result reused: %+v", view)
	}
}
func TestPartialErrorsPinnedAndNoTag(t *testing.T) {
	s, r, _ := testService(t, resolverFunc(func(_ context.Context, ref string, _ Platform, _ credential.RegistryMaterial) (Remote, error) {
		if strings.HasSuffix(ref, ":stable") {
			return Remote{}, &LookupError{Code: "rate_limited"}
		}
		return Remote{ConfigDigest: "old"}, nil
	}))
	r.mu.Lock()
	r.inventory.Images = append(r.inventory.Images, LocalImage{ID: "empty"}, LocalImage{ID: "pinned", Tags: []string{"example/app@sha256:" + strings.Repeat("ab", 32)}})
	r.mu.Unlock()
	row, err := s.Check(context.Background(), "local", CheckInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if awaitCheck(t, s, row).Status != "failed" {
		t.Fatal("partial failure not reported")
	}
	view, _ := s.View(context.Background(), "local", "")
	statuses := map[string]bool{}
	for _, r := range view.Results {
		statuses[r.Status+"/"+r.ReasonCode] = true
	}
	for _, expected := range []string{"current/", "unavailable/rate_limited", "pinned/digest_pinned", "unavailable/untagged"} {
		if !statuses[expected] {
			t.Fatalf("missing %s: %+v", expected, view)
		}
	}
}
func TestMutualExclusionCancellationAndGlobalLimit(t *testing.T) {
	var active, maximum atomic.Int32
	entered := make(chan struct{}, 20)
	s, _, _ := testService(t, resolverFunc(func(ctx context.Context, _ string, _ Platform, _ credential.RegistryMaterial) (Remote, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		<-ctx.Done()
		return Remote{}, ctx.Err()
	}))
	rows := []database.Task{}
	for _, id := range []string{"a", "b", "c"} {
		r, err := s.Check(context.Background(), id, CheckInput{}, Actor{})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, r)
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("no lookup")
		}
	}
	_, err := s.Check(context.Background(), "a", CheckInput{}, Actor{})
	var busy *BusyError
	if !errors.As(err, &busy) || busy.TaskID != rows[0].ID {
		t.Fatalf("busy: %v", err)
	}
	for _, row := range rows {
		_, _ = s.tasks.CancelForNode(context.Background(), row.NodeID, row.ID)
		if awaitCheck(t, s, row).Status != "canceled" {
			t.Fatal("not canceled")
		}
	}
	if maximum.Load() > 4 {
		t.Fatalf("concurrency=%d", maximum.Load())
	}
}
func TestPolicyDefaultsVersionSchedulingAndRestart(t *testing.T) {
	s, _, db := testService(t, resolverFunc(func(context.Context, string, Platform, credential.RegistryMaterial) (Remote, error) {
		return Remote{ConfigDigest: "old"}, nil
	}))
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	s.deps.Now = func() time.Time { return now }
	p, err := s.Policy(context.Background(), "local")
	if err != nil || p.Enabled || p.IntervalHours != 6 || p.Version != 0 {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	p, err = s.UpdatePolicy(context.Background(), "local", PolicyInput{Enabled: true, IntervalHours: 6}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 1 || p.NextRunAt == nil || !p.NextRunAt.Equal(now.Add(6*time.Hour)) {
		t.Fatalf("immediate check schedule: %+v", p)
	}
	var rows []database.Task
	db.Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("enable tasks=%d", len(rows))
	}
	awaitCheck(t, s, rows[0])
	s.ScanDue(context.Background())
	db.Find(&rows)
	if len(rows) != 1 {
		t.Fatal("duplicate scheduled task")
	}
	_, err = s.UpdatePolicy(context.Background(), "local", PolicyInput{Enabled: false, IntervalHours: 24, ExpectedVersion: 0}, Actor{})
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	p, err = s.UpdatePolicy(context.Background(), "local", PolicyInput{Enabled: false, IntervalHours: 24, ExpectedVersion: 1}, Actor{})
	if err != nil || p.NextRunAt != nil {
		t.Fatalf("disable: %+v %v", p, err)
	}
	fresh := NewService(db, s.tasks, s.audit, s.deps)
	defer fresh.Stop()
	view, _ := fresh.View(context.Background(), "local", "")
	for _, r := range view.Results {
		if r.CheckedAt != nil {
			t.Fatal("observation persisted across restart")
		}
	}
}
func TestScopeAndRuntimeInvalidation(t *testing.T) {
	s, _, _ := testService(t, resolverFunc(func(context.Context, string, Platform, credential.RegistryMaterial) (Remote, error) {
		return Remote{ConfigDigest: "old"}, nil
	}))
	row, _ := s.Check(context.Background(), "local", CheckInput{ProjectName: "shop"}, Actor{})
	awaitCheck(t, s, row)
	view, _ := s.View(context.Background(), "local", "other")
	if len(view.Results) != 0 {
		t.Fatal("project isolation")
	}
	s.deps.Node = func(_ context.Context, id string) (Node, error) {
		return Node{ID: id, Enabled: true, Available: true, RuntimeKey: "different"}, nil
	}
	view, _ = s.View(context.Background(), "local", "shop")
	if view.Results[0].CheckedAt != nil {
		t.Fatal("runtime result reused")
	}
}

type fakeCredentials struct{ uses atomic.Int32 }

func (c *fakeCredentials) AuthorizedForNode(_ context.Context, id uint, node string) error {
	c.uses.Add(1)
	if id != 7 || node != "local" {
		return errors.New("unauthorized")
	}
	return nil
}
func (c *fakeCredentials) Material(context.Context, uint) (credential.RegistryMaterial, error) {
	return credential.RegistryMaterial{ServerAddress: "docker.io", Username: "user", Secret: "fixture", AuthType: "basic"}, nil
}
func TestManualAnonymousOverridesPolicyAndAuthorization(t *testing.T) {
	var authenticated atomic.Int32
	s, _, db := testService(t, resolverFunc(func(_ context.Context, _ string, _ Platform, m credential.RegistryMaterial) (Remote, error) {
		if m.Secret != "" {
			authenticated.Add(1)
		}
		return Remote{ConfigDigest: "old"}, nil
	}))
	creds := &fakeCredentials{}
	s.deps.Credentials = creds
	db.Create(&database.RegistryCredential{ID: 7, Name: "fixture", ServerAddress: "docker.io", AuthType: "basic"})
	db.Create(&database.RegistryCredentialNode{CredentialID: 7, NodeID: "local"})
	_, err := s.UpdatePolicy(context.Background(), "local", PolicyInput{IntervalHours: 6, RegistryCredentials: map[string]uint{"docker.io": 7}}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.Check(context.Background(), "local", CheckInput{RegistryCredentials: map[string]uint{}}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	awaitCheck(t, s, row)
	if authenticated.Load() != 0 || creds.uses.Load() != 0 {
		t.Fatal("explicit anonymous used policy credential")
	}
	row, err = s.Check(context.Background(), "local", CheckInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	awaitCheck(t, s, row)
	if authenticated.Load() != 2 {
		t.Fatal("schedule did not use credential mapping")
	}
	if _, err = s.Check(context.Background(), "local", CheckInput{RegistryCredentials: map[string]uint{"docker.io": 99}}, Actor{}); err == nil {
		t.Fatal("accepted unauthorized credential")
	}
	if _, err = s.Check(context.Background(), "local", CheckInput{RegistryCredentials: map[string]uint{"another.example": 7}}, Actor{}); err == nil {
		t.Fatal("accepted wrong registry")
	}
}
func TestSchedulerOfflineBusyRestartAndStale(t *testing.T) {
	s, _, db := testService(t, resolverFunc(func(context.Context, string, Platform, credential.RegistryMaterial) (Remote, error) {
		return Remote{ConfigDigest: "old"}, nil
	}))
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC).UnixNano())
	s.deps.Now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	var available atomic.Bool
	s.deps.Node = func(_ context.Context, id string) (Node, error) {
		return Node{ID: id, Name: id, Enabled: true, Available: available.Load(), RuntimeKey: "engine"}, nil
	}
	p, err := s.UpdatePolicy(context.Background(), "local", PolicyInput{Enabled: true, IntervalHours: 1}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&database.Task{}).Count(&count)
	if count != 0 {
		t.Fatal("offline cycle created task")
	}
	available.Store(true)
	clock.Add(int64(25 * time.Hour))
	s.ScanDue(context.Background())
	var rows []database.Task
	db.Find(&rows)
	if len(rows) != 1 {
		t.Fatal("due recovery replayed cycles")
	}
	awaitCheck(t, s, rows[0])
	s.ScanDue(context.Background())
	db.Model(&database.Task{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate due cycle")
	}
	clock.Add(int64(2 * time.Hour))
	view, err := s.View(context.Background(), "local", "")
	if err != nil || !view.Results[0].Stale {
		t.Fatal("expired result not marked stale")
	}
	p, _ = s.Policy(context.Background(), "local")
	scheduled := *p.NextRunAt
	row, err := s.Check(context.Background(), "local", CheckInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	awaitCheck(t, s, row)
	p, _ = s.Policy(context.Background(), "local")
	if !scheduled.Equal(*p.NextRunAt) {
		t.Fatal("manual check moved schedule")
	}
	// A busy due cycle moves to the next interval without allocating a task.
	entered := make(chan struct{}, 4)
	s.deps.Resolver = resolverFunc(func(ctx context.Context, _ string, _ Platform, _ credential.RegistryMaterial) (Remote, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return Remote{}, ctx.Err()
	})
	row, err = s.Check(context.Background(), "local", CheckInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	db.Model(&database.Task{}).Count(&count)
	before := count
	s.ScanDue(context.Background())
	db.Model(&database.Task{}).Count(&count)
	if count != before {
		t.Fatal("busy scheduler allocated a task")
	}
	_, _ = s.tasks.CancelForNode(context.Background(), "local", row.ID)
	awaitCheck(t, s, row)
	s.Stop()
	if _, err = s.Check(context.Background(), "local", CheckInput{}, Actor{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("closed service accepted work")
	}
}

func TestInvalidCheckIdentifiers(t *testing.T) {
	s, _, _ := testService(t, resolverFunc(func(context.Context, string, Platform, credential.RegistryMaterial) (Remote, error) {
		return Remote{}, nil
	}))
	for _, in := range []CheckInput{{ProjectName: "../shop"}, {ProjectName: "UPPER"}, {ImageIDs: []string{"../../image"}}, {ImageIDs: []string{"old"}, ProjectName: "shop"}} {
		if _, err := s.Check(context.Background(), "local", in, Actor{}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid scope: %+v", in)
		}
	}
}

func TestCacheIsGloballyBounded(t *testing.T) {
	s, _, _ := testService(t, resolverFunc(func(context.Context, string, Platform, credential.RegistryMaterial) (Remote, error) {
		return Remote{ConfigDigest: "old"}, nil
	}))
	s.cache["retired"] = map[string]cached{}
	for i := 0; i < 10000; i++ {
		s.cache["retired"][fmt.Sprint(i)] = cached{Result: Result{}}
	}
	row, err := s.Check(context.Background(), "local", CheckInput{}, Actor{})
	if err != nil {
		t.Fatal(err)
	}
	awaitCheck(t, s, row)
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, results := range s.cache {
		count += len(results)
	}
	if count > 10000 {
		t.Fatalf("unbounded cache: %d", count)
	}
}
