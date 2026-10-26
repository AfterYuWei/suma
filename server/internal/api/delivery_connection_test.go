package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/cd"
	"github.com/suma/suma/server/internal/database"
	gitrepo "github.com/suma/suma/server/internal/git"
)

type connectionGit struct{ requests []gitrepo.SyncRequest }

func (g *connectionGit) TestConnection(_ context.Context, input gitrepo.SyncRequest) (gitrepo.ConnectionResult, error) {
	g.requests = append(g.requests, input)
	return gitrepo.ConnectionResult{RefChecked: true, CommitSHA: strings.Repeat("a", 40)}, nil
}
func (*connectionGit) Sync(context.Context, gitrepo.SyncRequest, io.Writer) (gitrepo.Revision, error) {
	panic("connection test must not sync")
}
func (*connectionGit) Verify(context.Context, string, string) error {
	panic("connection test must not verify a worktree")
}
func (*connectionGit) Cleanup(uint) error { panic("connection test must not remove worktrees") }

func TestDeliveryConnectionHTTPUsesDraftWithoutSavingOrDeploying(t *testing.T) {
	git := &connectionGit{}
	h := newDeliveryCreationHarness(t, git)
	repository := gitrepo.Repository{CloneURL: "https://git.example.test/team/repo", RefType: gitrepo.RefBranch, Ref: "main", Authentication: gitrepo.Authentication{Source: gitrepo.CredentialSourceProject, Credential: &gitrepo.CredentialInput{Name: "draft", AuthType: gitrepo.AuthHTTPToken, Secret: "draft-test-token"}, SaveToCenter: true}}
	request := func(path string, repository gitrepo.Repository) int {
		body, _ := json.Marshal(cd.ConnectionInput{Repository: repository, NodeIDs: []string{"local"}})
		response := h.request(http.MethodPost, path, body)
		if strings.Contains(response.Body.String(), "draft-test-token") || strings.Contains(response.Body.String(), "test-only-private-token") {
			t.Fatal("connection response exposed credential")
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("connection response can be cached")
		}
		return response.Code
	}
	if code := request("/api/v1/delivery-projects/test-connection", repository); code != http.StatusOK {
		t.Fatalf("draft status %d", code)
	}
	if len(git.requests) != 1 || git.requests[0].Credential.Secret != "draft-test-token" {
		t.Fatal("draft credential was not used")
	}
	for _, model := range []any{&database.DeliveryProject{}, &database.GitCredential{}, &database.DeliveryProjectGitCredential{}, &database.Task{}, &database.DeliveryRelease{}} {
		var count int64
		if err := h.db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("test persisted %T: %d, %v", model, count, err)
		}
	}
	if response := h.request(http.MethodPost, "/api/v1/delivery-projects", deliveryCreationBody("saved")); response.Code != http.StatusCreated {
		t.Fatalf("create: %d", response.Code)
	}
	repository.Authentication = gitrepo.Authentication{Source: gitrepo.CredentialSourceProject}
	if code := request("/api/v1/delivery-projects/saved/test-connection", repository); code != http.StatusOK {
		t.Fatalf("stored status %d", code)
	}
	if git.requests[len(git.requests)-1].Credential.Secret != "test-only-private-token" {
		t.Fatal("stored project secret was not retained")
	}
	repository.Authentication.Credential = &gitrepo.CredentialInput{Name: "edited", AuthType: gitrepo.AuthHTTPToken, Username: "repository-user"}
	if code := request("/api/v1/delivery-projects/saved/test-connection", repository); code != http.StatusOK {
		t.Fatalf("masked draft status %d", code)
	}
	if got := git.requests[len(git.requests)-1].Credential; got.Username != "repository-user" || got.Secret != "test-only-private-token" {
		t.Fatal("masked secret was not merged with draft username")
	}
	repository.Authentication.Credential.Secret = "draft-test-token"
	if code := request("/api/v1/delivery-projects/saved/test-connection", repository); code != http.StatusOK {
		t.Fatalf("new secret status %d", code)
	}
	var stored database.DeliveryProjectGitCredential
	if err := h.db.First(&stored).Error; err != nil || stored.Name != "private-git" || stored.Username != "" {
		t.Fatal("test changed stored credential metadata")
	}
	repository.Authentication.Credential = nil
	if code := request("/api/v1/delivery-projects/saved/test-connection", repository); code != http.StatusOK || git.requests[len(git.requests)-1].Credential.Secret != "test-only-private-token" {
		t.Fatal("test changed stored secret")
	}
	for _, model := range []any{&database.Task{}, &database.DeliveryRelease{}} {
		var count int64
		if err := h.db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("test created a task or release")
		}
	}
	before := len(git.requests)
	if code := request("/api/v1/delivery-projects/absent/test-connection", repository); code != http.StatusUnprocessableEntity {
		t.Fatalf("missing project status %d", code)
	}
	repository.CloneURL = "http://public.example/repo"
	if code := request("/api/v1/delivery-projects/test-connection", repository); code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid transport status %d", code)
	}
	if len(git.requests) != before {
		t.Fatal("invalid input reached Git")
	}
}

func TestDeliveryConnectionHTTPRequiresSessionAndCenterGrants(t *testing.T) {
	git := &connectionGit{}
	h := newDeliveryCreationHarness(t, git)
	var creation struct {
		Configuration cd.ConfigureInput `json:"configuration"`
	}
	_ = json.Unmarshal(deliveryCreationBody("shared"), &creation)
	creation.Configuration.Repository.Authentication.SaveToCenter = true
	body, _ := json.Marshal(map[string]any{"name": "shared", "configuration": creation.Configuration})
	if response := h.request(http.MethodPost, "/api/v1/delivery-projects", body); response.Code != http.StatusCreated {
		t.Fatalf("create: %d", response.Code)
	}
	var credential database.GitCredential
	if err := h.db.First(&credential).Error; err != nil {
		t.Fatal(err)
	}
	repository := creation.Configuration.Repository
	repository.Authentication = gitrepo.Authentication{Source: gitrepo.CredentialSourceCenter, CredentialID: &credential.ID}
	body, _ = json.Marshal(cd.ConnectionInput{Repository: repository, NodeIDs: []string{"local"}})
	if response := h.request(http.MethodPost, "/api/v1/delivery-projects/shared/test-connection", body); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unauthorized credential: %d", response.Code)
	}
	if len(git.requests) != 0 {
		t.Fatal("unauthorized credential reached Git")
	}
	if err := h.db.Create(&database.GitCredentialNode{CredentialID: credential.ID, NodeID: "local"}).Error; err != nil {
		t.Fatal(err)
	}
	if response := h.request(http.MethodPost, "/api/v1/delivery-projects/shared/test-connection", body); response.Code != http.StatusOK {
		t.Fatalf("authorized credential: %d", response.Code)
	}
	for name, cookie := range map[string]*http.Cookie{
		"missing": nil,
		"invalid": {Name: sessionCookie, Value: "invalid-session"},
	} {
		t.Run(name, func(t *testing.T) {
			unauthenticated := h
			unauthenticated.cookie = cookie
			for _, path := range []string{"/api/v1/delivery-projects/test-connection", "/api/v1/delivery-projects/shared/test-connection"} {
				if response := unauthenticated.request(http.MethodPost, path, body); response.Code != http.StatusUnauthorized {
					t.Fatalf("%s unauthenticated status %d", path, response.Code)
				}
			}
			if len(git.requests) != 1 {
				t.Fatal("unauthenticated request reached Git")
			}
		})
	}
}
