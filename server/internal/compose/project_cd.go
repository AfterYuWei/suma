package compose

import (
	"context"
	"errors"
	"time"

	"github.com/suma/suma/server/internal/database"
	projectdomain "github.com/suma/suma/server/internal/project"
	"gorm.io/gorm"
)

var ErrProjectCDManaged = errors.New("project uses CD deployment mode; use its CD tab")

// ReserveProject shares the ordinary Project's lock and task reservation. The
// release function is transferred to the background task only after submission.
func (s *Service) ReserveProject(name string) (func(), error) {
	unlock := s.lockProject(name)
	defer unlock()
	if s.operationActive(name) {
		return nil, ErrProjectBusy
	}
	key := s.operationKey(name)
	s.operations.Store(key, true)
	return func() { s.operations.Delete(key) }, nil
}

func (s *Service) projectCDOwned(name string) (bool, error) {
	if s.db == nil {
		return false, nil
	}
	var row database.ProjectCD
	err := s.db.Where("node_id = ? AND project_name = ? AND enabled = true", s.effectiveNodeID(), name).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Service) requireManual(name string) error {
	owned, err := s.projectCDOwned(name)
	if err != nil {
		return err
	}
	if owned {
		return ErrProjectCDManaged
	}
	return nil
}

func (s *Service) decorateDeployment(ctx context.Context, project *Project) error {
	owned, err := s.projectCDOwned(project.Name)
	if err != nil {
		return err
	}
	project.DeploymentMode = projectdomain.DeploymentManual
	if owned {
		project.DeploymentMode = projectdomain.DeploymentCD
		if s.projectCDView != nil {
			view, err := s.projectCDView(ctx, s.effectiveNodeID(), project.Name)
			if err != nil {
				// Preserve Docker inventory and keep CD settings reachable when a worktree
				// is missing. A nil declaration is the existing unavailable-data contract;
				// the source API returns the actionable error to the configuration page.
				project.Compose = ""
				project.DeclaredServiceNames = nil
			} else if view.Compose != "" {
				project.Compose = view.Compose
				project.DeclaredServiceNames = &view.DeclaredServiceNames
			}
		}

		project.Capabilities = []projectdomain.Capability{projectdomain.CapabilityView, projectdomain.CapabilityStart, projectdomain.CapabilityStop, projectdomain.CapabilityRestart, projectdomain.CapabilityServices, projectdomain.CapabilityLogs, projectdomain.CapabilitySync, projectdomain.CapabilityEditEnvironment, projectdomain.CapabilityRollback}
	}
	return nil
}

// PublishCDExport runs while the caller owns the shared reservation. A
// stopped/failed conversion leaves ownership enabled so ordinary writes fail.
func (s *Service) PublishCDExport(ctx context.Context, name, content, environment string, appliedAt *time.Time) error {
	if err := validateConfigurationIdentity(name, content, environment); err != nil {
		return err
	}
	p, err := s.managedProject(name)
	if err != nil {
		return err
	}
	if err := writeConfiguration(p.Path, content, environment); err != nil {
		return err
	}
	if appliedAt != nil {
		metadata, err := readManagedProjectMetadata(p.Path, s.effectiveNodeID(), name, p.UpdatedAt)
		if err != nil {
			return err
		}
		metadata.LastDeployedAt = appliedAt
		return writeManagedProjectMetadata(p.Path, metadata)
	}
	return nil
}

func (s *Service) SetProjectCDAction(action func(context.Context, string, string, string) (database.Task, error)) {
	s.projectCDAction = action
}

type ProjectDeployment struct {
	Compose              string
	DeclaredServiceNames []string
}

func (s *Service) SetProjectCDView(view func(context.Context, string, string) (ProjectDeployment, error)) {
	s.projectCDView = view
}
