package docker

import (
	"context"
	"sort"
	"strconv"

	dockertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	domain "github.com/suma/suma/server/internal/container"
)

func overviewPorts(row dockertypes.InspectResponse) []domain.OverviewPort {
	ports := map[nat.Port][]nat.PortBinding{}
	if row.Config != nil {
		for port := range row.Config.ExposedPorts {
			ports[port] = nil
		}
	}
	if row.HostConfig != nil {
		for port, bindings := range row.HostConfig.PortBindings {
			ports[port] = bindings
		}
	}
	// Prefer actual allocated bindings; stopped containers keep configured ports.
	if row.NetworkSettings != nil {
		for port, bindings := range row.NetworkSettings.Ports {
			if len(bindings) > 0 {
				ports[port] = bindings
			} else if _, exists := ports[port]; !exists {
				ports[port] = nil
			}
		}
	}
	out := []domain.OverviewPort{}
	for port, bindings := range ports {
		if len(bindings) == 0 {
			out = append(out, domain.OverviewPort{Port: domain.Port{PrivatePort: uint16(port.Int()), Type: port.Proto()}})
		}
		for _, binding := range bindings {
			public, _ := strconv.ParseUint(binding.HostPort, 10, 16)
			out = append(out, domain.OverviewPort{Port: domain.Port{PrivatePort: uint16(port.Int()), PublicPort: uint16(public), Type: port.Proto(), IP: binding.HostIP}, HostBinding: true})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.PrivatePort != b.PrivatePort {
			return a.PrivatePort < b.PrivatePort
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.IP != b.IP {
			return a.IP < b.IP
		}
		return a.PublicPort < b.PublicPort
	})
	return out
}

func (a *Adapter) ContainerOverview(ctx context.Context, id string) (domain.Overview, error) {
	row, err := a.client.ContainerInspect(ctx, id)
	if err != nil {
		return domain.Overview{}, err
	}
	if row.ContainerJSONBase == nil {
		row.ContainerJSONBase = &dockertypes.ContainerJSONBase{}
	}
	out := domain.Overview{ID: row.ID, Ports: overviewPorts(row), Networks: []domain.OverviewNetwork{}, Mounts: []domain.OverviewMount{}}
	if out.ID == "" {
		out.ID = id
	}
	if row.HostConfig != nil {
		mode := string(row.HostConfig.NetworkMode)
		out.NetworkMode = &mode
	}
	if row.NetworkSettings != nil {
		names := []string{}
		for name, endpoint := range row.NetworkSettings.Networks {
			if endpoint != nil {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		out.Counts.Networks = len(names)
		for _, name := range names[:min(3, len(names))] {
			endpoint := row.NetworkSettings.Networks[name]
			out.Networks = append(out.Networks, domain.OverviewNetwork{Name: name, IPAddress: endpoint.IPAddress, IPv6Address: endpoint.GlobalIPv6Address})
		}
	}
	// Resolve ownership only for visible mounts. Daemon-internal volume paths
	// are omitted; bind sources remain informational and are never opened.
	sort.Slice(row.Mounts, func(i, j int) bool { return row.Mounts[i].Destination < row.Mounts[j].Destination })
	out.Counts.Mounts = len(row.Mounts)
	for _, mount := range row.Mounts[:min(3, len(row.Mounts))] {
		item := domain.OverviewMount{Type: string(mount.Type), Name: mount.Name, Destination: mount.Destination, ReadWrite: mount.RW, Anonymous: anonymousVolume(row, string(mount.Type), mount.Destination)}
		if item.Type != "volume" {
			item.Source = mount.Source
		}
		out.Mounts = append(out.Mounts, item)
	}
	out.Counts.Ports = len(out.Ports)
	out.Ports = out.Ports[:min(4, len(out.Ports))]
	return out, nil
}
