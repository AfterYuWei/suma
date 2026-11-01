package projectcd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/task"
)

func (f *projectCDFixture) wait(t *testing.T, row database.Task, err error, status string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := f.tasks.Wait(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	finished, err := f.tasks.Get(ctx, row.ID)
	if err != nil || finished.Status != status {
		t.Fatalf("task status=%s message=%s error=%v", finished.Status, finished.Message, err)
	}
}

func (f *projectCDFixture) configuration(t *testing.T) Configuration {
	t.Helper()
	config, err := f.service.Get(context.Background(), "local", "app")
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func (f *projectCDFixture) upCalls(t *testing.T) int {
	t.Helper()
	raw, err := os.ReadFile(f.calls)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Count(string(raw), " up ")
}

func TestProjectCDDeliveryPoliciesAndFirstApplication(t *testing.T) {
	for _, mode := range []string{"observe", "manual", "auto"} {
		t.Run(mode, func(t *testing.T) {
			f := newProjectCDFixture(t)
			enabled := true
			input := f.input
			input.ReconcileMode, input.AutomationEnabled = mode, &enabled
			if _, err := f.service.Configure(context.Background(), "local", "app", input); err != nil {
				t.Fatal(err)
			}
			checked, err := f.service.Check(context.Background(), "local", "app", "poll", Actor{Name: "Scheduler"})
			f.wait(t, checked, err, task.StatusSuccess)
			config := f.configuration(t)
			if config.Candidate == nil || !config.InitialApplicationRequired || f.upCalls(t) != 0 {
				t.Fatal("first synchronization must only create a candidate")
			}
			applied, err := f.service.Apply(context.Background(), "local", "app", config.Candidate.ID, config.Revision, false, Actor{})
			if mode == "observe" {
				if err == nil || f.upCalls(t) != 0 {
					t.Fatal("observe mode allowed delivery")
				}
				return
			}
			f.wait(t, applied, err, task.StatusSuccess)
			f.git.commit = strings.Repeat("b", 40)
			checked, err = f.service.Check(context.Background(), "local", "app", "poll", Actor{})
			f.wait(t, checked, err, task.StatusSuccess)
			want := 1
			if mode == "auto" {
				want = 2
			}
			if f.upCalls(t) != want {
				t.Fatalf("mode %s: expected %d applications", mode, want)
			}
			checked, err = f.service.Check(context.Background(), "local", "app", "poll", Actor{})
			f.wait(t, checked, err, task.StatusSuccess)
			if f.upCalls(t) != want {
				t.Fatal("unchanged commit and configuration redeployed")
			}
			config = f.configuration(t)
			if _, err := f.service.SetAutomation(context.Background(), "local", "app", false, config.Revision); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.Check(context.Background(), "local", "app", "poll", Actor{}); err != ErrPaused {
				t.Fatal("paused polling was accepted", err)
			}
			checked, err = f.service.Check(context.Background(), "local", "app", "manual", Actor{})
			f.wait(t, checked, err, task.StatusSuccess)
			if f.upCalls(t) != want {
				t.Fatal("manual synchronization while paused deployed")
			}
		})
	}
}

func TestProjectCDAutomaticRollbackKeepsIndependentFailureEvidence(t *testing.T) {
	for _, recoveryFails := range []bool{false, true} {
		name := "restored"
		if recoveryFails {
			name = "recovery_failed"
		}
		t.Run(name, func(t *testing.T) {
			f := newProjectCDFixture(t)
			enabled := true
			input := f.input
			input.ReconcileMode, input.AutoRollback, input.AutomationEnabled = "auto", true, &enabled
			if _, err := f.service.Configure(context.Background(), "local", "app", input); err != nil {
				t.Fatal(err)
			}
			checked, err := f.service.Check(context.Background(), "local", "app", "manual", Actor{})
			f.wait(t, checked, err, task.StatusSuccess)
			config := f.configuration(t)
			applied, err := f.service.Apply(context.Background(), "local", "app", config.Candidate.ID, config.Revision, false, Actor{})
			f.wait(t, applied, err, task.StatusSuccess)
			previous := f.configuration(t).Applied.ID
			f.git.commit = strings.Repeat("b", 40)
			if recoveryFails {
				if err := os.WriteFile(filepath.Join(f.root, "up-fail"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				// Fail one application; the same runner must then pull and recreate
				// the previous configuration using its frozen execution parameters.
				script, err := os.ReadFile(filepath.Join(f.root, "compose-fixture"))
				if err != nil {
					t.Fatal(err)
				}
				script = []byte(strings.Replace(string(script), "then exit 1; fi", "then rm '"+filepath.Join(f.root, "up-fail")+"'; exit 1; fi", 1))
				if err := os.WriteFile(filepath.Join(f.root, "compose-fixture"), script, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(f.root, "up-fail"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			checked, err = f.service.Check(context.Background(), "local", "app", "poll", Actor{})
			f.wait(t, checked, err, task.StatusFailed)
			config = f.configuration(t)
			if config.ReconcileMode != "manual" || f.upCalls(t) != 3 {
				t.Fatal("recovery did not stop automatic publishing or attempt the previous configuration")
			}
			var failed, restore database.ProjectCDRecord
			if err := f.db.Where("project_cd_id = ? AND operation = ? AND status = ?", config.Candidate.ProjectCDID, "apply", "failed").First(&failed).Error; err != nil {
				t.Fatal("failed delivery evidence missing", err)
			}
			if err := f.db.Where("project_cd_id = ? AND operation = ?", failed.ProjectCDID, "auto_rollback").First(&restore).Error; err != nil {
				t.Fatal(err)
			}
			if restore.SourceRecordID == nil || *restore.SourceRecordID != failed.ID || restore.CommitSHA != strings.Repeat("a", 40) {
				t.Fatal("recovery did not retain the failed input and old snapshot identities")
			}
			want := "succeeded"
			if recoveryFails {
				want = "failed"
			}
			if restore.Status != want || recoveryFails && config.Applied.ID != previous || !recoveryFails && config.Applied.ID != restore.ID {
				t.Fatalf("recovery status=%s active=%d", restore.Status, config.Applied.ID)
			}
			raw, _ := os.ReadFile(f.calls)
			if !strings.Contains(string(raw), "--force-recreate") {
				t.Fatal("recovery did not force container replacement")
			}
			checked, err = f.service.Check(context.Background(), "local", "app", "poll", Actor{})
			f.wait(t, checked, err, task.StatusSuccess)
			if f.upCalls(t) != 3 {
				t.Fatal("failed snapshot was automatically applied again")
			}
		})
	}
}

func TestProjectCDSettingsCredentialCenterAndWebhookRotation(t *testing.T) {
	f := newProjectCDFixture(t)
	if err := f.db.Create(&database.Node{ID: "local", Name: "Local", Enabled: true, ConnectionType: "unix", Endpoint: "unix:///fixture.sock"}).Error; err != nil {
		t.Fatal(err)
	}
	input := f.input
	input.Repository.Authentication = gitrepo.Authentication{Source: gitrepo.CredentialSourceProject, SaveToCenter: true, Credential: &gitrepo.CredentialInput{Name: "Shared", AuthType: "http_token", Secret: "private-token", AuthorizedNodeIDs: []string{"foreign"}}}
	config, err := f.service.Configure(context.Background(), "local", "app", input)
	if err != nil || config.Repository.Authentication.Source != "center" {
		t.Fatal("project credential was not saved to the center", err)
	}
	id := *config.Repository.Authentication.CredentialID
	if err := f.service.credentials.AuthorizedForNodes(context.Background(), id, []string{"local"}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.credentials.AuthorizedForNodes(context.Background(), id, []string{"foreign"}); err == nil {
		t.Fatal("route's fixed node was overridden")
	}
	input.Repository = config.Repository
	input.ExpectedRevision = config.Revision
	input.WebhookSecret = "rotated-private-webhook-secret"
	updated, err := f.service.Configure(context.Background(), "local", "app", input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(updated)
	if strings.Contains(string(raw), input.WebhookSecret) || strings.Contains(string(raw), "private-token") {
		t.Fatal("settings leaked private material")
	}
	row, err := f.service.row(context.Background(), "local", "app")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := f.service.secrets.Decrypt(row.WebhookSecret)
	if err != nil || secret != input.WebhookSecret {
		t.Fatal("webhook secret was not rotated", err)
	}
}
