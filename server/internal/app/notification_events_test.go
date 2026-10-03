package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/notification"
	"github.com/suma/suma/server/internal/secret"
)

func TestExpectedRebuildDoesNotHideUnrelatedContainerFailure(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Create(&database.Task{ID: "update", NodeID: "local", Type: "compose.update", Name: "Update shop", Status: "running"})
	if !expectedTaskEvent(db, event.Event{NodeID: "local", ResourceID: "shop-container", Project: "shop"}) {
		t.Fatal("expected rebuild not correlated")
	}
	if expectedTaskEvent(db, event.Event{NodeID: "local", ResourceID: "database-container", Project: "database"}) {
		t.Fatal("unrelated failure suppressed")
	}
}

func TestOnlyCompleteLoginEmitsNotificationAndNewIPOnce(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "key"))
	if err != nil {
		t.Fatal(err)
	}
	notify := notification.NewService(db, store, notification.Dependencies{})
	sink := auditNotifications(db, notify)
	user := uint(1)
	for _, row := range []database.AuditLog{{Action: "login", Result: "requires_two_factor", UserID: &user, IP: "test-IP"}, {Action: "login", Result: "failed", UserID: &user, IP: "test-IP"}} {
		db.Create(&row)
		sink(row)
	}
	inbox, _ := notify.Inbox(context.Background(), user)
	if len(inbox.Items) != 0 {
		t.Fatal("partial authentication notified")
	}
	for i := 0; i < 2; i++ {
		row := database.AuditLog{Action: "login", Result: "success", UserID: &user, IP: "test-IP", CreatedAt: time.Now()}
		db.Create(&row)
		sink(row)
	}
	inbox, _ = notify.Inbox(context.Background(), user)
	counts := map[string]int{}
	for _, e := range inbox.Items {
		counts[e.Type]++
	}
	if counts["auth.login"] != 2 || counts["auth.new_ip"] != 1 {
		t.Fatal(counts)
	}
}
