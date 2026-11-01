package projectcd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
	gitrepo "github.com/suma/suma/server/internal/git"
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
		}
		return tx.Model(&database.ProjectCDRecord{}).Where("status = ?", "running").Updates(map[string]any{"status": "interrupted", "failure_reason": "SUMA restarted before the operation completed; inspect Docker before retrying", "finished_at": time.Now().UTC()}).Error
	})
}
func (s *Service) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			if err := s.reconcile(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("project CD scheduling failed; check database availability")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (s *Service) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		s.wg.Wait()
	}
}
func (s *Service) reconcile(ctx context.Context) error {
	var rows []database.ProjectCD
	if err := s.db.WithContext(ctx).Where("enabled = true AND automation_enabled = true AND initial_application_required = false").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if row.LastCheckAt != nil && time.Since(*row.LastCheckAt) < time.Duration(row.SyncIntervalSeconds)*time.Second {
			continue
		}
		if _, err := s.Check(ctx, row.NodeID, row.ProjectName, "poll", Actor{Name: "SUMA project CD"}); err != nil && !errors.Is(err, ErrPaused) {
			slog.Warn("project CD check could not be queued", "project", row.ProjectName)
		}
	}
	return nil
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
	if !row.AutomationEnabled || row.InitialApplicationRequired {
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
	task, queueErr := s.Check(ctx, row.NodeID, row.ProjectName, "webhook", Actor{Name: "Git webhook"})
	status := "queued"
	if queueErr != nil {
		status = "failed"
	}
	if err := s.db.WithContext(ctx).Model(&delivery).Updates(map[string]any{"status": status, "processed_at": time.Now().UTC()}).Error; err != nil {
		return false, errors.Join(queueErr, err)
	}
	_ = task
	return false, queueErr
}
