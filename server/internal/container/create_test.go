package container

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/task"
	"github.com/suma/suma/server/internal/testutil"
)

type creationRuntime struct {
	exists    bool
	pull      string
	startErr  error
	createErr error
	input     CreateRequest
	creates   atomic.Int32
	starts    atomic.Int32
	pulls     atomic.Int32
	stream    io.ReadCloser
	opened    chan struct{}
}

func (r *creationRuntime) ContainerImageExists(context.Context, string) (bool, error) {
	return r.exists, nil
}
func (r *creationRuntime) PullImage(context.Context, string) (io.ReadCloser, error) {
	r.pulls.Add(1)
	if r.opened != nil {
		close(r.opened)
	}
	if r.stream != nil {
		return r.stream, nil
	}
	return io.NopCloser(strings.NewReader(r.pull)), nil
}
func (r *creationRuntime) CreateContainer(_ context.Context, input CreateRequest) (CreateResult, error) {
	r.creates.Add(1)
	r.input = input
	return CreateResult{ID: strings.Repeat("ab", 32)}, r.createErr
}
func (r *creationRuntime) Start(context.Context, string) error { r.starts.Add(1); return r.startErr }

func TestContainerCreationValidation(t *testing.T) {
	base := func() CreateRequest { return CreateRequest{Name: "test-app", Image: "nginx:alpine"} }
	input := base()
	if err := ValidateCreateRequest(&input, false); err != nil || input.Network != "bridge" || input.PullPolicy != "missing" || !*input.Start {
		t.Fatalf("defaults: %#v %v", input, err)
	}
	for _, edit := range []func(*CreateRequest){
		func(i *CreateRequest) { i.Name = "../bad" },
		func(i *CreateRequest) { i.Image = "https://registry/image" },
		func(i *CreateRequest) { i.PullPolicy = "sometimes" },
		func(i *CreateRequest) { i.Ports = []CreatePort{{ContainerPort: 0}} },
		func(i *CreateRequest) { i.Ports = []CreatePort{{ContainerPort: 80, HostIP: "hostname"}} },
		func(i *CreateRequest) { i.Ports = []CreatePort{{ContainerPort: 80, Protocol: "sctp"}} },
		func(i *CreateRequest) {
			i.Ports = []CreatePort{{ContainerPort: 80, HostPort: 8080}, {ContainerPort: 81, HostPort: 8080}}
		},
		func(i *CreateRequest) { i.Network = "host"; i.Ports = []CreatePort{{ContainerPort: 80}} },
		func(i *CreateRequest) { i.Environment = []CreateEnvironment{{Key: "TOKEN"}, {Key: "TOKEN"}} },
		func(i *CreateRequest) {
			i.Environment = []CreateEnvironment{{Key: "BAD=KEY", Value: "private-never-echo"}}
		},
		func(i *CreateRequest) { i.Mounts = []CreateMount{{Type: "bind", Source: "./data", Target: "/data"}} },
		func(i *CreateRequest) {
			i.Mounts = []CreateMount{{Type: "bind", Source: "/srv/${DATA}", Target: "/data"}}
		},
		func(i *CreateRequest) {
			i.Mounts = []CreateMount{{Type: "bind", Source: "/srv/../data", Target: "/data"}}
		},
		func(i *CreateRequest) { i.Mounts = []CreateMount{{Type: "volume", Source: "data", Target: "/"}} },
		func(i *CreateRequest) {
			i.Mounts = []CreateMount{{Type: "bind", Source: "/run/docker.sock", Target: "/socket", ReadOnly: true}}
		},
		func(i *CreateRequest) { i.Network = "container:other" },
		func(i *CreateRequest) { i.MemoryMB = 1 },
		func(i *CreateRequest) { i.CPUs = -1 },
		func(i *CreateRequest) { i.Command = []string{"a\x00b"} },
	} {
		input := base()
		edit(&input)
		if err := ValidateCreateRequest(&input, false); !errors.Is(err, ErrInvalidCreate) || strings.Contains(err.Error(), "private-never-echo") {
			t.Fatalf("invalid config accepted or leaked: %#v %v", input, err)
		}
	}
	input = base()
	input.Mounts = []CreateMount{{Type: "bind", Source: "/run/docker.sock", Target: "/socket", ReadOnly: true}}
	if err := ValidateCreateRequest(&input, true); err != nil {
		t.Fatal(err)
	}
}

func TestContainerCreationTaskAndEvidence(t *testing.T) {
	for _, scenario := range []struct {
		name                   string
		exists                 bool
		policy                 string
		start                  bool
		startErr               error
		pull                   string
		want                   string
		creates, starts, pulls int32
	}{
		{"local-start", true, "missing", true, nil, "", "success", 1, 1, 0},
		{"create-only", true, "never", false, nil, "", "success", 1, 0, 0},
		{"missing-pull", false, "missing", true, nil, "{\"status\":\"Pull complete\"}\n", "success", 1, 1, 1},
		{"always-pull", true, "always", true, nil, "{\"status\":\"Pull complete\"}\n", "success", 1, 1, 1},
		{"missing-never", false, "never", true, nil, "", "failed", 0, 0, 0},
		{"pull-failed", false, "missing", true, nil, "{\"error\":\"denied private-creation-secret\"}\n", "failed", 0, 0, 1},
		{"start-failed", true, "missing", true, errors.New("port occupied private-creation-secret"), "", "failed", 1, 1, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db, err := testutil.Open(t)
			if err != nil {
				t.Fatal(err)
			}
			tasks, audits := task.NewService(db), audit.NewService(db)
			runtime := &creationRuntime{exists: scenario.exists, pull: scenario.pull, startErr: scenario.startErr}
			input := CreateRequest{Name: "app", Image: "nginx:alpine", PullPolicy: scenario.policy, Start: &scenario.start, Environment: []CreateEnvironment{{Key: "PASSWORD", Value: "private-creation-secret"}}}
			row, err := NewCreationService(runtime, tasks, audits).CreateForNode("edge", "Edge", input, CreationActor{}, credential.RegistryMaterial{}, false)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := tasks.Wait(ctx, row.ID); err != nil {
				t.Fatal(err)
			}
			result, err := tasks.GetForNode(ctx, "edge", row.ID)
			if err != nil || result.Status != scenario.want || result.Scope != "node" {
				t.Fatalf("task: %#v %v", result, err)
			}
			if runtime.creates.Load() != scenario.creates || runtime.starts.Load() != scenario.starts || runtime.pulls.Load() != scenario.pulls {
				t.Fatal("unexpected Docker operations")
			}
			steps, _ := tasks.Steps(ctx, row.ID)
			if scenario.creates == 1 && (len(steps) != 1 || steps[0].StepID != "container:"+strings.Repeat("ab", 32)) {
				t.Fatal("created ID checkpoint missing")
			}
			logs, _ := tasks.Logs(ctx, row.ID)
			evidence, _ := audits.ListForNode(ctx, 20, "edge")
			if len(evidence) != 1 || evidence[0].TaskID != row.ID || evidence[0].Result != scenario.want {
				t.Fatalf("linked audit: %#v", evidence)
			}
			encoded, _ := json.Marshal([]any{result, logs, evidence})
			if strings.Contains(string(encoded), "private-creation-secret") {
				t.Fatal("task or audit leaked secret")
			}
			if _, err := tasks.GetForNode(ctx, "local", row.ID); err == nil {
				t.Fatal("cross-node task exposed")
			}
		})
	}
}

func TestContainerCreationCancelClosesPull(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewService(db)
	reader, writer := io.Pipe()
	defer writer.Close()
	runtime := &creationRuntime{stream: reader, opened: make(chan struct{})}
	row, err := NewCreationService(runtime, tasks, nil).CreateForNode("edge", "Edge", CreateRequest{Name: "app", Image: "alpine:latest"}, CreationActor{}, credential.RegistryMaterial{}, false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("pull did not begin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := tasks.CancelForNode(ctx, "edge", row.ID); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Wait(ctx, row.ID); err != nil {
		t.Fatal("cancel did not close Docker pull promptly", err)
	}
	result, _ := tasks.Get(ctx, row.ID)
	if result.Status != task.StatusCanceled || runtime.creates.Load() != 0 {
		t.Fatal("canceled pull created a container")
	}
}

func TestContainerCreationRejectsWrongRegistry(t *testing.T) {
	_, err := NewCreationService(&creationRuntime{}, nil, nil).CreateForNode("edge", "Edge", CreateRequest{Name: "app", Image: "nginx:alpine"}, CreationActor{}, credential.RegistryMaterial{ServerAddress: "ghcr.io", Secret: "never-echo"}, false)
	if !errors.Is(err, ErrInvalidCreate) || strings.Contains(err.Error(), "never-echo") {
		t.Fatal("wrong registry credential accepted or leaked")
	}
}

type authenticatedCreationRuntime struct {
	creationRuntime
	material credential.RegistryMaterial
}

func (r *authenticatedCreationRuntime) PullImageAuthenticated(_ context.Context, _, server, username, secret string, token bool) (io.ReadCloser, error) {
	r.material = credential.RegistryMaterial{ServerAddress: server, Username: username, Secret: secret, AuthType: credential.RegistryBasic}
	if token {
		r.material.AuthType = credential.RegistryToken
	}
	return io.NopCloser(strings.NewReader("{\"status\":\"Pull complete\"}\n")), nil
}

func TestContainerCreationUsesAuthorizedRegistryWithoutPersistingMaterial(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	tasks, audits := task.NewService(db), audit.NewService(db)
	runtime := &authenticatedCreationRuntime{}
	material := credential.RegistryMaterial{ServerAddress: "ghcr.io", Secret: "registry-creation-private", AuthType: credential.RegistryToken}
	row, err := NewCreationService(runtime, tasks, audits).CreateForNode("edge", "Edge", CreateRequest{Name: "private-app", Image: "ghcr.io/team/app:v1"}, CreationActor{}, material, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tasks.Wait(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	result, _ := tasks.Get(ctx, row.ID)
	if result.Status != task.StatusSuccess || runtime.material != material || runtime.pulls.Load() != 0 {
		t.Fatal("private image did not use the requested registry credential")
	}
	logs, _ := tasks.Logs(ctx, row.ID)
	entries, _ := audits.ListForNode(ctx, 20, "edge")
	evidence, _ := json.Marshal([]any{result, logs, entries})
	if strings.Contains(string(evidence), material.Secret) {
		t.Fatal("registry material entered historical evidence")
	}
}
