package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/cleanup"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

func (r aiRuntime) freezeCache(ctx context.Context, nodeID string, req ai.OperationRequest, snap ai.Snapshot) (ai.Snapshot, error) {
	var params struct {
		Until         time.Time `json:"until"`
		ReservedBytes int64     `json:"reserved_bytes"`
	}
	if decodeParams(req.Parameters, &params) != nil || req.ResourceID != nodeID {
		return snap, ai.ErrInvalid
	}
	review, err := r.cleanup.ReviewCache(ctx, nodeID, params.Until, params.ReservedBytes)
	if err != nil {
		return snap, err
	}
	snap.Details = json.RawMessage(jsonText(review))
	snap.Fingerprint = stateHash(review)
	snap.Impact = "Prunes unused build cache only, using a fixed cutoff and reserved storage limit. Engine may retain candidates. Deleted cache requires rebuilding and is not automatically restored."
	node, err := r.nodes.Get(ctx, nodeID)
	if err != nil {
		return snap, err
	}
	snap.Confirmations = []ai.Confirmation{{Key: "cache", Label: "Confirm permanent build cache cleanup", Checkbox: true}, {Key: "name", Label: "Type the node name to confirm cleanup", Expected: node.Name}}
	return snap, nil
}
func (r aiRuntime) executeCache(ctx context.Context, row database.AIOperation, snap ai.Snapshot, report task.Reporter) error {
	var review cleanup.ReviewedCache
	if json.Unmarshal(snap.Details, &review) != nil {
		return ai.ErrInvalid
	}
	return r.cleanup.ApplyCacheReviewed(ctx, row.NodeID, review, report)
}
func (r aiRuntime) verifyCleanup(ctx context.Context, row database.AIOperation, snap ai.Snapshot) (ai.Verification, error) {
	runtime, err := r.nodes.Runtime(ctx, row.NodeID)
	if err != nil {
		return ai.Verification{}, err
	}
	out := ai.Verification{Satisfied: true, Evidence: []ai.Evidence{}}
	if row.Action == "cleanup.cache" {
		inventory, err := runtime.CleanupInventory(ctx)
		if err != nil {
			return out, err
		}
		out.Summary = "Engine cache prune completed; inspected actual cache inventory and storage. Engine retention rules may preserve candidates."
		out.Evidence = append(out.Evidence, ai.Evidence{NodeID: row.NodeID, Source: "verify", Resource: "build-cache", Time: time.Now().UTC(), Content: jsonText(inventory)})
		return out, nil
	}
	var review cleanup.ReviewedCleanup
	if json.Unmarshal(snap.Details, &review) != nil {
		return out, ai.ErrInvalid
	}
	remaining := []cleanup.FrozenCandidate{}
	for _, candidate := range review.Candidates {
		_, err := runtime.CleanupResource(ctx, candidate.Kind, candidate.ID)
		if err == nil {
			remaining = append(remaining, candidate)
			out.Satisfied = false
		} else if err != cleanup.ErrGone {
			return out, err
		}
	}
	out.Summary = "Checked existence of all frozen cleanup candidates"
	out.Evidence = append(out.Evidence, ai.Evidence{NodeID: row.NodeID, Source: "verify", Resource: "cleanup", Time: time.Now().UTC(), Content: jsonText(map[string]any{"remaining": remaining})})
	return out, nil
}
