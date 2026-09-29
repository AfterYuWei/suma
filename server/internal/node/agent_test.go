package node

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/agenthub"
	"github.com/suma/suma/server/internal/agentwire"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"
	"github.com/suma/suma/server/internal/secret"
)

func agentTestService(t *testing.T) *Service {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(db, store, "unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	hub, err := agenthub.New()
	if err != nil {
		t.Fatal(err)
	}
	service.SetAgentHub(hub)
	t.Cleanup(func() { _ = service.Close(); _ = hub.Close() })
	return service
}

func TestAgentEnrollmentIsSingleUseAndRevocable(t *testing.T) {
	service := agentTestService(t)
	ctx := context.Background()
	issued, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{Name: "Edge"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issued.Token) != 64 {
		t.Fatal("enrollment token must be 256-bit")
	}
	if view, err := service.Get(ctx, issued.NodeID); err != nil || view.Enabled || view.Status != "pairing" {
		t.Fatalf("new Agent node was enabled before identity verification: %+v, %v", view, err)
	}
	var stored database.AgentEnrollment
	if err := service.db.Where("node_id = ?", issued.NodeID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.TokenHash, issued.Token) || stored.TokenHash == "" {
		t.Fatal("raw token stored in database")
	}
	if _, _, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion+1); err == nil {
		t.Fatal("incompatible protocol accepted")
	}
	if view, err := service.Get(ctx, issued.NodeID); err != nil || view.Status != "incompatible" || view.AgentEnrollment == nil || view.AgentEnrollment.LastError != ErrAgentProtocol.Error() {
		t.Fatalf("incompatible pairing not reported: %+v, %v", view, err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan struct {
		credential string
		err        error
	}, 2)
	for range 2 {
		go func() {
			defer wg.Done()
			_, credential, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion)
			results <- struct {
				credential string
				err        error
			}{credential, err}
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	var secret string
	for result := range results {
		if result.err == nil {
			successes++
			secret = result.credential
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one successful claim, got %d", successes)
	}
	var credential database.AgentCredential
	if err := service.db.Where("node_id = ?", issued.NodeID).First(&credential).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, "invalid", agentwire.ProtocolVersion); err == nil {
		t.Fatal("invalid credential accepted")
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, secret, agentwire.ProtocolVersion); err != nil {
		t.Fatalf("valid credential rejected: %v", err)
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, secret, agentwire.ProtocolVersion+1); !errors.Is(err, ErrAgentProtocol) {
		t.Fatalf("incompatible reconnect not reported: %v", err)
	}
	if view, err := service.Get(ctx, issued.NodeID); err != nil || view.Status != "incompatible" {
		t.Fatalf("incompatible reconnect status: %+v, %v", view, err)
	}
	service.recordProbe(issued.NodeID, time.Now(), docker.Info{}, errors.New("Agent offline"))
	if view, err := service.Get(ctx, issued.NodeID); err != nil || view.Status != "incompatible" {
		t.Fatalf("background probe hid protocol mismatch: %+v, %v", view, err)
	}
	if err := service.RevokeAgent(ctx, issued.NodeID); err != nil {
		t.Fatal(err)
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, secret, agentwire.ProtocolVersion); err == nil {
		t.Fatal("revoked credential was accepted")
	}
	if err := service.db.Where("node_id = ?", issued.NodeID).First(&credential).Error; err != nil {
		t.Fatal(err)
	}
	if credential.RevokedAt == nil {
		t.Fatal("credential was not revoked")
	}
}

func TestAgentEnrollmentExpiryAndInPlaceReservation(t *testing.T) {
	service := agentTestService(t)
	ctx := context.Background()
	if err := service.db.Model(&database.Node{}).Where("id = ?", "local").Update("engine_id", "same-engine").Error; err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{NodeID: "local", Name: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.Get(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if view.ConnectionType != ConnectionUnix {
		t.Fatal("migration changed the node before Agent verification")
	}
	if err := service.db.Model(&database.AgentEnrollment{}).Where("node_id = ?", "local").Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestCancelIncompatibleNewAgentEnrollment(t *testing.T) {
	service := agentTestService(t)
	ctx := context.Background()
	issued, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{Name: "Old Agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion+1); !errors.Is(err, ErrAgentProtocol) {
		t.Fatalf("incompatible protocol: %v", err)
	}
	if err := service.CancelAgentEnrollment(ctx, issued.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, issued.NodeID); err == nil {
		t.Fatal("canceled pending Agent node still exists")
	}
}

func TestReissuePendingAgentRevokesClaimedCredential(t *testing.T) {
	service := agentTestService(t)
	ctx := context.Background()
	issued, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{Name: "Pending Agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, credential, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, credential, agentwire.ProtocolVersion+1); !errors.Is(err, ErrAgentProtocol) {
		t.Fatalf("incompatible reconnect: %v", err)
	}
	reissued, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{NodeID: issued.NodeID, Name: "Pending Agent"})
	if err != nil {
		t.Fatal(err)
	}
	if reissued.Token == issued.Token {
		t.Fatal("reissued token was reused")
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, credential, agentwire.ProtocolVersion); err == nil {
		t.Fatal("old claimed credential survived token reissue")
	}
	if view, err := service.Get(ctx, issued.NodeID); err != nil || view.Enabled || view.Status != "pairing" || view.LastError != "" {
		t.Fatalf("reissued pending Agent did not reset pairing state: %+v, %v", view, err)
	}
}
