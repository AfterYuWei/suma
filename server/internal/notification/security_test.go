package notification

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
)

func TestLoginFailureThresholdSeparatesIdentityIPAndTimezones(t *testing.T) {
	s, db, _, now := fixture(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		db.Create(&database.LoginLog{IP: fmt.Sprint("other-", i), Success: false, CreatedAt: *now})
	}
	s.LoginFailed(ctx, "", "target")
	inbox, _ := s.Inbox(ctx, 1)
	if len(inbox.Items) != 0 {
		t.Fatal("unknown identities aggregated across IPs")
	}
	for i := 0; i < 4; i++ {
		db.Create(&database.LoginLog{Username: "name", IP: "target", Success: false, CreatedAt: *now})
	}
	db.Create(&database.LoginLog{Username: "name", IP: "target", Success: false, CreatedAt: now.Add(-time.Hour).In(time.FixedZone("UTC+8", 8*3600))})
	s.LoginFailed(ctx, "name", "target")
	inbox, _ = s.Inbox(ctx, 1)
	if len(inbox.Items) != 0 {
		t.Fatal("old local-time login failure counted")
	}
	db.Create(&database.LoginLog{Username: "name", IP: "another", Success: false, CreatedAt: *now})
	s.LoginFailed(ctx, "name", "another")
	inbox, _ = s.Inbox(ctx, 1)
	if len(inbox.Items) != 1 || inbox.Items[0].Type != "auth.login_failed" {
		t.Fatal("distributed same-identity failures missed")
	}
	s.LoginFailed(ctx, "name", "yet-another")
	inbox, _ = s.Inbox(ctx, 1)
	if len(inbox.Items) != 1 {
		t.Fatal("same identity alert repeated for another IP")
	}
}

func TestLoginFailureIPThresholdAcrossDifferentIdentities(t *testing.T) {
	s, db, _, now := fixture(t)
	for i := 0; i < 5; i++ {
		db.Create(&database.LoginLog{Username: fmt.Sprint(i), IP: "shared-IP", Success: false, CreatedAt: *now})
	}
	s.LoginFailed(context.Background(), "4", "shared-IP")
	inbox, _ := s.Inbox(context.Background(), 1)
	if len(inbox.Items) != 1 {
		t.Fatal("same-IP failures across different identities missed")
	}
}
