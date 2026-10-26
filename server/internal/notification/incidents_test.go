package notification

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
)

func TestIncidentLifecyclePersistsWithoutRepeatingAndRenotifiesAfterRecovery(t *testing.T) {
	for recovery, faults := range incidentRecoveries {
		for _, fault := range faults {
			t.Run(fault+"/"+recovery, func(t *testing.T) {
				s, db, sender, now := fixture(t)
				c := channel(t, s)
				r := filterRule(t, s, "Faults only", fault)
				c.Config.Subscription.RuleIDs = []string{r.ID}
				saveConfig(t, s, c)
				e := event.Event{Type: fault, Severity: "error", NodeID: "node-a", ResourceID: "resource-a", Title: "reference:tag", Message: "First fault"}
				ctx := context.Background()
				if err := s.Ingest(ctx, e); err != nil {
					t.Fatal(err)
				}
				inbox, err := s.Inbox(ctx, 1)
				if err != nil || len(inbox.Items) != 1 {
					t.Fatal(inbox, err)
				}
				firstID := inbox.Items[0].ID
				if err := s.Read(ctx, 1, firstID); err != nil {
					t.Fatal(err)
				}
				*now = now.Add(25 * time.Hour)
				// Recreate the service: deduplication must not be runtime-only.
				s = NewService(db, s.secrets, Dependencies{Sender: sender, Now: func() time.Time { return *now }})
				e.Message = "Repeated observation"
				if err := s.Ingest(ctx, e); err != nil {
					t.Fatal(err)
				}
				inbox, err = s.Inbox(ctx, 1)
				if err != nil || len(inbox.Items) != 1 || inbox.Unread != 0 || inbox.Items[0].Occurrences != 2 || inbox.Items[0].Message != "First fault" || !inbox.Items[0].LastOccurredAt.Equal(*now) {
					t.Fatal("duplicate fault changed history, unread state or occurrence evidence", inbox, err)
				}
				*now = now.Add(3 * time.Minute)
				e.Type, e.Severity = recovery, "info"
				if err := s.Ingest(ctx, e); err != nil {
					t.Fatal(err)
				}
				if err := s.Ingest(ctx, e); err != nil {
					t.Fatal(err)
				}
				var first database.NotificationEvent
				if err := db.First(&first, "id = ?", firstID).Error; err != nil || first.ResolvedAt == nil {
					t.Fatal("recovery missing", first, err)
				}
				*now = now.Add(time.Second)
				e.Type, e.Severity = fault, "error"
				if err := s.Ingest(ctx, e); err != nil {
					t.Fatal(err)
				}
				rows, err := s.Deliveries(ctx)
				if err != nil || len(rows) != 3 {
					t.Fatal("expected fault, recovery, new fault deliveries", rows, err)
				}
				if err := s.Tick(ctx); err != nil {
					t.Fatal(err)
				}
				if len(sender.sent) != 3 {
					t.Fatal("fault-only subscriber did not receive recovery", len(sender.sent))
				}
				inbox, err = s.Inbox(ctx, 1)
				if err != nil || len(inbox.Items) != 3 {
					t.Fatal(inbox, err)
				}
				found := false
				for _, item := range inbox.Items {
					if len(item.RecoveryOf) == 1 && item.RecoveryOf[0] == firstID {
						found = true
					}
				}
				if !found {
					t.Fatal("recovery lost its original incident link")
				}
			})
		}
	}
}

func TestIncidentIsolationConcurrentIngestionAndStaleEvents(t *testing.T) {
	s, db, sender, now := fixture(t)
	ctx := context.Background()
	e := event.Event{Type: "container.oom", NodeID: "node", ResourceID: "container", Time: *now}
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other := NewService(db, s.secrets, Dependencies{Sender: sender, Now: func() time.Time { return *now }})
			errors <- other.Ingest(ctx, e)
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	inbox, err := s.Inbox(ctx, 1)
	if err != nil || len(inbox.Items) != 1 || inbox.Items[0].Occurrences != 12 {
		t.Fatal(inbox, err)
	}
	for _, other := range []event.Event{
		{Type: "container.oom", NodeID: "other-node", ResourceID: "container"},
		{Type: "container.oom", NodeID: "node", ResourceID: "other-container"},
		{Type: "container.unhealthy", NodeID: "node", ResourceID: "container"},
		{Type: "task.failed", NodeID: "node", ResourceID: "container", TaskID: "task-1"},
		{Type: "task.failed", NodeID: "node", ResourceID: "container", TaskID: "task-2"},
	} {
		if err := s.Ingest(ctx, other); err != nil {
			t.Fatal(err)
		}
	}
	*now = now.Add(3 * time.Minute)
	recovery := event.Event{Type: "container.running", NodeID: "node", ResourceID: "container", Time: *now}
	if err := s.Ingest(ctx, recovery); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(ctx, e); err != nil {
		t.Fatal(err)
	} // delayed pre-recovery fault
	*now = now.Add(time.Second)
	e.Time = *now
	if err := s.Ingest(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(ctx, recovery); err != nil {
		t.Fatal(err)
	} // stale recovery cannot close new fault
	var count int64
	if err := db.Model(&database.NotificationEvent{}).Where("incident_key <> '' AND resolved_at IS NULL").Count(&count).Error; err != nil || count != 3 {
		t.Fatal("unrelated/new fault hidden or stale fault reopened", count, err)
	}
}

func TestRecoveryDoesNotReachNewOrUnsubscribedDestinations(t *testing.T) {
	s, _, _, now := fixture(t)
	r := filterRule(t, s, "Failures", "container.unhealthy")
	c := channel(t, s)
	c.Config.Subscription.RuleIDs = []string{r.ID}
	c = saveConfig(t, s, c)
	e := event.Event{Type: "container.unhealthy", NodeID: "node", ResourceID: "container"}
	if err := s.Ingest(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	// A newly subscribed channel did not receive this incident's initial fault.
	other := channel(t, s)
	other.Config.Subscription.RuleIDs = []string{r.ID}
	saveConfig(t, s, other)
	c.Config.Subscription.RuleIDs = nil
	saveConfig(t, s, c)
	*now = now.Add(time.Minute)
	e.Type = "container.recovered"
	if err := s.Ingest(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Deliveries(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatal("recovery leaked to a new/unsubscribed channel", rows, err)
	}
}

func TestStableContainerRecoveryDoesNotEndFreshFaultOrUnhealthyRecoveryEndCrash(t *testing.T) {
	s, db, _, now := fixture(t)
	ctx := context.Background()
	e := event.Event{Type: "container.oom", NodeID: "node", ResourceID: "container"}
	if err := s.Ingest(ctx, e); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"container.recovered", "container.running"} {
		e.Type = typ
		if err := s.Ingest(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	db.Model(&database.NotificationEvent{}).Count(&count)
	if count != 1 {
		t.Fatal("premature crash-loop recovery", count)
	}
	*now = now.Add(2 * time.Minute)
	e.Type = "container.running"
	if err := s.Ingest(ctx, e); err != nil {
		t.Fatal(err)
	}
	db.Model(&database.NotificationEvent{}).Count(&count)
	if count != 2 {
		t.Fatal("stable recovery missing", count)
	}
}

func TestImageReferenceAndChannelRecipientIncidentIsolation(t *testing.T) {
	s, db, _, now := fixture(t)
	ctx := context.Background()
	// Identity must use the full reference, before display-title truncation.
	prefix := ""
	for i := 0; i < 270; i++ {
		prefix += "a"
	}
	for _, tag := range []string{"first", "second"} {
		if err := s.Ingest(ctx, event.Event{Type: "image.check_failed", NodeID: "node", ResourceID: "same-image", Title: prefix + tag}); err != nil {
			t.Fatal(err)
		}
	}
	c := channel(t, s)
	for _, target := range []string{"first-chat", "second-chat"} {
		if err := s.Ingest(ctx, event.Event{Type: "notification.failed", ResourceID: c.ID, DedupeKey: deliveryIncidentKey(c, target)}); err != nil {
			t.Fatal(err)
		}
	}
	*now = now.Add(time.Minute)
	if err := s.Ingest(ctx, event.Event{Type: "notification.recovered", ResourceID: c.ID, DedupeKey: deliveryIncidentKey(c, "first-chat")}); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&database.NotificationEvent{}).Where("incident_key <> '' AND resolved_at IS NULL").Count(&count).Error; err != nil || count != 3 {
		t.Fatal("a successful recipient resolved an unrelated recipient/reference", count, err)
	}
}
