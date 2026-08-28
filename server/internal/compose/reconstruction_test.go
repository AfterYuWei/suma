package compose

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	containerdomain "github.com/suma/suma/server/internal/container"
)

type reconstructionRunner struct {
	Runner
	rendered string
	hashes   map[string]string
	spec     ExecutionSpec
}

func (runner *reconstructionRunner) Render(_ context.Context, spec ExecutionSpec, _ io.Writer) (string, error) {
	runner.spec = spec
	return runner.rendered, nil
}

func (runner *reconstructionRunner) Hashes(_ context.Context, spec ExecutionSpec, _ io.Writer) (map[string]string, error) {
	runner.spec = spec
	return runner.hashes, nil
}

func TestBuildTakeoverDraftPrefersCompleteMappedProject(t *testing.T) {
	sourceRoot := t.TempDir()
	first, second := filepath.Join(sourceRoot, "compose.yml"), filepath.Join(sourceRoot, "override.yml")
	if err := os.WriteFile(first, []byte("services:\n  web:\n    image: app:v1\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("services:\n  web:\n    environment:\n      MODE: prod\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runner := &reconstructionRunner{
		rendered: `{"name":"shop","services":{"web":{"image":"app:v1","build":{"context":"."},"environment":{"MODE":"prod","PASSWORD":"secret"}}}}`,
		hashes:   map[string]string{"web": "expected-hash"},
	}
	containers := observableContainers{
		staticContainers: staticContainers{rows: []containerdomain.Summary{{ID: "web", Labels: map[string]string{ProjectLabel: "shop", WorkingDirLabel: sourceRoot, ConfigFilesLabel: first + "," + second}}}},
		snapshot:         RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{{ID: "web", Service: "web", ConfigHash: "old-hash", Config: RuntimeConfig{Image: "app:v1"}}}},
	}
	service := &Service{root: t.TempDir(), runner: runner, containers: containers, localSources: true}
	draft, err := service.BuildTakeoverDraft(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Source != "mapped" || draft.Confidence != "high" || len(runner.spec.Files) != 2 || runner.spec.Files[0] != first || runner.spec.Files[1] != second {
		t.Fatalf("draft/source spec = %#v / %#v", draft, runner.spec)
	}
	if len(draft.Variables) != 2 || draft.Variables[0].Source != "compose_explicit" || draft.Variables[1].Destination != EnvironmentFile {
		t.Fatalf("variables = %#v", draft.Variables)
	}
	if strings.Contains(draft.Compose, "build:") || !strings.Contains(draft.Environment, "PASSWORD='secret'") {
		t.Fatalf("rendered files:\n%s\n%s", draft.Compose, draft.Environment)
	}
	if draft.Observation.Services[0].DriftStatus != "runtime_drift" {
		t.Fatalf("expected source hash drift: %#v", draft.Observation.Services[0])
	}
}

func TestBuildTakeoverDraftFallsBackToWholeRuntimeProject(t *testing.T) {
	config := RuntimeConfig{
		Image: "app:v1", Environment: []string{"PATH=/usr/bin", "MODE=prod", "DATABASE_PASSWORD=secret"},
		Ports:    []RuntimePort{{Target: 8080, Published: 18080, Protocol: "tcp"}},
		Mounts:   []RuntimeMount{{Type: "volume", Name: "shop_data", Source: "shop_data", Target: "/data"}},
		Networks: []RuntimeEndpoint{{Name: "shop_default", Aliases: []string{"web"}}},
	}
	containers := observableContainers{
		staticContainers: staticContainers{rows: []containerdomain.Summary{{ID: "web-1", Labels: map[string]string{ProjectLabel: "shop"}}}},
		snapshot: RuntimeProjectSnapshot{
			ProjectName: "shop",
			Containers:  []RuntimeContainer{{ID: "web-1", Name: "shop-web-1", Service: "web", ContainerNumber: 1, CreatedAt: time.Unix(1, 0), ImageInspectOK: true, ImageEnvironment: []string{"PATH=/usr/bin"}, Config: config}},
			Networks:    []RuntimeNetwork{{ID: "net", Name: "shop_default", Driver: "bridge", Labels: map[string]string{ProjectLabel: "shop", "com.docker.compose.network": "default"}}},
			Volumes:     []RuntimeVolume{{Name: "shop_data", Driver: "local", Labels: map[string]string{ProjectLabel: "shop", "com.docker.compose.volume": "data"}}},
		},
	}
	service := &Service{root: t.TempDir(), containers: containers, nodeID: "tcp-node"}
	draft, err := service.BuildTakeoverDraft(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Source != "runtime" || len(draft.Variables) != 3 {
		t.Fatalf("draft = %#v", draft)
	}
	byKey := map[string]EnvironmentCandidate{}
	for _, variable := range draft.Variables {
		byKey[variable.Key] = variable
	}
	if byKey["PATH"].Destination != EnvironmentExclude || byKey["MODE"].Destination != EnvironmentCompose || byKey["DATABASE_PASSWORD"].Destination != EnvironmentFile {
		t.Fatalf("variables = %#v", byKey)
	}
	if strings.Contains(draft.Compose, "PATH") || !strings.Contains(draft.Compose, "MODE") || !strings.Contains(draft.Compose, "DATABASE_PASSWORD") || !strings.Contains(draft.Environment, "DATABASE_PASSWORD='secret'") {
		t.Fatalf("rendered files:\n%s\n%s", draft.Compose, draft.Environment)
	}
	if !strings.Contains(draft.Compose, "shop_default") || !strings.Contains(draft.Compose, "shop_data") {
		t.Fatalf("Project resources missing:\n%s", draft.Compose)
	}
}

func TestRenderTakeoverDraftAppliesPerVariableChoices(t *testing.T) {
	containers := observableContainers{
		staticContainers: staticContainers{rows: []containerdomain.Summary{{ID: "web", Labels: map[string]string{ProjectLabel: "shop"}}}},
		snapshot:         RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{{ID: "web", Service: "web", ImageInspectOK: true, Config: RuntimeConfig{Image: "app:v1", Environment: []string{"MODE=prod"}}}}},
	}
	service := &Service{root: t.TempDir(), containers: containers, nodeID: "tcp-node"}
	draft, err := service.BuildTakeoverDraft(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := service.RenderTakeoverDraft(context.Background(), "shop", draft.Fingerprint, []EnvironmentChoice{{ID: draft.Variables[0].ID, Destination: EnvironmentFile}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Compose, "${MODE:?required}") || !strings.Contains(rendered.Environment, "MODE='prod'") {
		t.Fatalf("rendered files:\n%s\n%s", rendered.Compose, rendered.Environment)
	}
}

func TestRuntimeServiceDoesNotMixProjectNetworkModeAndNetworks(t *testing.T) {
	service := runtimeServiceModel(RuntimeConfig{Image: "nginx:alpine", NetworkMode: "shop_default", Networks: []RuntimeEndpoint{{Name: "shop_default"}}}, 1)
	if _, exists := service["network_mode"]; exists {
		t.Fatalf("Project network was incorrectly emitted as network_mode: %#v", service)
	}
	if networks, ok := service["networks"].(map[string]any); !ok || len(networks) != 1 {
		t.Fatalf("Project network was not restored through networks: %#v", service)
	}
	host := runtimeServiceModel(RuntimeConfig{Image: "nginx:alpine", NetworkMode: "host", Networks: []RuntimeEndpoint{{Name: "host"}}}, 1)
	if host["network_mode"] != "host" {
		t.Fatalf("explicit host mode was lost: %#v", host)
	}
	if _, exists := host["networks"]; exists {
		t.Fatalf("host mode must not also declare networks: %#v", host)
	}
}
