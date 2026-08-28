package compose

import (
	"context"
	"encoding/json"
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

func TestMappedProjectKeepsDeclaredStoppedServiceAndMarksOrphan(t *testing.T) {
	sourceRoot := t.TempDir()
	file := filepath.Join(sourceRoot, "compose.yml")
	if err := os.WriteFile(file, []byte("services:\n  web:\n    image: app:v1\n  worker:\n    image: worker:v1\n    scale: 2\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	runner := &reconstructionRunner{
		rendered: `{"name":"shop","services":{"web":{"image":"app:v1"},"worker":{"image":"worker:v1","scale":2}}}`,
		hashes:   map[string]string{"web": "web-hash", "worker": "worker-hash"},
	}
	containers := observableContainers{
		staticContainers: staticContainers{rows: []containerdomain.Summary{{ID: "web", Labels: map[string]string{ProjectLabel: "shop", WorkingDirLabel: sourceRoot, ConfigFilesLabel: file}}}},
		snapshot: RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{
			{ID: "old", Name: "shop-old-1", Service: "old", ConfigHash: "old-hash", Config: RuntimeConfig{Image: "old:v1"}},
			{ID: "web", Name: "shop-web-1", Service: "web", ConfigHash: "web-hash", Config: RuntimeConfig{Image: "app:v1"}},
		}},
	}
	service := &Service{root: t.TempDir(), runner: runner, containers: containers, localSources: true}
	draft, err := service.BuildTakeoverDraft(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Observation.Services) != 3 {
		t.Fatalf("services = %#v", draft.Observation.Services)
	}
	byName := map[string]ObservedComposeService{}
	for _, observed := range draft.Observation.Services {
		byName[observed.Name] = observed
	}
	if !byName["web"].Declared || byName["web"].DriftStatus != "in_sync" {
		t.Fatalf("web = %#v", byName["web"])
	}
	if !byName["worker"].Declared || byName["worker"].DriftStatus != "not_created" || byName["worker"].DesiredReplicas != 2 || len(byName["worker"].Instances) != 0 {
		t.Fatalf("worker = %#v", byName["worker"])
	}
	if byName["worker"].Instances == nil || byName["worker"].ConfigVariants == nil {
		t.Fatalf("stopped Service collections must encode as arrays: %#v", byName["worker"])
	}
	if byName["old"].Declared || byName["old"].DriftStatus != "orphan" || len(byName["old"].DriftReasons) != 1 || byName["old"].DriftReasons[0] != "stale_container" {
		t.Fatalf("old = %#v", byName["old"])
	}
	if len(draft.Observation.OrphanContainers) != 1 || draft.Observation.OrphanContainers[0].ContainerID != "old" {
		t.Fatalf("orphans = %#v", draft.Observation.OrphanContainers)
	}
}

func TestTakeoverDraftJSONUsesArraysForEmptyCollections(t *testing.T) {
	containers := observableContainers{
		staticContainers: staticContainers{rows: []containerdomain.Summary{{ID: "web", Labels: map[string]string{ProjectLabel: "shop"}}}},
		snapshot:         RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{{ID: "web", Service: "web", ImageInspectOK: true, Config: RuntimeConfig{Image: "app:v1"}}}},
	}
	service := &Service{root: t.TempDir(), containers: containers, nodeID: "tcp-node"}
	draft, err := service.BuildTakeoverDraft(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, field := range []string{"variables", "blockers", "services", "networks", "volumes", "one_off_containers", "orphan_containers"} {
		if strings.Contains(text, `"`+field+`":null`) {
			t.Fatalf("takeover JSON contains null %s: %s", field, text)
		}
	}
}

func TestMappedProjectExplainsStaleAndPartialRecreate(t *testing.T) {
	observation := ObserveRuntimeProject(RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{
		{ID: "old", Service: "web", ConfigHash: "old-hash", CreatedAt: time.Unix(1, 0), Config: RuntimeConfig{Image: "app:old"}},
		{ID: "current", Service: "web", ConfigHash: "expected", CreatedAt: time.Unix(2, 0), Config: RuntimeConfig{Image: "app:new"}},
	}})
	applyExpectedProject(&observation, map[string]string{"web": "expected"}, map[string]any{"services": map[string]any{"web": map[string]any{"image": "app:new"}}})
	if !containsString(observation.Services[0].DriftReasons, "stale_container") {
		t.Fatalf("stale reasons = %#v", observation.Services[0].DriftReasons)
	}

	partial := ObserveRuntimeProject(RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{
		{ID: "current", Service: "web", ConfigHash: "expected", CreatedAt: time.Unix(1, 0), Config: RuntimeConfig{Image: "app:new"}},
		{ID: "other", Service: "web", ConfigHash: "other", CreatedAt: time.Unix(2, 0), Config: RuntimeConfig{Image: "app:other"}},
	}})
	applyExpectedProject(&partial, map[string]string{"web": "expected"}, map[string]any{"services": map[string]any{"web": map[string]any{"image": "app:new"}}})
	if !containsString(partial.Services[0].DriftReasons, "partial_recreate") {
		t.Fatalf("partial reasons = %#v", partial.Services[0].DriftReasons)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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

func TestRenderTakeoverModelPreservesExplicitEmptyEnvironment(t *testing.T) {
	variable := newEnvironmentCandidate("agent", "SORAIN_REG_TOKEN", "", "explicit_inferred")
	model := map[string]any{"services": map[string]any{"agent": map[string]any{"image": "agent:v1"}}}
	composeContent, environment, err := renderTakeoverModel(model, []EnvironmentCandidate{variable})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(composeContent, "${SORAIN_REG_TOKEN?required}") || strings.Contains(composeContent, "${SORAIN_REG_TOKEN:?required}") {
		t.Fatalf("empty environment must allow an explicitly empty value:\n%s", composeContent)
	}
	if !strings.Contains(environment, "SORAIN_REG_TOKEN=''\n") {
		t.Fatalf("empty environment value was not preserved:\n%s", environment)
	}
}

func TestRenderTakeoverDraftAlwaysExcludesImageDefaults(t *testing.T) {
	containers := observableContainers{
		staticContainers: staticContainers{rows: []containerdomain.Summary{{ID: "web", Labels: map[string]string{ProjectLabel: "shop"}}}},
		snapshot: RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{{
			ID: "web", Service: "web", ImageInspectOK: true,
			ImageEnvironment: []string{"PATH=/usr/bin"},
			Config:           RuntimeConfig{Image: "app:v1", Environment: []string{"PATH=/usr/bin"}},
		}}},
	}
	service := &Service{root: t.TempDir(), containers: containers, nodeID: "tcp-node"}
	draft, err := service.BuildTakeoverDraft(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Variables) != 1 || draft.Variables[0].Source != "image_default" {
		t.Fatalf("variables = %#v", draft.Variables)
	}
	rendered, err := service.RenderTakeoverDraft(context.Background(), "shop", draft.Fingerprint, []EnvironmentChoice{{ID: draft.Variables[0].ID, Destination: EnvironmentCompose}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.Compose, "PATH") || rendered.Variables[0].Destination != EnvironmentExclude {
		t.Fatalf("image default was not excluded: %#v\n%s", rendered.Variables, rendered.Compose)
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
