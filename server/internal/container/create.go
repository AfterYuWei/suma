package container

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/image"
	"github.com/suma/suma/server/internal/redact"
	"github.com/suma/suma/server/internal/task"
)

const DockerSocketConfirmationHeader = "X-SUMA-Allow-Docker-Socket"

var ErrInvalidCreate = errors.New("invalid container configuration")
var createName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
var environmentName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type CreatePort struct {
	ContainerPort uint16 `json:"container_port"`
	HostPort      uint16 `json:"host_port"`
	HostIP        string `json:"host_ip"`
	Protocol      string `json:"protocol"`
}
type CreateMount struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}
type CreateEnvironment struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
type CreateRequest struct {
	Name             string              `json:"name"`
	Image            string              `json:"image"`
	PullPolicy       string              `json:"pull_policy"`
	CredentialID     *uint               `json:"credential_id,omitempty"`
	Start            *bool               `json:"start"`
	Ports            []CreatePort        `json:"ports"`
	Environment      []CreateEnvironment `json:"environment"`
	Mounts           []CreateMount       `json:"mounts"`
	Network          string              `json:"network"`
	RestartPolicy    string              `json:"restart_policy"`
	Command          []string            `json:"command"`
	Entrypoint       []string            `json:"entrypoint"`
	WorkingDirectory string              `json:"working_directory"`
	User             string              `json:"user"`
	MemoryMB         int64               `json:"memory_mb"`
	CPUs             float64             `json:"cpus"`
	ReadOnlyRootFS   bool                `json:"read_only_rootfs"`
	TTY              bool                `json:"tty"`
	OpenStdin        bool                `json:"open_stdin"`
}
type CreateResult struct {
	ID       string   `json:"id"`
	Warnings []string `json:"warnings"`
}
type CreationAdapter interface {
	ContainerImageExists(context.Context, string) (bool, error)
	PullImage(context.Context, string) (io.ReadCloser, error)
	CreateContainer(context.Context, CreateRequest) (CreateResult, error)
	Start(context.Context, string) error
}
type CreationActor struct {
	UserID *uint
	IP     string
}
type CreationService struct {
	adapter CreationAdapter
	tasks   *task.Service
	audit   *audit.Service
}

func NewCreationService(adapter CreationAdapter, tasks *task.Service, audit *audit.Service) *CreationService {
	return &CreationService{adapter: adapter, tasks: tasks, audit: audit}
}

func ValidateCreateRequest(input *CreateRequest, allowSocket bool) error {
	invalid := func(message string) error { return fmt.Errorf("%w: %s", ErrInvalidCreate, message) }
	input.Name, input.Image = strings.TrimSpace(input.Name), strings.TrimSpace(input.Image)
	if len(input.Name) > 128 || !createName.MatchString(input.Name) {
		return invalid("name must use letters, numbers, dots, underscores or hyphens")
	}
	if len(input.Image) > 512 {
		return invalid("image reference is too long")
	}
	if _, err := reference.ParseAnyReference(input.Image); err != nil {
		return invalid("a valid image reference is required")
	}
	if input.PullPolicy == "" {
		input.PullPolicy = "missing"
	}
	if input.PullPolicy != "missing" && input.PullPolicy != "always" && input.PullPolicy != "never" {
		return invalid("unsupported image pull policy")
	}
	if input.Start == nil {
		start := true
		input.Start = &start
	}
	if input.Network == "" {
		input.Network = "bridge"
	}
	if len(input.Network) > 128 || !createName.MatchString(input.Network) {
		return invalid("invalid network name")
	}
	if input.RestartPolicy == "" {
		input.RestartPolicy = "no"
	}
	if input.RestartPolicy != "no" && input.RestartPolicy != "always" && input.RestartPolicy != "unless-stopped" && input.RestartPolicy != "on-failure" {
		return invalid("unsupported restart policy")
	}
	if len(input.Ports) > 100 || len(input.Environment) > 200 || len(input.Mounts) > 100 {
		return invalid("too many ports, environment variables or mounts")
	}
	if (input.Network == "host" || input.Network == "none") && len(input.Ports) != 0 {
		return invalid("host and none networks cannot publish ports")
	}
	ports := map[string]bool{}
	for i := range input.Ports {
		port := &input.Ports[i]
		if port.Protocol == "" {
			port.Protocol = "tcp"
		}
		if port.HostIP == "" {
			port.HostIP = "127.0.0.1"
		}
		if port.ContainerPort == 0 || (port.Protocol != "tcp" && port.Protocol != "udp") || net.ParseIP(port.HostIP) == nil {
			return invalid("invalid container port, protocol or host IP")
		}
		key := fmt.Sprintf("%s/%d/%s", port.HostIP, port.HostPort, port.Protocol)
		if port.HostPort != 0 && ports[key] {
			return invalid("a host port binding is duplicated")
		}
		ports[key] = true
	}
	env := map[string]bool{}
	for _, variable := range input.Environment {
		if !environmentName.MatchString(variable.Key) || env[variable.Key] || len(variable.Value) > 64<<10 || strings.ContainsRune(variable.Value, 0) {
			return invalid("invalid or duplicate environment variable")
		}
		env[variable.Key] = true
	}
	targets := map[string]bool{}
	for _, mount := range input.Mounts {
		if !validContainerPath(mount.Target) || targets[mount.Target] || mount.Target == "/" {
			return invalid("mount targets must be distinct absolute paths below root")
		}
		targets[mount.Target] = true
		switch mount.Type {
		case "bind":
			if !validContainerPath(mount.Source) || strings.Contains(mount.Source, "$") {
				return invalid("bind sources must be non-interpolated absolute paths on the Docker node")
			}
		case "volume":
			if len(mount.Source) > 128 || !createName.MatchString(mount.Source) {
				return invalid("a valid named volume is required")
			}
		default:
			return invalid("unsupported mount type")
		}
		if (mount.Source == "/var/run/docker.sock" || mount.Source == "/run/docker.sock" || mount.Target == "/var/run/docker.sock" || mount.Target == "/run/docker.sock") && !allowSocket {
			return invalid("Docker socket mounts require explicit confirmation, including read-only mounts")
		}
	}
	if input.WorkingDirectory != "" && !validContainerPath(input.WorkingDirectory) {
		return invalid("working directory must be an absolute path")
	}
	if len(input.User) > 128 || strings.ContainsAny(input.User, "\x00\r\n") {
		return invalid("invalid container user")
	}
	if input.MemoryMB < 0 || input.MemoryMB > 1<<30 || (input.MemoryMB > 0 && input.MemoryMB < 6) {
		return invalid("memory limit must be zero or at least 6 MiB")
	}
	if math.IsNaN(input.CPUs) || math.IsInf(input.CPUs, 0) || input.CPUs < 0 || input.CPUs > 1024 {
		return invalid("CPU limit must be between zero and 1024")
	}
	for _, args := range [][]string{input.Command, input.Entrypoint} {
		if len(args) > 100 {
			return invalid("too many command arguments")
		}
		for _, argument := range args {
			if len(argument) > 64<<10 || strings.ContainsRune(argument, 0) {
				return invalid("invalid command argument")
			}
		}
	}
	return nil
}
func validContainerPath(value string) bool {
	return len(value) <= 4096 && strings.HasPrefix(value, "/") && path.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func (s *CreationService) CreateForNode(nodeID, nodeName string, input CreateRequest, actor CreationActor, material credential.RegistryMaterial, allowSocket bool) (database.Task, error) {
	if err := ValidateCreateRequest(&input, allowSocket); err != nil {
		return database.Task{}, err
	}
	if material.ServerAddress != "" {
		named, err := reference.ParseNormalizedNamed(input.Image)
		canonical := func(host string) string {
			switch host {
			case "index.docker.io", "registry-1.docker.io":
				return "docker.io"
			}
			return strings.ToLower(host)
		}
		if err != nil || canonical(reference.Domain(named)) != canonical(material.ServerAddress) {
			return database.Task{}, fmt.Errorf("%w: registry credential does not match the image registry", ErrInvalidCreate)
		}
	}
	if s.tasks == nil {
		return database.Task{}, errors.New("container task service is unavailable")
	}
	// The configuration and registry material stay in memory. Task evidence never
	// contains environment values, registry secrets, or a replayable config.
	safe := func(value string) string {
		for _, variable := range input.Environment {
			if variable.Value != "" {
				value = strings.ReplaceAll(value, variable.Value, "[REDACTED]")
			}
		}
		if material.Secret != "" {
			value = strings.ReplaceAll(value, material.Secret, "[REDACTED]")
		}
		return redact.Bounded(value, 4096)
	}
	return s.tasks.StartWithIDForNode(nodeID, nodeName, "container.create", "Create "+input.Name, func(ctx context.Context, taskID string, report task.Reporter) (workErr error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		defer cancel()
		resource := input.Name
		defer func() {
			if s.audit != nil {
				result := "success"
				if workErr != nil {
					result = "failed"
				}
				if ctx.Err() != nil {
					result = "canceled"
				}
				auditCtx, auditCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer auditCancel()
				_ = s.audit.RecordLinkedForNode(auditCtx, nodeID, nodeName, actor.UserID, "container.create", "container", resource, actor.IP, result, taskID, nil)
			}
		}()
		fail := func(err error) error { return errors.New(safe(err.Error())) }
		report(5, "Checking image on the selected node")
		exists, err := s.adapter.ContainerImageExists(ctx, input.Image)
		if err != nil {
			return fail(fmt.Errorf("unable to inspect image: %w", err))
		}
		if !exists && input.PullPolicy == "never" {
			return errors.New("image is not present on the selected node; allow pulling or select a local image")
		}
		if input.PullPolicy == "always" || !exists {
			report(10, "Pulling image")
			var stream io.ReadCloser
			if material.ServerAddress != "" {
				authenticated, ok := s.adapter.(image.AuthenticatedAdapter)
				if !ok {
					return errors.New("runtime does not support registry authentication")
				}
				stream, err = authenticated.PullImageAuthenticated(ctx, input.Image, material.ServerAddress, material.Username, material.Secret, material.AuthType == credential.RegistryToken)
			} else {
				stream, err = s.adapter.PullImage(ctx, input.Image)
			}
			if err != nil {
				return fail(err)
			}
			err = consumeCreationPull(ctx, stream, func(message string) { report(35, safe(message)) })
			if err != nil {
				return fail(fmt.Errorf("image pull failed: %w", err))
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		report(70, "Creating container")
		created, err := s.adapter.CreateContainer(ctx, input)
		if err != nil {
			return fail(fmt.Errorf("unable to create container: %w", err))
		}
		resource = created.ID
		// Persist the created ID before starting; failed/canceled starts retain
		// the container and this checkpoint so the operator can inspect it.
		checkpointCtx, checkpointCancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.tasks.ReportStep(checkpointCtx, taskID, "container:"+created.ID, "Created", 1, 1, 100)
		checkpointCancel()
		report(85, "Created container "+created.ID)
		for _, warning := range created.Warnings {
			report(85, safe(warning))
		}
		if *input.Start {
			if err := ctx.Err(); err != nil {
				return err
			}
			report(90, "Starting container")
			if err := s.adapter.Start(ctx, created.ID); err != nil {
				return fail(fmt.Errorf("container %s was created but could not start; inspect it before retrying: %w", created.ID, err))
			}
		}
		report(100, "Container ready: "+created.ID)
		return nil
	})
}

func consumeCreationPull(ctx context.Context, stream io.ReadCloser, report func(string)) error {
	defer stream.Close()
	stop := context.AfterFunc(ctx, func() { _ = stream.Close() })
	defer stop()
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	last := time.Time{}
	for scanner.Scan() {
		var event struct {
			Status string `json:"status"`
			ID     string `json:"id"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return errors.New("invalid image pull response")
		}
		if event.Error != "" {
			return errors.New(event.Error)
		}
		if time.Since(last) >= time.Second && event.Status != "" {
			report(strings.TrimSpace(event.ID + " " + event.Status))
			last = time.Now()
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return scanner.Err()
}
