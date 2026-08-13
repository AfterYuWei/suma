package cd

import (
	"context"
	"fmt"
	"time"

	"github.com/dockport/dockport/server/internal/database"
)

func (s *Service) Recover(ctx context.Context) error {
	finished := time.Now()
	interrupted := []string{StatusPulling, StatusDeploying, StatusVerifying, StatusRollingBack, StatusValidating}
	if err := s.db.WithContext(ctx).Model(&database.DeliveryRelease{}).Where("status IN ?", interrupted).Updates(map[string]any{
		"status": StatusFailed, "failure_reason": "DockPort restarted before the release completed", "finished_at": finished,
	}).Error; err != nil {
		return fmt.Errorf("recover interrupted releases: %w", err)
	}
	if err := s.tasks.RecoverInterrupted(ctx); err != nil {
		return fmt.Errorf("recover interrupted tasks: %w", err)
	}
	return nil
}

func (s *Service) Start() {
	s.backgroundMu.Lock()
	if s.backgroundCancel != nil {
		s.backgroundMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.backgroundCancel = cancel
	s.backgroundWG.Add(1)
	s.backgroundMu.Unlock()
	go func() {
		defer s.backgroundWG.Done()
		s.reconcileDue(ctx)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reconcileDue(ctx)
			}
		}
	}()
}

func (s *Service) Stop() {
	s.backgroundMu.Lock()
	cancel := s.backgroundCancel
	s.backgroundCancel = nil
	s.backgroundMu.Unlock()
	if cancel != nil {
		cancel()
		s.backgroundWG.Wait()
	}
}

func (s *Service) reconcileDue(ctx context.Context) {
	var projects []database.DeliveryProject
	if err := s.db.WithContext(ctx).Where("git_clone_url <> ''").Find(&projects).Error; err != nil {
		return
	}
	now := time.Now()
	for _, project := range projects {
		if ctx.Err() != nil {
			return
		}
		interval := time.Duration(project.SyncIntervalSeconds) * time.Second
		if interval < 30*time.Second {
			interval = 5 * time.Minute
		}
		if project.LastSyncAt != nil && now.Sub(*project.LastSyncAt) < interval {
			continue
		}
		_, _ = s.Sync(ctx, project.Name, "poll", Actor{Name: "DockPort reconciler"})
	}
}
