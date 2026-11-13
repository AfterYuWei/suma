package projectcd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
	gitrepo "github.com/suma/suma/server/internal/git"
	"github.com/suma/suma/server/internal/taskcenter"
	"gorm.io/gorm"
)

func (s *Service) Recover(ctx context.Context) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []uint
		if err := tx.Model(&database.ProjectCDRecord{}).Where("status = ?", "running").Distinct().Pluck("project_cd_id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := tx.Model(&database.ProjectCD{}).Where("id IN ?", ids).Update("automation_enabled", false).Error; err != nil {
				return err
			}
			if s.automation != nil {
				var rows []database.ProjectCD
				if err := tx.Where("id IN ?", ids).Find(&rows).Error; err != nil {
					return err
				}
				for _, row := range rows {
					if err := s.syncAutomation(tx, row); err != nil {
						return err
					}
				}
			}
		}
		return tx.Model(&database.ProjectCDRecord{}).Where("status = ?", "running").Updates(map[string]any{"status": "interrupted", "failure_reason": "SUMA restarted before the operation completed; inspect Docker before retrying", "finished_at": time.Now().UTC()}).Error
	})
}
func (s *Service) Webhook(ctx context.Context, hook string, headers http.Header, body []byte) (bool, error) {
	if len(body) == 0 || len(body) > 2<<20 {
		return false, errors.New("invalid webhook payload size")
	}
	var row database.ProjectCD
	if err := s.db.WithContext(ctx).Where("webhook_id = ? AND enabled = true AND webhook_enabled = true", hook).First(&row).Error; err != nil {
		return false, err
	}
	secret, err := s.secrets.Decrypt(row.WebhookSecret)
	if err != nil {
		return false, err
	}
	format := deployment.WebhookFormat(headers)
	if err := deployment.VerifyWebhook(format, secret, headers, body); err != nil {
		return false, err
	}
	if !row.AutomationEnabled && s.automation == nil {
		return true, nil
	}
	event, err := deployment.ParseWebhook(format, headers, body)
	if errors.Is(err, deployment.ErrWebhookIgnored) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var repo gitrepo.Repository
	if err := json.Unmarshal([]byte(row.RepositoryJSON), &repo); err != nil {
		return false, err
	}
	configured, err := gitrepo.CanonicalRepository(repo.CloneURL)
	if err != nil {
		return false, err
	}
	received, err := gitrepo.CanonicalRepository(event.Repository)
	if err != nil {
		return false, err
	}
	prefix := "refs/heads/"
	if repo.RefType == gitrepo.RefTag {
		prefix = "refs/tags/"
	}
	if configured != received || repo.RefType != gitrepo.RefCommit && event.Ref != "" && event.Ref != prefix+repo.Ref {
		return true, nil
	}
	id := event.DeliveryID
	if id == "" {
		sum := sha256.Sum256(body)
		id = hex.EncodeToString(sum[:])
	}
	delivery := database.GitWebhookDelivery{Format: format, HookID: hook, DeliveryID: id, Event: event.Event, Repository: received, GitRef: event.Ref, CommitSHA: event.Commit, Status: "accepted"}
	if err := s.db.WithContext(ctx).Create(&delivery).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return true, nil
		}
		return false, err
	}
	task, queueErr := s.Check(ctx, row.NodeID, row.ProjectName, "webhook", Actor{Name: "Git webhook", SourceID: id})
	status := "queued"
	if queueErr != nil {
		status = "failed"
		if errors.Is(queueErr, taskcenter.ErrPaused) || errors.Is(queueErr, taskcenter.ErrBusy) {
			status = "skipped"
		}
	}
	if err := s.db.WithContext(ctx).Model(&delivery).Updates(map[string]any{"status": status, "processed_at": time.Now().UTC()}).Error; err != nil {
		return false, errors.Join(queueErr, err)
	}
	_ = task
	if errors.Is(queueErr, taskcenter.ErrPaused) || errors.Is(queueErr, taskcenter.ErrBusy) {
		return true, nil
	}
	return false, queueErr
}
