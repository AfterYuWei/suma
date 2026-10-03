package imageupdate

import (
	"context"
	"errors"
	"fmt"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
	"sort"
	"sync"
	"time"
)

type cached struct {
	RuntimeKey string
	Result     Result
}
type Service struct {
	policyMu sync.Mutex
	db       *gorm.DB
	tasks    *task.Service
	audit    *audit.Service
	deps     Dependencies
	mu       sync.Mutex
	running  map[string]string
	cancels  map[string]context.CancelFunc
	cache    map[string]map[string]cached
	slots    chan struct{}
	stop     context.CancelFunc
	wg       sync.WaitGroup
	closing  bool
}

func NewService(db *gorm.DB, tasks *task.Service, audits *audit.Service, deps Dependencies) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{db: db, tasks: tasks, audit: audits, deps: deps, running: map[string]string{}, cancels: map[string]context.CancelFunc{}, cache: map[string]map[string]cached{}, slots: make(chan struct{}, 4)}
}
func (s *Service) Policy(ctx context.Context, id string) (Policy, error) {
	if _, err := s.deps.Node(ctx, id); err != nil {
		return Policy{}, err
	}
	p := Policy{IntervalHours: 6, RegistryCredentials: map[string]uint{}}
	var row database.ImageUpdatePolicy
	if err := s.db.WithContext(ctx).First(&row, "node_id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return p, nil
		}
		return p, err
	}
	p.Version, p.Enabled, p.IntervalHours, p.NextRunAt = row.Version, row.Enabled, row.IntervalHours, row.NextRunAt
	var refs []database.ImageUpdateRegistryCredential
	if err := s.db.WithContext(ctx).Where("node_id = ?", id).Find(&refs).Error; err != nil {
		return p, err
	}
	for _, r := range refs {
		p.RegistryCredentials[r.Registry] = r.CredentialID
	}
	return p, nil
}
func (s *Service) UpdatePolicy(ctx context.Context, id string, in PolicyInput, actor Actor) (Policy, error) {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	if _, err := s.deps.Node(ctx, id); err != nil {
		return Policy{}, err
	}
	if in.IntervalHours != 1 && in.IntervalHours != 6 && in.IntervalHours != 24 {
		return Policy{}, ErrInvalid
	}
	mappings := map[string]uint{}
	for host, cid := range in.RegistryCredentials {
		host = RegistryHost(host)
		_, normalized, reason := Normalize(host + "/suma/check:latest")
		if reason != "" || host != normalized || cid == 0 {
			return Policy{}, ErrInvalid
		}
		mappings[host] = cid
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row database.ImageUpdatePolicy
		err := tx.First(&row, "node_id = ?", id).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if row.Version != in.ExpectedVersion {
			return ErrConflict
		}
		for host, cid := range mappings {
			var cred database.RegistryCredential
			if tx.First(&cred, cid).Error != nil || RegistryHost(cred.ServerAddress) != host {
				return ErrInvalid
			}
			var count int64
			if err := tx.Model(&database.RegistryCredentialNode{}).Where("credential_id = ? AND node_id = ?", cid, id).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return errors.New("registry credential is not authorized for this node")
			}
		}
		now := s.deps.Now().UTC()
		next := now
		if row.Enabled && row.IntervalHours == in.IntervalHours && row.NextRunAt != nil {
			next = *row.NextRunAt
		}
		row = database.ImageUpdatePolicy{NodeID: id, Version: row.Version + 1, Enabled: in.Enabled, IntervalHours: in.IntervalHours}
		if in.Enabled {
			row.NextRunAt = &next
		}
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("node_id = ?", id).Delete(&database.ImageUpdateRegistryCredential{}).Error; err != nil {
			return err
		}
		for host, cid := range mappings {
			if err := tx.Create(&database.ImageUpdateRegistryCredential{NodeID: id, Registry: host, CredentialID: cid}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Policy{}, err
	}
	s.record(ctx, id, actor, "image.updates.policy", "success", "")
	s.ScanDue(ctx)
	return s.Policy(ctx, id)
}
func (s *Service) View(ctx context.Context, id, project string) (View, error) {
	if project != "" && (!projectIdentifier.MatchString(project) || len(project) > 128) {
		return View{}, ErrInvalid
	}
	node, err := s.deps.Node(ctx, id)
	if err != nil {
		return View{}, err
	}
	runtime, err := s.deps.Runtime(ctx, id)
	if err != nil {
		return View{}, ErrUnavailable
	}
	inv, err := runtime.UpdateInventory(ctx)
	if err != nil {
		return View{}, ErrUnavailable
	}
	p, err := s.Policy(ctx, id)
	if err != nil {
		return View{}, err
	}
	age := time.Duration(p.IntervalHours) * time.Hour
	result := View{Results: []Result{}}
	s.mu.Lock()
	pendingEvents := []event.Event{}
	defer func() {
		s.mu.Unlock()
		if s.deps.Emit != nil {
			for _, e := range pendingEvents {
				s.deps.Emit(e)
			}
		}
	}()
	result.RunningTaskID = s.running[id]
	for _, t := range targets(inv, CheckInput{ProjectName: project}) {
		r := t.result
		if c, ok := s.cache[id][key(r)]; ok && c.RuntimeKey == node.RuntimeKey {
			r = c.Result
			r.Containers = t.result.Containers
			r.Stale = r.CheckedAt == nil || s.deps.Now().Sub(*r.CheckedAt) >= age
			r.PullRequired = r.RemoteConfigDigest != "" && t.tagID != r.RemoteConfigDigest
			r.RecreateRequired = false
			for _, u := range r.Containers {
				if r.RemoteConfigDigest != "" && u.ImageID != r.RemoteConfigDigest {
					r.RecreateRequired = true
				}
			}
		} else if r.Status == "unchecked" && result.RunningTaskID != "" {
			r.Status = "checking"
		}
		if r.RecreateRequired && !r.PullRequired {
			pendingEvents = append(pendingEvents, event.Event{Type: "image.recreate_required", Severity: "info", NodeID: node.ID, NodeName: node.Name, ResourceType: "image", ResourceID: r.LocalImageID, Title: r.Reference, Message: "Local image updated; containers still use an older image", DedupeKey: node.ID + "|" + r.Reference + "|" + r.RemoteManifestDigest + "|recreate"})
		}
		result.Results = append(result.Results, r)
	}
	sort.Slice(result.Results, func(i, j int) bool {
		a, b := result.Results[i], result.Results[j]
		return a.Reference+a.LocalImageID < b.Reference+b.LocalImageID
	})
	return result, nil
}
func (s *Service) Check(ctx context.Context, id string, in CheckInput, actor Actor) (database.Task, error) {
	if len(in.ImageIDs) > 0 && in.ProjectName != "" || len(in.ImageIDs) > 1000 {
		return database.Task{}, ErrInvalid
	}
	if in.ProjectName != "" && (!projectIdentifier.MatchString(in.ProjectName) || len(in.ProjectName) > 128) {
		return database.Task{}, ErrInvalid
	}
	for _, selected := range in.ImageIDs {
		if len(selected) > 128 || !imageIdentifier.MatchString(selected) {
			return database.Task{}, ErrInvalid
		}
	}
	node, err := s.deps.Node(ctx, id)
	if err != nil {
		return database.Task{}, err
	}
	if !node.Enabled {
		return database.Task{}, ErrUnavailable
	}
	policy, err := s.Policy(ctx, id)
	if err != nil {
		return database.Task{}, err
	}
	mappings := policy.RegistryCredentials
	if in.RegistryCredentials != nil {
		mappings = map[string]uint{}
		for host, cid := range in.RegistryCredentials {
			mappings[RegistryHost(host)] = cid
		}
	}
	// Validate supplied selections before allocating an asynchronous task.
	for host, cid := range mappings {
		if _, err := s.material(ctx, id, host, cid); err != nil {
			return database.Task{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return database.Task{}, ErrUnavailable
	}
	if current := s.running[id]; current != "" {
		return database.Task{}, &BusyError{TaskID: current}
	}
	ready := make(chan struct{})
	row, err := s.tasks.StartWithIDForNode(id, node.Name, "image.updates.check", "Check image updates", func(taskCtx context.Context, taskID string, report task.Reporter) error {
		<-ready
		ctx, cancel := context.WithTimeout(taskCtx, 15*time.Minute)
		s.mu.Lock()
		s.cancels[id] = cancel
		s.mu.Unlock()
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.running, id)
			delete(s.cancels, id)
			s.mu.Unlock()
			s.wg.Done()
		}()
		err := s.check(ctx, node, in, mappings, report)
		outcome := "success"
		if err != nil {
			outcome = "failed"
		}
		if ctx.Err() != nil {
			outcome = "canceled"
		}
		s.record(context.Background(), id, actor, "image.updates.completed", outcome, taskID)
		return err
	})
	if err != nil {
		return row, err
	}
	s.wg.Add(1)
	s.running[id] = row.ID
	close(ready)
	s.record(ctx, id, actor, "image.updates.check", "success", row.ID)
	return row, nil
}
func (s *Service) material(ctx context.Context, id, host string, cid uint) (credential.RegistryMaterial, error) {
	if cid == 0 {
		return credential.RegistryMaterial{}, nil
	}
	if s.deps.Credentials == nil {
		return credential.RegistryMaterial{}, ErrInvalid
	}
	if err := s.deps.Credentials.AuthorizedForNode(ctx, cid, id); err != nil {
		return credential.RegistryMaterial{}, err
	}
	m, err := s.deps.Credentials.Material(ctx, cid)
	if err != nil {
		return m, errors.New("registry credential unavailable")
	}
	if RegistryHost(m.ServerAddress) != RegistryHost(host) {
		return m, errors.New("registry credential does not match image registry")
	}
	return m, nil
}
func (s *Service) check(ctx context.Context, node Node, in CheckInput, mappings map[string]uint, report task.Reporter) error {
	runtime, err := s.deps.Runtime(ctx, node.ID)
	if err != nil {
		return ErrUnavailable
	}
	inv, err := runtime.UpdateInventory(ctx)
	if err != nil {
		return ErrUnavailable
	}
	work := targets(inv, in)
	if len(in.ImageIDs) > 0 {
		found := map[string]bool{}
		for _, img := range inv.Images {
			found[img.ID] = true
		}
		for _, id := range in.ImageIDs {
			if !found[id] {
				return errors.New("selected image no longer exists")
			}
		}
	}
	if in.ProjectName != "" && len(work) == 0 {
		report(100, "No deployed containers to check")
		return nil
	}
	var resultMu sync.Mutex
	failed, done := 0, 0
	jobs := make(chan target)
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for t := range jobs {
				r := t.result
				now := s.deps.Now().UTC()
				r.CheckedAt = &now
				if r.Status == "unchecked" {
					m, err := s.material(ctx, node.ID, r.Registry, mappings[r.Registry])
					code := "credential_unavailable"
					if err == nil && r.Platform.OS != "" && r.Platform.Architecture != "" {
						select {
						case s.slots <- struct{}{}:
						case <-ctx.Done():
							return
						}
						lookupCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
						remote, lookupErr := s.deps.Resolver.Resolve(lookupCtx, r.Reference, r.Platform, m)
						cancel()
						<-s.slots
						if lookupErr == nil {
							r.RemoteManifestDigest = remote.ManifestDigest
							r.RemoteConfigDigest = remote.ConfigDigest
							r.Status = "current"
							if remote.ConfigDigest != r.LocalImageID {
								r.Status = "update_available"
							}
						}
						if lookupErr != nil {
							err = lookupErr
							code = "registry_unreachable"
							var e *LookupError
							if errors.As(err, &e) {
								code = e.Code
							}
						}
					} else if err == nil {
						err = errors.New("platform unavailable")
						code = "platform_unavailable"
					}
					if err != nil {
						r.Status = "unavailable"
						r.ReasonCode = code
					}
				}
				resultMu.Lock()
				done++
				if r.Status == "unavailable" && r.ReasonCode != "untagged" && r.ReasonCode != "image_id" {
					failed++
				}
				report(done*100/max(1, len(work)), fmt.Sprintf("Checked %d/%d references; %d unavailable", done, len(work), failed))
				resultMu.Unlock()
				current, nodeErr := s.deps.Node(ctx, node.ID)
				if nodeErr == nil && current.RuntimeKey == node.RuntimeKey {
					s.mu.Lock()
					if s.cache[node.ID] == nil {
						s.cache[node.ID] = map[string]cached{}
					}
					for k, c := range s.cache[node.ID] {
						if c.Result.CheckedAt == nil || now.Sub(*c.Result.CheckedAt) > 24*time.Hour {
							delete(s.cache[node.ID], k)
						}
					}
					cacheSize := 0
					for _, results := range s.cache {
						cacheSize += len(results)
					}
					if cacheSize >= 10000 {
						s.cache = map[string]map[string]cached{node.ID: {}}
					}
					s.cache[node.ID][key(r)] = cached{RuntimeKey: node.RuntimeKey, Result: r}
					s.mu.Unlock()
					if s.deps.Emit != nil {
						kind := ""
						severity := "info"
						switch r.Status {
						case "update_available":
							kind = "image.available"
						case "recreate_required":
							kind = "image.recreate_required"
						case "unavailable":
							kind = "image.check_failed"
							severity = "warning"
						}
						if kind != "" {
							s.deps.Emit(event.Event{Type: kind, Severity: severity, NodeID: node.ID, NodeName: node.Name, ResourceType: "image", ResourceID: r.LocalImageID, Title: r.Reference, Message: r.Status + " " + r.RemoteManifestDigest + " " + r.ReasonCode, DedupeKey: node.ID + "|" + r.Reference + "|" + r.RemoteManifestDigest + "|" + kind})
						}
					}
				}
			}
		}()
	}
send:
	for _, t := range work {
		select {
		case jobs <- t:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	workers.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if failed > 0 {
		return fmt.Errorf("image check completed: %d of %d references unavailable", failed, len(work))
	}
	report(100, "Image check complete")
	return nil
}
func (s *Service) record(ctx context.Context, id string, actor Actor, action, result, taskID string) {
	if s.audit == nil {
		return
	}
	node, err := s.deps.Node(ctx, id)
	if err == nil {
		_ = s.audit.RecordLinkedForNode(ctx, id, node.Name, actor.UserID, action, "image_updates", id, actor.IP, result, taskID, nil)
	}
}
func (s *Service) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		s.ScanDue(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.ScanDue(ctx)
			}
		}
	}()
}
func (s *Service) ScanDue(ctx context.Context) {
	var rows []database.ImageUpdatePolicy
	now := s.deps.Now().UTC()
	if s.db.WithContext(ctx).Where("enabled = ? AND next_run_at <= ?", true, now).Find(&rows).Error != nil {
		return
	}
	for _, row := range rows {
		next := now.Add(time.Duration(row.IntervalHours) * time.Hour)
		claim := s.db.WithContext(ctx).Model(&database.ImageUpdatePolicy{}).Where("node_id = ? AND version = ? AND enabled = ? AND next_run_at = ?", row.NodeID, row.Version, true, row.NextRunAt).Update("next_run_at", next)
		if claim.Error != nil || claim.RowsAffected != 1 {
			continue
		}
		node, err := s.deps.Node(ctx, row.NodeID)
		if err != nil || !node.Enabled || !node.Available {
			continue
		}
		_, _ = s.Check(ctx, row.NodeID, CheckInput{}, Actor{})
	}
}
func (s *Service) Stop() {
	if s.stop != nil {
		s.stop()
	}
	s.mu.Lock()
	s.closing = true
	for id := range s.running {
		_, _ = s.tasks.CancelForNode(context.Background(), id, s.running[id])
	}
	for _, cancel := range s.cancels {
		cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}
