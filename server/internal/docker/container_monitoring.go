package docker

import (
	"context"
	types "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	domain "github.com/suma/suma/server/internal/container"
	monitor "github.com/suma/suma/server/internal/containermonitor"
	"io"
	"time"
)

func (a *Adapter) MonitoringInventory(ctx context.Context) ([]monitor.Inventory, error) {
	rows, err := a.client.ContainerList(ctx, types.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := make([]monitor.Inventory, 0, len(rows))
	for _, r := range rows {
		v := monitor.Inventory{ID: r.ID, Image: r.Image, State: string(r.State), Project: r.Labels["com.docker.compose.project"], Service: r.Labels["com.docker.compose.service"]}
		if len(r.Names) > 0 {
			v.Name = r.Names[0]
			if len(v.Name) > 0 && v.Name[0] == '/' {
				v.Name = v.Name[1:]
			}
		}
		// Only configuration needed for historical limits. No secret or host material.
		out = append(out, v)
	}
	return out, nil
}
func (a *Adapter) MonitoringEvents(ctx context.Context, since time.Time, consume func(domain.ContainerEvent, monitor.Inventory) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	opts := events.ListOptions{Filters: filters.NewArgs(filters.Arg("type", "container"))}
	if !since.IsZero() {
		opts.Since = since.Format(time.RFC3339Nano)
	}
	messages, failures := a.client.Events(ctx, opts)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-failures:
			if !ok || err == nil {
				return io.EOF
			}
			return err
		case message, ok := <-messages:
			if !ok {
				return io.EOF
			}
			e, valid := normalizeContainerEvent(message)
			if !valid {
				continue
			}
			meta := monitor.Inventory{ID: e.ContainerID, Name: message.Actor.Attributes["name"], Image: message.Actor.Attributes["image"], Project: message.Actor.Attributes["com.docker.compose.project"], Service: message.Actor.Attributes["com.docker.compose.service"]}
			if err := consume(e, meta); err != nil {
				return err
			}
		}
	}
}
