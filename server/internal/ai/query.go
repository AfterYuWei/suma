package ai

import (
	"context"
	"time"
)

// QuerySummary deliberately cannot carry resource names, IDs, endpoints,
// configuration, logs, model text, errors, or credentials. Guest queries never
// enter the model/tool/proposal pipeline.
type QuerySummary struct {
	Time       time.Time `json:"time"`
	Available  bool      `json:"available"`
	Containers struct {
		Total      int `json:"total"`
		Running    int `json:"running"`
		Stopped    int `json:"stopped"`
		Restarting int `json:"restarting"`
		Unhealthy  int `json:"unhealthy"`
	} `json:"containers"`
	Images struct {
		Total     int `json:"total"`
		Updates   int `json:"updates"`
		Recreate  int `json:"recreate"`
		Unchecked int `json:"unchecked"`
		Stale     int `json:"stale"`
	} `json:"images"`
	Cleanup struct {
		HasResult      bool   `json:"has_result"`
		Deleted        int    `json:"deleted"`
		Skipped        int    `json:"skipped"`
		Failed         int    `json:"failed"`
		ReclaimedBytes uint64 `json:"reclaimed_bytes"`
	} `json:"cleanup"`
	MissingContainers bool `json:"missing_containers"`
	MissingImages     bool `json:"missing_images"`
	MissingCleanup    bool `json:"missing_cleanup"`
}

func (s *Service) Query(ctx context.Context, node string) (QuerySummary, error) {
	return s.QueryAs(ctx, node, Actor{Source: "query"})
}

func (s *Service) QueryAs(ctx context.Context, node string, actor Actor) (out QuerySummary, queryErr error) {
	s.mu.Lock()
	allowed := !s.stopped && s.cfg.Enabled && has(s.cfg.NodeIDs, node)
	s.mu.Unlock()
	resource, result := "safe_status", "denied"
	if allowed {
		resource = node
	}
	defer func() {
		if err := s.audit(context.Background(), s.db, actor, "", "", "query", resource, result); err != nil {
			out, queryErr = QuerySummary{}, ErrInvalid
		}
	}()
	if !allowed {
		return QuerySummary{}, ErrScope
	}
	if s.deps.Query == nil {
		return QuerySummary{}, ErrInvalid
	}
	select {
	case s.querySlots <- struct{}{}:
		defer func() { <-s.querySlots }()
	default:
		return QuerySummary{}, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := s.deps.Query(ctx, node)
	if err != nil {
		result = "unavailable"
		return QuerySummary{}, ErrScope
	}
	s.mu.Lock()
	allowed = !s.stopped && s.cfg.Enabled && has(s.cfg.NodeIDs, node)
	s.mu.Unlock()
	if !allowed {
		return QuerySummary{}, ErrScope
	}
	out.Time = s.deps.Now()
	result = "success"
	return out, nil
}
