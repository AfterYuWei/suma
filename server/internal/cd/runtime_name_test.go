package cd

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

type namedRuntimeResolver struct {
	fakeTargetResolver
	snapshot compose.RuntimeProjectSnapshot
	err      error
}

func (r *namedRuntimeResolver) InspectComposeNamespace(context.Context, string, string) (compose.RuntimeProjectSnapshot, error) {
	return r.snapshot, r.err
}

type namedRuntimeRunner struct {
	*fakeComposeRunner
	names []string
}

func (r *namedRuntimeRunner) UpRelease(ctx context.Context, spec compose.ExecutionSpec, timeout int, output io.Writer) error {
	r.mu.Lock()
	r.names = append(r.names, spec.ProjectName)
	r.mu.Unlock()
	return r.fakeComposeRunner.UpRelease(ctx, spec, timeout, output)
}

func TestCDProjectUsesItsNameForDeploymentAndKeepsLegacyNames(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	project, err := h.service.CreateProject(context.Background(), "eizhu_admin")
	if err != nil {
		t.Fatal(err)
	}
	var stored database.DeliveryProject
	if err := h.db.First(&stored, project.ID).Error; err != nil {
		t.Fatal(err)
	}
	repository, err := repositoryFromProject(h.project)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := executionSpec(stored, repository, h.git.revision.WorktreePath)
	if err != nil || spec.ProjectName != "eizhu_admin" || project.DeploymentName != "eizhu_admin" {
		t.Fatalf("named spec=%#v, %v", spec, err)
	}
	for _, saved := range []string{"suma-cd-1", "custom_existing", ""} {
		legacy := database.DeliveryProject{ID: 77, Name: "existing-app", DeploymentName: saved}
		want := saved
		if want == "" {
			want = "suma-cd-77"
		}
		if runtimeName(legacy) != want || projectSummary(legacy).DeploymentName != want {
			t.Fatalf("legacy namespace changed: %#v", legacy)
		}
	}
}

func TestNamedCDDeploymentPassesTheSavedNameToTheRunner(t *testing.T) {
	for _, deploymentName := range []string{"production", "suma-cd-legacy"} {
		t.Run(deploymentName, func(t *testing.T) {
			h := newCDHarness(t, ModeManual)
			if err := h.db.Model(&h.project).Update("deployment_name", deploymentName).Error; err != nil {
				t.Fatal(err)
			}
			if err := h.db.Create(&database.Node{ID: "local", Name: "Aliyun", ConnectionType: "unix", Endpoint: "unix:///docker.sock", Enabled: true}).Error; err != nil {
				t.Fatal(err)
			}
			if err := h.db.Create(&database.DeliveryProjectNode{ProjectID: h.project.ID, NodeID: "local"}).Error; err != nil {
				t.Fatal(err)
			}
			runner := &namedRuntimeRunner{fakeComposeRunner: h.runner}
			h.service.compose = &targetedFakeRunner{fakeComposeRunner: h.runner, runners: map[string]compose.Runner{"local": runner}}
			h.service.SetTargetResolver(&namedRuntimeResolver{})
			result := syncAndDeploy(t, h)
			if result.Status != StatusSucceeded {
				t.Fatalf("named deployment failed: %#v", result)
			}
			runner.mu.Lock()
			defer runner.mu.Unlock()
			if len(runner.names) != 1 || runner.names[0] != deploymentName {
				t.Fatalf("runner namespace=%#v", runner.names)
			}
		})
	}
}

func TestNamedCDDeploymentDoesNotPullOrApplyAForeignNamespace(t *testing.T) {
	h := newCDHarness(t, ModeAuto)
	if err := h.db.Model(&h.project).Update("deployment_name", h.project.Name).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Create(&database.Node{ID: "local", Name: "Aliyun", ConnectionType: "unix", Endpoint: "unix:///docker.sock", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Create(&database.DeliveryProjectNode{ProjectID: h.project.ID, NodeID: "local"}).Error; err != nil {
		t.Fatal(err)
	}
	h.service.SetTargetResolver(&namedRuntimeResolver{snapshot: compose.RuntimeProjectSnapshot{Volumes: []compose.RuntimeVolume{{Name: "production_data"}}}})
	h.service.compose = &targetedFakeRunner{fakeComposeRunner: h.runner, runners: map[string]compose.Runner{"local": h.runner}}
	sync, err := h.service.Sync(context.Background(), h.project.Name, "manual", Actor{Name: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, h.db, sync.ID, task.StatusFailed)
	for _, call := range h.runner.Calls() {
		if call == "pull" || call == "up" {
			t.Fatalf("foreign namespace executed %s", call)
		}
	}
	releases, err := h.service.ListReleases(context.Background(), h.project.Name)
	if err != nil || len(releases) != 1 || len(releases[0].Deployments) != 1 || len(releases[0].Deployments[0].Attempts) != 1 {
		t.Fatalf("failure evidence missing: %#v, %v", releases, err)
	}
	attempt := releases[0].Deployments[0].Attempts[0]
	if attempt.Status != StatusFailed || !strings.Contains(attempt.FailureReason, "already in use") {
		t.Fatalf("namespace failure not retained: %#v", attempt)
	}
}

func TestCDProjectNamesRejectInvalidOrReservedNamespaces(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	for _, name := range []string{"App", "app.prod", "-app", "_app", "", strings.Repeat("a", 129)} {
		if _, err := h.service.CreateProject(context.Background(), name); err == nil {
			t.Fatalf("invalid Compose name %q accepted", name)
		}
	}
	for _, stored := range []string{"suma-cd-legacy", ""} {
		row := database.DeliveryProject{Name: "legacy-" + stored, DeploymentName: stored}
		if err := h.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := h.service.CreateProject(context.Background(), runtimeName(row)); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("reserved name accepted: %v", err)
		}
	}
}

func TestNamedCDNamespaceRejectsExistingResourcesAndSupportsSafeRetry(t *testing.T) {
	for _, resource := range []string{"container", "network", "volume"} {
		t.Run(resource, func(t *testing.T) {
			h := newCDHarness(t, ModeManual)
			project := h.project
			project.DeploymentName = project.Name
			if err := h.db.Model(&project).Update("deployment_name", project.DeploymentName).Error; err != nil {
				t.Fatal(err)
			}
			spec := compose.ExecutionSpec{ProjectName: project.Name, ProjectDir: h.git.revision.WorktreePath}
			resolver := &namedRuntimeResolver{}
			switch resource {
			case "container":
				resolver.snapshot.Containers = []compose.RuntimeContainer{{ID: "foreign", Labels: map[string]string{compose.WorkingDirLabel: "/srv/other"}}}
			case "network":
				resolver.snapshot.Networks = []compose.RuntimeNetwork{{Name: project.Name + "_default"}}
			case "volume":
				resolver.snapshot.Volumes = []compose.RuntimeVolume{{Name: project.Name + "_data"}}
			}
			h.service.SetTargetResolver(resolver)
			for range 2 {
				if err := h.service.claimDeploymentNamespace(context.Background(), project, "local", spec); err == nil || !strings.Contains(err.Error(), "already in use") {
					t.Fatalf("foreign resources adopted: %v", err)
				}
			}
			var count int64
			h.db.Model(&database.DeliveryTargetState{}).Count(&count)
			if count != 0 {
				t.Fatal("a rejected claim reserved the foreign namespace")
			}
			resolver.snapshot = compose.RuntimeProjectSnapshot{}
			if err := h.service.claimDeploymentNamespace(context.Background(), project, "local", spec); err != nil {
				t.Fatal(err)
			}
			resolver.snapshot.Networks = []compose.RuntimeNetwork{{Name: project.Name + "_default"}}
			resolver.snapshot.Volumes = []compose.RuntimeVolume{{Name: project.Name + "_data"}}
			if err := h.service.claimDeploymentNamespace(context.Background(), project, "local", spec); err != nil {
				t.Fatalf("partial first up cannot retry: %v", err)
			}
			resolver.snapshot.Containers = []compose.RuntimeContainer{{Labels: map[string]string{compose.WorkingDirLabel: spec.ProjectDir}}}
			if err := h.service.claimDeploymentNamespace(context.Background(), project, "local", spec); err != nil {
				t.Fatalf("own runtime rejected: %v", err)
			}
			resolver.snapshot.Containers[0].Labels[compose.WorkingDirLabel] = "/srv/other"
			if err := h.service.claimDeploymentNamespace(context.Background(), project, "local", spec); err == nil {
				t.Fatal("foreign container in reserved namespace accepted")
			}
			resolver.snapshot = compose.RuntimeProjectSnapshot{Containers: []compose.RuntimeContainer{{Labels: map[string]string{compose.WorkingDirLabel: filepath.Join(spec.ProjectDir, "old-release")}}}}
			old := database.DeliveryRelease{ProjectID: project.ID, CommitSHA: strings.Repeat("b", 40), Status: StatusSucceeded, WorktreePath: filepath.Join(spec.ProjectDir, "old-release"), ComposeFilesJSON: `["compose.yml"]`}
			if err := h.db.Create(&old).Error; err != nil {
				t.Fatal(err)
			}
			if err := h.db.Model(&database.DeliveryTargetState{}).Where("project_id = ? AND node_id = ?", project.ID, "local").Updates(map[string]any{"active_release_id": old.ID, "observed_commit": old.CommitSHA, "health_summary": "existing evidence"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := h.service.claimDeploymentNamespace(context.Background(), project, "local", spec); err != nil {
				t.Fatalf("previous release namespace rejected: %v", err)
			}
			var retained database.DeliveryTargetState
			if err := h.db.Where("project_id = ? AND node_id = ?", project.ID, "local").First(&retained).Error; err != nil || retained.ActiveReleaseID == nil || *retained.ActiveReleaseID != old.ID || retained.ObservedCommit != old.CommitSHA || retained.HealthSummary != "existing evidence" {
				t.Fatalf("existing state changed: %#v, %v", retained, err)
			}
			resolver.err = errors.New("node offline")
			if err := h.service.claimDeploymentNamespace(context.Background(), project, "local", spec); err == nil || !strings.Contains(err.Error(), "node offline") {
				t.Fatalf("unreachable node did not fail closed: %v", err)
			}
		})
	}
}

func TestRuntimeProjectNamesAreNodeScopedAndUseSavedIdentities(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	project := h.project
	if err := h.db.Model(&project).Update("deployment_name", "suma-cd-1").Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Create(&database.DeliveryProjectNode{ProjectID: project.ID, NodeID: "local"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, nodeID := range []string{"local", "other"} {
		names, err := h.service.RuntimeProjectNames(context.Background(), nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if nodeID == "local" && names["suma-cd-1"] != project.Name {
			t.Fatalf("saved name association lost: %#v", names)
		}
		if nodeID == "other" && len(names) != 0 {
			t.Fatalf("cross-node association: %#v", names)
		}
	}
	newProject, err := h.service.CreateProject(context.Background(), "unclaimed-app")
	if err != nil {
		t.Fatal(err)
	}
	names, err := h.service.RuntimeProjectNames(context.Background(), "local")
	if err != nil || names["unclaimed-app"] != "" {
		t.Fatalf("an unclaimed native name was labelled as CD: %#v, %v", names, err)
	}
	if err := h.db.Create(&database.DeliveryTargetState{ProjectID: newProject.ID, NodeID: "local"}).Error; err != nil {
		t.Fatal(err)
	}
	names, err = h.service.RuntimeProjectNames(context.Background(), "local")
	if err != nil || names["unclaimed-app"] != "unclaimed-app" {
		t.Fatalf("claimed native name missing: %#v, %v", names, err)
	}
}
