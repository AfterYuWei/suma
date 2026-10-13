package docker

import (
	"context"
	"strconv"

	"github.com/containerd/errdefs"
	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	domain "github.com/suma/suma/server/internal/container"
)

func (a *Adapter) ContainerImageExists(ctx context.Context, reference string) (bool, error) {
	_, err := a.client.ImageInspect(ctx, reference)
	if errdefs.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (a *Adapter) CreateContainer(ctx context.Context, input domain.CreateRequest) (domain.CreateResult, error) {
	config := &dockercontainer.Config{Image: input.Image, Cmd: input.Command, Entrypoint: input.Entrypoint, WorkingDir: input.WorkingDirectory, User: input.User, Tty: input.TTY, OpenStdin: input.OpenStdin, ExposedPorts: nat.PortSet{}}
	for _, variable := range input.Environment {
		config.Env = append(config.Env, variable.Key+"="+variable.Value)
	}
	host := &dockercontainer.HostConfig{NetworkMode: dockercontainer.NetworkMode(input.Network), RestartPolicy: dockercontainer.RestartPolicy{Name: dockercontainer.RestartPolicyMode(input.RestartPolicy)}, ReadonlyRootfs: input.ReadOnlyRootFS, PortBindings: nat.PortMap{}, Resources: dockercontainer.Resources{Memory: input.MemoryMB * 1024 * 1024, NanoCPUs: int64(input.CPUs * 1e9)}}
	for _, port := range input.Ports {
		key := nat.Port(strconv.Itoa(int(port.ContainerPort)) + "/" + port.Protocol)
		config.ExposedPorts[key] = struct{}{}
		hostPort := ""
		if port.HostPort != 0 {
			hostPort = strconv.Itoa(int(port.HostPort))
		}
		host.PortBindings[key] = append(host.PortBindings[key], nat.PortBinding{HostIP: port.HostIP, HostPort: hostPort})
	}
	for _, volume := range input.Mounts {
		host.Mounts = append(host.Mounts, mount.Mount{Type: mount.Type(volume.Type), Source: volume.Source, Target: volume.Target, ReadOnly: volume.ReadOnly})
	}
	networking := &dockernetwork.NetworkingConfig{}
	if input.Network != "bridge" && input.Network != "host" && input.Network != "none" {
		networking.EndpointsConfig = map[string]*dockernetwork.EndpointSettings{input.Network: {}}
	}
	created, err := a.client.ContainerCreate(ctx, config, host, networking, nil, input.Name)
	return domain.CreateResult{ID: created.ID, Warnings: created.Warnings}, err
}
