//go:build dockersmoke

package api

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/docker"
	"github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/notification"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

type smokeNotificationSender struct {
	mu       sync.Mutex
	messages []notification.Message
}

func (s *smokeNotificationSender) Send(_ context.Context, _ notification.Channel, _ notification.Secrets, message notification.Message) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	if len(s.messages) == 1 {
		return "", errors.New("controlled transient delivery failure")
	}
	return "smoke-receipt", nil
}

// Each caller supplies an explicit Unix, mTLS TCP or verified WSS Agent runtime
// backed by the disposable smoke Engine. Delivery never targets a real channel.
func notificationTransportSmoke(t *testing.T, ctx context.Context, root, nodeID, transport string, db *gorm.DB, adapter *docker.Adapter, tasks *task.Service, current *compose.Service) {
	t.Helper()
	name := fmt.Sprintf("notice-%s-%d", transport, time.Now().UnixNano())
	if _, err := current.Create(ctx, name, "services:\n  app:\n    image: alpine:3.24\n    command: [sh, -c, 'sleep 2; exit 7']\n", ""); err != nil {
		t.Fatal(err)
	}
	defer current.ForceRemove(context.Background(), name, true)
	store, err := secret.Open(filepath.Join(root, "notification-key"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sender := &smokeNotificationSender{}
	notify := notification.NewService(db, store, notification.Dependencies{Sender: sender, Now: func() time.Time { return now }})
	channel, err := notify.SaveChannel(ctx, "", notification.ChannelInput{Name: "Isolated notification", Provider: "webhook", Enabled: true, Config: notification.Config{Endpoint: "https://example.com/isolated-smoke", Language: "en-US", Timezone: "UTC"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = notify.SaveRule(ctx, "", notification.RuleInput{Name: "Container failure", Enabled: true, Config: notification.RuleConfig{Events: []string{"container.exited"}, NodeIDs: []string{nodeID}, Projects: []string{name}, ChannelIDs: []string{channel.ID}, Mode: "immediate", Timezone: "UTC"}}); err != nil {
		t.Fatal(err)
	}
	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	observed := make(chan event.Event, 1)
	go func() {
		done <- adapter.WatchEvents(watchCtx, func(e event.Event) {
			if e.Project == name && e.Type == "container.exited" {
				e.NodeID, e.NodeName = nodeID, transport
				notify.Emit(e)
				select {
				case observed <- e:
				default:
				}
			}
		})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Docker event stream did not close")
		}
	}()
	started, err := current.Action(ctx, name, "up")
	if err != nil {
		t.Fatal(err)
	}
	if err = tasks.Wait(ctx, started.ID); err != nil {
		t.Fatal(err)
	}
	var e event.Event
	select {
	case e = <-observed:
	case <-time.After(15 * time.Second):
		t.Fatal("actual Docker exit event was not observed")
	}
	inbox, err := notify.Inbox(ctx, 1)
	if err != nil || inbox.Unread != 1 || len(inbox.Items) != 1 || inbox.Items[0].ResourceID != e.ResourceID || inbox.Items[0].NodeID != nodeID {
		t.Fatal("Docker event missing from node-scoped inbox", inbox, err)
	}
	if err = notify.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	deliveries, err := notify.Deliveries(ctx)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != "retry" {
		t.Fatal("transient failure was not queued for retry", deliveries, err)
	}
	now = now.Add(10 * time.Minute)
	if err = notify.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	deliveries, err = notify.Deliveries(ctx)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != "sent" || deliveries[0].Attempts != 2 || deliveries[0].ProviderMessageID != "smoke-receipt" {
		t.Fatal("notification retry failed", deliveries, err)
	}
	if err = notify.Read(ctx, 1, inbox.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	inbox, err = notify.Inbox(ctx, 1)
	if err != nil || inbox.Unread != 0 {
		t.Fatal("read notification remained unread", inbox, err)
	}
	t.Log("actual Docker exit → inbox → simulated delivery failure/retry verified over", transport)
}
