package cleanup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"github.com/suma/suma/server/internal/task"
)

// Explicit volume deletion still uses protection references and fresh usage.
func (s *Service) ReviewVolumeDeletion(ctx context.Context, nodeID, name string) (Resource, error) {
	p, err := s.policy(ctx, nodeID)
	if err != nil {
		return Resource{}, err
	}
	runtime, err := s.deps.Runtime(ctx, nodeID)
	if err != nil {
		return Resource{}, err
	}
	refs, err := s.deps.Protection(ctx, nodeID)
	if err != nil {
		return Resource{}, err
	}
	volume, err := runtime.CleanupResource(ctx, Volume, name)
	if err != nil {
		return Resource{}, err
	}
	if volume.InUse {
		return Resource{}, ErrInUse
	}
	if reason := protected(volume, p, refs); reason != "" {
		return Resource{}, errors.New("volume is protected: " + reason)
	}
	return volume, nil
}

type ReviewedCache struct {
	RuntimeFingerprint string    `json:"runtime_fingerprint"`
	PolicyVersion      uint64    `json:"policy_version"`
	Until              time.Time `json:"until"`
	ReservedBytes      int64     `json:"reserved_bytes"`
	Candidates         []string  `json:"candidates"`
}

func (s *Service) ReviewCache(ctx context.Context, nodeID string, until time.Time, reserved int64) (ReviewedCache, error) {
	if until.IsZero() || until.After(s.now()) || reserved < 0 {
		return ReviewedCache{}, ErrInvalid
	}
	p, err := s.policy(ctx, nodeID)
	if err != nil {
		return ReviewedCache{}, err
	}
	node, err := s.deps.Node(ctx, nodeID)
	if err != nil || !node.Enabled {
		return ReviewedCache{}, ErrUnavailable
	}
	runtime, err := s.deps.Runtime(ctx, nodeID)
	if err != nil {
		return ReviewedCache{}, err
	}
	inventory, err := runtime.CleanupInventory(ctx)
	if err != nil {
		return ReviewedCache{}, err
	}
	if !inventory.Capabilities.BuildCache {
		return ReviewedCache{}, ErrUnavailable
	}
	runtimeHash := sha256.Sum256([]byte(node.RuntimeKey))
	out := ReviewedCache{RuntimeFingerprint: hex.EncodeToString(runtimeHash[:]), PolicyVersion: p.Version, Until: until, ReservedBytes: reserved, Candidates: []string{}}
	for _, resource := range inventory.Resources {
		if resource.Kind == Cache && !resource.InUse && resource.LastUsedAt != nil && resource.LastUsedAt.Before(until) {
			out.Candidates = append(out.Candidates, resource.ID)
		}
	}
	sort.Strings(out.Candidates)
	return out, nil
}
func (s *Service) ApplyCacheReviewed(ctx context.Context, nodeID string, review ReviewedCache, report task.Reporter) error {
	s.mu.Lock()
	if s.active[nodeID] != "" || s.stopped {
		s.mu.Unlock()
		return ErrConflict
	}
	s.active[nodeID] = "reviewed-cache"
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.active, nodeID); s.mu.Unlock() }()
	current, err := s.ReviewCache(ctx, nodeID, review.Until, review.ReservedBytes)
	if err != nil || current.RuntimeFingerprint != review.RuntimeFingerprint || current.PolicyVersion != review.PolicyVersion {
		return ErrConflict
	}
	allowed := map[string]bool{}
	for _, candidate := range review.Candidates {
		allowed[candidate] = true
	}
	for _, candidate := range current.Candidates {
		if !allowed[candidate] {
			return ErrConflict
		}
	}
	runtime, err := s.deps.Runtime(ctx, nodeID)
	if err != nil {
		return err
	}
	result, err := runtime.CleanupPruneCache(ctx, CacheOptions{Until: review.Until, ReservedBytes: review.ReservedBytes})
	if err != nil {
		return err
	}
	report(95, "Reviewed build cache pruning completed")
	for _, deleted := range result.Deleted {
		if !allowed[deleted] {
			return errors.New("Engine cache prune returned an unexpected cache ID; verify actual state")
		}
	}
	return nil
}
