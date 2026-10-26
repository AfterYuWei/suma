package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"
	"github.com/suma/suma/server/internal/event"
	nodeService "github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/notification"
	"gorm.io/gorm"
)

func auditNotifications(db *gorm.DB, notify *notification.Service) func(database.AuditLog) {
	return func(row database.AuditLog) {
		kind := ""
		severity := "info"
		message := row.Action + ": " + row.Result
		switch {
		case row.Action == "login" && row.Result == "success" && row.UserID != nil:
			kind = "auth.login"
			message = "Login completed from " + row.IP
			var count int64
			db.Model(&database.AuditLog{}).Where("id < ? AND action = ? AND result = ? AND user_id = ? AND ip = ?", row.ID, "login", "success", row.UserID, row.IP).Count(&count)
			if count == 0 {
				notify.Emit(event.Event{Type: "auth.new_ip", Severity: "warning", Scope: "control_plane", ActorID: row.UserID, Title: "New login IP", Message: message, DedupeKey: fmt.Sprintf("login-ip:%d:%s", *row.UserID, row.IP)})
			}
		case strings.Contains(row.Action, "password") || strings.Contains(row.Action, "two_factor") || strings.Contains(row.Action, "passkey"):
			if row.Result == "success" {
				kind = "account.changed"
			}
		case strings.Contains(row.Action, "credential") && row.Result == "success":
			kind = "credential.changed"
		case strings.HasPrefix(row.Action, "node.") && row.Result == "success":
			kind = "node.changed"
		case row.Action == "cleanup.finish" && row.Result == "skipped":
			kind = "cleanup.skipped"
		case row.Action == "cleanup.finish":
			kind = "cleanup.completed"
			var run database.CleanupRun
			if db.First(&run, "task_id = ?", row.TaskID).Error == nil {
				message = run.Status + ": " + run.Message + " " + run.ResultJSON
			}
		case row.Action == "cleanup.skip":
			kind = "cleanup.skipped"
		case strings.HasPrefix(row.Action, "cd.") && row.Result != "accepted" && row.TaskID != "":
			kind = "cd.completed"
		}
		if kind == "" {
			return
		}
		if row.Result == "failed" || row.Result == "partial_failed" {
			severity = "error"
		}
		notify.Emit(event.Event{Type: kind, Severity: severity, Scope: row.Scope, NodeID: row.NodeID, NodeName: row.NodeName, ResourceType: row.ResourceType, ResourceID: row.ResourceName, Title: row.Action, Message: message, TaskID: row.TaskID, ReleaseID: row.ReleaseID, ActorID: row.UserID, Time: row.CreatedAt})
	}
}

// Observers own cancellable Docker streams per explicit runtime. They persist
// historical events only; the current resource state stays in Docker.
func observeNotifications(ctx context.Context, nodes *nodeService.Service, db *gorm.DB, notify *notification.Service) {
	type watcher struct {
		key    string
		cancel context.CancelFunc
	}
	watchers := map[string]watcher{}
	offline := map[string]int{}
	restartTimes := map[string][]time.Time{}
	containerStates := map[string]map[string]docker.NotificationState{}
	stableSince := map[string]map[string]containerStability{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	defer func() {
		for _, w := range watchers {
			w.cancel()
		}
		wg.Wait()
	}()
	scan := func() {
		views, err := nodes.List(ctx)
		if err != nil {
			clear(stableSince)
			return
		}
		seen := map[string]bool{}
		for _, view := range views {
			seen[view.ID] = true
			if view.TLSCredentialID != nil {
				expiry, fp, err := nodes.TLSExpiry(ctx, *view.TLSCredentialID)
				if err == nil && time.Until(expiry) < 30*24*time.Hour {
					notify.Emit(event.Event{Type: "tls.expiring", Severity: "warning", NodeID: view.ID, NodeName: view.Name, Title: "Docker TLS certificate expires soon", Message: expiry.UTC().Format(time.RFC3339), DedupeKey: "tls-expiry:" + fp})
				} else if err == nil {
					notify.Emit(event.Event{Type: "tls.recovered", Severity: "info", NodeID: view.ID, NodeName: view.Name, Title: "Docker TLS certificate renewed", Message: expiry.UTC().Format(time.RFC3339)})
				}
			}
			if view.Enabled && view.ConnectionType == nodeService.ConnectionAgent && view.Status == "incompatible" {
				notify.Emit(event.Event{Type: "agent.error", Severity: "error", NodeID: view.ID, NodeName: view.Name, Title: "Agent protocol incompatible", Message: view.LastError})
			}
			if view.Status == "offline" {
				offline[view.ID]++
			} else {
				offline[view.ID] = 0
			}
			if view.Enabled && view.Status == "offline" && offline[view.ID] >= 2 {
				notify.Emit(event.Event{Type: "node.offline", Severity: "error", NodeID: view.ID, NodeName: view.Name, Title: view.Name, Message: "Docker node unavailable for two consecutive observations"})
				if view.ConnectionType == nodeService.ConnectionAgent {
					notify.Emit(event.Event{Type: "agent.error", Severity: "error", NodeID: view.ID, NodeName: view.Name, Title: "Agent connection unavailable", Message: view.LastError})
				}
			}
			if view.Enabled && view.Status == "online" {
				// Persisted fault evidence makes recovery work across control-plane restarts.
				notify.Emit(event.Event{Type: "node.recovered", Severity: "info", NodeID: view.ID, NodeName: view.Name, Title: view.Name, Message: "Docker node recovered"})
			}
			node, err := imageUpdateNode(nodes, db)(ctx, view.ID)
			if err != nil {
				if existing, ok := watchers[view.ID]; ok {
					delete(stableSince, existing.key)
				}
				continue
			}
			if view.Enabled && view.Status == "online" {
				checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				runtime, e := nodes.Runtime(checkCtx, view.ID)
				if e == nil {
					current, e := runtime.NotificationStates(checkCtx)
					if e == nil {
						previous := containerStates[node.RuntimeKey]
						next := map[string]docker.NotificationState{}
						stableSince[node.RuntimeKey] = observeContainerRecovery(view.ID, view.Name, current, previous, stableSince[node.RuntimeKey], time.Now(), notify.Emit)
						for _, state := range current {
							next[state.ID] = state
							old, known := previous[state.ID]
							if !known || old.Fingerprint == state.Fingerprint {
								continue
							}
							for _, event := range state.Events {
								event.NodeID, event.NodeName = view.ID, view.Name
								if event.Type != "container.oom" && (notify.IsExpected(view.ID, state.ID) || expectedTaskEvent(db, event)) {
									continue
								}
								notify.Emit(event)
							}
							if state.RestartCount-old.RestartCount >= 5 && !notify.IsExpected(view.ID, state.ID) {
								notify.Emit(event.Event{Type: "container.restart_loop", Severity: "warning", NodeID: view.ID, NodeName: view.Name, ResourceType: "container", ResourceID: state.ID, Project: state.Project, Title: state.Name, Message: "State reconciliation: at least five automatic restarts since the previous observation"})
							}
						}
						containerStates[node.RuntimeKey] = next
					} else {
						delete(stableSince, node.RuntimeKey)
					}
				} else {
					delete(stableSince, node.RuntimeKey)
				}
				cancel()
			} else {
				delete(stableSince, node.RuntimeKey)
			}
			existing, ok := watchers[view.ID]
			if ok && existing.key == node.RuntimeKey && view.Enabled {
				continue
			}
			if ok {
				delete(containerStates, existing.key)
				delete(stableSince, existing.key)
				existing.cancel()
				delete(watchers, view.ID)
			}
			if !view.Enabled {
				continue
			}
			runCtx, cancel := context.WithCancel(ctx)
			watchers[view.ID] = watcher{key: node.RuntimeKey, cancel: cancel}
			id, name := view.ID, view.Name
			wg.Add(1)
			go func() {
				defer wg.Done()
				for runCtx.Err() == nil {
					runtime, err := nodes.Runtime(runCtx, id)
					if err == nil {
						err = runtime.WatchEvents(runCtx, func(e event.Event) {
							e.NodeID = id
							e.NodeName = name
							e.Scope = "node"
							if e.Type == "container.restart_loop" {
								mu.Lock()
								now := time.Now()
								key := id + e.ResourceID
								recent := []time.Time{}
								for _, t := range restartTimes[key] {
									if now.Sub(t) < 5*time.Minute {
										recent = append(recent, t)
									}
								}
								recent = append(recent, now)
								restartTimes[key] = recent
								mu.Unlock()
								if len(recent) < 5 {
									return
								}
							}
							if e.Type != "container.oom" {
								if notify.IsExpected(id, e.ResourceID) {
									return
								}
								if expectedTaskEvent(db, e) {
									return
								}
							}
							notify.Emit(e)
						})
					}
					select {
					case <-runCtx.Done():
						return
					case <-time.After(5 * time.Second):
					}
				}
			}()
		}
		for id, w := range watchers {
			if !seen[id] {
				w.cancel()
				delete(containerStates, w.key)
				delete(stableSince, w.key)
				delete(watchers, id)
			}
		}
	}
	scan()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			scan()
		}
	}
}

type containerStability struct {
	Since        time.Time
	LastObserved time.Time
}

// Require two minutes of observed, uninterrupted running with no new restarts.
// A start event alone is not recovery from a crash loop. Missing observations,
// unhealthy/starting checks, and restart counter changes reset this interval.
func observeContainerRecovery(nodeID, nodeName string, current []docker.NotificationState, previous map[string]docker.NotificationState, stable map[string]containerStability, now time.Time, emit event.Sink) map[string]containerStability {
	next := map[string]containerStability{}
	for _, state := range current {
		if !state.Running || state.Health != "" && state.Health != "healthy" {
			continue
		}
		checkpoint, exists := stable[state.ID]
		if old, known := previous[state.ID]; !exists || !known || old.RestartCount != state.RestartCount || old.Fingerprint != state.Fingerprint || now.Sub(checkpoint.LastObserved) > 75*time.Second {
			checkpoint.Since = now
		}
		checkpoint.LastObserved = now
		next[state.ID] = checkpoint
		if now.Sub(checkpoint.Since) >= 2*time.Minute {
			emit(event.Event{Type: "container.running", Severity: "info", NodeID: nodeID, NodeName: nodeName, ResourceType: "container", ResourceID: state.ID, Project: state.Project, Title: state.Name, Message: "Container has been running stably for at least two minutes", Time: now})
		}
	}
	return next
}

// Correlate a rebuild with its resource or project. An unrelated running Task
// on the same node must not hide a different container's failure.
func expectedTaskEvent(db *gorm.DB, e event.Event) bool {
	var rows []database.Task
	if db.Where("node_id = ? AND (status IN ? OR finished_at > ?)", e.NodeID, []string{"pending", "running"}, time.Now().Add(-2*time.Minute)).Limit(100).Find(&rows).Error != nil {
		return false
	}
	for _, row := range rows {
		if e.Project != "" && strings.HasPrefix(row.Type, "compose.") && strings.HasSuffix(row.Name, " "+e.Project) {
			return true
		}
		if e.Project != "" && strings.HasPrefix(row.Type, "cd.") {
			var projectID uint
			var deployment database.DeliveryReleaseDeployment
			if db.First(&deployment, "task_id = ? AND node_id = ?", row.ID, e.NodeID).Error == nil {
				var release database.DeliveryRelease
				if db.First(&release, deployment.ReleaseID).Error == nil {
					projectID = release.ProjectID
				}
			}
			var project database.DeliveryProject
			if projectID != 0 && db.First(&project, projectID).Error == nil && (project.DeploymentName == e.Project || fmt.Sprintf("suma-cd-%d", projectID) == e.Project) {
				return true
			}
		}
	}
	return false
}
