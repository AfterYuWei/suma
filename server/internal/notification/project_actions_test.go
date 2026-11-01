package notification

import (
	"context"
	"encoding/json"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/imageupdate"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestProjectPublisherValidationAndCallbackPayload(t *testing.T) {
	for _, row := range []struct {
		provider string
		ids      []string
	}{{"telegram", []string{"12345"}}, {"feishu_app", []string{"tenant:ou_user"}}, {"webhook", nil}} {
		if err := validateApprovers(row.provider, row.ids); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		provider string
		ids      []string
	}{{"telegram", []string{"@user"}}, {"telegram", []string{"1", "1"}}, {"feishu_app", []string{"ou_user"}}, {"webhook", []string{"1"}}} {
		if validateApprovers(row.provider, row.ids) == nil {
			t.Fatal("invalid publisher accepted", row)
		}
	}
	for _, language := range []string{"zh-CN", "en-US"} {
		m := Message{Text: "Project update", Language: language, PublishToken: "0123456789abcdef0123456789abcdef"}
		raw, _ := json.Marshal(telegramButtons(m))
		if !strings.Contains(string(raw), "publish:"+m.PublishToken) || len("publish:"+m.PublishToken) > 64 {
			t.Fatal(string(raw))
		}
		raw, _ = json.Marshal(feishuCard(m))
		label := "发布"
		if language == "en-US" {
			label = "Publish"
		}
		if !strings.Contains(string(raw), label) || !strings.Contains(string(raw), "publish_token") {
			t.Fatal(string(raw))
		}
	}
	c := Channel{Config: Config{ApproverIDs: []string{"123"}}}
	c.Provider = "telegram"
	c.Enabled = true
	if !needsConnection(c) || strings.Join(telegramUpdateTypes(c), ",") != "callback_query" {
		t.Fatal("callbacks incorrectly require discovery")
	}
	c.Config.ApproverIDs = nil
	c.Config.AutoDiscover = true
	if strings.Join(telegramUpdateTypes(c), ",") != "message" {
		t.Fatal("discovery requested callbacks")
	}
}

type fakeProjectPublisher struct {
	calls int
	actor imageupdate.Actor
}

func (f *fakeProjectPublisher) Candidate(context.Context, string, string, string) (*imageupdate.Candidate, error) {
	return &imageupdate.Candidate{}, nil
}
func (f *fakeProjectPublisher) Publish(_ context.Context, _, _, _ string, actor imageupdate.Actor, _ ...bool) (database.Task, error) {
	f.calls++
	f.actor = actor
	return database.Task{ID: "publication-task"}, nil
}
func TestProjectCallbackChecksCurrentUserChatDestinationAndGeneration(t *testing.T) {
	s, db, _, _ := fixture(t)
	publisher := &fakeProjectPublisher{}
	s.SetProjectUpdates(publisher)
	c := database.NotificationChannel{ID: "publisher-channel", Provider: "telegram", Enabled: true, DestinationVersion: 1, ConfigJSON: encodedConfigForPublisher()}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	candidate := database.ProjectImageCandidate{ID: "candidate", NodeID: "node", Project: "project", Fingerprint: "fingerprint", Revision: "revision", RuntimeKey: "runtime", ServicesJSON: "[]", Status: "pending", ActorJSON: "{}"}
	if err := db.Create(&candidate).Error; err != nil {
		t.Fatal(err)
	}
	token := "0123456789abcdef0123456789abcdef"
	action := database.ProjectImageAction{TokenHash: hash(token), CandidateID: candidate.ID, ChannelID: c.ID, ChatID: "456", DestinationVersion: 1, State: "pending"}
	if err := db.Create(&action).Error; err != nil {
		t.Fatal(err)
	}
	for _, in := range []ProjectActionInput{{ChannelID: c.ID, ChatID: "456", UserID: "999", Token: token}, {ChannelID: c.ID, ChatID: "wrong", UserID: "123", Token: token}, {ChannelID: "other", ChatID: "456", UserID: "123", Token: token}} {
		if _, err := s.HandleProjectAction(context.Background(), in); err == nil {
			t.Fatal("unauthorized callback accepted", in)
		}
	}
	s.connections[c.ID] = connectionEntry{generation: "current"}
	in := ProjectActionInput{ChannelID: c.ID, ChatID: "456", UserID: "123", Name: "Publisher", Token: token, Generation: "old"}
	if _, err := s.HandleProjectAction(context.Background(), in); err == nil {
		t.Fatal("old connection accepted")
	}
	in.Generation = "current"
	if id, err := s.HandleProjectAction(context.Background(), in); err != nil || id != "publication-task" || publisher.calls != 1 || publisher.actor.ExternalID != "123" {
		t.Fatal(id, err, publisher)
	}
	if err := db.Model(&c).Update("config_json", encode(Config{ChatID: "456", Language: "en-US"})).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.HandleProjectAction(context.Background(), in); err == nil || publisher.calls != 1 {
		t.Fatal("revoked publisher accepted")
	}
}
func encodedConfigForPublisher() string {
	return encode(Config{ChatID: "456", ApproverIDs: []string{"123"}, Language: "en-US"})
}

func TestTelegramPollingOnlyRoutesProjectPublishCallbacks(t *testing.T) {
	s, db, _, _ := fixture(t)
	publisher := &fakeProjectPublisher{}
	s.SetProjectUpdates(publisher)
	c, err := s.SaveChannel(context.Background(), "", ChannelInput{Name: "Publisher", Provider: "telegram", Enabled: true, Config: Config{ChatID: "456", ApproverIDs: []string{"123"}, Language: "en-US", Subscription: DefaultSubscription("UTC")}, Secrets: &Secrets{Token: "123:private-token"}})
	if err != nil {
		t.Fatal(err)
	}
	candidate := database.ProjectImageCandidate{ID: "candidate", NodeID: "node", Project: "project", Fingerprint: "fingerprint", Revision: "revision", RuntimeKey: "runtime", ServicesJSON: "[]", Status: "pending", ActorJSON: "{}"}
	if err := db.Create(&candidate).Error; err != nil {
		t.Fatal(err)
	}
	token := "0123456789abcdef0123456789abcdef"
	if err := db.Create(&database.ProjectImageAction{TokenHash: hash(token), CandidateID: candidate.ID, ChannelID: c.ID, ChatID: "456", DestinationVersion: c.DestinationVersion, State: "pending"}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	polls, answers := 0, 0
	a := NewAdapter()
	a.Client = func(bool) *http.Client {
		return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.HasSuffix(req.URL.Path, "/getWebhookInfo"):
				return response(200, `{"ok":true,"result":{"url":""}}`), nil
			case strings.HasSuffix(req.URL.Path, "/getUpdates"):
				polls++
				if polls == 1 {
					return response(200, `{"ok":true,"result":[{"update_id":1,"callback_query":{"id":"legacy","data":"approve:old-token"}},{"update_id":2,"callback_query":{"id":"publish","data":"publish:`+token+`","from":{"id":123,"first_name":"Publisher"},"message":{"chat":{"id":456}}}}]}`), nil
				}
				var request struct {
					Offset int64 `json:"offset"`
				}
				if err := json.NewDecoder(req.Body).Decode(&request); err != nil || request.Offset != 3 {
					t.Fatal("ignored callback did not advance the cursor", request.Offset, err)
				}
				cancel()
				return response(200, `{"ok":true,"result":[]}`), nil
			case strings.HasSuffix(req.URL.Path, "/answerCallbackQuery"):
				answers++
				body, err := io.ReadAll(req.Body)
				if err != nil || !strings.Contains(string(body), `"callback_query_id":"publish"`) || !strings.Contains(string(body), `"show_alert":false`) {
					t.Fatal("unexpected callback response", err)
				}
				return response(200, `{"ok":true,"result":true}`), nil
			default:
				t.Fatal("unexpected Telegram operation")
				return nil, context.Canceled
			}
		})}
	}
	s.telegram(ctx, a, c, Secrets{Token: "123:private-token"}, func(Incoming) error {
		t.Fatal("publishing callbacks entered recipient discovery")
		return nil
	})
	if publisher.calls != 1 || publisher.actor.ExternalID != "123" || answers != 1 {
		t.Fatalf("project callback routing: calls=%d actor=%#v answers=%d", publisher.calls, publisher.actor, answers)
	}
}
