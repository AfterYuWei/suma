package cd

import (
	"context"
	"errors"
	"fmt"
	"time"

	gitrepo "github.com/suma/suma/server/internal/git"
)

type ConnectionInput struct {
	Repository gitrepo.Repository `json:"repository"`
	NodeIDs    []string           `json:"node_ids"`
}

// TestConnection uses draft configuration without saving it or queuing a sync.
func (s *Service) TestConnection(ctx context.Context, name string, input ConnectionInput) (gitrepo.ConnectionResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := gitrepo.ValidateRemote(input.Repository); err != nil {
		return gitrepo.ConnectionResult{}, err
	}
	var projectID uint
	if name != "" {
		project, err := s.project(ctx, name)
		if err != nil {
			return gitrepo.ConnectionResult{}, errors.New("delivery project not found")
		}
		projectID = project.ID
		if input.NodeIDs == nil {
			input.NodeIDs, err = s.projectNodeIDs(ctx, projectID)
			if err != nil {
				return gitrepo.ConnectionResult{}, err
			}
		}
	}
	auth := input.Repository.Authentication
	material := gitrepo.CredentialMaterial{AuthType: gitrepo.AuthNone}
	var err error
	switch auth.Source {
	case gitrepo.CredentialSourceNone:
		if auth.CredentialID != nil || auth.Credential != nil || auth.Summary != nil || auth.SaveToCenter {
			return gitrepo.ConnectionResult{}, errors.New("no-authentication source cannot include a credential")
		}
	case gitrepo.CredentialSourceCenter:
		if auth.CredentialID == nil || *auth.CredentialID == 0 || auth.Credential != nil || auth.Summary != nil || auth.SaveToCenter {
			return gitrepo.ConnectionResult{}, errors.New("authentication center credential ID is required")
		}
		if err = s.credentials.AuthorizedForNodes(ctx, *auth.CredentialID, input.NodeIDs); err != nil {
			return gitrepo.ConnectionResult{}, err
		}
		material, err = s.credentials.Material(ctx, *auth.CredentialID)
	case gitrepo.CredentialSourceProject:
		if auth.CredentialID != nil || auth.Summary != nil {
			return gitrepo.ConnectionResult{}, errors.New("project authentication cannot reference an authentication center credential")
		}
		material, err = s.credentials.PreviewProjectMaterial(ctx, projectID, auth.Credential)
	default:
		return gitrepo.ConnectionResult{}, errors.New("authentication source must be none, center, or project")
	}
	if err != nil {
		return gitrepo.ConnectionResult{}, fmt.Errorf("load Git credential: %w", err)
	}
	if err := validateCredentialTransport(input.Repository.CloneURL, material.AuthType); err != nil {
		return gitrepo.ConnectionResult{}, err
	}
	if s.git == nil {
		return gitrepo.ConnectionResult{}, errors.New("Git client is unavailable")
	}
	return s.git.TestConnection(ctx, gitrepo.SyncRequest{Repository: input.Repository, Credential: material})
}
