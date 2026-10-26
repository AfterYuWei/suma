package cd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm/clause"
)

type deploymentRuntimeInspector interface {
	InspectComposeNamespace(context.Context, string, string) (compose.RuntimeProjectSnapshot, error)
}

// RuntimeProjectNames associates Docker inventory with SUMA-owned delivery
// metadata. Native names and all runtime/resource state still come from Docker.
func (s *Service) RuntimeProjectNames(ctx context.Context, nodeID string) (map[string]string, error) {
	var rows []database.DeliveryProject
	if err := s.db.WithContext(ctx).Where(`id IN (
		SELECT project_id FROM delivery_project_nodes WHERE node_id = ?
		UNION SELECT project_id FROM delivery_target_states WHERE node_id = ?
	) AND (
		COALESCE(NULLIF(deployment_name, ''), 'suma-cd-' || id::text) <> name
		OR id IN (SELECT project_id FROM delivery_target_states WHERE node_id = ?)
	)`, nodeID, nodeID, nodeID).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]string, len(rows))
	for _, row := range rows {
		name := runtimeName(row)
		if existing, ok := result[name]; ok && existing != row.Name {
			return nil, fmt.Errorf("multiple delivery projects use Compose project name %q", name)
		}
		result[name] = row.Name
	}
	return result, nil
}

// Preserve the saved namespace for existing deployments. New namespaces use
// the CD name and must not adopt another project's containers, networks or data.
func (s *Service) claimDeploymentNamespace(ctx context.Context, project database.DeliveryProject, nodeID string, spec compose.ExecutionSpec) error {
	if s.targets == nil || project.DeploymentName != project.Name {
		return nil
	}
	inspector, ok := s.targets.(deploymentRuntimeInspector)
	if !ok {
		return errors.New("node runtime cannot check Compose project name conflicts")
	}
	snapshot, err := inspector.InspectComposeNamespace(ctx, nodeID, spec.ProjectName)
	if err != nil {
		return fmt.Errorf("check Compose project name %q on node %s: %w", spec.ProjectName, nodeID, err)
	}
	var claimed int64
	if err := s.db.WithContext(ctx).Model(&database.DeliveryTargetState{}).Where("project_id = ? AND node_id = ?", project.ID, nodeID).Count(&claimed).Error; err != nil {
		return err
	}
	if claimed == 0 && len(snapshot.Containers)+len(snapshot.Networks)+len(snapshot.Volumes) > 0 {
		return fmt.Errorf("Compose project name %q is already in use on node %s; choose a different CD project name", spec.ProjectName, nodeID)
	}
	if len(snapshot.Containers) > 0 {
		var releases []database.DeliveryRelease
		if err := s.db.WithContext(ctx).Select("worktree_path", "compose_files_json").Where("project_id = ?", project.ID).Find(&releases).Error; err != nil {
			return err
		}
		allowed := map[string]bool{filepath.Clean(spec.ProjectDir): true}
		for _, release := range releases {
			var files []string
			if json.Unmarshal([]byte(release.ComposeFilesJSON), &files) == nil && len(files) > 0 {
				allowed[filepath.Clean(filepath.Join(release.WorktreePath, filepath.Dir(files[0])))] = true
			}
		}
		for _, container := range snapshot.Containers {
			directory := container.Labels[compose.WorkingDirLabel]
			if directory == "" || !allowed[filepath.Clean(directory)] {
				return fmt.Errorf("Compose project name %q contains containers from another working directory on node %s", spec.ProjectName, nodeID)
			}
		}
	}
	// A target-state row with no active Release is a namespace reservation,
	// not a claim of deployment success. It lets retries reuse resources left
	// by a partially failed first up without adopting pre-existing resources.
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&database.DeliveryTargetState{ProjectID: project.ID, NodeID: nodeID}).Error
}
