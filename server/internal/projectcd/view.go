package projectcd

import (
	"context"
	"encoding/json"
	"io"
	"sort"
	"time"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/deployment"
)

// ProjectView projects the Git declaration into the existing node Project API;
// Docker still supplies container counts, resource state and runtime health.
func (s *Service) ProjectView(ctx context.Context, node, name string) (compose.ProjectDeployment, error) {
	preview, err := s.Preview(ctx, node, name)
	if err != nil {
		row, lookupErr := s.row(ctx, node, name)
		if lookupErr != nil {
			return compose.ProjectDeployment{}, lookupErr
		}
		if row.CandidateRecordID == nil && row.AppliedRecordID == nil {
			return compose.ProjectDeployment{}, nil
		}
		return compose.ProjectDeployment{}, err
	}
	var document struct{ Services map[string]json.RawMessage }
	if err := json.Unmarshal([]byte(preview.Compose), &document); err != nil {
		return compose.ProjectDeployment{}, err
	}
	names := make([]string, 0, len(document.Services))
	for name := range document.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return compose.ProjectDeployment{Compose: preview.Compose, DeclaredServiceNames: names}, nil
}

type Drift struct {
	Status    string    `json:"status"`
	Reason    string    `json:"reason"`
	CheckedAt time.Time `json:"checked_at"`
}

func (s *Service) Drift(ctx context.Context, node, name string) (Drift, error) {
	result := Drift{Status: "unknown", Reason: "runtime_unavailable", CheckedAt: time.Now().UTC()}
	row, err := s.row(ctx, node, name)
	if err != nil {
		return result, err
	}
	if row.AppliedRecordID == nil {
		result.Reason = "pending_application"
		return result, nil
	}
	if s.deps.Inspect == nil {
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	record, err := s.record(ctx, row, *row.AppliedRecordID)
	if err != nil {
		return result, err
	}
	value, err := s.decode(record)
	if err != nil {
		return result, err
	}
	target, err := s.deps.Target(ctx, node)
	if err != nil || target.RuntimeIdentity != value.RuntimeIdentity {
		return result, nil
	}
	spec, err := deployment.Spec(name, value.Worktree, value.Repository)
	if err != nil {
		return result, err
	}
	spec.Environment = value.Environment
	runner := s.runner.ForTarget(target)
	hashes, err := runner.Hashes(ctx, spec, io.Discard)
	if err != nil {
		return result, nil
	}
	runtime, err := s.deps.Inspect(ctx, node, name)
	if err != nil {
		return result, nil
	}
	health, err := runner.PS(ctx, spec, io.Discard)
	if err != nil {
		return result, nil
	}
	var document struct {
		Services map[string]struct {
			Scale    *int
			Deploy   struct{ Replicas *int }
			Profiles []string
		}
	}
	if err := json.Unmarshal([]byte(value.Rendered), &document); err != nil {
		return result, err
	}
	desired := map[string]int{}
	for name, service := range document.Services {
		if len(service.Profiles) > 0 {
			continue
		}
		count := 1
		if service.Deploy.Replicas != nil {
			count = *service.Deploy.Replicas
		}
		if service.Scale != nil {
			count = *service.Scale
		}
		desired[name] = count
	}
	result.Reason = runtimeDifference(desired, hashes, runtime, deployment.RuntimeIsHealthy(health))
	if result.Reason == "" {
		result.Status = "healthy"
		result.Reason = "hash_and_health_match"
	} else {
		result.Status = "drifted"
	}
	return result, nil
}
func runtimeDifference(desired map[string]int, hashes map[string]string, runtime compose.RuntimeProjectSnapshot, healthy bool) string {
	counts := map[string]int{}
	for _, container := range runtime.Containers {
		if container.OneOff {
			continue
		}
		if _, ok := desired[container.Service]; !ok {
			return "orphan_service"
		}
		counts[container.Service]++
		if hashes[container.Service] == "" || container.ConfigHash != hashes[container.Service] {
			return "compose_hash_mismatch"
		}
		if container.State != "running" {
			return "service_not_running"
		}
	}
	for service, count := range desired {
		if counts[service] != count {
			return "service_count_changed"
		}
	}
	if !healthy {
		return "runtime_unhealthy"
	}
	return ""
}
