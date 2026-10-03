package cd

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
)

type reviewedFakeRunner struct {
	*fakeComposeRunner
	fail    error
	runtime string
}

func (r *reviewedFakeRunner) Review(context.Context, compose.ExecutionSpec, compose.ImageResolver) (compose.ReviewedConfig, error) {
	r.record("review")
	return compose.ReviewedConfig{TargetHash: r.runtime, ConfigHash: "frozen-config", Images: map[string]string{"web": "sha256:" + strings.Repeat("a", 64)}}, nil
}
func (r *reviewedFakeRunner) ApplyReviewed(context.Context, compose.ExecutionSpec, compose.ReviewedConfig, int, io.Writer) error {
	r.record("reviewed_apply")
	return r.fail
}

func TestReviewedCDNeverPullsBuildsOrAutomaticallyRollsBack(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "changed_runtime"}[changed], func(t *testing.T) {
			h := newCDHarness(t, ModeManual)
			h.db.Model(&database.DeliveryProject{}).Where("id = ?", h.project.ID).Update("auto_rollback", true)
			release := database.DeliveryRelease{ProjectID: h.project.ID, Status: StatusAwaitingApproval, CommitSHA: strings.Repeat("a", 40), ConfigHash: "frozen-config", WorktreePath: makeWorktree(t, "example/web:1"), ComposeFilesJSON: `["compose.yml"]`}
			if err := h.db.Create(&release).Error; err != nil {
				t.Fatal(err)
			}
			deployment := database.DeliveryReleaseDeployment{ReleaseID: release.ID, NodeID: "local", Status: StatusAwaitingApproval}
			if err := h.db.Create(&deployment).Error; err != nil {
				t.Fatal(err)
			}
			runner := &reviewedFakeRunner{fakeComposeRunner: h.runner, fail: errors.New("reviewed deployment failed"), runtime: "original-runtime"}
			h.service.compose = &targetedFakeRunner{fakeComposeRunner: h.runner, runners: map[string]compose.Runner{"local": runner}}
			h.service.SetTargetResolver(fakeTargetResolver{})
			resolve := func(context.Context, string) (string, error) { return "sha256:" + strings.Repeat("a", 64), nil }
			review, err := h.service.ReviewRelease(context.Background(), "local", release.ID, false, resolve)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				runner.runtime = "replacement-runtime"
			}
			if err := h.service.ApplyReleaseReviewed(context.Background(), review, resolve, "approved-task", func(int, string) {}); err == nil {
				t.Fatal("unsafe/failing release reported success")
			}
			applied := false
			for _, call := range runner.Calls() {
				if call == "pull" || call == "up" || call == "down" || call == "build" {
					t.Fatal("implicit CD action executed", call)
				}
				if call == "reviewed_apply" {
					applied = true
				}
			}
			if applied == changed {
				t.Fatal("runtime change bypassed review or approved execution did not run")
			}
		})
	}
}
