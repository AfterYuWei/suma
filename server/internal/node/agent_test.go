package node

import (
	"context"
	"errors"
	"fmt"
	"github.com/suma/suma/server/internal/testutil"
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
	"gorm.io/gorm"
)

func agentTestService(t *testing.T) *Service {
	t.Helper()
	db, err := testutil.Open(t)
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
		t.Fatalf("expected exactly one successful token claim, got %d", successes)
	}
	if _, _, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion); err == nil {
		t.Fatal("used token was accepted again")
	}
	if view, err := service.GetAgentEnrollment(ctx, issued.NodeID); err != nil || view.ConsumedAt == nil {
		t.Fatalf("used token was not marked consumed: %+v, %v", view, err)
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
	if credential.SecretHash != hashSecret(secret) || credential.SecretHash == secret {
		t.Fatal("Agent credential was not stored as a hash")
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
	if err := service.db.Model(&database.AgentEnrollment{}).Where("node_id = ?", issued.NodeID).Update("expires_at", time.Now().AddDate(-1, 0, 0)).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, secret, agentwire.ProtocolVersion); err != nil {
		t.Fatalf("enrollment expiry invalidated the persistent credential: %v", err)
	}
	// Reopening the PostgreSQL connection simulates a control-plane restart.
	reopened, err := database.Open(database.ConnectionDSN(service.db))
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewService(reopened, service.secrets, "unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if err := restarted.AuthenticateAgent(ctx, issued.NodeID, secret, agentwire.ProtocolVersion); err != nil {
		t.Fatalf("credential did not survive a control-plane restart: %v", err)
	}
	if err := service.RevokeAgent(ctx, issued.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion); err == nil {
		t.Fatal("revoked Agent re-enrolled with the old token")
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, secret, agentwire.ProtocolVersion); err == nil {
		t.Fatal("revoked credential was accepted")
	}
	if err := restarted.AuthenticateAgent(ctx, issued.NodeID, secret, agentwire.ProtocolVersion); err == nil {
		t.Fatal("restarted service accepted a revoked credential")
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
	if _, _, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion); err == nil {
		t.Fatal("old token remained valid after manual refresh")
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, credential, agentwire.ProtocolVersion); err == nil {
		t.Fatal("old claimed credential survived token reissue")
	}
	if view, err := service.Get(ctx, issued.NodeID); err != nil || view.Enabled || view.Status != "pairing" || view.LastError != "" {
		t.Fatalf("reissued pending Agent did not reset pairing state: %+v, %v", view, err)
	}
}

func TestReissueConnectedAgentRequiresNewTokenOnReconnect(t *testing.T) {
	service := agentTestService(t)
	ctx := context.Background()
	issued, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{Name: "Connected Agent"})
	if err != nil {
		t.Fatal(err)
	}
	_, oldCredential, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&database.Node{}).Where("id = ?", issued.NodeID).Updates(map[string]any{"engine_id": "connected-engine", "status": "online", "agent_connected_at": time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	reissued, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{NodeID: issued.NodeID, Name: "Connected Agent"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, oldCredential, agentwire.ProtocolVersion); err == nil {
		t.Fatal("old credential reconnected after token refresh")
	}
	if _, _, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion); err == nil {
		t.Fatal("old token remained valid after refresh")
	}
	_, newCredential, err := service.ClaimAgentEnrollment(ctx, reissued.Token, agentwire.ProtocolVersion)
	if err != nil || newCredential == oldCredential {
		t.Fatalf("new token did not issue a new credential: %v", err)
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, newCredential, agentwire.ProtocolVersion); err != nil {
		t.Fatalf("new credential was rejected: %v", err)
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, oldCredential, agentwire.ProtocolVersion); err == nil {
		t.Fatal("replaced credential was accepted")
	}
}

func TestAgentEngineReplacementPreservesNodeData(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled=%t", enabled), func(t *testing.T) {
			service := agentTestService(t)
			ctx := context.Background()
			issued, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{Name: "HuaWeiCloud"})
			if err != nil {
				t.Fatal(err)
			}
			_, oldCredential, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.db.Model(&database.Node{}).Where("id = ?", issued.NodeID).Updates(map[string]any{"engine_id": "old-engine", "engine_version": "old-version", "enabled": enabled, "status": "offline", "agent_connected_at": time.Now(), "agent_version": "old-agent", "allowed_bind_roots_json": `["/srv/data"]`}).Error; err != nil {
				t.Fatal(err)
			}
			before, err := service.Get(ctx, issued.NodeID)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range []any{
				&database.DeliveryProject{ID: 71, NodeID: issued.NodeID, Name: "retained-project", DeploymentName: "retained-project", EnvironmentCiphertext: []byte("encrypted-environment")},
				&database.DeliveryProjectNode{ProjectID: 71, NodeID: issued.NodeID},
				&database.GitCredential{ID: 81, Name: "retained-git", AuthType: "token", SecretCiphertext: []byte("encrypted-secret")},
				&database.GitCredentialNode{CredentialID: 81, NodeID: issued.NodeID},
				&database.NodeGroup{ID: 91, Name: "retained-group"},
				&database.NodeGroupNode{GroupID: 91, NodeID: issued.NodeID},
				&database.Task{ID: "retained-task", NodeID: issued.NodeID, Name: "retained-task", Type: "compose.up", Status: "success"},
				&database.AuditLog{NodeID: issued.NodeID, NodeName: before.Name, Action: "compose.up", ResourceName: "retained-project", Result: "success"},
				&database.MonitoringInstance{ID: strings.Repeat("a", 64), NodeID: issued.NodeID, EngineID: "old-engine", ContainerID: strings.Repeat("b", 64), Name: "old-container", FirstSeen: time.Now(), LastSeen: time.Now()},
			} {
				if err := service.db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			// Compare persisted rows, including ciphertext and original Engine history.
			snapshots := map[string]string{}
			for _, table := range []string{"delivery_projects", "delivery_project_nodes", "git_credentials", "git_credential_nodes", "node_group_nodes", "tasks", "audit_logs", "monitoring_instances"} {
				var snapshot string
				if err := service.db.Raw("SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text FROM " + table + " t").Scan(&snapshot).Error; err != nil {
					t.Fatal(err)
				}
				snapshots[table] = snapshot
			}
			replacement := &AgentEngineReplacementInput{PreviousEngineID: "old-engine", EngineID: "new-engine", Confirmation: before.Name}
			reissued, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{NodeID: before.ID, Name: before.Name, Replacement: replacement})
			if err != nil {
				t.Fatal(err)
			}
			service.recordProbe(before.ID, time.Now(), docker.Info{ID: "old-engine", ServerVersion: "old-version"}, nil)
			after, err := service.Get(ctx, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.ID != before.ID || after.Name != before.Name || !after.CreatedAt.Equal(before.CreatedAt) || after.Enabled != enabled || after.EngineID != "new-engine" || after.Status != "offline" || after.EngineVersion != "" || after.AgentConnectedAt != nil || after.AgentVersion != "" {
				t.Fatalf("unexpected replacement node: %+v", after)
			}
			var saved database.Node
			if err := service.db.First(&saved, "id = ?", before.ID).Error; err != nil || saved.AllowedBindRootsJSON != `["/srv/data"]` {
				t.Fatalf("node settings changed: %v", err)
			}
			for table, before := range snapshots {
				var after string
				if err := service.db.Raw("SELECT jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text FROM " + table + " t").Scan(&after).Error; err != nil || after != before {
					t.Fatalf("retained table %s changed: %v", table, err)
				}
			}
			if err := service.AuthenticateAgent(ctx, before.ID, oldCredential, agentwire.ProtocolVersion); err == nil {
				t.Fatal("replacement retained old Agent credential")
			}
			if _, _, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion); err == nil {
				t.Fatal("replacement retained old token")
			}
			id, credential, err := service.ClaimAgentEnrollment(ctx, reissued.Token, agentwire.ProtocolVersion)
			if err != nil || id != before.ID {
				t.Fatalf("replacement pairing lost node identity: %s %v", id, err)
			}
			if err := service.AuthenticateAgent(ctx, id, credential, agentwire.ProtocolVersion); err != nil {
				t.Fatal(err)
			}
			if err := service.CancelAgentEnrollment(ctx, id); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Get(ctx, id); err != nil {
				t.Fatal("cancel removed an existing replacement node")
			}
		})
	}
}

func TestAgentEngineReplacementRejectsUnconfirmedChanges(t *testing.T) {
	service := agentTestService(t)
	ctx := context.Background()
	issued, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{Name: "HuaWeiCloud"})
	if err != nil {
		t.Fatal(err)
	}
	_, credential, err := service.ClaimAgentEnrollment(ctx, issued.Token, agentwire.ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&database.Node{}).Where("id = ?", issued.NodeID).Update("engine_id", "old-engine").Error; err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&database.Node{}).Where("id = ?", "local").Update("engine_id", "registered-engine").Error; err != nil {
		t.Fatal(err)
	}
	for _, input := range []AgentEngineReplacementInput{
		{PreviousEngineID: "old-engine", EngineID: "new-engine", Confirmation: "wrong-name"},
		{PreviousEngineID: "stale-engine", EngineID: "new-engine", Confirmation: "HuaWeiCloud"},
		{PreviousEngineID: "old-engine", EngineID: "old-engine", Confirmation: "HuaWeiCloud"},
		{PreviousEngineID: "old-engine", EngineID: "registered-engine", Confirmation: "HuaWeiCloud"},
		{PreviousEngineID: "old-engine", EngineID: "", Confirmation: "HuaWeiCloud"},
		{PreviousEngineID: "old-engine", EngineID: "bad\nengine", Confirmation: "HuaWeiCloud"},
		{PreviousEngineID: "old-engine", EngineID: strings.Repeat("x", 129), Confirmation: "HuaWeiCloud"},
	} {
		if _, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{NodeID: issued.NodeID, Name: "HuaWeiCloud", Replacement: &input}); err == nil {
			t.Fatalf("unconfirmed replacement accepted: %+v", input)
		}
	}
	if _, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{NodeID: "local", Name: "Local", Replacement: &AgentEngineReplacementInput{PreviousEngineID: "registered-engine", EngineID: "new-engine", Confirmation: "Local"}}); err == nil {
		t.Fatal("direct node accepted Agent Engine replacement")
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, credential, agentwire.ProtocolVersion); err != nil {
		t.Fatalf("rejected change revoked original credential: %v", err)
	}
	view, err := service.Get(ctx, issued.NodeID)
	if err != nil || view.EngineID != "old-engine" {
		t.Fatalf("rejected change modified node: %+v %v", view, err)
	}

	// A pairing write failure must roll back both the identity change and revocation.
	if err := service.db.Callback().Create().Before("gorm:create").Register("test:fail_replacement_enrollment", func(tx *gorm.DB) {
		if tx.Statement.Table == "agent_enrollments" {
			tx.AddError(errors.New("test enrollment write failed"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.db.Callback().Create().Remove("test:fail_replacement_enrollment") })
	if _, err := service.IssueAgentEnrollment(ctx, AgentEnrollmentInput{NodeID: issued.NodeID, Name: "HuaWeiCloud", Replacement: &AgentEngineReplacementInput{PreviousEngineID: "old-engine", EngineID: "new-engine", Confirmation: "HuaWeiCloud"}}); err == nil {
		t.Fatal("failed enrollment write accepted")
	}
	view, err = service.Get(ctx, issued.NodeID)
	if err != nil || view.EngineID != "old-engine" {
		t.Fatalf("failed replacement was not rolled back: %+v %v", view, err)
	}
	if err := service.AuthenticateAgent(ctx, issued.NodeID, credential, agentwire.ProtocolVersion); err != nil {
		t.Fatalf("failed replacement revoked original credential: %v", err)
	}
}
