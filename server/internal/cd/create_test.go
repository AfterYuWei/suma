package cd

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
	"gorm.io/gorm"
)

func creationInput() ConfigureInput {
	return ConfigureInput{
		Repository:    gitrepo.Repository{CloneURL: "https://git.example.test/team/deploy.git", RefType: gitrepo.RefBranch, Ref: "main", Authentication: gitrepo.Authentication{Source: gitrepo.CredentialSourceNone}, ComposeFiles: []string{"compose.yml", "deploy/production.yml"}, EnvironmentFile: "env/production.env"},
		ReconcileMode: ModeManual, SyncIntervalSeconds: 300, DeploymentTimeout: 120,
		AutoRollback: true, WebhookEnabled: true, NodeIDs: []string{"local"}, RegistryCredentialIDs: []uint{},
	}
}

func TestCreateConfiguredProjectSavesCompleteConfiguration(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	sql, _ := h.db.DB()
	sql.SetMaxOpenConns(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := creationInput()
	project, configuration, err := h.service.CreateConfiguredProject(ctx, "configured-new", input)
	if err != nil {
		t.Fatal(err)
	}
	if !project.Configured || project.Name != "configured-new" || !reflect.DeepEqual(project.NodeIDs, input.NodeIDs) || !reflect.DeepEqual(configuration.Repository.ComposeFiles, input.Repository.ComposeFiles) || configuration.Repository.EnvironmentFile != input.Repository.EnvironmentFile || !configuration.AutoRollback {
		t.Fatal("full configuration was not saved")
	}
	if len(configuration.WebhookSecret) != 64 || configuration.WebhookID == "" {
		t.Fatal("new webhook secret was not returned once")
	}
	loaded, err := h.service.GetConfiguration(ctx, project.Name)
	if err != nil || loaded.WebhookSecret != "" {
		t.Fatal("stored webhook secret was returned by GET")
	}
	var row database.DeliveryProject
	if err := h.db.First(&row, project.ID).Error; err != nil || row.DeploymentName != deliveryRuntimeName(project.ID) {
		t.Fatal("missing stable Docker deployment identity")
	}
	var tasks, releases int64
	h.db.Model(&database.Task{}).Count(&tasks)
	h.db.Model(&database.DeliveryRelease{}).Count(&releases)
	if tasks != 0 || releases != 0 {
		t.Fatal("saving configuration unexpectedly started deployment")
	}
}

func TestCreateConfiguredProjectValidationLeavesNoProject(t *testing.T) {
	for _, name := range []string{"repository", "path", "nodes", "policy", "interval", "credential"} {
		t.Run(name, func(t *testing.T) {
			h := newCDHarness(t, ModeManual)
			input := creationInput()
			switch name {
			case "repository":
				input.Repository.CloneURL = "https://user:password@git.example.test/deploy.git"
			case "path":
				input.Repository.ComposeFiles = []string{"../outside.yml"}
			case "nodes":
				input.NodeIDs = []string{}
			case "policy":
				input.ReconcileMode = "invalid"
			case "interval":
				input.SyncIntervalSeconds = 1
			case "credential":
				input.Repository.Authentication = gitrepo.Authentication{Source: gitrepo.CredentialSourceProject, Credential: &gitrepo.CredentialInput{Name: "invalid", AuthType: gitrepo.AuthHTTPToken}}
			}
			if _, _, err := h.service.CreateConfiguredProject(context.Background(), "invalid-new", input); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
			var count int64
			h.db.Model(&database.DeliveryProject{}).Where("name = ?", "invalid-new").Count(&count)
			if count != 0 {
				t.Fatal("failed creation left an empty project")
			}
			h.db.Model(&database.DeliveryProjectNode{}).Count(&count)
			if count != 0 {
				t.Fatal("failed creation left target records")
			}
		})
	}
}

func TestCreateConfiguredProjectRejectsUnauthorizedCredential(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	credential, err := h.service.credentials.Create(context.Background(), gitrepo.CredentialInput{Name: "ungranted", AuthType: gitrepo.AuthHTTPToken, Secret: "test-only-token"})
	if err != nil {
		t.Fatal(err)
	}
	input := creationInput()
	input.Repository.Authentication = gitrepo.Authentication{Source: gitrepo.CredentialSourceCenter, CredentialID: &credential.ID}
	if _, _, err := h.service.CreateConfiguredProject(context.Background(), "ungranted-new", input); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatal("creation bypassed credential node authorization")
	}
	if _, err := h.service.GetProject(context.Background(), "ungranted-new"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("unauthorized creation left a project")
	}
}

func TestCreateConfiguredProjectRollsBackCredentialAndGrants(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	if err := h.db.Create(&database.Node{ID: "local", Name: "Local", Enabled: true, Endpoint: "unix:///var/run/docker.sock"}).Error; err != nil {
		t.Fatal(err)
	}
	input := creationInput()
	input.Repository.Authentication = gitrepo.Authentication{Source: gitrepo.CredentialSourceProject, SaveToCenter: true, Credential: &gitrepo.CredentialInput{Name: "atomic-credential", AuthType: gitrepo.AuthHTTPToken, Secret: "test-only-token", AuthorizedNodeIDs: []string{"local"}}}
	h.db.Callback().Update().Before("gorm:update").Register("reject_new_configuration", func(tx *gorm.DB) {
		if values, ok := tx.Statement.Dest.(map[string]any); ok && values["git_clone_url"] != nil {
			tx.AddError(errors.New("injected configuration write failure"))
		}
	})
	if _, _, err := h.service.CreateConfiguredProject(context.Background(), "atomic-new", input); err == nil {
		t.Fatal("injected failure was ignored")
	}
	for _, model := range []any{&database.GitCredential{}, &database.GitCredentialNode{}, &database.DeliveryProjectNode{}, &database.DeliveryProjectGitCredential{}} {
		var count int64
		if err := h.db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("failed creation left credential or target records")
		}
	}
	if _, err := h.service.GetProject(context.Background(), "atomic-new"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("failed creation left a project")
	}
}
