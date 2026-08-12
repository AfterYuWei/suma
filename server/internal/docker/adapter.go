package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dockerimage "github.com/docker/docker/api/types/image"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/system"
	dockervolume "github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	domain "github.com/dockport/dockport/server/internal/container"
	imagedomain "github.com/dockport/dockport/server/internal/image"
	networkdomain "github.com/dockport/dockport/server/internal/network"
	"github.com/dockport/dockport/server/internal/task"
	volumedomain "github.com/dockport/dockport/server/internal/volume"
)

type Info struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ServerVersion   string `json:"server_version"`
	OperatingSystem string `json:"operating_system"`
	OSType          string `json:"os_type"`
	Architecture    string `json:"architecture"`
	KernelVersion   string `json:"kernel_version"`
	Containers      int    `json:"containers"`
	Running         int    `json:"containers_running"`
	Stopped         int    `json:"containers_stopped"`
	Images          int    `json:"images"`
	CPUs            int    `json:"cpus"`
	MemoryBytes     int64  `json:"memory_bytes"`
}

type Engine interface {
	Ping(context.Context) error
	Info(context.Context) (Info, error)
	Close() error
}

type Adapter struct{ client *client.Client }

func New(host string) (*Adapter, error) {
	cli, err := client.NewClientWithOpts(client.WithHost(host), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create Docker client: %w", err)
	}
	return &Adapter{client: cli}, nil
}

func (a *Adapter) Ping(ctx context.Context) error {
	if _, err := a.client.Ping(ctx); err != nil {
		return fmt.Errorf("ping Docker: %w", err)
	}
	return nil
}

func (a *Adapter) Info(ctx context.Context) (Info, error) {
	value, err := a.client.Info(ctx)
	if err != nil {
		return Info{}, fmt.Errorf("read Docker info: %w", err)
	}
	return mapInfo(value), nil
}

func mapInfo(value system.Info) Info {
	return Info{ID: value.ID, Name: value.Name, ServerVersion: value.ServerVersion, OperatingSystem: value.OperatingSystem, OSType: value.OSType, Architecture: value.Architecture, KernelVersion: value.KernelVersion, Containers: value.Containers, Running: value.ContainersRunning, Stopped: value.ContainersStopped, Images: value.Images, CPUs: value.NCPU, MemoryBytes: value.MemTotal}
}

func (a *Adapter) Close() error { return a.client.Close() }

func (a *Adapter) List(ctx context.Context) ([]domain.Summary, error) {
	rows, err := a.client.ContainerList(ctx, dockercontainer.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	result := make([]domain.Summary, 0, len(rows))
	for _, row := range rows {
		name := row.ID[:min(12, len(row.ID))]
		if len(row.Names) > 0 {
			name = strings.TrimPrefix(row.Names[0], "/")
		}
		ports := make([]domain.Port, 0, len(row.Ports))
		for _, port := range row.Ports {
			ports = append(ports, domain.Port{PrivatePort: port.PrivatePort, PublicPort: port.PublicPort, Type: port.Type, IP: port.IP})
		}
		result = append(result, domain.Summary{ID: row.ID, Name: name, Image: row.Image, Command: row.Command, Created: time.Unix(row.Created, 0), State: string(row.State), Status: row.Status, Ports: ports, Labels: row.Labels})
	}
	return result, nil
}

func (a *Adapter) Metrics(ctx context.Context) ([]domain.Metrics, error) {
	rows, err := a.client.ContainerList(ctx, dockercontainer.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list running containers for metrics: %w", err)
	}
	summaries := make([]domain.Summary, len(rows))
	var wait sync.WaitGroup
	limit := make(chan struct{}, 6)
	for index, row := range rows {
		summaries[index] = domain.Summary{ID: row.ID, State: string(row.State)}
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			a.loadSummaryStats(ctx, &summaries[index])
		}(index)
	}
	wait.Wait()
	result := make([]domain.Metrics, len(summaries))
	for index, summary := range summaries {
		result[index] = domain.Metrics{ID: summary.ID, CPUPercent: summary.CPUPercent, MemoryBytes: summary.MemoryBytes, UptimeSeconds: summary.UptimeSeconds}
	}
	return result, nil
}

func (a *Adapter) loadSummaryStats(ctx context.Context, summary *domain.Summary) {
	response, err := a.client.ContainerStats(ctx, summary.ID, false)
	if err == nil {
		defer response.Body.Close()
		var stats dockercontainer.StatsResponse
		if json.NewDecoder(response.Body).Decode(&stats) == nil {
			cpuDelta, systemDelta := uint64(0), uint64(0)
			if stats.CPUStats.CPUUsage.TotalUsage >= stats.PreCPUStats.CPUUsage.TotalUsage {
				cpuDelta = stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage
			}
			if stats.CPUStats.SystemUsage >= stats.PreCPUStats.SystemUsage {
				systemDelta = stats.CPUStats.SystemUsage - stats.PreCPUStats.SystemUsage
			}
			cpus := stats.CPUStats.OnlineCPUs
			if cpus == 0 {
				cpus = uint32(len(stats.CPUStats.CPUUsage.PercpuUsage))
			}
			if systemDelta > 0 {
				summary.CPUPercent = float64(cpuDelta) / float64(systemDelta) * float64(cpus) * 100
			}
			summary.MemoryBytes = stats.MemoryStats.Usage
			if cache := stats.MemoryStats.Stats["inactive_file"]; cache < summary.MemoryBytes {
				summary.MemoryBytes -= cache
			}
		}
	}
	if inspect, err := a.client.ContainerInspect(ctx, summary.ID); err == nil && inspect.State != nil {
		if started, err := time.Parse(time.RFC3339Nano, inspect.State.StartedAt); err == nil {
			summary.UptimeSeconds = int64(time.Since(started).Seconds())
		}
	}
}

func (a *Adapter) Get(ctx context.Context, id string) (domain.Detail, error) {
	row, err := a.client.ContainerInspect(ctx, id)
	if err != nil {
		return domain.Detail{}, fmt.Errorf("inspect container: %w", err)
	}
	created, _ := time.Parse(time.RFC3339Nano, row.Created)
	detail := domain.Detail{ID: row.ID, Name: strings.TrimPrefix(row.Name, "/"), Created: created}
	if row.Config != nil {
		detail.Image = row.Config.Image
		detail.Command = []string(row.Config.Cmd)
		detail.Entrypoint = []string(row.Config.Entrypoint)
		detail.WorkingDirectory = row.Config.WorkingDir
		detail.Environment = domain.MaskEnvironment(row.Config.Env)
		detail.Labels = row.Config.Labels
	}
	if row.HostConfig != nil {
		detail.RestartPolicy = string(row.HostConfig.RestartPolicy.Name)
	}
	if row.State != nil {
		detail.PID = row.State.Pid
		detail.State = string(row.State.Status)
		if row.State.Running {
			detail.Status = "Running"
		} else {
			detail.Status = string(row.State.Status)
		}
	}
	if row.NetworkSettings != nil {
		for port, bindings := range row.NetworkSettings.Ports {
			for _, host := range bindings {
				private, proto := uint16(0), "tcp"
				fmt.Sscanf(string(port), "%d/%s", &private, &proto)
				var public uint16
				fmt.Sscanf(host.HostPort, "%d", &public)
				detail.Ports = append(detail.Ports, domain.Port{PrivatePort: private, PublicPort: public, Type: proto, IP: host.HostIP})
			}
		}
	}
	for _, mount := range row.Mounts {
		detail.Mounts = append(detail.Mounts, domain.Mount{Type: string(mount.Type), Name: mount.Name, Source: mount.Source, Destination: mount.Destination, ReadWrite: mount.RW})
	}
	if row.NetworkSettings != nil {
		for name, network := range row.NetworkSettings.Networks {
			detail.Networks = append(detail.Networks, domain.Network{Name: name, IPAddress: network.IPAddress, Gateway: network.Gateway, MacAddress: network.MacAddress})
		}
	}
	return detail, nil
}

func (a *Adapter) Start(ctx context.Context, id string) error {
	return a.client.ContainerStart(ctx, id, dockercontainer.StartOptions{})
}
func (a *Adapter) Stop(ctx context.Context, id string) error {
	return a.client.ContainerStop(ctx, id, dockercontainer.StopOptions{})
}
func (a *Adapter) Restart(ctx context.Context, id string) error {
	return a.client.ContainerRestart(ctx, id, dockercontainer.StopOptions{})
}
func (a *Adapter) Pause(ctx context.Context, id string) error {
	return a.client.ContainerPause(ctx, id)
}
func (a *Adapter) Unpause(ctx context.Context, id string) error {
	return a.client.ContainerUnpause(ctx, id)
}
func (a *Adapter) Kill(ctx context.Context, id string) error {
	return a.client.ContainerKill(ctx, id, "SIGKILL")
}
func (a *Adapter) Rename(ctx context.Context, id, name string) error {
	return a.client.ContainerRename(ctx, id, name)
}
func (a *Adapter) Remove(ctx context.Context, id string, volumes bool) error {
	return a.client.ContainerRemove(ctx, id, dockercontainer.RemoveOptions{RemoveVolumes: volumes})
}

func (a *Adapter) Logs(ctx context.Context, id, since, tail string) (io.ReadCloser, error) {
	if tail == "" {
		tail = "200"
	}
	stream, err := a.client.ContainerLogs(ctx, id, dockercontainer.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: true, Since: since, Tail: tail})
	if err != nil {
		return nil, fmt.Errorf("open container logs: %w", err)
	}
	inspect, err := a.client.ContainerInspect(ctx, id)
	if err != nil {
		stream.Close()
		return nil, fmt.Errorf("inspect container logging mode: %w", err)
	}
	if inspect.Config != nil && inspect.Config.Tty {
		return stream, nil
	}
	reader, writer := io.Pipe()
	go func() { _, err := stdcopy.StdCopy(writer, writer, stream); stream.Close(); writer.CloseWithError(err) }()
	return reader, nil
}

func (a *Adapter) Stats(ctx context.Context, id string) (io.ReadCloser, error) {
	response, err := a.client.ContainerStats(ctx, id, true)
	if err != nil {
		return nil, fmt.Errorf("open container stats: %w", err)
	}
	return response.Body, nil
}

type terminalSession struct {
	client   *client.Client
	id       string
	response dockertypes.HijackedResponse
}

func (s *terminalSession) Read(value []byte) (int, error)  { return s.response.Reader.Read(value) }
func (s *terminalSession) Write(value []byte) (int, error) { return s.response.Conn.Write(value) }
func (s *terminalSession) Close() error                    { s.response.Close(); return nil }
func (s *terminalSession) Resize(ctx context.Context, columns, rows uint) error {
	return s.client.ContainerExecResize(ctx, s.id, dockercontainer.ResizeOptions{Width: columns, Height: rows})
}

func (a *Adapter) Terminal(ctx context.Context, id string, columns, rows uint) (domain.Terminal, error) {
	size := [2]uint{rows, columns}
	created, err := a.client.ContainerExecCreate(ctx, id, dockercontainer.ExecOptions{AttachStdin: true, AttachStdout: true, AttachStderr: true, Tty: true, ConsoleSize: &size, Cmd: []string{"/bin/sh", "-c", "if [ -x /bin/bash ]; then exec /bin/bash; else exec /bin/sh; fi"}})
	if err != nil {
		return nil, fmt.Errorf("create container terminal: %w", err)
	}
	response, err := a.client.ContainerExecAttach(ctx, created.ID, dockercontainer.ExecAttachOptions{Tty: true, ConsoleSize: &size})
	if err != nil {
		return nil, fmt.Errorf("attach container terminal: %w", err)
	}
	return &terminalSession{client: a.client, id: created.ID, response: response}, nil
}

func (a *Adapter) ListImages(ctx context.Context) ([]imagedomain.Summary, error) {
	rows, err := a.client.ImageList(ctx, dockerimage.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	result := make([]imagedomain.Summary, 0, len(rows))
	for _, row := range rows {
		result = append(result, imagedomain.Summary{ID: row.ID, Tags: row.RepoTags, Digests: row.RepoDigests, Size: row.Size, Created: time.Unix(row.Created, 0), Containers: row.Containers, Labels: row.Labels})
	}
	return result, nil
}

func (a *Adapter) InspectImage(ctx context.Context, id string) (imagedomain.Detail, error) {
	row, err := a.client.ImageInspect(ctx, id)
	if err != nil {
		return imagedomain.Detail{}, fmt.Errorf("inspect image: %w", err)
	}
	created, _ := time.Parse(time.RFC3339Nano, row.Created)
	return imagedomain.Detail{Summary: imagedomain.Summary{ID: row.ID, Tags: row.RepoTags, Digests: row.RepoDigests, Size: row.Size, Created: created}, Architecture: row.Architecture, OS: row.Os, Author: row.Author, DockerVersion: row.DockerVersion, Layers: row.RootFS.Layers}, nil
}

func (a *Adapter) PullImage(ctx context.Context, reference string) (io.ReadCloser, error) {
	stream, err := a.client.ImagePull(ctx, reference, dockerimage.PullOptions{})
	if err != nil {
		return nil, fmt.Errorf("pull image: %w", err)
	}
	return stream, nil
}
func (a *Adapter) RemoveImage(ctx context.Context, id string, force bool) error {
	_, err := a.client.ImageRemove(ctx, id, dockerimage.RemoveOptions{Force: force, PruneChildren: true})
	return err
}
func (a *Adapter) TagImage(ctx context.Context, id, reference string) error {
	return a.client.ImageTag(ctx, id, reference)
}

func mapNetwork(row dockernetwork.Inspect) networkdomain.Resource {
	value := networkdomain.Resource{ID: row.ID, Name: row.Name, Driver: row.Driver, Scope: row.Scope, IPv6: row.EnableIPv6, Internal: row.Internal, Containers: len(row.Containers), Labels: row.Labels}
	for _, config := range row.IPAM.Config {
		value.IPAM = append(value.IPAM, networkdomain.IPAM{Subnet: config.Subnet, Gateway: config.Gateway})
	}
	return value
}
func (a *Adapter) ListNetworks(ctx context.Context) ([]networkdomain.Resource, error) {
	rows, err := a.client.NetworkList(ctx, dockernetwork.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	result := make([]networkdomain.Resource, 0, len(rows))
	for _, row := range rows {
		result = append(result, mapNetwork(row))
	}
	return result, nil
}
func (a *Adapter) InspectNetwork(ctx context.Context, id string) (networkdomain.Resource, error) {
	row, err := a.client.NetworkInspect(ctx, id, dockernetwork.InspectOptions{Verbose: true})
	if err != nil {
		return networkdomain.Resource{}, fmt.Errorf("inspect network: %w", err)
	}
	return mapNetwork(row), nil
}
func (a *Adapter) CreateNetwork(ctx context.Context, input networkdomain.CreateRequest) (networkdomain.Resource, error) {
	driver := input.Driver
	if driver == "" {
		driver = "bridge"
	}
	ipv6 := input.IPv6
	options := dockernetwork.CreateOptions{Driver: driver, EnableIPv6: &ipv6}
	if input.Subnet != "" || input.Gateway != "" {
		options.IPAM = &dockernetwork.IPAM{Config: []dockernetwork.IPAMConfig{{Subnet: input.Subnet, Gateway: input.Gateway}}}
	}
	created, err := a.client.NetworkCreate(ctx, input.Name, options)
	if err != nil {
		return networkdomain.Resource{}, fmt.Errorf("create network: %w", err)
	}
	return a.InspectNetwork(ctx, created.ID)
}
func (a *Adapter) RemoveNetwork(ctx context.Context, id string) error {
	return a.client.NetworkRemove(ctx, id)
}

func (a *Adapter) volumeUsage(ctx context.Context, name string) ([]string, error) {
	rows, err := a.client.ContainerList(ctx, dockercontainer.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("volume", name))})
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		value := row.ID[:min(12, len(row.ID))]
		if len(row.Names) > 0 {
			value = strings.TrimPrefix(row.Names[0], "/")
		}
		result = append(result, value)
	}
	return result, nil
}
func (a *Adapter) mapVolume(ctx context.Context, row dockervolume.Volume) (volumedomain.Resource, error) {
	usedBy, err := a.volumeUsage(ctx, row.Name)
	if err != nil {
		return volumedomain.Resource{}, err
	}
	size := int64(-1)
	if row.UsageData != nil {
		size = row.UsageData.Size
	}
	return volumedomain.Resource{Name: row.Name, Driver: row.Driver, Mountpoint: row.Mountpoint, CreatedAt: row.CreatedAt, Scope: row.Scope, Labels: row.Labels, UsedBy: usedBy, Size: size}, nil
}
func (a *Adapter) ListVolumes(ctx context.Context) ([]volumedomain.Resource, error) {
	rows, err := a.client.VolumeList(ctx, dockervolume.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	result := make([]volumedomain.Resource, 0, len(rows.Volumes))
	for _, row := range rows.Volumes {
		value, err := a.mapVolume(ctx, *row)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}
func (a *Adapter) InspectVolume(ctx context.Context, id string) (volumedomain.Resource, error) {
	row, err := a.client.VolumeInspect(ctx, id)
	if err != nil {
		return volumedomain.Resource{}, fmt.Errorf("inspect volume: %w", err)
	}
	return a.mapVolume(ctx, row)
}
func (a *Adapter) CreateVolume(ctx context.Context, input volumedomain.CreateRequest) (volumedomain.Resource, error) {
	driver := input.Driver
	if driver == "" {
		driver = "local"
	}
	row, err := a.client.VolumeCreate(ctx, dockervolume.CreateOptions{Name: input.Name, Driver: driver, DriverOpts: input.Options, Labels: input.Labels})
	if err != nil {
		return volumedomain.Resource{}, fmt.Errorf("create volume: %w", err)
	}
	return a.mapVolume(ctx, row)
}
func (a *Adapter) RemoveVolume(ctx context.Context, id string) error {
	usedBy, err := a.volumeUsage(ctx, id)
	if err != nil {
		return err
	}
	if len(usedBy) > 0 {
		return volumedomain.ErrInUse
	}
	return a.client.VolumeRemove(ctx, id, false)
}

func (a *Adapter) Prune(ctx context.Context, report task.Reporter) error {
	report(10, "Pruning stopped containers")
	if _, err := a.client.ContainersPrune(ctx, filters.Args{}); err != nil {
		return fmt.Errorf("prune containers: %w", err)
	}
	report(35, "Pruning unused networks")
	if _, err := a.client.NetworksPrune(ctx, filters.Args{}); err != nil {
		return fmt.Errorf("prune networks: %w", err)
	}
	report(60, "Pruning dangling images")
	if _, err := a.client.ImagesPrune(ctx, filters.Args{}); err != nil {
		return fmt.Errorf("prune images: %w", err)
	}
	report(85, "Pruning unused anonymous volumes")
	if _, err := a.client.VolumesPrune(ctx, filters.Args{}); err != nil {
		return fmt.Errorf("prune volumes: %w", err)
	}
	report(100, "System prune complete")
	return nil
}
