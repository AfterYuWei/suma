package imageupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/distribution/reference"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	domainEvent "github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/redact"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/taskcenter"
	"gorm.io/gorm"
)

// Dependencies wires the kind into node runtimes, registries and Compose
// without import cycles. Every field except Emit is required for checks.
type Dependencies struct {
	Emit           domainEvent.Sink
	Node           func(context.Context, string) (Node, error)
	Runtime        func(context.Context, string) (Runtime, error)
	Resolver       Resolver
	Credentials    CredentialStore
	Compose        func(context.Context, string) (*compose.Service, error)
	Audit          *audit.Service
	OnError        func(error)
	Now            func() time.Time
	Automation     *taskcenter.Service
	ConfigRevision func(context.Context, string, Params) (string, error)
}

// Service implements the image.update Task Center kind.
type Service struct {
	db        *gorm.DB
	tasks     *task.Service
	deps      Dependencies
	slots     chan struct{}
	admission sync.Mutex
}

func NewService(db *gorm.DB, tasks *task.Service, deps Dependencies) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{db: db, tasks: tasks, deps: deps, slots: make(chan struct{}, 4)}
}

// Kind registers this feature with the Task Center: one node-scoped task type
// driven by Params, schedulable and manually startable like every other kind.
func (s *Service) Kind() taskcenter.KindSpec {
	return taskcenter.KindSpec{
		Type:           KindImageUpdate,
		RequiresNode:   true,
		ScheduleTypes:  []string{"cron", "none"},
		Actions:        []string{"run", "check", "update"},
		ValidateParams: ValidateParams,
		Subject: func(raw json.RawMessage) string {
			var params Params
			_ = json.Unmarshal(raw, &params)
			return params.Project
		},
		Describe: describe,
		Start:    s.StartKind,
		ValidateChange: func(ctx context.Context, next database.TaskCenterSchedule) error {
			var params Params
			if json.Unmarshal(next.Params, &params) != nil || params.Project != next.Subject {
				return taskcenter.ErrInvalidParams
			}
			if next.Enabled && next.TimerEnabled && s.deps.ConfigRevision != nil {
				_, err := s.deps.ConfigRevision(ctx, next.NodeID, params)
				return err
			}
			return nil
		},
		ConfigVersion: func(_ context.Context, _ *gorm.DB, row database.TaskCenterSchedule) (string, error) {
			return fmt.Sprint(row.Version), nil
		},
	}
}

func describe(params json.RawMessage) string {
	var parsed Params
	if json.Unmarshal(params, &parsed) != nil || parsed.Project == "" {
		return ""
	}
	if parsed.AutoApply {
		return parsed.Project + " (detect and update)"
	}
	return parsed.Project + " (detect only)"
}

// ValidateParams checks stored params before a plan is saved or a run starts.
func ValidateParams(params json.RawMessage) error {
	var parsed Params
	decoder := json.NewDecoder(bytes.NewReader(params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return fmt.Errorf("%w: %s", taskcenter.ErrInvalidParams, err)
	}
	if !projectIdentifier.MatchString(parsed.Project) || len(parsed.Project) > 128 {
		return fmt.Errorf("%w: project name", taskcenter.ErrInvalidParams)
	}
	seen := map[string]bool{}
	for _, service := range parsed.Services {
		if !serviceIdentifier.MatchString(service) || len(service) > 128 || seen[service] {
			return fmt.Errorf("%w: selected service", taskcenter.ErrInvalidParams)
		}
		seen[service] = true
	}
	return nil
}

// StartKind launches one image.update run for the Task Center. A project may
// only have one active run at a time.
func (s *Service) StartKind(ctx context.Context, request taskcenter.StartRequest) (string, error) {
	s.admission.Lock()
	defer s.admission.Unlock()
	var params Params
	if err := json.Unmarshal(request.Params, &params); err != nil {
		return "", fmt.Errorf("%w: %s", taskcenter.ErrInvalidParams, err)
	}
	if request.NodeID == "" {
		return "", taskcenter.ErrInvalidParams
	}
	node, err := s.deps.Node(ctx, request.NodeID)
	if err != nil || !node.Enabled {
		return "", ErrUnavailable
	}
	if err := ValidateParams(request.Params); err != nil {
		return "", err
	}
	if request.ScheduleID != nil && params.Project != request.Subject {
		return "", taskcenter.ErrInvalidParams
	}
	if request.Action == "check" {
		params.AutoApply = false
	}
	if request.Action == "update" {
		params.AutoApply = true
	}
	if running, err := s.runningTask(ctx, request.NodeID, params.Project); err != nil {
		return "", err
	} else if running != "" {
		return "", &BusyError{TaskID: running}
	}
	if params.AutoApply && s.deps.ConfigRevision != nil {
		if _, err := s.deps.ConfigRevision(ctx, node.ID, params); err != nil {
			return "", err
		}
	}
	name := "Check image updates " + params.Project
	if params.AutoApply {
		name = "Image update " + params.Project
	}
	row, err := s.tasks.StartDefinition(ctx, task.Definition{
		Type: KindImageUpdate, Name: name,
		NodeID: node.ID, NodeName: nodeName(request, node),
		Params:          params,
		TriggerRunID:    request.RunID,
		ArtifactSubject: params.Project,
	}, func(taskCtx context.Context, taskID string, report task.Reporter) (any, error) {
		if s.deps.Automation != nil {
			if err := s.deps.Automation.ValidateRun(taskCtx, request.RunID); err != nil {
				return nil, err
			}
		}
		return s.run(taskCtx, node, params, taskID, report)
	})
	if err != nil {
		return "", err
	}
	return row.ID, nil
}

func nodeName(request taskcenter.StartRequest, node Node) string {
	if request.NodeName != "" {
		return request.NodeName
	}
	return node.Name
}

// runningTask returns the active image.update task of one project, if any.
func (s *Service) runningTask(ctx context.Context, nodeID, project string) (string, error) {
	var row database.Task
	err := s.db.WithContext(ctx).Where(
		"type = ? AND node_id = ? AND status IN ? AND params->>'project' = ?",
		KindImageUpdate, nodeID, []string{task.StatusPending, task.StatusRunning}, project,
	).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.ID, nil
}

// checkTarget is one eligible Compose service to compare against its registry.
type checkTarget struct {
	service   string
	usage     Usage
	image     LocalImage
	reference string
	registry  string
	replicas  int
}

// run performs the check and, when enabled, the digest-pinned update.
func (s *Service) run(ctx context.Context, node Node, params Params, taskID string, report task.Reporter) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	mode := "check"
	if params.AutoApply {
		mode = "auto"
	}
	report(2, "Collecting project services from Docker")
	runtime, err := s.deps.Runtime(ctx, node.ID)
	if err != nil {
		return nil, err
	}
	inventory, err := runtime.UpdateInventory(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to collect images: %w", err)
	}
	revision := ""
	var declared map[string]string
	if s.deps.Compose != nil {
		projectService, err := s.deps.Compose(ctx, node.ID)
		if err != nil {
			return nil, err
		}
		resolved, err := projectService.ResolvedImages(ctx, params.Project)
		if err != nil {
			return nil, err
		}
		if resolved != nil {
			revision, declared = resolved.Revision, resolved.Images
		}
	}
	targets, blocked := planChecks(inventory, params, declared)
	for _, item := range blocked {
		report(5, "Skipping "+item.Service+": "+item.ReasonCode)
	}
	materials, err := s.materials(ctx, node.ID)
	if err != nil {
		return nil, err
	}
	report(8, fmt.Sprintf("Checking %d services against their registries", len(targets)))
	results := make([]ServiceCheck, len(targets))
	var wg sync.WaitGroup
	var progressMu sync.Mutex
	checked := 0
	for i := range targets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case s.slots <- struct{}{}:
			case <-ctx.Done():
				results[i] = ServiceCheck{Service: targets[i].service, Status: StatusUnavailable, ReasonCode: "timeout"}
				return
			}
			defer func() { <-s.slots }()
			results[i] = s.checkOne(ctx, targets[i], materials)
			progressMu.Lock()
			checked++
			report(8+checked*40/max(1, len(targets)), fmt.Sprintf("Checked %d/%d services", checked, len(targets)))
			progressMu.Unlock()
		}(i)
	}
	wg.Wait()
	for _, step := range results {
		s.tasks.ReportStep(ctx, taskID, "service:"+step.Service, step.Status, int64(step.Replicas), int64(step.Replicas), 100)
	}
	results = append(results, blocked...)
	sort.Slice(results, func(i, j int) bool { return results[i].Service < results[j].Service })
	summary := Summary{Project: params.Project, Mode: mode, Services: results, FinishedAt: s.deps.Now().UTC()}

	var updates []ServiceCheck
	for _, item := range results {
		if item.Status == StatusUpdateAvailable && (len(params.Services) == 0 || contains(params.Services, item.Service)) {
			updates = append(updates, item)
		}
	}
	if len(updates) > 0 && !params.AutoApply {
		s.emit(domainEvent.Event{
			Type: "image.available", Severity: "info", Scope: "node", NodeID: node.ID, NodeName: node.Name,
			ResourceType: "project", ResourceID: params.Project, Project: params.Project,
			Title:  fmt.Sprintf("%d image updates available for %s", len(updates), params.Project),
			TaskID: taskID, DedupeKey: "image.available:" + node.ID + ":" + params.Project, Time: s.deps.Now().UTC(),
		})
	}
	if params.AutoApply && len(updates) > 0 && ctx.Err() == nil {
		if err := s.apply(ctx, node, params, updates, taskID, report, &summary, revision); err != nil {
			summary.FinishedAt = s.deps.Now().UTC()
			s.finish(ctx, node, params, taskID, summary, err)
			return summary, err
		}
	}
	failure := firstUnavailable(results)
	summary.FinishedAt = s.deps.Now().UTC()
	s.finish(ctx, node, params, taskID, summary, failure)
	return summary, failure
}

// apply pins the detected digests and recreates the selected services through
// the ordinary Compose pipeline; the child compose.update task carries the
// pull and recreate evidence in the Task Center.
func (s *Service) apply(ctx context.Context, node Node, params Params, updates []ServiceCheck, taskID string, report task.Reporter, summary *Summary, revision string) error {
	composeService, err := s.deps.Compose(ctx, node.ID)
	if err != nil {
		return err
	}
	project, err := composeService.ManagedConfiguration(params.Project)
	if err != nil {
		return err
	}
	if revision == "" || project.Revision != revision {
		return compose.ErrRevisionConflict
	}
	services := make([]compose.ServiceImageUpdate, 0, len(updates))
	for _, item := range updates {
		services = append(services, compose.ServiceImageUpdate{
			Name: item.Service, Reference: item.Reference, ManifestDigest: item.RemoteManifestDigest,
			ConfigDigest: item.RemoteConfigDigest,
			OS:           strings.SplitN(item.Platform, "/", 2)[0],
			Architecture: strings.SplitN(item.Platform, "/", 3)[1], Variant: platformVariant(item.Platform),
			Replicas: item.Replicas,
		})
	}
	report(55, fmt.Sprintf("Pulling and recreating %d updated services", len(services)))
	row, err := composeService.UpdateSelected(ctx, params.Project, project.Revision, compose.SelectedUpdate{
		Services: services, AllowDockerSocket: params.AllowDockerSocket,
		ParentTaskID: taskID,
		// Check re-resolves the digests right before recreating so a
		// reference that moved again after the check never deploys stale bytes.
		Check: func(checkCtx context.Context) error {
			materials, mErr := s.materials(checkCtx, node.ID)
			if mErr != nil {
				return mErr
			}
			for _, item := range updates {
				remote, rErr := s.deps.Resolver.Resolve(checkCtx, item.Reference, parsePlatform(item.Platform), materials[item.Registry])
				if rErr != nil {
					return fmt.Errorf("verify %s: %w", item.Service, rErr)
				}
				if remote.ManifestDigest != item.RemoteManifestDigest {
					return fmt.Errorf("%s moved to a newer manifest; check the project again", item.Service)
				}
			}
			return nil
		},
		Complete: func(childID string, childErr error) {
			if childErr != nil {
				s.reportAudit(node, params.Project, childID, "failed")
			}
		},
	})
	if err != nil {
		return err
	}
	summary.UpdateTaskID = row.ID
	stopCancel := context.AfterFunc(ctx, func() { s.tasks.Cancel(row.ID) })
	defer stopCancel()
	if err := s.tasks.Wait(context.Background(), row.ID); err != nil {
		return err
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished, err := s.tasks.Get(finishCtx, row.ID)
	if err != nil {
		return err
	}
	if finished.Status != task.StatusSuccess {
		return fmt.Errorf("image update failed: %s", finished.Message)
	}
	for _, item := range updates {
		summary.Updated = append(summary.Updated, item.Service)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.emit(domainEvent.Event{
		Type: "image.updated", Severity: "info", Scope: "node", NodeID: node.ID, NodeName: node.Name,
		ResourceType: "project", ResourceID: params.Project, Project: params.Project,
		Title:  fmt.Sprintf("Updated %d services in %s", len(updates), params.Project),
		TaskID: taskID, DedupeKey: "image.updated:" + node.ID + ":" + params.Project + ":" + row.ID, Time: s.deps.Now().UTC(),
	})
	return nil
}

// checkOne compares one service's local image against its remote digests.
func (s *Service) checkOne(ctx context.Context, target checkTarget, materials map[string]credential.RegistryMaterial) ServiceCheck {
	result := ServiceCheck{
		Service: target.service, Reference: target.reference, Registry: target.registry,
		Platform: target.image.Platform.String(), LocalImageID: target.image.ID, Replicas: target.replicas,
	}
	remote, err := s.deps.Resolver.Resolve(ctx, target.reference, target.image.Platform, materials[target.registry])
	if err != nil {
		var lookup *LookupError
		code := "registry_unreachable"
		if errors.As(err, &lookup) {
			code = lookup.Code
		}
		if code == "authentication_required" && materials[target.registry].Secret == "" {
			code = "credential_unavailable"
		}
		result.Status = StatusUnavailable
		result.ReasonCode = code
		return result
	}
	result.RemoteManifestDigest = remote.ManifestDigest
	result.RemoteConfigDigest = remote.ConfigDigest
	if remote.ConfigDigest == target.image.ID {
		result.Status = StatusCurrent
	} else {
		result.Status = StatusUpdateAvailable
	}
	return result
}

// planChecks groups a project's containers into eligible check targets plus
// blocked entries that stay untouched by updates.
func planChecks(inventory Inventory, params Params, declarations ...map[string]string) (targets []checkTarget, blocked []ServiceCheck) {
	var declared map[string]string
	if len(declarations) > 0 {
		declared = declarations[0]
	}
	images := map[string]LocalImage{}
	for _, image := range inventory.Images {
		images[image.ID] = image
	}
	type group struct {
		usages []Usage
	}
	groups := map[string]*group{}
	order := []string{}
	for _, usage := range inventory.Containers {
		if usage.Project != params.Project || usage.OneOff || usage.Service == "" {
			continue
		}
		if _, ok := groups[usage.Service]; !ok {
			groups[usage.Service] = &group{}
			order = append(order, usage.Service)
		}
		if len(params.Services) > 0 && !contains(params.Services, usage.Service) {
			continue
		}
		groups[usage.Service].usages = append(groups[usage.Service].usages, usage)
	}
	sort.Strings(order)
	for _, service := range order {
		entries := groups[service].usages
		if len(entries) == 0 {
			continue
		}
		first := entries[0]
		blockedItem := func(reason string) {
			blocked = append(blocked, ServiceCheck{Service: service, Status: StatusBlocked, ReasonCode: reason, Replicas: len(entries)})
		}
		if first.DeliveryProject != "" {
			blockedItem("managed_by_cd")
			continue
		}
		if first.State != "running" {
			blockedItem("not_running")
			continue
		}
		normalized, registry, reason := Normalize(first.Reference)
		if declared != nil {
			declaredRef, declaredRegistry, declaredReason := Normalize(declared[service])
			if declaredReason != "" {
				blockedItem(declaredReason)
				continue
			}
			runningRef, runningErr := reference.ParseNormalizedNamed(first.Reference)
			configuredRef, configuredErr := reference.ParseNormalizedNamed(declaredRef)
			if runningErr != nil || configuredErr != nil || reference.TrimNamed(runningRef).String() != reference.TrimNamed(configuredRef).String() || (reason != "digest_pinned" && normalized != declaredRef) {
				blockedItem("image_mismatch")
				continue
			}
			normalized, registry, reason = declaredRef, declaredRegistry, ""
		}
		if reason != "" {
			blockedItem(reason)
			continue
		}
		image, ok := images[first.ImageID]
		if !ok {
			blockedItem("image_id")
			continue
		}
		for _, usage := range entries {
			if usage.ImageID != first.ImageID || usage.State != "running" || usage.DeliveryProject != "" || usage.Reference != first.Reference {
				blockedItem("image_mismatch")
				break
			}
		}
		if blocked != nil && len(blocked) > 0 && blocked[len(blocked)-1].Service == service {
			continue
		}
		targets = append(targets, checkTarget{service: service, usage: first, image: image, reference: normalized, registry: registry, replicas: len(entries)})
	}
	return targets, blocked
}

// materials loads every registry credential authorized for the node, keyed by
// canonical registry host.
func (s *Service) materials(ctx context.Context, nodeID string) (map[string]credential.RegistryMaterial, error) {
	result := map[string]credential.RegistryMaterial{}
	if s.deps.Credentials == nil {
		return result, nil
	}
	rows, err := s.deps.Credentials.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err := s.deps.Credentials.AuthorizedForNode(ctx, row.ID, nodeID); err != nil {
			continue
		}
		material, err := s.deps.Credentials.Material(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		result[RegistryHost(row.ServerAddress)] = material
	}
	return result, nil
}

// finish persists the run artifact, audit trail and failure events.
func (s *Service) finish(ctx context.Context, node Node, params Params, taskID string, _ Summary, failure error) {
	outcome := "success"
	if failure != nil {
		outcome = "failed"
	}
	s.reportAudit(node, params.Project, taskID, outcome)
	if failure != nil {
		severity := "error"
		if ctx.Err() != nil {
			severity = "warning"
		}
		s.emit(domainEvent.Event{
			Type: "image.check_failed", Severity: severity, Scope: "node", NodeID: node.ID, NodeName: node.Name,
			ResourceType: "project", ResourceID: params.Project, Project: params.Project,
			Title: "Image update check failed for " + params.Project, Message: redact.Bounded(failure.Error(), 512),
			TaskID: taskID, DedupeKey: "image.check_failed:" + node.ID + ":" + params.Project, Time: s.deps.Now().UTC(),
		})
	}
}

func (s *Service) reportAudit(node Node, project, taskID, outcome string) {
	if s.deps.Audit == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.deps.Audit.RecordLinkedForNode(ctx, node.ID, node.Name, nil, KindImageUpdate, "project", project, "", outcome, taskID, nil)
}

func (s *Service) emit(value domainEvent.Event) {
	if s.deps.Emit != nil {
		s.deps.Emit(value)
	}
}

// firstUnavailable surfaces the first registry failure so the task fails with
// a meaningful message while partial results stay recorded.
func firstUnavailable(results []ServiceCheck) error {
	for _, item := range results {
		if item.Status == StatusUnavailable {
			return fmt.Errorf("%s: %s", item.Service, item.ReasonCode)
		}
	}
	return nil
}

func parsePlatform(value string) Platform {
	parts := strings.SplitN(value, "/", 3)
	var parsed Platform
	if len(parts) > 0 {
		parsed.OS = parts[0]
	}
	if len(parts) > 1 {
		parsed.Architecture = parts[1]
	}
	if len(parts) > 2 {
		parsed.Variant = parts[2]
	}
	return parsed
}

func platformVariant(value string) string {
	parts := strings.SplitN(value, "/", 3)
	if len(parts) > 2 {
		return parts[2]
	}
	return ""
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// ProjectStatus is the persisted image update state of one project.
type ProjectStatus struct {
	Project            string     `json:"project"`
	LastTaskID         string     `json:"last_task_id,omitempty"`
	LastTaskStatus     string     `json:"last_task_status,omitempty"`
	LastTaskFinishedAt *time.Time `json:"last_task_finished_at,omitempty"`
	RunningTaskID      string     `json:"running_task_id,omitempty"`
	LastResult         *Summary   `json:"last_result,omitempty"`
}

// View assembles the latest check outcome per project of a node, optionally
// focused on a single project. Live runs override the stored artifact.
func (s *Service) View(ctx context.Context, nodeID, project string) ([]ProjectStatus, error) {
	clause := ""
	args := []any{KindImageUpdate, nodeID, []string{task.StatusPending, task.StatusRunning}}
	if project != "" {
		clause = " AND COALESCE(t.params->>'project', a.subject) = ?"
		args = append(args, project)
	}
	var rows []struct {
		TaskID     string
		Subject    string
		Status     string
		FinishedAt *time.Time
		Data       database.JSONB
	}
	query := "SELECT DISTINCT ON (COALESCE(t.params->>'project', a.subject)) t.id AS task_id, COALESCE(t.params->>'project', a.subject) AS subject, t.status, t.finished_at, COALESCE(t.result,a.data) AS data FROM tasks t LEFT JOIN task_center_artifacts a ON a.task_id = t.id WHERE t.type = ? AND t.node_id = ? AND t.status NOT IN ? AND COALESCE(t.params->>'project',a.subject,'') <> ''" + clause + " ORDER BY COALESCE(t.params->>'project',a.subject),t.created_at DESC,t.id DESC"
	if err := s.db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	byProject := map[string]*ProjectStatus{}
	for _, row := range rows {
		status := &ProjectStatus{Project: row.Subject, LastTaskID: row.TaskID, LastTaskStatus: row.Status, LastTaskFinishedAt: row.FinishedAt}
		if len(row.Data) > 0 {
			var summary Summary
			if err := json.Unmarshal(row.Data, &summary); err != nil {
				return nil, fmt.Errorf("invalid stored image update result")
			}
			status.LastResult = &summary
		}
		byProject[row.Subject] = status
	}
	var running []database.Task
	if err := s.db.WithContext(ctx).Select("id", "params").Where("type = ? AND node_id = ? AND status IN ?", KindImageUpdate, nodeID, []string{task.StatusPending, task.StatusRunning}).Find(&running).Error; err != nil {
		return nil, err
	}
	for _, row := range running {
		subject := gormScanSubject(row.Params)
		if subject == "" || (project != "" && subject != project) {
			continue
		}
		status := byProject[subject]
		if status == nil {
			status = &ProjectStatus{Project: subject}
			byProject[subject] = status
		}
		status.RunningTaskID = row.ID
	}
	order := make([]string, 0, len(byProject))
	for subject := range byProject {
		order = append(order, subject)
	}
	sort.Strings(order)
	result := make([]ProjectStatus, 0, len(order))
	for _, subject := range order {
		result = append(result, *byProject[subject])
	}
	return result, nil
}

// gormScanSubject extracts the project subject from stored task params.
func gormScanSubject(params database.JSONB) string {
	var parsed Params
	if json.Unmarshal(params, &parsed) != nil {
		return ""
	}
	return parsed.Project
}

// SetEventSink wires notification delivery after the notification service is
// constructed; events emitted before wiring are dropped.
func (s *Service) SetEventSink(sink domainEvent.Sink) { s.deps.Emit = sink }
