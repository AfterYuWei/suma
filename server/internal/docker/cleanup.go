package docker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/distribution/reference"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dockerimage "github.com/docker/docker/api/types/image"
	dockernetwork "github.com/docker/docker/api/types/network"
	dockervolume "github.com/docker/docker/api/types/volume"
	"github.com/suma/suma/server/internal/cleanup"
)

func apiAtLeast(version string, major, minor int) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 2 {
		return false
	}
	a, e := strconv.Atoi(parts[0])
	if e != nil {
		return false
	}
	b, e := strconv.Atoi(parts[1])
	return e == nil && (a > major || (a == major && b >= minor))
}
func (a *Adapter) CleanupCapabilities(ctx context.Context) (cleanup.Capabilities, error) {
	ping, err := a.client.Ping(ctx)
	if err != nil {
		return cleanup.Capabilities{}, cleanupError(err)
	}
	a.client.NegotiateAPIVersionPing(ping)
	version := a.client.ClientVersion()
	// Older prune endpoints predate BuildKit and cannot guarantee its retention
	// and storage budget contract. Expose them as unsupported rather than prune
	// with silently ignored limits.
	return cleanup.Capabilities{Available: true, BuildCache: apiAtLeast(version, 1, 39), API: version}, nil
}
func sizePointer(size int64) *int64 {
	if size < 0 {
		return nil
	}
	return &size
}
func containerResource(row dockertypes.ContainerJSON) cleanup.Resource {
	r := cleanup.Resource{Kind: cleanup.Container, ID: row.ID, Name: strings.TrimPrefix(row.Name, "/"), SizeBytes: row.SizeRw}
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, row.Created)
	if row.State != nil {
		r.State = row.State.Status
		r.InUse = row.State.Running || row.State.Paused || row.State.Restarting
		r.FinishedAt, _ = time.Parse(time.RFC3339Nano, row.State.FinishedAt)
	}
	if row.Config != nil {
		r.Labels = row.Config.Labels
		image := strings.ToLower(row.Config.Image)
		imageBase := image[strings.LastIndex(image, "/")+1:]
		imageBase = strings.Split(strings.Split(imageBase, "@")[0], ":")[0]
		r.System = r.Name == "suma" || r.Name == "suma-agent" || strings.HasPrefix(r.Name, "buildx_buildkit_") || imageBase == "suma" || imageBase == "suma-agent" || strings.Contains(image, "moby/buildkit") || r.Labels["com.docker.buildx.builder"] != ""
	}
	return r
}
func imageResource(id string, tags, digests []string, labels map[string]string, size int64, created time.Time, inUse bool) cleanup.Resource {
	refs := []string{id}
	tagged := false
	name := id
	for _, ref := range append(append([]string{}, tags...), digests...) {
		if ref == "" || strings.Contains(ref, "<none>") {
			continue
		}
		tagged = true
		refs = append(refs, ref)
		if name == id {
			name = ref
		}
		refs = append(refs, imageAliases(ref)...)
	}
	return cleanup.Resource{Kind: cleanup.Image, ID: id, Name: name, Aliases: refs, Labels: labels, SizeBytes: sizePointer(size), CreatedAt: created, Tagged: tagged, InUse: inUse}
}
func imageAliases(ref string) []string {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return []string{ref}
	}
	named = reference.TagNameOnly(named)
	familiar := reference.FamiliarString(named)
	result := []string{familiar, named.String()}
	if strings.HasSuffix(familiar, ":latest") {
		result = append(result, strings.TrimSuffix(familiar, ":latest"))
	}
	return result
}

func networkResource(row dockernetwork.Inspect, inUse bool) cleanup.Resource {
	return cleanup.Resource{Kind: cleanup.Network, ID: row.ID, Name: row.Name, CreatedAt: row.Created, Labels: row.Labels, InUse: inUse || len(row.Containers) > 0, System: row.Name == "bridge" || row.Name == "host" || row.Name == "none"}
}
func volumeResource(row dockervolume.Volume, inUse bool) cleanup.Resource {
	r := cleanup.Resource{Kind: cleanup.Volume, ID: row.Name, Name: row.Name, Labels: row.Labels, InUse: inUse}
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, row.CreatedAt)
	if row.UsageData != nil {
		r.SizeBytes = sizePointer(row.UsageData.Size)
	}
	return r
}
func (a *Adapter) CleanupInventory(ctx context.Context) (cleanup.Inventory, error) {
	cap, err := a.CleanupCapabilities(ctx)
	if err != nil {
		return cleanup.Inventory{}, err
	}
	usage, err := a.client.DiskUsage(ctx, dockertypes.DiskUsageOptions{})
	if err != nil {
		return cleanup.Inventory{}, cleanupError(err)
	}
	containers, err := a.client.ContainerList(ctx, dockercontainer.ListOptions{All: true})
	if err != nil {
		return cleanup.Inventory{}, cleanupError(err)
	}
	images, err := a.client.ImageList(ctx, dockerimage.ListOptions{All: true})
	if err != nil {
		return cleanup.Inventory{}, cleanupError(err)
	}
	networks, err := a.client.NetworkList(ctx, dockernetwork.ListOptions{})
	if err != nil {
		return cleanup.Inventory{}, cleanupError(err)
	}
	volumes, err := a.client.VolumeList(ctx, dockervolume.ListOptions{})
	if err != nil {
		return cleanup.Inventory{}, cleanupError(err)
	}
	inv := cleanup.Inventory{Capabilities: cap, Resources: []cleanup.Resource{}, LayersBytes: sizePointer(usage.LayersSize)}
	inv.Usage.ImageLayersBytes = inv.LayersBytes
	var containerBytes, volumeBytes int64
	containersKnown, volumesKnown := true, true
	imageUse := map[string]bool{}
	networkUse := map[string]bool{}
	volumeUse := map[string]bool{}
	containerSizes := map[string]int64{}
	volumeSizes := map[string]int64{}
	for _, row := range usage.Containers {
		if row != nil {
			containerSizes[row.ID] = row.SizeRw
			if row.SizeRw < 0 {
				containersKnown = false
			} else {
				containerBytes += row.SizeRw
			}
		} else {
			containersKnown = false
		}
	}
	for _, row := range usage.Volumes {
		if row != nil && row.UsageData != nil {
			volumeSizes[row.Name] = row.UsageData.Size
			if row.UsageData.Size < 0 {
				volumesKnown = false
			} else {
				volumeBytes += row.UsageData.Size
			}
		} else {
			volumesKnown = false
		}
	}
	if containersKnown {
		inv.Usage.ContainerBytes = &containerBytes
	}
	if volumesKnown {
		inv.Usage.VolumeBytes = &volumeBytes
	}
	for _, row := range containers {
		imageUse[row.ImageID] = true
		if row.NetworkSettings != nil {
			for name, endpoint := range row.NetworkSettings.Networks {
				networkUse[name] = true
				if endpoint != nil {
					networkUse[endpoint.NetworkID] = true
				}
			}
		}
		for _, mount := range row.Mounts {
			if mount.Type == "volume" {
				volumeUse[mount.Name] = true
			}
		}
		inspected, e := a.client.ContainerInspect(ctx, row.ID)
		if errdefs.IsNotFound(e) {
			continue
		}
		if e != nil {
			return cleanup.Inventory{}, cleanupError(e)
		}
		if inspected.Config == nil || inspected.State == nil {
			return cleanup.Inventory{}, fmt.Errorf("%w: incomplete container inspect", cleanup.ErrUnavailable)
		}
		r := containerResource(inspected)
		if size, ok := containerSizes[row.ID]; ok {
			r.SizeBytes = sizePointer(size)
		}
		inv.Resources = append(inv.Resources, r)
	}
	for _, row := range images {
		inv.Resources = append(inv.Resources, imageResource(row.ID, row.RepoTags, row.RepoDigests, row.Labels, row.Size, time.Unix(row.Created, 0), imageUse[row.ID]))
	}
	for _, row := range networks {
		inv.Resources = append(inv.Resources, networkResource(row, networkUse[row.ID] || networkUse[row.Name]))
	}
	for _, row := range volumes.Volumes {
		if row != nil {
			r := volumeResource(*row, volumeUse[row.Name])
			if size, ok := volumeSizes[row.Name]; ok {
				r.SizeBytes = sizePointer(size)
			}
			inv.Resources = append(inv.Resources, r)
		}
	}
	for _, row := range usage.BuildCache {
		if row != nil {
			inv.Resources = append(inv.Resources, cleanup.Resource{Kind: cleanup.Cache, ID: row.ID, Name: row.ID, CreatedAt: row.CreatedAt, LastUsedAt: row.LastUsedAt, SizeBytes: sizePointer(row.Size), InUse: row.InUse, System: row.Shared})
		}
	}
	return inv, nil
}
func (a *Adapter) CleanupResource(ctx context.Context, kind cleanup.Kind, id string) (cleanup.Resource, error) {
	if !cleanup.ValidIdentifier(id) {
		return cleanup.Resource{}, cleanup.ErrInvalid
	}
	switch kind {
	case cleanup.Container:
		row, err := a.client.ContainerInspect(ctx, id)
		if err != nil {
			return cleanup.Resource{}, cleanupError(err)
		}
		if row.Config == nil || row.State == nil {
			return cleanup.Resource{}, cleanup.ErrUnavailable
		}
		return containerResource(row), nil
	case cleanup.Image:
		row, err := a.client.ImageInspect(ctx, id)
		if err != nil {
			return cleanup.Resource{}, cleanupError(err)
		}
		containers, err := a.client.ContainerList(ctx, dockercontainer.ListOptions{All: true})
		if err != nil {
			return cleanup.Resource{}, cleanupError(err)
		}
		inUse := false
		for _, c := range containers {
			inUse = inUse || c.ImageID == row.ID
		}
		created, _ := time.Parse(time.RFC3339Nano, row.Created)
		labels := map[string]string{}
		if row.Config != nil {
			labels = row.Config.Labels
		}
		return imageResource(row.ID, row.RepoTags, row.RepoDigests, labels, row.Size, created, inUse), nil
	case cleanup.Network:
		row, err := a.client.NetworkInspect(ctx, id, dockernetwork.InspectOptions{})
		if err != nil {
			return cleanup.Resource{}, cleanupError(err)
		}
		containers, err := a.client.ContainerList(ctx, dockercontainer.ListOptions{All: true})
		if err != nil {
			return cleanup.Resource{}, cleanupError(err)
		}
		inUse := false
		for _, c := range containers {
			if c.NetworkSettings != nil {
				for name, endpoint := range c.NetworkSettings.Networks {
					inUse = inUse || name == row.Name || (endpoint != nil && endpoint.NetworkID == row.ID)
				}
			}
		}
		return networkResource(row, inUse), nil
	case cleanup.Volume:
		row, err := a.client.VolumeInspect(ctx, id)
		if err != nil {
			return cleanup.Resource{}, cleanupError(err)
		}
		used, err := a.volumeUsage(ctx, id)
		if err != nil {
			return cleanup.Resource{}, cleanupError(err)
		}
		return volumeResource(row, len(used) > 0), nil
	}
	return cleanup.Resource{}, cleanup.ErrInvalid
}
func (a *Adapter) CleanupRemove(ctx context.Context, kind cleanup.Kind, id string) error {
	r, err := a.CleanupResource(ctx, kind, id)
	if err != nil {
		return err
	}
	if r.InUse {
		return cleanup.ErrInUse
	}
	if r.System || r.Labels["suma.cleanup.protect"] == "true" || ((kind == cleanup.Container || kind == cleanup.Network) && r.Labels["com.docker.compose.project"] != "") {
		return cleanup.ErrInUse
	}
	switch kind {
	case cleanup.Container:
		if r.State != "exited" {
			return cleanup.ErrInUse
		}
		err = a.client.ContainerRemove(ctx, id, dockercontainer.RemoveOptions{Force: false, RemoveVolumes: false})
	case cleanup.Image:
		_, err = a.client.ImageRemove(ctx, id, dockerimage.RemoveOptions{Force: false, PruneChildren: false})
	case cleanup.Network:
		err = a.client.NetworkRemove(ctx, id)
	default:
		return cleanup.ErrInvalid // No volume deletion in this executor.
	}
	return cleanupError(err)
}
func (a *Adapter) CleanupPruneCache(ctx context.Context, opts cleanup.CacheOptions) (cleanup.CacheReport, error) {
	cap, err := a.CleanupCapabilities(ctx)
	if err != nil {
		return cleanup.CacheReport{}, err
	}
	if !cap.BuildCache {
		return cleanup.CacheReport{}, cleanup.ErrUnavailable
	}
	options := build.CachePruneOptions{All: false, Filters: filters.NewArgs(filters.Arg("until", fmt.Sprintf("%dh", opts.RetentionDays*24)))}
	if apiAtLeast(cap.API, 1, 48) {
		options.ReservedSpace = opts.ReservedBytes
	} else {
		options.KeepStorage = opts.ReservedBytes
	}
	report, err := a.client.BuildCachePrune(ctx, options)
	if err != nil {
		return cleanup.CacheReport{}, cleanupError(err)
	}
	return cleanup.CacheReport{Deleted: report.CachesDeleted, ReclaimedBytes: report.SpaceReclaimed}, nil
}
func cleanupError(err error) error {
	if err == nil {
		return nil
	}
	if errdefs.IsNotFound(err) {
		return cleanup.ErrGone
	}
	if errdefs.IsConflict(err) {
		return cleanup.ErrInUse
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return cleanup.ErrUnavailable
	}
	return err
}
