package cleanup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

type FrozenCandidate struct {
	Kind Kind   `json:"kind"`
	ID   string `json:"id"`
}
type ReviewedCleanup struct {
	RuntimeFingerprint string            `json:"runtime_fingerprint"`
	PolicyVersion      uint64            `json:"policy_version"`
	Candidates         []FrozenCandidate `json:"candidates"`
}

func (s *Service) ReviewCandidates(ctx context.Context, nodeID string, candidates []FrozenCandidate) (ReviewedCleanup, error) {
	if len(candidates) == 0 || len(candidates) > 200 {
		return ReviewedCleanup{}, ErrInvalid
	}
	p, err := s.policy(ctx, nodeID)
	if err != nil {
		return ReviewedCleanup{}, err
	}
	node, err := s.deps.Node(ctx, nodeID)
	if err != nil || !node.Enabled {
		return ReviewedCleanup{}, ErrUnavailable
	}
	runtime, err := s.deps.Runtime(ctx, nodeID)
	if err != nil {
		return ReviewedCleanup{}, err
	}
	refs, err := s.deps.Protection(ctx, nodeID)
	if err != nil {
		return ReviewedCleanup{}, err
	}
	runtimeHash := sha256.Sum256([]byte(node.RuntimeKey))
	out := ReviewedCleanup{RuntimeFingerprint: hex.EncodeToString(runtimeHash[:]), PolicyVersion: p.Version, Candidates: append([]FrozenCandidate{}, candidates...)}
	seen := map[string]bool{}
	for _, c := range candidates {
		if (c.Kind != Container && c.Kind != Image && c.Kind != Network) || c.ID == "" || seen[string(c.Kind)+c.ID] {
			return ReviewedCleanup{}, ErrInvalid
		}
		seen[string(c.Kind)+c.ID] = true
		r, err := runtime.CleanupResource(ctx, c.Kind, c.ID)
		if err != nil {
			return ReviewedCleanup{}, err
		}
		r = evaluate(r, p, refs, s.now())
		if !r.Candidate {
			return ReviewedCleanup{}, errors.New("candidate is in use, protected or outside cleanup retention rules")
		}
	}
	sort.Slice(out.Candidates, func(i, j int) bool {
		return string(out.Candidates[i].Kind)+out.Candidates[i].ID < string(out.Candidates[j].Kind)+out.Candidates[j].ID
	})
	return out, nil
}
func (s *Service) ApplyCandidatesReviewed(ctx context.Context, nodeID string, review ReviewedCleanup, taskID string, actor Actor, report task.Reporter) error {
	s.mu.Lock()
	node, err := s.deps.Node(ctx, nodeID)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if err = s.ready(ctx, node); err != nil {
		s.mu.Unlock()
		return err
	}
	if s.active[nodeID] != "" || s.stopped {
		s.mu.Unlock()
		return ErrConflict
	}
	current, err := s.ReviewCandidates(ctx, nodeID, review.Candidates)
	if err != nil || current.PolicyVersion != review.PolicyVersion || current.RuntimeFingerprint != review.RuntimeFingerprint {
		s.mu.Unlock()
		return ErrConflict
	}
	p, err := s.policy(ctx, nodeID)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	p.Cache.Enabled = false
	p.ScanVolumes = false
	preview, err := s.inventory(ctx, nodeID, p)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	frozen := []Resource{}
	for _, candidate := range review.Candidates {
		for _, r := range preview.Resources {
			if candidate.Kind == r.Kind && candidate.ID == r.ID {
				frozen = append(frozen, r)
			}
		}
	}
	if len(frozen) != len(review.Candidates) {
		s.mu.Unlock()
		return ErrConflict
	}
	preview.Resources = frozen
	run := database.CleanupRun{ID: id(), NodeID: nodeID, NodeName: node.Name, TaskID: taskID, PolicyVersion: p.Version, PolicyJSON: encode(p.Config), Trigger: "ai_reviewed", UserID: actor.UserID, Status: "pending"}
	if err = s.db.Create(&run).Error; err != nil {
		s.mu.Unlock()
		return err
	}
	s.active[nodeID] = run.ID
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.active, nodeID); s.mu.Unlock() }()
	return s.execute(ctx, node, preview, run, actor, report)
}
