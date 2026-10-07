package notification

import (
	"context"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"time"
)

// Authentication failures contain no submitted credentials. Five failures from
// the same identity or IP within five minutes trigger a deduplicated alert.
func (s *Service) LoginFailed(ctx context.Context, identity, ip string) {
	query := s.db.WithContext(ctx).Model(&database.LoginLog{}).Where("success = ? AND created_at > ?", false, s.deps.Now().Add(-5*time.Minute))
	var byIdentity, byIP int64
	if identity != "" {
		query.WithContext(ctx).Where("username = ?", identity).Count(&byIdentity)
	}
	query.WithContext(ctx).Where("ip = ?", ip).Count(&byIP)
	key := "login-failed:ip:" + ip
	if byIdentity >= 5 {
		key = "login-failed:identity:" + hash(identity)
	}
	if byIdentity >= 5 || byIP >= 5 {
		s.Emit(event.Event{Type: "auth.login_failed", Severity: "critical", Title: "Repeated login failures", Message: "Repeated authentication failures from " + ip, DedupeKey: key})
	}
}
