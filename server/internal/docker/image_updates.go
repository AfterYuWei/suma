package docker

import (
	"context"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockerimage "github.com/docker/docker/api/types/image"
	"github.com/suma/suma/server/internal/imageupdate"
	"strings"
)

func (a *Adapter) UpdateInventory(ctx context.Context) (imageupdate.Inventory, error) {
	result := imageupdate.Inventory{Images: []imageupdate.LocalImage{}, Containers: []imageupdate.Usage{}}
	images, err := a.client.ImageList(ctx, dockerimage.ListOptions{All: true})
	if err != nil {
		return result, err
	}
	for _, img := range images {
		inspect, err := a.client.ImageInspect(ctx, img.ID)
		if err != nil {
			return result, err
		}
		result.Images = append(result.Images, imageupdate.LocalImage{ID: img.ID, Tags: img.RepoTags, Platform: imageupdate.Platform{OS: inspect.Os, Architecture: inspect.Architecture, Variant: inspect.Variant}})
	}
	containers, err := a.client.ContainerList(ctx, dockercontainer.ListOptions{All: true})
	if err != nil {
		return result, err
	}
	for _, row := range containers {
		inspect, err := a.client.ContainerInspect(ctx, row.ID)
		if err != nil {
			continue
		}
		ref := row.Image
		if inspect.Config != nil {
			ref = inspect.Config.Image
		}
		result.Containers = append(result.Containers, imageupdate.Usage{ContainerID: row.ID, ContainerName: shortContainerName(row.ID, row.Names), Project: row.Labels["com.docker.compose.project"], Service: row.Labels["com.docker.compose.service"], State: string(row.State), ImageID: row.ImageID, Reference: ref, DeliveryProject: strings.TrimSpace(row.Labels["io.suma.delivery.project"])})
	}
	return result, nil
}
