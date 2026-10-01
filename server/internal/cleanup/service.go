package cleanup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

type Dependencies struct {
	Now        func() time.Time
	Node       func(context.Context, string) (Node, error)
	Runtime    func(context.Context, string) (Runtime, error)
	Protection func(context.Context, string) (Protection, error)
	Timezone   func(context.Context) string
	OnError    func(error)
}
type Service struct {
	db       *gorm.DB
	tasks    *task.Service
	audits   *audit.Service
	deps     Dependencies
	mu       sync.Mutex
	previews map[string]Preview
	active   map[string]string
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopped  bool
	started  bool
	now      func() time.Time
}

func NewService(db *gorm.DB, tasks *task.Service, audits *audit.Service, deps Dependencies) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	clock := deps.Now
	if clock == nil {
		clock = time.Now
	}
	return &Service{db: db, tasks: tasks, audits: audits, deps: deps, previews: map[string]Preview{}, active: map[string]string{}, ctx: ctx, cancel: cancel, now: clock}
}
func id() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func encode(v any) string { b, _ := json.Marshal(v); return string(b) }
func (s *Service) policy(ctx context.Context, nodeID string) (Policy, error) {
	var row database.CleanupPolicy
	err := s.db.WithContext(ctx).First(&row, "node_id = ?", nodeID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		zone := "UTC"
		if s.deps.Timezone != nil {
			zone = s.deps.Timezone(ctx)
		}
		return Policy{Config: DefaultConfig(zone), NodeID: nodeID}, nil
	}
	if err != nil {
		return Policy{}, err
	}
	var c Config
	if err = json.Unmarshal([]byte(row.ConfigJSON), &c); err != nil {
		return Policy{}, err
	}
	if err = Validate(c); err != nil {
		return Policy{}, err
	}
	return Policy{Config: c, NodeID: nodeID, Version: row.Version, AuthorizedBy: row.AuthorizedBy, AuthorizedAt: row.AuthorizedAt, NextRunAt: row.NextRunAt}, nil
}
func (s *Service) Get(ctx context.Context, nodeID string, inspect bool) (View, error) {
	node, err := s.deps.Node(ctx, nodeID)
	if err != nil {
		return View{}, err
	}
	p, err := s.policy(ctx, nodeID)
	if err != nil {
		return View{}, err
	}
	v := View{Policy: p, NextRuns: NextOccurrences(p.Schedule, s.now(), 3), Capabilities: Capabilities{Reason: "not inspected"}}
	if inspect {
		check, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if node.Enabled {
			if runtime, e := s.deps.Runtime(check, nodeID); e == nil {
				v.Capabilities, err = runtime.CleanupCapabilities(check)
				if err != nil {
					v.Capabilities = Capabilities{Reason: "Unable to inspect Docker capabilities"}
				}
			} else {
				v.Capabilities.Reason = "Docker node is unavailable"
			}
		} else {
			v.Capabilities.Reason = "Docker node is disabled"
		}
	}
	rows, err := s.Runs(ctx, nodeID, 1, false)
	if err != nil {
		return View{}, err
	}
	if len(rows.Items) > 0 {
		v.LatestRun = &rows.Items[0]
	}
	var active database.CleanupRun
	if err := s.db.WithContext(ctx).Where("node_id = ? AND status IN ?", nodeID, []string{"pending", "running"}).Order("created_at DESC").First(&active).Error; err == nil {
		r := decodeRun(active)
		v.ActiveRun = &r
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return View{}, err
	}
	return v, nil
}
func (s *Service) Update(ctx context.Context, nodeID string, in Update, actor Actor) (Policy, error) {
	node, err := s.deps.Node(ctx, nodeID)
	if err != nil {
		return Policy{}, err
	}
	if err = Validate(in.Config); err != nil {
		return Policy{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.policy(ctx, nodeID)
	if err != nil {
		return Policy{}, err
	}
	pauseConfig := old.Config
	pauseConfig.Enabled = false
	pauseOnly := old.Enabled && !in.Enabled && encode(pauseConfig) == encode(in.Config)
	if s.stopped || old.Version != in.Version || (s.active[nodeID] != "" && !pauseOnly) {
		return Policy{}, ErrConflict
	}
	needsAuthorization := in.Enabled && (!old.Enabled || old.AuthorizedAt == nil || expanded(old.Config, in.Config))
	if needsAuthorization && (in.ConfirmationName != node.Name || !in.Authorize || actor.UserID == nil) {
		return Policy{}, ErrConfirmation
	}
	now := s.now().UTC()
	p := Policy{Config: in.Config, NodeID: nodeID, Version: old.Version + 1, AuthorizedBy: old.AuthorizedBy, AuthorizedAt: old.AuthorizedAt}
	// A disabled expansion also invalidates the old authorization, so enabling
	// later cannot reuse consent granted to a narrower configuration.
	if expanded(old.Config, in.Config) {
		p.AuthorizedBy = nil
		p.AuthorizedAt = nil
	}
	if needsAuthorization {
		p.AuthorizedBy = actor.UserID
		p.AuthorizedAt = &now
	}
	if p.Enabled {
		next := NextOccurrences(p.Schedule, now, 1)
		if len(next) == 0 {
			return Policy{}, ErrInvalid
		}
		p.NextRunAt = &next[0]
	}
	row := database.CleanupPolicy{NodeID: nodeID, Version: p.Version, Enabled: p.Enabled, ConfigJSON: encode(p.Config), AuthorizedBy: p.AuthorizedBy, AuthorizedAt: p.AuthorizedAt, NextRunAt: p.NextRunAt}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if old.Version == 0 {
			return tx.Create(&row).Error
		}
		result := tx.Model(&database.CleanupPolicy{}).Where("node_id = ? AND version = ?", nodeID, old.Version).Updates(map[string]any{"version": row.Version, "enabled": row.Enabled, "config_json": row.ConfigJSON, "authorized_by": row.AuthorizedBy, "authorized_at": row.AuthorizedAt, "next_run_at": row.NextRunAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return Policy{}, err
	}
	for key, preview := range s.previews {
		if preview.NodeID == nodeID {
			delete(s.previews, key)
		}
	}
	s.record(node, actor, "cleanup.policy.update", "success", "")
	if needsAuthorization {
		s.record(node, actor, "cleanup.authorize", "success", "")
	}
	return p, nil
}
func protected(r Resource, p Policy, refs Protection) string {
	if r.Labels["suma.cleanup.protect"] == "true" {
		return "protection_label"
	}
	if r.System {
		return "system_resource"
	}
	if (r.Kind == Container || r.Kind == Network || r.Kind == Volume) && r.Labels["com.docker.compose.project"] != "" {
		return "compose_project"
	}
	for _, set := range [][]string{p.Protected[r.Kind], refs[r.Kind]} {
		for _, name := range set {
			if name == r.ID || name == r.Name {
				return "protected_reference"
			}
			for _, alias := range r.Aliases {
				if name == alias {
					return "protected_reference"
				}
			}
		}
	}
	return ""
}
func evaluate(r Resource, p Policy, refs Protection, now time.Time) Resource {
	r.Candidate = false
	r.Manual = false
	if reason := protected(r, p, refs); reason != "" {
		r.Reason = reason
		return r
	}
	if r.InUse {
		r.Reason = "in_use"
		return r
	}
	var enabled bool
	var days int
	var date time.Time
	switch r.Kind {
	case Image:
		enabled = p.Images.Enabled
		days = p.Images.RetentionDays
		date = r.CreatedAt
		if enabled && r.Tagged && !p.Images.IncludeTagged {
			r.Reason = "tagged_image"
			return r
		}
	case Container:
		enabled = p.Containers.Enabled
		days = p.Containers.RetentionDays
		date = r.FinishedAt
		if enabled && r.State != "exited" {
			r.Reason = "not_exited"
			return r
		}
	case Network:
		enabled = p.Networks.Enabled
		days = p.Networks.RetentionDays
		date = r.CreatedAt
	case Cache:
		enabled = p.Cache.Enabled
		days = p.Cache.RetentionDays
		date = time.Time{}
		if r.LastUsedAt != nil {
			date = *r.LastUsedAt
		}
	case Volume:
		if !p.ScanVolumes {
			r.Reason = "disabled"
			return r
		}
		r.Manual = true
		r.Reason = "manual_confirmation"
		return r
	default:
		r.Reason = "unsupported"
		return r
	}
	if !enabled {
		r.Reason = "disabled"
		return r
	}
	if date.IsZero() || date.After(now) {
		r.Reason = "unknown_age"
		return r
	}
	if date.After(now.Add(-time.Duration(days) * 24 * time.Hour)) {
		r.Reason = "retention"
		return r
	}
	r.Candidate = true
	r.Reason = "eligible"
	return r
}
func (s *Service) inventory(ctx context.Context, nodeID string, p Policy) (Preview, error) {
	node, err := s.deps.Node(ctx, nodeID)
	if err != nil {
		return Preview{}, err
	}
	if !node.Enabled {
		return Preview{}, ErrUnavailable
	}
	runtime, err := s.deps.Runtime(ctx, nodeID)
	if err != nil {
		return Preview{}, fmt.Errorf("%w: Docker runtime", ErrUnavailable)
	}
	inv, err := runtime.CleanupInventory(ctx)
	if err != nil {
		return Preview{}, fmt.Errorf("%w: Docker inventory", ErrUnavailable)
	}
	refs, err := s.deps.Protection(ctx, nodeID)
	if err != nil {
		return Preview{}, fmt.Errorf("%w: unable to read project protection", ErrUnavailable)
	}
	current, err := s.deps.Node(ctx, nodeID)
	if err != nil || !current.Enabled || current.RuntimeKey != node.RuntimeKey {
		return Preview{}, ErrConflict
	}
	now := s.now().UTC()
	preview := Preview{ID: id(), NodeID: nodeID, RuntimeKey: node.RuntimeKey, PolicyVersion: p.Version, GeneratedAt: now, ExpiresAt: now.Add(5 * time.Minute), Resources: []Resource{}, Capabilities: inv.Capabilities, ImageLayersBytes: inv.LayersBytes, CacheApproximate: true, Policy: p}
	for _, r := range inv.Resources {
		preview.Resources = append(preview.Resources, evaluate(r, p, refs, now))
	}
	preview.Usage = inv.Usage
	return preview, nil
}
func (s *Service) Preview(ctx context.Context, nodeID string) (Preview, error) {
	p, err := s.policy(ctx, nodeID)
	if err != nil {
		return Preview{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	preview, err := s.inventory(ctx, nodeID, p)
	if err != nil {
		return Preview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.policy(ctx, nodeID)
	if err != nil {
		return Preview{}, err
	}
	if current.Version != p.Version || s.stopped {
		return Preview{}, ErrConflict
	}
	for key, v := range s.previews {
		if !v.ExpiresAt.After(s.now()) {
			delete(s.previews, key)
		}
	}
	if len(s.previews) >= 256 {
		return Preview{}, fmt.Errorf("%w: too many previews", ErrConflict)
	}
	s.previews[preview.ID] = preview
	return preview, nil
}
func (s *Service) busy(ctx context.Context, nodeID string) error {
	var count int64
	// CD parent tasks are global, but their deployment children are node-scoped.
	err := s.db.WithContext(ctx).Model(&database.Task{}).Where("node_id = ? AND status IN ? AND (type LIKE ? OR type LIKE ? OR type LIKE ? OR type = ?)", nodeID, []string{task.StatusPending, task.StatusRunning}, "compose.%", "cd.%", "project.%", "image.pull").Count(&count).Error
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: deployment, build, or image pull in progress", ErrConflict)
	}
	return nil
}
func (s *Service) ready(ctx context.Context, node Node) error {
	current, err := s.deps.Node(ctx, node.ID)
	if err != nil || !current.Enabled || current.RuntimeKey != node.RuntimeKey {
		return ErrUnavailable
	}
	return s.busy(ctx, node.ID)
}
func (s *Service) StartRun(ctx context.Context, nodeID, previewID, confirmation string, actor Actor) (database.Task, error) {
	node, err := s.deps.Node(ctx, nodeID)
	if err != nil {
		return database.Task{}, err
	}
	if confirmation != node.Name {
		return database.Task{}, ErrConfirmation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	preview, ok := s.previews[previewID]
	if !ok || preview.NodeID != nodeID || !preview.ExpiresAt.After(s.now()) {
		return database.Task{}, ErrConflict
	}
	p, err := s.policy(ctx, nodeID)
	if err != nil {
		return database.Task{}, err
	}
	if p.Version != preview.PolicyVersion {
		return database.Task{}, ErrConflict
	}
	row, err := s.startLocked(ctx, node, preview, actor, "manual", nil, "")
	if err == nil {
		delete(s.previews, previewID)
	}
	return row, err
}

// Legacy callers still require PRUNE; they use the same executor and protections.
func (s *Service) LegacyRun(ctx context.Context, nodeID string, actor Actor) (database.Task, error) {
	preview, err := s.Preview(ctx, nodeID)
	if err != nil {
		return database.Task{}, err
	}
	node, err := s.deps.Node(ctx, nodeID)
	if err != nil {
		return database.Task{}, err
	}
	return s.StartRun(ctx, nodeID, preview.ID, node.Name, actor)
}
func (s *Service) startLocked(ctx context.Context, node Node, preview Preview, actor Actor, trigger string, scheduled *time.Time, claimedID string) (database.Task, error) {
	if s.stopped || preview.RuntimeKey != node.RuntimeKey || s.active[node.ID] != "" || len(s.active) >= 2 {
		return database.Task{}, ErrConflict
	}
	if !node.Enabled {
		return database.Task{}, ErrUnavailable
	}
	if err := s.ready(ctx, node); err != nil {
		return database.Task{}, err
	}
	now := s.now().UTC()
	run := database.CleanupRun{ID: id(), NodeID: node.ID, NodeName: node.Name, PolicyVersion: preview.PolicyVersion, PolicyJSON: encode(preview.Policy.Config), Trigger: trigger, ScheduledFor: scheduled, UserID: actor.UserID, AuthorizedBy: preview.Policy.AuthorizedBy, Status: "pending", CreatedAt: now}
	if claimedID != "" {
		run.ID = claimedID
		if err := s.db.WithContext(ctx).Model(&database.CleanupRun{}).Where("id = ? AND status = ?", claimedID, "pending").Updates(map[string]any{"node_name": node.Name}).Error; err != nil {
			return database.Task{}, err
		}
	} else if err := s.db.WithContext(ctx).Create(&run).Error; err != nil {
		return database.Task{}, err
	}
	s.active[node.ID] = run.ID
	s.wg.Add(1)
	row, err := s.tasks.StartWithIDForNode(node.ID, node.Name, "system.cleanup", "Docker storage cleanup", func(ctx context.Context, taskID string, report task.Reporter) error {
		defer s.wg.Done()
		defer func() { s.mu.Lock(); delete(s.active, node.ID); s.mu.Unlock() }()
		run.TaskID = taskID
		return s.execute(ctx, node, preview, run, actor, report)
	})
	if err != nil {
		delete(s.active, node.ID)
		s.wg.Done()
		s.db.Model(&database.CleanupRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": "failed", "message": "Unable to create task", "finished_at": now})
		return database.Task{}, err
	}
	// Execution also sets TaskID; setting only this column is safe if it finishes first.
	if err := s.db.Model(&database.CleanupRun{}).Where("id = ?", run.ID).Update("task_id", row.ID).Error; err != nil {
		s.tasks.Cancel(row.ID)
	}
	s.record(node, actor, "cleanup.start", "accepted", row.ID)
	return row, nil
}
func (s *Service) execute(taskCtx context.Context, node Node, preview Preview, run database.CleanupRun, actor Actor, report task.Reporter) (workErr error) {
	ctx, cancel := context.WithTimeout(taskCtx, 30*time.Minute)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	started := s.now().UTC()
	result := Result{Outcomes: []Outcome{}, Stats: map[Kind]*Stats{}, ImageLayersBefore: preview.ImageLayersBytes, UsageBefore: preview.Usage}
	for _, k := range []Kind{Container, Cache, Image, Network, Volume} {
		result.Stats[k] = &Stats{}
	}
	defer func() {
		status, message := "success", "Completed"
		if workErr != nil {
			status, message = "failed", workErr.Error()
		}
		failures := 0
		deleted := 0
		for _, stats := range result.Stats {
			failures += stats.Failed
			deleted += stats.Deleted
		}
		if failures > 0 && workErr == nil {
			status = "partial_failed"
			message = "Some resources could not be removed"
			workErr = errors.New(message)
		}
		if failures > 0 && deleted > 0 && status == "failed" {
			status = "partial_failed"
		}
		if taskCtx.Err() != nil || s.ctx.Err() != nil {
			status = "canceled"
			message = "Canceled; completed deletions are preserved"
			workErr = context.Canceled
		}
		now := s.now().UTC()
		saved := s.db.Model(&database.CleanupRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": status, "message": message, "result_json": encode(result), "finished_at": now}).Error
		if saved != nil {
			workErr = fmt.Errorf("unable to persist cleanup result: %w", saved)
		}
		s.record(node, actor, "cleanup.finish", status, run.TaskID)
	}()
	if err := s.db.Model(&database.CleanupRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": "running", "task_id": run.TaskID, "started_at": started}).Error; err != nil {
		return fmt.Errorf("unable to persist cleanup start: %w", err)
	}
	runtime, err := s.deps.Runtime(ctx, node.ID)
	if err != nil {
		return ErrUnavailable
	}
	if err = s.ready(ctx, node); err != nil {
		return err
	}
	// Observe usage at execution time without expanding the frozen candidates.
	before, err := runtime.CleanupInventory(ctx)
	if err != nil {
		return ErrUnavailable
	}
	result.ImageLayersBefore = before.LayersBytes
	result.UsageBefore = before.Usage
	stageProgress := 0
	persistResult := func() error {
		return s.db.WithContext(ctx).Model(&database.CleanupRun{}).Where("id = ?", run.ID).Update("result_json", encode(result)).Error
	}
	appendOutcome := func(o Outcome) error {
		result.Outcomes = append(result.Outcomes, o)
		stats := result.Stats[o.Kind]
		switch o.Status {
		case "deleted":
			stats.Deleted++
		case "failed":
			stats.Failed++
		case "scanned":
			stats.Scanned++
		default:
			stats.Skipped++
		}
		report(stageProgress, fmt.Sprintf("%s %s: %s (%s)", o.Kind, o.Name, o.Status, o.Reason))
		return persistResult()
	}
	for step, k := range []Kind{Container, Cache, Image, Network, Volume} {
		stageProgress = step * 20
		if err := ctx.Err(); err != nil {
			return err
		}
		if k == Cache {
			if !preview.Policy.Cache.Enabled {
				continue
			}
			if !preview.Capabilities.BuildCache {
				if err := appendOutcome(Outcome{Kind: Cache, ID: "engine", Name: "Engine BuildKit", Status: "skipped", Reason: "unsupported"}); err != nil {
					return err
				}
				continue
			}
			if err = s.ready(ctx, node); err != nil {
				return err
			}
			// Re-read protection before the cache operation as well, even though the
			// Engine selects cache records and does not expose per-record deletion.
			if _, err = s.deps.Protection(ctx, node.ID); err != nil {
				return ErrUnavailable
			}
			report(25, "Pruning Engine build cache (rule-based estimate)")
			cache, cacheErr := runtime.CleanupPruneCache(ctx, CacheOptions{RetentionDays: preview.Policy.Cache.RetentionDays, ReservedBytes: preview.Policy.Cache.ReservedBytes})
			if cacheErr != nil {
				if err := appendOutcome(Outcome{Kind: Cache, ID: "engine", Name: "Engine BuildKit", Status: "failed", Reason: "Engine cache prune failed"}); err != nil {
					return err
				}
				if errors.Is(cacheErr, ErrUnavailable) || ctx.Err() != nil {
					return ErrUnavailable
				}
			} else {
				result.Stats[Cache].ReclaimedBytes = &cache.ReclaimedBytes
				if err := persistResult(); err != nil {
					return err
				}
				for _, cacheID := range cache.Deleted {
					if err := appendOutcome(Outcome{Kind: Cache, ID: cacheID, Name: cacheID, Status: "deleted"}); err != nil {
						return err
					}
				}
			}
			continue
		}
		for _, candidate := range preview.Resources {
			if candidate.Kind != k || (!candidate.Candidate && !candidate.Manual) {
				continue
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			if err = s.ready(ctx, node); err != nil {
				return err
			}
			// Fail closed if either Docker inventory or project references cannot be
			// obtained. Never continue deleting based on a stale preview alone.
			fresh, e := runtime.CleanupResource(ctx, k, candidate.ID)
			if e != nil && !errors.Is(e, ErrGone) {
				return ErrUnavailable
			}
			refs, e := s.deps.Protection(ctx, node.ID)
			if e != nil {
				return ErrUnavailable
			}
			var current *Resource
			if fresh.ID != "" {
				r := evaluate(fresh, preview.Policy, refs, s.now())
				current = &r
			}
			o := Outcome{Kind: k, ID: candidate.ID, Name: candidate.Name, EstimatedBytes: candidate.SizeBytes}
			if current == nil {
				o.Status = "skipped"
				o.Reason = "not_found"
			} else if k == Volume {
				o.Status = "scanned"
				o.Reason = current.Reason
			} else if !current.Candidate {
				o.Status = "skipped"
				o.Reason = current.Reason
			} else {
				e = runtime.CleanupRemove(ctx, k, candidate.ID)
				switch {
				case e == nil:
					o.Status = "deleted"
				case errors.Is(e, ErrGone):
					o.Status = "skipped"
					o.Reason = "not_found"
				case errors.Is(e, ErrInUse):
					o.Status = "skipped"
					o.Reason = "in_use"
				default:
					o.Status = "failed"
					o.Reason = "Docker refused resource removal"
				}
			}
			if err := appendOutcome(o); err != nil {
				return err
			}
			if errors.Is(e, ErrUnavailable) {
				return ErrUnavailable
			}
		}
		report((step+1)*20, fmt.Sprintf("%s cleanup complete", k))
	}
	if inv, e := runtime.CleanupInventory(ctx); e == nil {
		result.ImageLayersAfter = inv.LayersBytes
		result.UsageAfter = inv.Usage
	}
	report(100, "Cleanup complete; volumes require manual confirmation")
	return nil
}
func decodeRun(row database.CleanupRun) Run {
	r := Run{CleanupRun: row, Result: Result{Outcomes: []Outcome{}, Stats: map[Kind]*Stats{}}}
	_ = json.Unmarshal([]byte(row.PolicyJSON), &r.Policy)
	_ = json.Unmarshal([]byte(row.ResultJSON), &r.Result)
	return r
}
func (s *Service) Runs(ctx context.Context, nodeID string, page int, failedOnly bool) (RunPage, error) {
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	query := s.db.WithContext(ctx).Model(&database.CleanupRun{}).Where("node_id = ?", nodeID)
	if failedOnly {
		query = query.Where("status IN ?", []string{"failed", "partial_failed", "interrupted"})
	}
	result := RunPage{Items: []Run{}, Page: page}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}
	var rows []database.CleanupRun
	if err := query.Order("created_at DESC, id DESC").Limit(20).Offset((page - 1) * 20).Find(&rows).Error; err != nil {
		return result, err
	}
	for _, row := range rows {
		result.Items = append(result.Items, decodeRun(row))
	}
	return result, nil
}
func (s *Service) Run(ctx context.Context, nodeID, runID string) (Run, error) {
	var row database.CleanupRun
	err := s.db.WithContext(ctx).First(&row, "node_id = ? AND id = ?", nodeID, runID).Error
	return decodeRun(row), err
}
func (s *Service) CheckVolumeDeletion(ctx context.Context, nodeID, name string) error {
	p, err := s.policy(ctx, nodeID)
	if err != nil {
		return err
	}
	p.ScanVolumes = true
	preview, err := s.inventory(ctx, nodeID, p)
	if err != nil {
		return err
	}
	for _, r := range preview.Resources {
		if r.Kind == Volume && r.ID == name {
			if r.Manual {
				return nil
			}
			return fmt.Errorf("%w: volume %s", ErrInUse, r.Reason)
		}
	}
	return ErrGone
}
func (s *Service) record(node Node, actor Actor, action, result, taskID string) {
	if s.audits != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.audits.RecordLinkedForNode(ctx, node.ID, node.Name, actor.UserID, action, "cleanup", node.ID, actor.IP, result, taskID, nil); err != nil && s.deps.OnError != nil {
			s.deps.OnError(err)
		}
	}
}

// Recover records interruptions and advances missed schedules without replaying
// destructive work. It is called before the scheduler starts accepting tasks.
func (s *Service) Recover(ctx context.Context) error {
	now := s.now().UTC()
	var unfinished []database.CleanupRun
	if err := s.db.WithContext(ctx).Where("status IN ?", []string{"pending", "running"}).Find(&unfinished).Error; err != nil {
		return err
	}
	for _, run := range unfinished {
		if err := s.db.WithContext(ctx).Model(&database.CleanupRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": "interrupted", "message": "SUMA restarted; cleanup was not replayed", "finished_at": now}).Error; err != nil {
			return err
		}
		if run.TaskID != "" {
			if err := s.db.WithContext(ctx).Model(&database.Task{}).Where("id = ? AND status IN ?", run.TaskID, []string{task.StatusPending, task.StatusRunning}).Updates(map[string]any{"status": task.StatusCanceled, "message": "SUMA restarted before cleanup completed", "finished_at": now}).Error; err != nil {
				return err
			}
		}
		s.record(Node{ID: run.NodeID, Name: run.NodeName}, Actor{UserID: run.UserID}, "cleanup.finish", "interrupted", run.TaskID)
	}
	var rows []database.CleanupPolicy
	if err := s.db.WithContext(ctx).Where("enabled = ?", true).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		p, err := s.policy(ctx, row.NodeID)
		if err != nil {
			return err
		}
		if row.NextRunAt == nil || !row.NextRunAt.After(now) {
			if row.NextRunAt != nil {
				if err := s.skip(ctx, p, row.NextRunAt, "Missed while SUMA was stopped"); err != nil {
					return err
				}
			}
			next := NextOccurrences(p.Schedule, now, 1)
			if len(next) == 0 {
				return ErrInvalid
			}
			if err := s.db.WithContext(ctx).Model(&database.CleanupPolicy{}).Where("node_id = ? AND version = ?", row.NodeID, row.Version).Update("next_run_at", next[0]).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Service) Start() {
	s.mu.Lock()
	if s.stopped || s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if err := s.Tick(s.ctx); err != nil && s.ctx.Err() == nil && s.deps.OnError != nil {
					s.deps.OnError(err)
				}
			}
		}
	}()
}
func (s *Service) Stop() {
	s.mu.Lock()
	s.stopped = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}
func (s *Service) skip(ctx context.Context, p Policy, scheduled *time.Time, message string) error {
	now := s.now().UTC()
	name := p.NodeID
	if node, e := s.deps.Node(ctx, p.NodeID); e == nil {
		name = node.Name
	}
	row := database.CleanupRun{ID: id(), NodeID: p.NodeID, NodeName: name, PolicyVersion: p.Version, PolicyJSON: encode(p.Config), Trigger: "scheduled", ScheduledFor: scheduled, AuthorizedBy: p.AuthorizedBy, Status: "skipped", Message: message, CreatedAt: now, FinishedAt: &now}
	var existing database.CleanupRun
	err := s.db.WithContext(ctx).First(&existing, "node_id = ? AND scheduled_for = ?", p.NodeID, scheduled).Error
	if err == nil {
		if existing.Status != "pending" {
			return nil
		}
		if err = s.db.WithContext(ctx).Model(&database.CleanupRun{}).Where("id = ? AND status = ?", existing.ID, "pending").Updates(map[string]any{"node_name": name, "status": "skipped", "message": message, "finished_at": now}).Error; err != nil {
			return err
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		if err = s.db.WithContext(ctx).Create(&row).Error; err != nil {
			return err
		}
	} else {
		return err
	}

	s.record(Node{ID: p.NodeID, Name: name}, Actor{}, "cleanup.finish", "skipped", "")
	return nil
}
func (s *Service) Tick(ctx context.Context) error {
	now := s.now().UTC()
	var rows []database.CleanupPolicy
	if err := s.db.WithContext(ctx).Where("enabled = ? AND next_run_at <= ?", true, now).Order("next_run_at").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		p, err := s.policy(ctx, row.NodeID)
		if err != nil {
			return err
		}
		if !p.Enabled || p.NextRunAt == nil || row.NextRunAt == nil || p.NextRunAt.After(now) || p.Version != row.Version || !p.NextRunAt.Equal(*row.NextRunAt) {
			continue
		}
		scheduled := *p.NextRunAt
		next := NextOccurrences(p.Schedule, now, 1)
		if len(next) == 0 {
			return ErrInvalid
		}
		claimID := id()
		s.mu.Lock()
		claimErr := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			changed := tx.Model(&database.CleanupPolicy{}).Where("node_id = ? AND enabled = ? AND version = ? AND next_run_at = ?", p.NodeID, true, p.Version, scheduled).Update("next_run_at", next[0])
			if changed.Error != nil {
				return changed.Error
			}
			if changed.RowsAffected != 1 {
				return ErrConflict
			}
			return tx.Create(&database.CleanupRun{ID: claimID, NodeID: p.NodeID, NodeName: p.NodeID, PolicyVersion: p.Version, PolicyJSON: encode(p.Config), Trigger: "scheduled", ScheduledFor: &scheduled, AuthorizedBy: p.AuthorizedBy, Status: "pending", CreatedAt: now}).Error
		})
		s.mu.Unlock()
		if errors.Is(claimErr, ErrConflict) {
			continue
		}
		if claimErr != nil {
			return claimErr
		}

		if now.Sub(scheduled) >= time.Minute {
			if err := s.skip(ctx, p, &scheduled, "Missed execution window; not replayed"); err != nil {
				return err
			}
			continue
		}
		node, err := s.deps.Node(ctx, p.NodeID)
		if err != nil || !node.Enabled || p.AuthorizedAt == nil || p.AuthorizedBy == nil {
			if err := s.skip(ctx, p, &scheduled, "Node disabled, removed, or policy not authorized"); err != nil {
				return err
			}
			continue
		}
		scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		preview, scanErr := s.inventory(scanCtx, p.NodeID, p)
		cancel()
		if scanErr != nil {
			if err := s.skip(ctx, p, &scheduled, scanErr.Error()); err != nil {
				return err
			}
			continue
		}
		s.mu.Lock()
		latest, err := s.policy(ctx, p.NodeID)
		if err == nil && latest.Version == p.Version && latest.Enabled {
			_, err = s.startLocked(ctx, node, preview, Actor{}, "scheduled", &scheduled, claimID)
		} else {
			err = ErrConflict
		}
		s.mu.Unlock()
		if err != nil {
			if e := s.skip(ctx, p, &scheduled, err.Error()+"; not replayed"); e != nil {
				return e
			}
		}
	}
	return nil
}

// Sanitize identifiers supplied by callers that bypass the HTTP path validator.
func ValidIdentifier(value string) bool {
	return value != "" && len(value) <= 512 && !strings.ContainsAny(value, "\x00\r\n")
}
