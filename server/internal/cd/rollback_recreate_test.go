package cd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/task"
)

func TestRollbackRecreatesUnchangedLatestAndStopsOnPullFailure(t *testing.T) {
	for _, targeted := range []bool{false, true} {
		for _, pullFails := range []bool{false, true} {
			name := "single"
			if targeted {
				name = "node-targeted"
			}
			if pullFails {
				name += "/pull-fails"
			}
			t.Run(name, func(t *testing.T) {
				h := newCDHarness(t, ModeManual)
				ctx := context.Background()
				if targeted {
					if err := h.db.Create(&database.Node{ID: "node-a", Name: "A", ConnectionType: "unix", Endpoint: "unix:///a.sock", Enabled: true}).Error; err != nil {
						t.Fatal(err)
					}
					if err := h.db.Create(&database.DeliveryProjectNode{ProjectID: h.project.ID, NodeID: "node-a"}).Error; err != nil {
						t.Fatal(err)
					}
					h.service.compose = &targetedFakeRunner{fakeComposeRunner: h.runner, runners: map[string]compose.Runner{"node-a": h.runner}}
					h.service.SetTargetResolver(fakeTargetResolver{})
				}
				h.git.SetRevision(gitrepo.Revision{CommitSHA: strings.Repeat("a", 40), WorktreePath: makeWorktree(t, "example/web:latest")})
				h.runner.SetRendered(`{"services":{"web":{"image":"example/web:latest"}}}`)
				first := syncAndDeploy(t, h)
				h.git.SetRevision(gitrepo.Revision{CommitSHA: strings.Repeat("b", 40), WorktreePath: makeWorktree(t, "example/web:latest")})
				second := syncAndDeploy(t, h)
				if first.ConfigHash != second.ConfigHash {
					t.Fatal("fixture must keep Compose configuration unchanged")
				}
				if pullFails {
					h.runner.mu.Lock()
					h.runner.pull = errors.New("registry unavailable")
					h.runner.mu.Unlock()
				}
				before := len(h.runner.Calls())
				rollback, err := h.service.Rollback(ctx, h.project.Name, first.ID, Actor{Name: "operator"})
				if err != nil {
					t.Fatal(err)
				}
				want := task.StatusSuccess
				if pullFails {
					want = task.StatusFailed
				}
				waitTask(t, h.db, rollback.ID, want)
				calls := h.runner.Calls()[before:]
				if pullFails {
					for _, call := range calls {
						if call == "up" || call == "down" {
							t.Fatal("failed pull changed containers", calls)
						}
					}
					var project database.DeliveryProject
					if err := h.db.First(&project, h.project.ID).Error; err != nil || project.ActiveReleaseID == nil || *project.ActiveReleaseID != second.ID {
						t.Fatal("failed rollback changed active release", err)
					}
					return
				}
				assertCallSubsequence(t, calls, []string{"pull", "up", "ps"})
				specs := h.runner.UpSpecs()
				last := specs[len(specs)-1]
				if len(specs) != 3 || !last.ForceRecreate || last.Files[0] != specs[0].Files[0] || last.ProjectName != runtimeName(h.project) {
					t.Fatalf("rollback skipped replacement or changed selected release: %#v", specs)
				}
			})
		}
	}
}

func TestAutomaticRollbackForcesContainerReplacement(t *testing.T) {
	for _, targeted := range []bool{false, true} {
		name := "single"
		if targeted {
			name = "node-targeted"
		}
		t.Run(name, func(t *testing.T) {
			h := newCDHarness(t, ModeAuto)
			if err := h.db.Model(&h.project).Update("auto_rollback", true).Error; err != nil {
				t.Fatal(err)
			}
			if targeted {
				if err := h.db.Create(&database.Node{ID: "node-a", Name: "A", ConnectionType: "unix", Endpoint: "unix:///a.sock", Enabled: true}).Error; err != nil {
					t.Fatal(err)
				}
				if err := h.db.Create(&database.DeliveryProjectNode{ProjectID: h.project.ID, NodeID: "node-a"}).Error; err != nil {
					t.Fatal(err)
				}
				h.service.compose = &targetedFakeRunner{fakeComposeRunner: h.runner, runners: map[string]compose.Runner{"node-a": h.runner}}
				h.service.SetTargetResolver(fakeTargetResolver{})
			}
			firstTask, err := h.service.Sync(context.Background(), h.project.Name, "poll", Actor{Name: "scheduler"})
			if err != nil {
				t.Fatal(err)
			}
			waitTask(t, h.db, firstTask.ID, task.StatusSuccess)
			h.git.SetRevision(gitrepo.Revision{CommitSHA: strings.Repeat("b", 40), WorktreePath: makeWorktree(t, "example/web:latest")})
			h.runner.mu.Lock()
			h.runner.upFailures = []error{errors.New("deployment failed"), nil}
			h.runner.mu.Unlock()
			before := len(h.runner.Calls())
			secondTask, err := h.service.Sync(context.Background(), h.project.Name, "poll", Actor{Name: "scheduler"})
			if err != nil {
				t.Fatal(err)
			}
			want := task.StatusFailed
			if targeted {
				want = task.StatusSuccess
			}
			waitTask(t, h.db, secondTask.ID, want)
			assertCallSubsequence(t, h.runner.Calls()[before:], []string{"pull", "up", "pull", "up", "ps"})
			specs := h.runner.UpSpecs()
			if len(specs) != 3 || specs[1].ForceRecreate || !specs[2].ForceRecreate || specs[2].Files[0] != specs[0].Files[0] {
				t.Fatalf("automatic restore did not replace containers: %#v", specs)
			}
		})
	}
}
