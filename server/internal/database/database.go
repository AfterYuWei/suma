package database

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func Open(path string) (*gorm.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.AutoMigrate(&User{}, &Session{}, &Setting{}, &Node{}, &DockerTLSCredential{}, &DockerTLSCredentialNode{}, &GitCredentialNode{}, &RegistryCredentialNode{}, &DeliveryProject{}, &DeliveryProjectNode{}, &DeliveryProjectRegistryCredential{}, &DeliveryTargetState{}, &GitCredential{}, &DeliveryProjectGitCredential{}, &RegistryCredential{}, &DeliveryRelease{}, &DeliveryReleaseDeployment{}, &DeliveryDeploymentAttempt{}, &GitWebhookDelivery{}, &Task{}, &TaskLog{}, &TaskStep{}, &AuditLog{}, &LoginLog{}); err != nil {
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}
	if err := migrateNodeScopesAndDeliveryHistory(db); err != nil {
		return nil, fmt.Errorf("backfill scoped history: %w", err)
	}
	return db, nil
}

func migrateNodeScopesAndDeliveryHistory(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Task{}).Where("type IN ?", []string{"cd.sync", "cd.deploy", "cd.rollback"}).Updates(map[string]any{"scope": "control_plane", "node_id": "", "node_name": ""}).Error; err != nil {
			return err
		}
		controlActions := []string{"login", "account.", "settings.", "node.", "git.", "registry.", "docker_tls.", "cd."}
		for _, prefix := range controlActions {
			query := "action = ?"
			value := prefix
			if strings.HasSuffix(prefix, ".") {
				query, value = "action LIKE ?", prefix+"%"
			}
			if err := tx.Model(&AuditLog{}).Where(query, value).Updates(map[string]any{"scope": "control_plane", "node_id": "", "node_name": ""}).Error; err != nil {
				return err
			}
		}
		var deployments []DeliveryReleaseDeployment
		if err := tx.Find(&deployments).Error; err != nil {
			return err
		}
		for _, deployment := range deployments {
			var count int64
			if err := tx.Model(&DeliveryDeploymentAttempt{}).Where("deployment_id = ?", deployment.ID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				attempt := DeliveryDeploymentAttempt{DeploymentID: deployment.ID, Operation: "deploy", TargetReleaseID: deployment.ReleaseID, TaskID: deployment.TaskID, Status: deployment.Status, FailureReason: deployment.FailureReason, HealthSummary: deployment.HealthSummary, StartedAt: deployment.StartedAt, FinishedAt: deployment.FinishedAt, CreatedAt: deployment.CreatedAt, UpdatedAt: deployment.UpdatedAt}
				if attempt.CreatedAt.IsZero() {
					attempt.CreatedAt = time.Now()
				}
				if err := tx.Create(&attempt).Error; err != nil {
					return err
				}
			}
		}
		if err := backfillDeliveryTargetStates(tx); err != nil {
			return err
		}
		return nil
	})
}

func backfillDeliveryTargetStates(tx *gorm.DB) error {
	var projects []DeliveryProject
	if err := tx.Find(&projects).Error; err != nil {
		return err
	}
	for _, project := range projects {
		var targets []DeliveryProjectNode
		if err := tx.Where("project_id = ?", project.ID).Find(&targets).Error; err != nil {
			return err
		}
		for _, target := range targets {
			var count int64
			if err := tx.Model(&DeliveryTargetState{}).Where("project_id = ? AND node_id = ?", project.ID, target.NodeID).Count(&count).Error; err != nil || count > 0 {
				if err != nil {
					return err
				}
				continue
			}
			var deployment DeliveryReleaseDeployment
			err := tx.Table("delivery_release_deployments AS d").Select("d.*").Joins("JOIN delivery_releases r ON r.id = d.release_id").Where("r.project_id = ? AND d.node_id = ? AND d.status IN ?", project.ID, target.NodeID, []string{"succeeded", "rolled_back"}).Order("d.updated_at DESC, d.id DESC").First(&deployment).Error
			if err == gorm.ErrRecordNotFound {
				continue
			}
			if err != nil {
				return err
			}
			activeID := deployment.ReleaseID
			if deployment.Status == "rolled_back" && deployment.PreviousReleaseID != nil {
				activeID = *deployment.PreviousReleaseID
			}
			var release DeliveryRelease
			if err := tx.First(&release, activeID).Error; err != nil {
				continue
			}
			state := DeliveryTargetState{ProjectID: project.ID, NodeID: target.NodeID, ActiveReleaseID: &activeID, ObservedCommit: release.CommitSHA, HealthSummary: deployment.HealthSummary}
			if err := tx.Create(&state).Error; err != nil {
				return err
			}
		}
		if len(targets) > 0 {
			var states []DeliveryTargetState
			if err := tx.Where("project_id = ?", project.ID).Find(&states).Error; err != nil {
				return err
			}
			var activeID *uint
			uniform := len(states) == len(targets)
			for _, state := range states {
				if state.ActiveReleaseID == nil {
					uniform = false
					continue
				}
				if activeID == nil {
					id := *state.ActiveReleaseID
					activeID = &id
				} else if *activeID != *state.ActiveReleaseID {
					uniform = false
				}
			}
			values := map[string]any{"active_release_id": nil, "active_commit": ""}
			if uniform && activeID != nil {
				var release DeliveryRelease
				if err := tx.First(&release, *activeID).Error; err == nil {
					values["active_release_id"] = *activeID
					values["active_commit"] = release.CommitSHA
				}
			}
			if err := tx.Model(&DeliveryProject{}).Where("id = ?", project.ID).Updates(values).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
