package imageupdate

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/event"
)

func TestCronPreviewValidationAndTimezone(t *testing.T) {
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	rows, err := PreviewSchedule(ScheduleInput{"30 9 * * 1-5", "Asia/Shanghai"}, now)
	if err != nil || len(rows) != 5 || rows[0].Format(time.RFC3339) != "2026-10-29T01:30:00Z" {
		t.Fatal(rows, err)
	}
	for _, in := range []ScheduleInput{{"0 0 0 * * *", "UTC"}, {"*/0 * * * *", "UTC"}, {"0 0 31 2 *", "UTC"}, {"* * * * *", "Local"}, {"* * * * *", "invalid/zone"}} {
		if _, err := PreviewSchedule(in, now); err == nil {
			t.Fatal("accepted invalid schedule", in)
		}
	}
	rows, err = PreviewSchedule(ScheduleInput{"30 2 * * *", "America/New_York"}, time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC))
	if err != nil || rows[0].Day() != 9 {
		t.Fatal("nonexistent DST time not skipped", rows, err)
	}
}
func TestSelectedServicesPreserveStoppedAndMixedReplicas(t *testing.T) {
	r := Result{Reference: "docker.io/team/app:latest", RemoteManifestDigest: "sha256:new", RemoteConfigDigest: "new", Status: "update_available", Platform: Platform{OS: "linux", Architecture: "amd64"}, Containers: []Usage{
		{Service: "web", State: "running", ImageID: "old"}, {Service: "web", State: "running", ImageID: "old"},
		{Service: "stopped", State: "exited", ImageID: "old"}, {Service: "mixed", State: "running", ImageID: "old"}, {Service: "mixed", State: "exited", ImageID: "old"},
		{Service: "paused", State: "paused", ImageID: "old"}, {Service: "job", State: "running", ImageID: "old", OneOff: true},
	}}
	selected, blocked := selectedServices([]Result{r})
	if len(selected) != 1 || selected[0].Name != "web" || selected[0].Replicas != 2 || blocked != 3 {
		t.Fatal(selected, blocked)
	}
}
func TestBatchLookupDeduplicatesButVerificationIsFresh(t *testing.T) {
	var calls atomic.Int32
	service := &Service{slots: make(chan struct{}, 4), deps: Dependencies{Resolver: resolverFunc(func(context.Context, string, Platform, credential.RegistryMaterial) (Remote, error) {
		calls.Add(1)
		time.Sleep(time.Millisecond)
		return Remote{ManifestDigest: "manifest", ConfigDigest: "config"}, nil
	})}}
	batch := &lookupBatch{entries: map[string]*lookupEntry{}}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.resolve(context.Background(), "app:latest", Platform{OS: "linux", Architecture: "amd64"}, credential.RegistryMaterial{}, batch); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicate metadata lookups", calls.Load())
	}
	if _, err := service.resolve(context.Background(), "app:latest", Platform{OS: "linux", Architecture: "amd64"}, credential.RegistryMaterial{}, &lookupBatch{entries: map[string]*lookupEntry{}}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("verification reused stale metadata")
	}
}
func TestGlobalChecksNeverEmitAndDoNotPopulateProjectResults(t *testing.T) {
	service, _, _ := testService(t, resolverFunc(func(context.Context, string, Platform, credential.RegistryMaterial) (Remote, error) {
		return Remote{ManifestDigest: "digest", ConfigDigest: "new"}, nil
	}))
	var count atomic.Int32
	service.deps.Emit = func(event.Event) { count.Add(1) }
	for _, fail := range []bool{false, true} {
		service.deps.Resolver = resolverFunc(func(context.Context, string, Platform, credential.RegistryMaterial) (Remote, error) {
			if fail {
				return Remote{}, errors.New("unreachable")
			}
			return Remote{ManifestDigest: "digest", ConfigDigest: "new"}, nil
		})
		row, err := service.Check(context.Background(), "local", CheckInput{}, Actor{})
		if err != nil {
			t.Fatal(err)
		}
		awaitCheck(t, service, row)
	}
	if count.Load() != 0 {
		t.Fatal("global check emitted notifications")
	}
	view, err := service.View(context.Background(), "local", "shop")
	if err != nil || len(view.Results) != 1 || view.Results[0].CheckedAt != nil {
		t.Fatal("global results leaked into a project", view, err)
	}
}
