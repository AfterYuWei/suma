package app

import (
	"context"
	"github.com/suma/suma/server/internal/testutil"
	"path/filepath"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"
	"github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/notification"
	"github.com/suma/suma/server/internal/secret"
)

func TestContainerRecoveryRequiresContinuousStableRunning(t *testing.T) {
	now := time.Now()
	state := docker.NotificationState{ID: "container", Running: true, RestartCount: 5}
	previous := map[string]docker.NotificationState{}
	var stable map[string]containerStability
	var events []event.Event
	emit := func(e event.Event) { events = append(events, e) }
	observe := func(elapsed time.Duration) {
		stable = observeContainerRecovery("node", "Node", []docker.NotificationState{state}, previous, stable, now.Add(elapsed), emit)
		previous[state.ID] = state
	}
	observe(0)
	observe(time.Minute)
	if len(events) != 0 {
		t.Fatal("a start was treated as recovery")
	}
	state.RestartCount++
	observe(90 * time.Second)
	observe(2 * time.Minute)
	if len(events) != 0 {
		t.Fatal("new restart did not reset stability")
	}
	state.Health = "unhealthy"
	observe(3 * time.Minute)
	state.Health = "healthy"
	observe(4 * time.Minute)
	observe(5 * time.Minute)
	observe(6 * time.Minute)
	if len(events) != 1 || events[0].Type != "container.running" || events[0].NodeID != "node" {
		t.Fatal("stable recovery missing", events)
	}
	stable = observeContainerRecovery("node", "Node", nil, previous, stable, now.Add(7*time.Minute), emit)
	if len(stable) != 0 {
		t.Fatal("removed container retained a stability checkpoint")
	}
	observe(8 * time.Minute)
	observe(10 * time.Minute)
	if len(events) != 1 {
		t.Fatal("a gap in observations was treated as continuous stability")
	}
}

func TestExpectedRebuildDoesNotHideUnrelatedContainerFailure(t *testing.T) {
	db, err := testutil.Open(t)
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
	db, err := testutil.Open(t)
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
