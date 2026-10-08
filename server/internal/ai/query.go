package ai

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/database"
)

var ErrQueryTarget = errors.New("safe status requires one enabled authorized node")

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

// QueryTextAs resolves a guest's explicit node name or ID without invoking the
// model or exposing directory entries. QueryAs rechecks authorization before
// and after collecting the numeric summary.
func (s *Service) QueryTextAs(ctx context.Context, text string, actor Actor) (QuerySummary, error) {
	node, err := s.queryTarget(ctx, text)
	if err != nil {
		if auditErr := s.audit(context.Background(), s.db, actor, "", "", "query", "safe_status", "denied"); auditErr != nil {
			return QuerySummary{}, ErrInvalid
		}
		return QuerySummary{}, err
	}
	return s.QueryAs(ctx, node, actor)
}

func (s *Service) queryTarget(ctx context.Context, text string) (string, error) {
	s.mu.Lock()
	cfg, stopped := cloneSettings(s.cfg), s.stopped
	s.mu.Unlock()
	if stopped || !cfg.Enabled {
		return "", ErrScope
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ErrQueryTarget
	}
	// Include unavailable directory entries only to prevent an explicit request
	// for another node from silently falling back to the sole authorized node.
	var nodes []database.Node
	if err := s.db.WithContext(ctx).Select("id", "name", "enabled").Find(&nodes).Error; err != nil {
		return "", ErrInvalid
	}
	allowed := func(node database.Node) bool { return node.Enabled && has(cfg.NodeIDs, node.ID) }
	parts := strings.Fields(text)
	if parts[0] == "/node" {
		if len(parts) < 2 {
			return "", ErrQueryTarget
		}
		// An exact ID takes priority over a different node with the same name.
		for _, node := range nodes {
			if node.ID == parts[1] {
				if allowed(node) {
					return node.ID, nil
				}
				return "", ErrQueryTarget
			}
		}
	}
	matches := []database.Node{}
	lower := strings.ToLower(text)
	for _, node := range nodes {
		match := mentionsTarget(lower, node.ID) || mentionsTarget(lower, node.Name)
		if parts[0] == "/node" {
			match = strings.EqualFold(parts[1], node.Name)
		}
		if match {
			matches = append(matches, node)
		}
	}
	if len(matches) == 1 && allowed(matches[0]) {
		return matches[0].ID, nil
	}
	if len(matches) == 0 && parts[0] != "/node" && len(cfg.NodeIDs) == 1 && !strings.Contains(lower, "节点") && !mentionsTarget(lower, "node") && !mentionsTarget(lower, "nodes") {
		for _, node := range nodes {
			if allowed(node) {
				return node.ID, nil
			}
		}
	}
	return "", ErrQueryTarget
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
