package imageupdate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/suma/suma/server/internal/credential"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrSuperseded = fmt.Errorf("%w: image version or project changed; a new project check is required", task.ErrNotExecuted)

func cacheScope(node, project string) string { return node + "\x00" + project }
func encoded(v any) string                   { b, _ := json.Marshal(v); return string(b) }
func fingerprint(v any) string {
	sum := sha256.Sum256([]byte(encoded(v)))
	return hex.EncodeToString(sum[:])
}
func identifier() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

type Candidate struct {
	database.ProjectImageCandidate
	Services []compose.ServiceImageUpdate `json:"services"`
}
type ProjectPolicyInput struct {
	ExpectedVersion uint64 `json:"expected_version"`
	Mode            string `json:"mode"`
	Cron            string `json:"cron"`
	Timezone        string `json:"timezone"`
}
type ProjectSummary struct {
	Project          string     `json:"project"`
	UpdatedServices  int        `json:"updated_services"`
	BlockedServices  int        `json:"blocked_services"`
	RecreateRequired bool       `json:"recreate_required"`
	CheckedAt        *time.Time `json:"checked_at,omitempty"`
	Error            string     `json:"error,omitempty"`
	Candidate        *Candidate `json:"candidate,omitempty"`
}

type ScheduleInput struct {
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"`
}

func PreviewSchedule(input ScheduleInput, now time.Time) ([]time.Time, error) {
	if len(strings.Fields(input.Cron)) != 5 || len(input.Cron) > 256 {
		return nil, fmt.Errorf("%w: use five Cron fields: minute hour day month weekday", ErrInvalid)
	}
	loc, err := time.LoadLocation(input.Timezone)
	if err != nil || input.Timezone == "" || input.Timezone == "Local" {
		return nil, fmt.Errorf("%w: use an IANA timezone", ErrInvalid)
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse("CRON_TZ=" + loc.String() + " " + input.Cron)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid Cron expression", ErrInvalid)
	}
	rows := []time.Time{}
	for i := 0; i < 5; i++ {
		now = schedule.Next(now)
		if now.IsZero() {
			return nil, fmt.Errorf("%w: Cron has no future execution", ErrInvalid)
		}
		rows = append(rows, now.UTC())
	}
	return rows, nil
}

func (s *Service) project(ctx context.Context, node, name string) (compose.Project, error) {
	if !projectIdentifier.MatchString(name) || len(name) > 128 || s.deps.Compose == nil {
		return compose.Project{}, ErrInvalid
	}
	service, err := s.deps.Compose(ctx, node)
	if err != nil {
		return compose.Project{}, err
	}
	row, err := service.ManagedConfiguration(name)
	if err != nil {
		return row, err
	}
	if !row.Managed || row.DeliveryProjectName != "" || row.DeploymentMode == "cd" {
		return row, errors.New("image publishing requires an ordinary managed Compose project")
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&database.DeliveryProject{}).Where("deployment_name = ? OR (deployment_name = '' AND ? = 'suma-cd-' || id::text)", name, name).Count(&count).Error; err != nil {
		return row, err
	}
	if count > 0 {
		return row, errors.New("CD projects use the release workflow")
	}
	return row, nil
}
func (s *Service) ProjectPolicy(ctx context.Context, node, name string) (database.ProjectImageUpdate, error) {
	if _, err := s.project(ctx, node, name); err != nil {
		return database.ProjectImageUpdate{}, err
	}
	row := database.ProjectImageUpdate{NodeID: node, Project: name, Mode: "inherit", Cron: "0 */6 * * *", Timezone: "UTC", ResultsJSON: "[]"}
	err := s.db.WithContext(ctx).First(&row, "node_id = ? AND project = ?", node, name).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = nil
	}
	return row, err
}
func (s *Service) SaveProjectPolicy(ctx context.Context, node, name string, in ProjectPolicyInput, actor Actor) (database.ProjectImageUpdate, error) {
	if _, err := s.project(ctx, node, name); err != nil {
		return database.ProjectImageUpdate{}, err
	}
	if in.Mode != "inherit" && in.Mode != "custom" && in.Mode != "disabled" {
		return database.ProjectImageUpdate{}, ErrInvalid
	}
	next, err := PreviewSchedule(ScheduleInput{in.Cron, in.Timezone}, s.deps.Now())
	if err != nil {
		return database.ProjectImageUpdate{}, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", encoded([]string{node, name})).Error; err != nil {
			return err
		}
		row := database.ProjectImageUpdate{NodeID: node, Project: name, ResultsJSON: "[]"}
		err := tx.First(&row, "node_id = ? AND project = ?", node, name).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if row.Version != in.ExpectedVersion {
			return ErrConflict
		}
		row.Version++
		row.Mode = in.Mode
		row.Cron = in.Cron
		row.Timezone = in.Timezone
		row.NextRunAt = nil
		if in.Mode == "custom" {
			row.NextRunAt = &next[0]
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "node_id"}, {Name: "project"}}, DoUpdates: clause.AssignmentColumns([]string{"version", "mode", "cron", "timezone", "next_run_at"})}).Create(&row).Error
	})
	if err != nil {
		return database.ProjectImageUpdate{}, err
	}
	s.record(ctx, node, actor, "image.updates.project_policy", "success", "")
	return s.ProjectPolicy(ctx, node, name)
}
func candidateView(row database.ProjectImageCandidate) (*Candidate, error) {
	candidate := &Candidate{ProjectImageCandidate: row}
	if err := json.Unmarshal([]byte(row.ServicesJSON), &candidate.Services); err != nil {
		return nil, err
	}
	return candidate, nil
}
func (s *Service) Candidate(ctx context.Context, node, name, id string) (*Candidate, error) {
	var row database.ProjectImageCandidate
	q := s.db.WithContext(ctx).Where("node_id = ? AND project = ?", node, name)
	if id != "" {
		q = q.Where("id = ?", id)
	}
	if err := q.Order("created_at DESC, id DESC").First(&row).Error; err != nil {
		if id == "" && errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return candidateView(row)
}

// Projection is read-only. Live usages are always supplied by the node runtime.
func (s *Service) projectViewLocked(ctx context.Context, node Node, name string, inv Inventory, age time.Duration) (View, error) {
	var saved database.ProjectImageUpdate
	err := s.db.WithContext(ctx).First(&saved, "node_id = ? AND project = ?", node.ID, name).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return View{}, err
	}
	if saved.Mode == "custom" && saved.LastCheckedAt != nil {
		if times, err := PreviewSchedule(ScheduleInput{saved.Cron, saved.Timezone}, *saved.LastCheckedAt); err == nil {
			age = times[0].Sub(*saved.LastCheckedAt)
		}
	}
	observations := map[string]cached{}
	if saved.RuntimeKey == node.RuntimeKey {
		var rows []Result
		if err == nil {
			if err := json.Unmarshal([]byte(saved.ResultsJSON), &rows); err != nil {
				return View{}, err
			}
		}
		for _, r := range rows {
			observations[key(r)] = cached{RuntimeKey: node.RuntimeKey, Result: r}
		}
	}
	for k, c := range s.cache[cacheScope(node.ID, name)] {
		observations[k] = c
	}
	result := View{Results: []Result{}, RunningTaskID: s.scopes[cacheScope(node.ID, name)]}
	for _, t := range targets(inv, CheckInput{ProjectName: name}) {
		r := t.result
		if c, ok := observations[key(r)]; ok && c.RuntimeKey == node.RuntimeKey {
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
		} else if result.RunningTaskID != "" && r.Status == "unchecked" {
			r.Status = "checking"
		}
		result.Results = append(result.Results, r)
	}
	sort.Slice(result.Results, func(i, j int) bool { return key(result.Results[i]) < key(result.Results[j]) })
	result.Candidate, err = s.Candidate(ctx, node.ID, name, "")
	return result, err
}

func selectedServices(results []Result) ([]compose.ServiceImageUpdate, int) {
	selected := map[string]compose.ServiceImageUpdate{}
	blocked := map[string]bool{}
	changed := map[string]bool{}
	for _, r := range results {
		for _, u := range r.Containers {
			if u.Service == "" || u.OneOff {
				continue
			}
			if u.State != "running" || u.DeliveryProject != "" {
				blocked[u.Service] = true
			}
			if r.RemoteConfigDigest == "" || r.RemoteManifestDigest == "" || r.Status == "unavailable" || r.Status == "pinned" {
				blocked[u.Service] = true
				continue
			}
			value, ok := selected[u.Service]
			if ok && (value.Reference != r.Reference || value.ConfigDigest != r.RemoteConfigDigest || value.OS != r.Platform.OS || value.Architecture != r.Platform.Architecture || value.Variant != r.Platform.Variant) {
				blocked[u.Service] = true
			}
			if !ok {
				value = compose.ServiceImageUpdate{Name: u.Service, Reference: r.Reference, ManifestDigest: r.RemoteManifestDigest, ConfigDigest: r.RemoteConfigDigest, OS: r.Platform.OS, Architecture: r.Platform.Architecture, Variant: r.Platform.Variant}
			}
			value.Replicas++
			selected[u.Service] = value
			if u.ImageID != r.RemoteConfigDigest {
				changed[u.Service] = true
			}
		}
	}
	rows := []compose.ServiceImageUpdate{}
	skipped := 0
	for name := range changed {
		if blocked[name] {
			skipped++
			continue
		}
		rows = append(rows, selected[name])
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, skipped
}
func (s *Service) observeProject(ctx context.Context, node Node, name string, checkErr error) error {
	project, err := s.project(ctx, node.ID, name)
	if err != nil {
		return err
	}
	view, err := s.View(ctx, node.ID, name)
	if err != nil {
		return err
	}
	services, blocked := selectedServices(view.Results)
	if checkErr != nil {
		services = nil
	}
	now := s.deps.Now().UTC()
	message := ""
	if checkErr != nil {
		message = checkErr.Error()
	}
	row := database.ProjectImageUpdate{NodeID: node.ID, Project: name, Mode: "inherit", Cron: "0 */6 * * *", Timezone: "UTC", ResultsJSON: encoded(view.Results), RuntimeKey: node.RuntimeKey, LastCheckedAt: &now, LastError: message}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "node_id"}, {Name: "project"}}, DoUpdates: clause.AssignmentColumns([]string{"results_json", "runtime_key", "last_checked_at", "last_error"})}).Create(&row).Error; err != nil {
		return err
	}
	var created *Candidate
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", encoded([]string{node.ID, name})).Error; err != nil {
			return err
		}
		var previous database.ProjectImageCandidate
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("node_id = ? AND project = ? AND status IN ?", node.ID, name, []string{"pending", "publishing"}).First(&previous).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if previous.Status == "publishing" {
			return nil
		}
		signature := fingerprint([]any{node.RuntimeKey, project.Revision, services})
		if previous.ID != "" && previous.Fingerprint == signature {
			return nil
		}
		if previous.ID != "" {
			if err := tx.Model(&previous).Updates(map[string]any{"status": "superseded", "updated_at": now}).Error; err != nil {
				return err
			}
		}
		if len(services) == 0 {
			return nil
		}
		candidate := database.ProjectImageCandidate{ID: identifier(), NodeID: node.ID, Project: name, Fingerprint: signature, Revision: project.Revision, RuntimeKey: node.RuntimeKey, RequiresDockerSocket: requiresSocket(project.Compose), ServicesJSON: encoded(services), Status: "pending", ActorJSON: "{}", CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&candidate).Error; err != nil {
			return err
		}
		created = &Candidate{ProjectImageCandidate: candidate, Services: services}
		return nil
	})
	if err != nil {
		return err
	}
	if s.deps.Emit != nil {
		if checkErr == nil {
			s.deps.Emit(event.Event{Type: "image.check_recovered", Severity: "info", NodeID: node.ID, NodeName: node.Name, Project: name, ResourceType: "project", ResourceID: name, Title: name, Message: "Project image registry check succeeded"})
		}
		if checkErr != nil {
			s.deps.Emit(event.Event{Type: "image.check_failed", Severity: "warning", NodeID: node.ID, NodeName: node.Name, Project: name, ResourceType: "project", ResourceID: name, Title: name, Message: message, DedupeKey: node.ID + "|" + name + "|check_failed"})
		}
		if created != nil {
			s.emitCandidate(node, *created)
		} else if len(services) == 0 && blocked > 0 {
			s.deps.Emit(event.Event{Type: "image.available", Severity: "info", NodeID: node.ID, NodeName: node.Name, Project: name, ResourceType: "project", ResourceID: name, Title: name, Message: "Stopped or paused services have updates; start and check them before publishing", DedupeKey: blockedFingerprint(node.ID, name, view.Results)})
		}
	}
	return nil
}
func (s *Service) emitCandidate(node Node, c Candidate) {
	lines := []string{}
	for _, v := range c.Services {
		lines = append(lines, v.Name+" · "+v.Reference+" → "+v.ManifestDigest)
	}
	s.deps.Emit(event.Event{Type: "image.available", Severity: "info", NodeID: node.ID, NodeName: node.Name, Project: c.Project, ResourceType: "project", ResourceID: c.Project, Title: c.Project, Message: strings.Join(lines, "\n"), CandidateID: c.ID, DedupeKey: c.ID})
}
func (s *Service) ProjectSummaries(ctx context.Context, node string) ([]ProjectSummary, error) {
	var rows []database.ProjectImageUpdate
	if _, err := s.deps.Node(ctx, node); err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Where("node_id = ?", node).Find(&rows).Error; err != nil {
		return nil, err
	}
	var candidates []database.ProjectImageCandidate
	if err := s.db.WithContext(ctx).Select("DISTINCT ON (project) *").Where("node_id = ?", node).Order("project, created_at DESC, id DESC").Find(&candidates).Error; err != nil {
		return nil, err
	}
	latest := map[string]*Candidate{}
	for _, c := range candidates {
		if _, ok := latest[c.Project]; !ok {
			v, err := candidateView(c)
			if err != nil {
				return nil, err
			}
			latest[c.Project] = v
		}
	}
	result := []ProjectSummary{}
	for _, row := range rows {
		var observations []Result
		if err := json.Unmarshal([]byte(row.ResultsJSON), &observations); err != nil {
			return nil, err
		}
		services, blocked := selectedServices(observations)
		summary := ProjectSummary{Project: row.Project, UpdatedServices: len(services) + blocked, BlockedServices: blocked, CheckedAt: row.LastCheckedAt, Error: row.LastError, Candidate: latest[row.Project]}
		for _, r := range observations {
			summary.RecreateRequired = summary.RecreateRequired || r.RecreateRequired && !r.PullRequired
		}
		result = append(result, summary)
	}
	return result, nil
}

func (s *Service) verifyCandidate(ctx context.Context, c Candidate) error {
	node, err := s.deps.Node(ctx, c.NodeID)
	if err != nil {
		return err
	}
	project, err := s.project(ctx, c.NodeID, c.Project)
	if err != nil {
		return err
	}
	if !node.Enabled || !node.Available {
		return ErrUnavailable
	}
	if node.RuntimeKey != c.RuntimeKey || project.Revision != c.Revision {
		return ErrSuperseded
	}
	runtime, err := s.deps.Runtime(ctx, c.NodeID)
	if err != nil {
		return err
	}
	inventory, err := runtime.UpdateInventory(ctx)
	if err != nil {
		return err
	}
	for _, service := range c.Services {
		changed := false
		for _, usage := range inventory.Containers {
			if usage.Project == c.Project && usage.Service == service.Name && !usage.OneOff && usage.ImageID != service.ConfigDigest {
				changed = true
			}
		}
		if !changed {
			return ErrSuperseded
		}
	}
	policy, err := s.Policy(ctx, c.NodeID)
	if err != nil {
		return err
	}
	batch := &lookupBatch{entries: map[string]*lookupEntry{}}
	for _, v := range c.Services {
		_, host, _ := Normalize(v.Reference)
		material, err := s.material(ctx, c.NodeID, host, policy.RegistryCredentials[host])
		if err != nil {
			return err
		}
		remote, err := s.resolve(ctx, v.Reference, Platform{v.OS, v.Architecture, v.Variant}, material, batch)
		if err != nil {
			return err
		}
		if remote.ManifestDigest != v.ManifestDigest || remote.ConfigDigest != v.ConfigDigest {
			return ErrSuperseded
		}
	}
	return nil
}
func (s *Service) Publish(ctx context.Context, node, name, id string, actor Actor, allowSocket ...bool) (database.Task, error) {
	candidate, err := s.Candidate(ctx, node, name, id)
	if err != nil {
		return database.Task{}, err
	}
	project, err := s.project(ctx, node, name)
	if err != nil {
		return database.Task{}, err
	}
	if project.Revision != candidate.Revision {
		s.report(s.db.WithContext(ctx).Model(&database.ProjectImageCandidate{}).Where("id = ? AND status = ?", id, "pending").Updates(map[string]any{"status": "superseded", "message": ErrSuperseded.Error(), "updated_at": s.deps.Now()}).Error)
		s.requestProjectCheck(node, name)
		return database.Task{}, ErrSuperseded
	}
	if candidate.RequiresDockerSocket && (len(allowSocket) == 0 || !allowSocket[0]) {
		return database.Task{}, errors.New("Docker socket mount requires explicit confirmation")
	}
	if candidate.Status == "publishing" && candidate.TaskID != "" {
		var row database.Task
		err = s.db.WithContext(ctx).First(&row, "id = ?", candidate.TaskID).Error
		return row, err
	}
	claim := s.db.WithContext(ctx).Model(&database.ProjectImageCandidate{}).Where("id = ? AND status = ?", id, "pending").Updates(map[string]any{"status": "publishing", "actor_json": encoded(actor), "updated_at": s.deps.Now()})
	if claim.Error != nil {
		return database.Task{}, claim.Error
	}
	if claim.RowsAffected != 1 {
		return database.Task{}, ErrConflict
	}
	service, err := s.deps.Compose(ctx, node)
	if err != nil {
		s.report(s.db.Model(&database.ProjectImageCandidate{}).Where("id = ?", id).Update("status", "pending").Error)
		return database.Task{}, err
	}
	result, err := service.UpdateSelected(ctx, name, candidate.Revision, compose.SelectedUpdate{Services: candidate.Services, AllowDockerSocket: candidate.RequiresDockerSocket,
		Start: func(ctx context.Context, taskID string) error {
			return s.db.WithContext(ctx).Model(&database.ProjectImageCandidate{}).Where("id = ? AND status = ?", id, "publishing").Updates(map[string]any{"task_id": taskID, "updated_at": s.deps.Now()}).Error
		},
		Check: func(ctx context.Context) error { return s.verifyCandidate(ctx, *candidate) },
		Complete: func(taskID string, workErr error) {
			status, message := "succeeded", "Published selected services"
			if workErr != nil {
				status = "failed"
				message = workErr.Error()
			}
			if errors.Is(workErr, context.Canceled) {
				status = "canceled"
			}
			if errors.Is(workErr, ErrSuperseded) {
				status = "superseded"
			}
			e := s.db.Model(&database.ProjectImageCandidate{}).Where("id = ?", id).Updates(map[string]any{"status": status, "message": message, "task_id": taskID, "updated_at": s.deps.Now()}).Error
			if e != nil {
				s.report(e)
				return
			}
			s.record(context.Background(), node, actor, "project.image.publish", status, taskID)
			if actor.ExternalID != "" {
				s.report(s.db.Create(&database.AuditLog{Scope: "node", NodeID: node, Action: "project.image.publisher", ResourceType: "project", ResourceName: name, Source: actor.Source, Details: encoded(actor), Result: status, TaskID: taskID}).Error)
			}
			if workErr == nil || errors.Is(workErr, ErrSuperseded) {
				s.requestProjectCheck(node, name)
			}
		},
	})
	if err != nil {
		s.report(s.db.Model(&database.ProjectImageCandidate{}).Where("id = ? AND task_id = ''", id).Update("status", "pending").Error)
	}
	return result, err
}
func (s *Service) report(err error) {
	if err != nil && s.deps.OnError != nil {
		s.deps.OnError(err)
	}
}

func (s *Service) scanProjectDue(ctx context.Context, global map[string]bool) {
	if s.deps.Nodes == nil || s.deps.Projects == nil {
		return
	}
	nodes, err := s.deps.Nodes(ctx)
	if err != nil {
		s.report(err)
		return
	}
	now := s.deps.Now().UTC()
	for _, nodeID := range nodes {
		node, err := s.deps.Node(ctx, nodeID)
		if err != nil {
			s.report(err)
			continue
		}
		projects, err := s.deps.Projects(ctx, nodeID)
		if err != nil {
			s.report(err)
			continue
		}
		inherited, err := s.Policy(ctx, nodeID)
		if err != nil {
			s.report(err)
			continue
		}
		due := []string{}
		for _, p := range projects {
			if !p.Managed || p.DeliveryProjectName != "" {
				continue
			}
			row, err := s.ProjectPolicy(ctx, nodeID, p.Name)
			if err != nil {
				s.report(err)
				continue
			}
			if row.Mode == "disabled" || row.Mode == "inherit" && !inherited.Enabled {
				continue
			}
			if row.NextRunAt != nil && row.NextRunAt.After(now) {
				continue
			}
			next := now.Add(time.Duration(inherited.IntervalHours) * time.Hour)
			if row.Mode == "custom" {
				times, err := PreviewSchedule(ScheduleInput{row.Cron, row.Timezone}, now)
				if err != nil {
					s.report(err)
					continue
				}
				next = times[0]
			}
			if row.NextRunAt == nil {
				row.NextRunAt = &next
				if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "node_id"}, {Name: "project"}}, DoUpdates: clause.Assignments(map[string]any{"next_run_at": next}), Where: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "project_image_updates.next_run_at IS NULL AND project_image_updates.version = ?", Vars: []any{row.Version}}}}}).Create(&row).Error; err != nil {
					s.report(err)
				}
				continue
			}
			claim := s.db.WithContext(ctx).Model(&database.ProjectImageUpdate{}).Where("node_id = ? AND project = ? AND version = ? AND next_run_at = ?", nodeID, p.Name, row.Version, row.NextRunAt).Update("next_run_at", next)
			if claim.Error != nil {
				s.report(claim.Error)
				continue
			}
			if claim.RowsAffected != 1 {
				continue
			}
			if !node.Available || !node.Enabled || row.NextRunAt != nil && now.Sub(*row.NextRunAt) >= time.Minute {
				s.report(s.db.WithContext(ctx).Model(&row).Update("last_error", "Scheduled check skipped: node unavailable or execution window missed").Error)
				continue
			}
			due = append(due, p.Name)
		}
		// A single ordinary image-check task owns the node; project scopes stay isolated.
		if len(due) > 0 || global[nodeID] {
			s.startProjectBatch(ctx, node, due, global[nodeID])
		}
	}
}
func (s *Service) startProjectBatch(ctx context.Context, node Node, names []string, global bool) {
	policy, err := s.Policy(ctx, node.ID)
	if err != nil {
		s.report(err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.running[node.ID] != "" {
		for _, name := range names {
			s.report(s.db.WithContext(ctx).Model(&database.ProjectImageUpdate{}).Where("node_id = ? AND project = ?", node.ID, name).Update("last_error", "Scheduled check skipped: another check is running").Error)
		}
		return
	}
	ready := make(chan struct{})
	kind := "project.image.check"
	if len(names) == 0 {
		kind = "image.updates.check"
	}
	row, err := s.tasks.StartWithIDForNode(node.ID, node.Name, kind, "Check project images", func(taskCtx context.Context, _ string, report task.Reporter) error {
		<-ready
		defer func() {
			s.mu.Lock()
			delete(s.running, node.ID)
			for _, name := range names {
				delete(s.scopes, cacheScope(node.ID, name))
			}
			delete(s.scopes, cacheScope(node.ID, ""))
			s.mu.Unlock()
			s.wg.Done()
		}()
		batch := &lookupBatch{entries: map[string]*lookupEntry{}}
		if global {
			err := s.check(taskCtx, node, CheckInput{}, policy.RegistryCredentials, report, batch)
			if len(names) == 0 {
				return err
			}
		}
		for _, name := range names {
			if taskCtx.Err() != nil {
				return taskCtx.Err()
			}
			err := s.check(taskCtx, node, CheckInput{ProjectName: name}, policy.RegistryCredentials, report, batch)
			if e := s.observeProject(taskCtx, node, name, err); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		s.report(err)
		return
	}
	s.running[node.ID] = row.ID
	for _, name := range names {
		s.scopes[cacheScope(node.ID, name)] = row.ID
	}
	if global {
		s.scopes[cacheScope(node.ID, "")] = row.ID
	}
	s.wg.Add(1)
	close(ready)
}

func requiresSocket(content string) bool {
	err := compose.ValidateComposeBindMounts(content, false, false)
	return err != nil && strings.Contains(err.Error(), "Docker socket mount")
}

// Deduplicate reference/platform/auth lookups only within one check batch.
// Publication verification always uses a fresh batch.
type lookupEntry struct {
	ready  chan struct{}
	remote Remote
	err    error
}
type lookupBatch struct {
	mu      sync.Mutex
	entries map[string]*lookupEntry
}

func (s *Service) resolve(ctx context.Context, reference string, platform Platform, material credential.RegistryMaterial, batch *lookupBatch) (Remote, error) {
	key := fingerprint([]any{reference, platform, material})
	batch.mu.Lock()
	entry, exists := batch.entries[key]
	if !exists {
		entry = &lookupEntry{ready: make(chan struct{})}
		batch.entries[key] = entry
	}
	batch.mu.Unlock()
	if exists {
		select {
		case <-entry.ready:
			return entry.remote, entry.err
		case <-ctx.Done():
			return Remote{}, ctx.Err()
		}
	}
	defer close(entry.ready)
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		entry.err = ctx.Err()
		return Remote{}, entry.err
	}
	defer func() { <-s.slots }()
	lookup, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	entry.remote, entry.err = s.deps.Resolver.Resolve(lookup, reference, platform, material)
	return entry.remote, entry.err
}

func blockedFingerprint(node, name string, rows []Result) string {
	values := []string{}
	for _, r := range rows {
		values = append(values, r.Reference+"|"+r.RemoteManifestDigest+"|"+r.LocalImageID)
	}
	sort.Strings(values)
	return fingerprint([]any{node, name, values})
}

func (s *Service) requestProjectCheck(node, name string) {
	_, err := s.Check(context.Background(), node, CheckInput{ProjectName: name}, Actor{})
	var busy *BusyError
	if !errors.As(err, &busy) {
		s.report(err)
		return
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err := s.tasks.Wait(ctx, busy.TaskID); err != nil {
			s.report(err)
			return
		}
		_, err := s.Check(ctx, node, CheckInput{ProjectName: name}, Actor{})
		s.report(err)
	}()
}
