package cleanup

import (
	"context"
	"testing"
)

func TestReviewedCleanupOnlyFrozenCandidatesAndFreshProtection(t *testing.T) {
	f := &fakeRuntime{resources: []Resource{oldResource(Container, "selected"), oldResource(Container, "unselected"), oldResource(Image, "image"), oldResource(Volume, "volume")}, cacheSupported: true}
	s, actor := fixture(t, f)
	view, err := s.Get(context.Background(), "local", false)
	if err != nil {
		t.Fatal(err)
	}
	cfg := view.Policy.Config
	cfg.Containers.Enabled = true
	if _, err := s.Update(context.Background(), "local", Update{Config: cfg, Version: view.Policy.Version}, actor); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []Kind{Volume, Cache} {
		if _, err := s.ReviewCandidates(context.Background(), "local", []FrozenCandidate{{Kind: kind, ID: "volume"}}); err == nil {
			t.Fatal("AI deletion allowed", kind)
		}
	}
	review, err := s.ReviewCandidates(context.Background(), "local", []FrozenCandidate{{Kind: Container, ID: "selected"}})
	if err != nil {
		t.Fatal(err)
	}
	f.fresh = func(r Resource) Resource {
		if r.ID == "selected" {
			r.State = "running"
		}
		return r
	}
	if err := s.ApplyCandidatesReviewed(context.Background(), "local", review, "reviewed-task", actor, func(int, string) {}); err == nil {
		t.Fatal("running candidate accepted")
	}
	f.fresh = nil
	if err := s.ApplyCandidatesReviewed(context.Background(), "local", review, "reviewed-task", actor, func(int, string) {}); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 1 || f.removed[0].ID != "selected" || f.cacheCalls != 0 {
		t.Fatalf("expanded AI cleanup: %#v, caches %d", f.removed, f.cacheCalls)
	}
}
