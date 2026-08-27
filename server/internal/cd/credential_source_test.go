package cd

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

func TestProjectCredentialCanMoveToAuthenticationCenter(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	credentials := gitrepo.NewCredentialService(db, store)
	service := NewService(db, nil, credentials, nil, task.NewService(db), nil, store)
	project := database.DeliveryProject{Name: "production"}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	input := credentialConfigureInput(gitrepo.Authentication{Source: gitrepo.CredentialSourceProject, Credential: &gitrepo.CredentialInput{Name: "project-only", AuthType: gitrepo.AuthHTTPToken, Secret: "project-secret"}})
	configured, err := service.Configure(context.Background(), project.Name, input)
	if err != nil {
		t.Fatal(err)
	}
	if configured.Repository.Authentication.Source != gitrepo.CredentialSourceProject || configured.Repository.Authentication.Summary == nil || configured.Repository.Authentication.Credential != nil {
		t.Fatalf("unexpected project authentication response: %#v", configured.Repository.Authentication)
	}
	material, err := credentials.ProjectMaterial(context.Background(), project.ID)
	if err != nil || material.Secret != "project-secret" {
		t.Fatalf("project credential material = %#v, err=%v", material, err)
	}

	input.Repository.Authentication = gitrepo.Authentication{Source: gitrepo.CredentialSourceProject, Credential: &gitrepo.CredentialInput{Name: "shared", AuthType: gitrepo.AuthHTTPToken, Secret: "shared-secret"}, SaveToCenter: true}
	configured, err = service.Configure(context.Background(), project.Name, input)
	if err != nil {
		t.Fatal(err)
	}
	if configured.Repository.Authentication.Source != gitrepo.CredentialSourceCenter || configured.Repository.Authentication.CredentialID == nil {
		t.Fatalf("credential was not moved to center: %#v", configured.Repository.Authentication)
	}
	if _, err := credentials.ProjectSummary(context.Background(), project.ID); err == nil {
		t.Fatal("project credential remained after moving to center")
	}
	rows, err := credentials.List(context.Background())
	if err != nil || len(rows) != 1 || rows[0].Name != "shared" || rows[0].UsedBy != 1 {
		t.Fatalf("center credentials = %#v, err=%v", rows, err)
	}

	input.Repository.Authentication = gitrepo.Authentication{Source: gitrepo.CredentialSourceNone}
	if _, err := service.Configure(context.Background(), project.Name, input); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Delete(context.Background(), rows[0].ID); err != nil {
		t.Fatalf("detached center credential could not be deleted: %v", err)
	}
}

func credentialConfigureInput(authentication gitrepo.Authentication) ConfigureInput {
	return ConfigureInput{Repository: gitrepo.Repository{CloneURL: "https://git.example.test/team/deploy.git", RefType: gitrepo.RefBranch, Ref: "main", Authentication: authentication, ComposeFiles: []string{"compose.yml"}}, ReconcileMode: ModeManual, SyncIntervalSeconds: 300, DeploymentTimeout: 120}
}
