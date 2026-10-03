package notification

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
)

func TestUnboundPendingAndRevokedIdentitiesOnlyReachSafeQueries(t *testing.T) {
	s, db, sender, now := fixture(t)
	db.Create(&database.User{Username: "admin", PasswordHash: "private"})
	c, err := s.SaveChannel(context.Background(), "", ChannelInput{Name: "chat", Provider: "telegram", Enabled: true, Config: Config{Interactive: true, Language: "en-US", Timezone: "UTC"}, Secrets: &Secrets{Token: "123:private"}})
	if err != nil {
		t.Fatal(err)
	}
	guest, operator := 0, 0
	s.SetChatHandler(func(in Incoming, binding database.NotificationBinding) {
		if binding.ID == "" {
			if in.Action != "" {
				t.Fatal("guest action reached handler")
			}
			guest++
		} else {
			operator++
		}
	})
	in := Incoming{ChannelID: c.ID, UserID: "42", ChatID: "group", Text: "query", Name: "Administrator"}
	serial := 0
	send := func() {
		serial++
		in.ID = fmt.Sprint(serial)
		if err := s.HandleIncoming(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	send()
	if guest != 1 || operator != 0 {
		t.Fatal("unbound identity authorized")
	}
	send()
	if guest != 1 {
		t.Fatal("query rate limit bypassed")
	}
	*now = now.Add(11 * time.Second)
	if err := s.HandleIncoming(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if guest != 1 {
		t.Fatal("duplicate platform event replayed")
	}
	b := database.NotificationBinding{ID: "binding", ChannelID: c.ID, UserID: 1, CodeHash: "hash", ExternalUserID: in.UserID, Status: "claimed", ExpiresAt: now.Add(time.Minute)}
	db.Create(&b)
	send()
	if guest != 2 || operator != 0 {
		t.Fatal("unconfirmed identity authorized")
	}
	db.Model(&b).Update("status", "active")
	send()
	if operator != 1 {
		t.Fatal("confirmed identity not recognized")
	}
	if err := s.RevokeBinding(context.Background(), 1, b.ID); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"preview", "approve", "reject"} {
		in.Action, in.OperationID = action, "private-operation"
		send()
	}
	in.Action, in.Text = "", "/approve private-operation"
	send()
	if guest != 2 || operator != 1 {
		t.Fatal("revoked identity reached operation handler")
	}
	for _, message := range sender.sent {
		if message.ApprovalToken != "" || message.OperationID != "" || message.ApproveID != "" {
			t.Fatal("guest received approval controls")
		}
	}
	in.Text = "query"
	*now = now.Add(11 * time.Second)
	send()
	if guest != 3 {
		t.Fatal("revoked identity could not use safe query")
	}
}
