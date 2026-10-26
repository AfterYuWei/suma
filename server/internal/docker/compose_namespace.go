package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/suma/suma/server/internal/compose"
)

// InspectComposeNamespace includes unused networks and volumes. Container-only
// observation cannot prove a name is safe for a first deployment.
func (a *Adapter) InspectComposeNamespace(ctx context.Context, name string) (compose.RuntimeProjectSnapshot, error) {
	filter := filters.NewArgs(filters.Arg("label", compose.ProjectLabel+"="+name))
	result := compose.RuntimeProjectSnapshot{ProjectName: name}
	containers, err := a.client.ContainerList(ctx, container.ListOptions{All: true, Filters: filter})
	if err != nil {
		return result, fmt.Errorf("list Compose namespace containers: %w", err)
	}
	for _, row := range containers {
		result.Containers = append(result.Containers, compose.RuntimeContainer{ID: row.ID, Labels: cloneStringMap(row.Labels)})
	}
	networks, err := a.client.NetworkList(ctx, network.ListOptions{Filters: filter})
	if err != nil {
		return result, fmt.Errorf("list Compose namespace networks: %w", err)
	}
	for _, row := range networks {
		result.Networks = append(result.Networks, compose.RuntimeNetwork{ID: row.ID, Name: row.Name, Labels: cloneStringMap(row.Labels)})
	}
	volumes, err := a.client.VolumeList(ctx, volume.ListOptions{Filters: filter})
	if err != nil {
		return result, fmt.Errorf("list Compose namespace volumes: %w", err)
	}
	for _, row := range volumes.Volumes {
		if row != nil {
			result.Volumes = append(result.Volumes, compose.RuntimeVolume{Name: row.Name, Labels: cloneStringMap(row.Labels)})
		}
	}
	return result, nil
}
