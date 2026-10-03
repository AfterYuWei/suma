package ai

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/suma/suma/server/internal/database"
)

func TestGuestQueryCannotEnterDiagnosisProposalOrApproval(t *testing.T) {
	s, executions, _ := aiFixture(t)
	var reads atomic.Int32
	s.deps.Query = func(context.Context, string) (QuerySummary, error) {
		reads.Add(1)
		out := QuerySummary{Available: true}
		out.Containers.Total = 2
		return out, nil
	}
	ctx := context.Background()
	if _, err := s.Query(ctx, "other"); !errors.Is(err, ErrScope) || reads.Load() != 0 {
		t.Fatal("out-of-scope query reached adapter", err)
	}
	out, err := s.Query(ctx, "local")
	if err != nil || out.Containers.Total != 2 || reads.Load() != 1 {
		t.Fatal("safe query failed", err)
	}
	guest := Actor{Source: "chat", ExternalUserID: "guest", ChatID: "chat"}
	if _, err := s.Start(ctx, RunInput{NodeID: "local", Question: "restart"}, guest); !errors.Is(err, ErrScope) {
		t.Fatal("guest started model diagnosis", err)
	}
	if _, err := s.propose(ctx, database.AIRun{NodeID: "local"}, guest, OperationRequest{Action: "container.restart", ResourceID: "frozen-container"}); !errors.Is(err, ErrScope) {
		t.Fatal("guest created proposal", err)
	}
	if _, err := s.Decide(ctx, "fake", Decision{Approve: true}, guest); !errors.Is(err, ErrScope) {
		t.Fatal("guest approved operation", err)
	}
	s.db.Create(&database.AIOperation{ID: "queued-for-guest-test", NodeID: "local", Status: "queued", Action: "container.restart", ResourceID: "frozen-container"})
	if err := s.execute(ctx, "queued-for-guest-test", guest, func(int, string) {}); !errors.Is(err, ErrScope) || executions.Load() != 0 {
		t.Fatal("guest reached execution")
	}
	guest.UserID = 1
	if _, err := s.Start(ctx, RunInput{NodeID: "local", Question: "restart"}, guest); !errors.Is(err, ErrScope) {
		t.Fatal("forged SUMA ID bypassed binding")
	}
}

func TestQueryRechecksScopeAndHidesAdapterErrors(t *testing.T) {
	s, _, _ := aiFixture(t)
	s.deps.Query = func(context.Context, string) (QuerySummary, error) {
		return QuerySummary{}, errors.New("raw-private-key-and-endpoint")
	}
	if _, err := s.Query(context.Background(), "local"); err == nil || err.Error() == "raw-private-key-and-endpoint" {
		t.Fatal("private adapter error escaped")
	}
	entries, err := s.Audits(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Result != "unavailable" || strings.Contains(entries[0].Result, "raw-private-key-and-endpoint") {
		t.Fatal("safe query audit lost failure or exposed the private error", entries, err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	s.deps.Query = func(context.Context, string) (QuerySummary, error) {
		close(entered)
		<-release
		return QuerySummary{Available: true}, nil
	}
	done := make(chan error, 1)
	go func() { _, err := s.Query(context.Background(), "local"); done <- err }()
	<-entered
	cfg := s.Settings()
	cfg.Enabled = false
	if _, err := s.SaveSettings(context.Background(), SettingsInput{Settings: cfg}, Actor{UserID: 1}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrScope) {
		t.Fatal("disabled AI returned collected data", err)
	}
}
