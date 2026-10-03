package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/suma/suma/server/internal/event"
)

func (a *Adapter) WatchEvents(ctx context.Context, consume func(event.Event)) error {
	stream, errors := a.client.Events(ctx, events.ListOptions{Filters: filters.NewArgs(filters.Arg("type", "container"))})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errors:
			if err != nil {
				return err
			}
			return fmt.Errorf("Docker event stream closed")
		case message, ok := <-stream:
			if !ok {
				return fmt.Errorf("Docker event stream closed")
			}
			kind, severity := "", "warning"
			switch string(message.Action) {
			case "oom":
				kind = "container.oom"
				severity = "error"
			case "die":
				if message.Actor.Attributes["exitCode"] != "0" {
					kind = "container.exited"
				}
			case "health_status: unhealthy":
				kind = "container.unhealthy"
			case "health_status: healthy":
				kind = "container.recovered"
				severity = "info"
			case "restart":
				kind = "container.restart_loop"
			case "start":
				// Automatic restarts emit start/die, without a restart action.
				row, err := a.client.ContainerInspect(ctx, message.Actor.ID)
				if err == nil && row.RestartCount > 0 {
					kind = "container.restart_loop"
				}
			}
			if kind != "" {
				consume(event.Event{Type: kind, Severity: severity, ResourceType: "container", ResourceID: message.Actor.ID, Project: message.Actor.Attributes["com.docker.compose.project"], Title: message.Actor.Attributes["name"], Message: string(message.Action) + " exit=" + message.Actor.Attributes["exitCode"]})
			}
		}
	}
}

type NotificationState struct {
	ID           string
	Name         string
	Project      string
	RestartCount int
	Fingerprint  string
	Events       []event.Event
}

// Reconcile state changes that occurred while an event stream was reconnecting.
// This returns current Engine state; only emitted historical events are stored.
func (a *Adapter) NotificationStates(ctx context.Context) ([]NotificationState, error) {
	rows, err := a.client.ContainerList(ctx, container.ListOptions{All: true, Limit: 5000})
	if err != nil {
		return nil, err
	}
	out := make([]NotificationState, 0, len(rows))
	for _, brief := range rows {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		row, err := a.client.ContainerInspect(ctx, brief.ID)
		if err != nil || row.State == nil {
			continue
		}
		s := NotificationState{ID: row.ID, Name: row.Name, RestartCount: row.RestartCount}
		if row.Config != nil {
			s.Project = row.Config.Labels["com.docker.compose.project"]
		}
		health := ""
		if row.State.Health != nil {
			health = row.State.Health.Status
		}
		s.Fingerprint = fmt.Sprintf("%s|%s|%s|%d|%d|%t", row.State.Status, health, row.State.FinishedAt, row.State.ExitCode, row.RestartCount, row.State.OOMKilled)
		add := func(kind, severity, message string) {
			s.Events = append(s.Events, event.Event{Type: kind, Severity: severity, ResourceType: "container", ResourceID: row.ID, Project: s.Project, Title: s.Name, Message: message, Time: time.Now().UTC()})
		}
		if row.State.OOMKilled {
			add("container.oom", "error", "State reconciliation: container was OOM killed")
		}
		if !row.State.Running && row.State.ExitCode != 0 {
			add("container.exited", "warning", fmt.Sprintf("State reconciliation: exited with code %d", row.State.ExitCode))
		}
		if health == "unhealthy" {
			add("container.unhealthy", "warning", "State reconciliation: health check failed")
		}
		if health == "healthy" {
			add("container.recovered", "info", "State reconciliation: health check recovered")
		}
		out = append(out, s)
	}
	return out, nil
}
