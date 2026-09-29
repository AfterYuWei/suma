package node

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/agenthub"
	"github.com/suma/suma/server/internal/agentwire"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"
	"gorm.io/gorm"
)

type AgentEnrollmentInput struct {
	Name     string `json:"name"`
	NodeID   string `json:"node_id,omitempty"`
	GroupIDs []uint `json:"group_ids,omitempty"`
}

type AgentEnrollmentView struct {
	NodeID     string     `json:"node_id"`
	Token      string     `json:"token,omitempty"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
}

func agentEnrollmentView(row database.AgentEnrollment) *AgentEnrollmentView {
	return &AgentEnrollmentView{NodeID: row.NodeID, ExpiresAt: row.ExpiresAt, ConsumedAt: row.ConsumedAt, LastError: row.LastError}
}

var ErrAgentProtocol = errors.New("Agent protocol version is incompatible")

func (s *Service) SetAgentHub(hub *agenthub.Hub) {
	s.agents = hub
	if hub != nil {
		hub.SetDisconnect(s.AgentDisconnected)
	}
}

func randomSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func hashSecret(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func (s *Service) IssueAgentEnrollment(ctx context.Context, input AgentEnrollmentInput) (AgentEnrollmentView, error) {
	if s.agents == nil {
		return AgentEnrollmentView{}, errors.New("Agent hub is unavailable")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 128 {
		return AgentEnrollmentView{}, errors.New("node name is required and must not exceed 128 characters")
	}
	secret, err := randomSecret()
	if err != nil {
		return AgentEnrollmentView{}, err
	}
	nodeID := input.NodeID
	dropPendingCredential := false
	if nodeID == "" {
		nodeID, err = newID(name)
		if err != nil {
			return AgentEnrollmentView{}, err
		}
		if _, err := s.validateGroupIDs(ctx, input.GroupIDs); err != nil {
			return AgentEnrollmentView{}, err
		}
	} else {
		row, err := s.row(ctx, nodeID)
		if err != nil {
			return AgentEnrollmentView{}, err
		}
		if row.EngineID == "" && row.ConnectionType != ConnectionAgent {
			if _, err := s.Test(ctx, nodeID); err != nil {
				return AgentEnrollmentView{}, fmt.Errorf("verify original Docker Engine before migration: %w", err)
			}
		}
		name = row.Name
		dropPendingCredential = row.ConnectionType != ConnectionAgent || row.EngineID == ""
	}
	expires := time.Now().Add(10 * time.Minute)
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if input.NodeID == "" {
			row := database.Node{ID: nodeID, Name: name, ConnectionType: ConnectionAgent, Endpoint: "agent://" + nodeID, TLSMode: TLSDisabled, AllowedBindRootsJSON: "[]", Enabled: false, Status: "pairing"}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			// GORM applies the Node model's default:true to a false Create value.
			if err := tx.Model(&database.Node{}).Where("id = ?", nodeID).UpdateColumn("enabled", false).Error; err != nil {
				return err
			}
			if err := replaceNodeGroups(tx, nodeID, input.GroupIDs); err != nil {
				return err
			}
		} else {
			if err := tx.Where("node_id = ?", nodeID).Delete(&database.AgentEnrollment{}).Error; err != nil {
				return err
			}
			if dropPendingCredential {
				if err := tx.Where("node_id = ?", nodeID).Delete(&database.AgentCredential{}).Error; err != nil {
					return err
				}
			}
			if err := tx.Model(&database.Node{}).Where("id = ? AND connection_type = ? AND engine_id = ''", nodeID, ConnectionAgent).UpdateColumns(map[string]any{"enabled": false, "status": "pairing", "last_error": ""}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&database.AgentEnrollment{NodeID: nodeID, TokenHash: hashSecret(secret), ExpiresAt: expires}).Error
	})
	if err != nil {
		return AgentEnrollmentView{}, err
	}
	if dropPendingCredential {
		s.agents.Disconnect(nodeID)
	}
	return AgentEnrollmentView{NodeID: nodeID, Token: secret, ExpiresAt: expires}, nil
}

func (s *Service) GetAgentEnrollment(ctx context.Context, id string) (AgentEnrollmentView, error) {
	if !validID.MatchString(id) {
		return AgentEnrollmentView{}, errors.New("invalid node ID")
	}
	var row database.AgentEnrollment
	if err := s.db.WithContext(ctx).Where("node_id = ?", id).First(&row).Error; err != nil {
		return AgentEnrollmentView{}, err
	}
	return AgentEnrollmentView{NodeID: id, ExpiresAt: row.ExpiresAt, ConsumedAt: row.ConsumedAt, LastError: row.LastError}, nil
}

func (s *Service) CancelAgentEnrollment(ctx context.Context, id string) error {
	if !validID.MatchString(id) {
		return errors.New("invalid node ID")
	}
	disconnect := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row database.Node
		if err := tx.Where("id = ?", id).First(&row).Error; err != nil {
			return err
		}
		var enrollment database.AgentEnrollment
		if err := tx.Where("node_id = ?", id).First(&enrollment).Error; err != nil {
			return err
		}
		if err := tx.Where("node_id = ?", id).Delete(&database.AgentEnrollment{}).Error; err != nil {
			return err
		}
		newPending := row.ConnectionType == ConnectionAgent && row.EngineID == "" && row.AgentConnectedAt == nil
		if row.ConnectionType != ConnectionAgent || newPending {
			if err := tx.Where("node_id = ?", id).Delete(&database.AgentCredential{}).Error; err != nil {
				return err
			}
			disconnect = true
		}
		if newPending {
			if err := tx.Where("node_id = ?", id).Delete(&database.NodeGroupNode{}).Error; err != nil {
				return err
			}
			return tx.Delete(&database.Node{}, "id = ?", id).Error
		}
		return nil
	})
	if err == nil && disconnect && s.agents != nil {
		s.agents.Disconnect(id)
	}
	return err
}

func (s *Service) ClaimAgentEnrollment(ctx context.Context, token string, protocol int) (string, string, error) {
	if len(token) != 64 {
		return "", "", errors.New("invalid Agent enrollment token")
	}
	if protocol != agentwire.ProtocolVersion {
		var enrollment database.AgentEnrollment
		if err := s.db.WithContext(ctx).Where("token_hash = ? AND consumed_at IS NULL AND expires_at > ?", hashSecret(token), time.Now()).First(&enrollment).Error; err == nil {
			_ = s.db.WithContext(ctx).Model(&database.AgentEnrollment{}).Where("node_id = ?", enrollment.NodeID).Update("last_error", ErrAgentProtocol.Error()).Error
			_ = s.db.WithContext(ctx).Model(&database.Node{}).Where("id = ? AND connection_type = ?", enrollment.NodeID, ConnectionAgent).UpdateColumns(map[string]any{"status": "incompatible", "last_error": ErrAgentProtocol.Error()}).Error
		}
		return "", "", ErrAgentProtocol
	}
	credential, err := randomSecret()
	if err != nil {
		return "", "", err
	}
	var nodeID string
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var enrollment database.AgentEnrollment
		if err := tx.Where("token_hash = ?", hashSecret(token)).First(&enrollment).Error; err != nil {
			return errors.New("Agent enrollment token is invalid")
		}
		if enrollment.ConsumedAt != nil || !time.Now().Before(enrollment.ExpiresAt) {
			return errors.New("Agent enrollment token has expired or was used")
		}
		now := time.Now()
		updated := tx.Model(&database.AgentEnrollment{}).Where("node_id = ? AND consumed_at IS NULL AND expires_at > ?", enrollment.NodeID, now).Update("consumed_at", now)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return errors.New("Agent enrollment token was used")
		}
		if err := tx.Where("node_id = ?", enrollment.NodeID).Delete(&database.AgentCredential{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.AgentCredential{NodeID: enrollment.NodeID, SecretHash: hashSecret(credential)}).Error; err != nil {
			return err
		}
		nodeID = enrollment.NodeID
		return nil
	})
	if err == nil && s.agents != nil {
		s.agents.Disconnect(nodeID)
	}
	return nodeID, credential, err
}

func (s *Service) AuthenticateAgent(ctx context.Context, id, credential string, protocol int) error {
	if !validID.MatchString(id) || len(credential) != 64 {
		return errors.New("invalid Agent credential")
	}
	var row database.AgentCredential
	if err := s.db.WithContext(ctx).Where("node_id = ? AND revoked_at IS NULL", id).First(&row).Error; err != nil {
		return errors.New("Agent credential is invalid")
	}
	actual := hashSecret(credential)
	if subtle.ConstantTimeCompare([]byte(actual), []byte(row.SecretHash)) != 1 {
		return errors.New("Agent credential is invalid")
	}
	if protocol != agentwire.ProtocolVersion {
		if s.agents == nil || !s.agents.Connected(id) {
			_ = s.db.WithContext(ctx).Model(&database.Node{}).Where("id = ? AND connection_type = ?", id, ConnectionAgent).UpdateColumns(map[string]any{"status": "incompatible", "last_error": ErrAgentProtocol.Error()}).Error
		}
		_ = s.db.WithContext(ctx).Model(&database.AgentEnrollment{}).Where("node_id = ?", id).Update("last_error", ErrAgentProtocol.Error()).Error
		return ErrAgentProtocol
	}
	return nil
}

func (s *Service) ActivateAgent(ctx context.Context, id, version string, session <-chan struct{}) error {
	if s.agents == nil || !s.agents.Active(id, session) {
		return errors.New("Agent is offline")
	}
	endpoint, err := s.agents.Endpoint(id)
	if err != nil {
		return err
	}
	client, err := docker.New(endpoint)
	if err != nil {
		return err
	}
	defer client.Close()
	// Each Docker request opens a separate Agent stream across the public WSS
	// route. Allow enough time for both round trips on remote hosts.
	probe, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	started := time.Now()
	if err := client.Ping(probe); err != nil {
		return s.agentActivationError(id, err)
	}
	info, err := client.Info(probe)
	if err != nil {
		return s.agentActivationError(id, err)
	}
	if info.ID == "" {
		return s.agentActivationError(id, errors.New("Agent Docker Engine ID is empty"))
	}
	s.engineMu.Lock()
	defer s.engineMu.Unlock()
	row, err := s.row(ctx, id)
	if err != nil {
		return err
	}
	if row.ConnectionType == ConnectionAgent && !row.Enabled && row.Status != "pairing" {
		return s.agentActivationError(id, errors.New("Agent node is disabled"))
	}
	if row.EngineID != "" && row.EngineID != info.ID {
		return s.agentActivationError(id, errors.New("Agent Docker Engine ID does not match the node"))
	}
	if err := s.ensureUniqueEngine(ctx, id, info.ID); err != nil {
		return s.agentActivationError(id, err)
	}
	if !s.agents.Active(id, session) {
		return errors.New("Agent disconnected during activation")
	}
	now := time.Now()
	if len(version) > 64 {
		version = version[:64]
	}
	err = s.db.WithContext(ctx).Model(&database.Node{}).Where("id = ?", id).Updates(map[string]any{
		"connection_type": ConnectionAgent, "endpoint": "agent://" + id, "tls_mode": TLSDisabled, "tls_credential_id": nil,
		"enabled": row.Enabled || row.Status == "pairing", "status": "online", "last_error": "", "engine_id": info.ID, "engine_version": info.ServerVersion,
		"last_latency_ms": time.Since(started).Milliseconds(), "last_checked_at": now, "agent_connected_at": now, "agent_version": version,
	}).Error
	if err != nil {
		return err
	}
	if !s.agents.Active(id, session) {
		s.AgentDisconnected(id)
		return errors.New("Agent disconnected during activation")
	}
	_ = s.db.Model(&database.AgentEnrollment{}).Where("node_id = ?", id).Update("last_error", "").Error
	s.invalidate(id)
	return nil
}

func (s *Service) agentActivationError(id string, reason error) error {
	_ = s.db.Model(&database.AgentEnrollment{}).Where("node_id = ?", id).Update("last_error", reason.Error()).Error
	_ = s.db.Model(&database.Node{}).Where("id = ? AND connection_type = ? AND engine_id = ''", id, ConnectionAgent).UpdateColumns(map[string]any{"status": "pairing", "last_error": reason.Error()}).Error
	return reason
}

func (s *Service) AgentDisconnected(id string) {
	_ = s.db.Model(&database.Node{}).Where("id = ? AND connection_type = ? AND engine_id <> ''", id, ConnectionAgent).UpdateColumns(map[string]any{"status": "offline", "last_error": "Agent disconnected"}).Error
	s.invalidate(id)
}

func (s *Service) RevokeAgent(ctx context.Context, id string) error {
	now := time.Now()
	if err := s.db.WithContext(ctx).Model(&database.AgentCredential{}).Where("node_id = ?", id).Update("revoked_at", now).Error; err != nil {
		return err
	}
	if s.agents != nil {
		s.agents.Disconnect(id)
	}
	_ = s.db.WithContext(ctx).Model(&database.Node{}).Where("id = ? AND connection_type = ?", id, ConnectionAgent).UpdateColumns(map[string]any{"status": "offline", "last_error": "Agent credential revoked"}).Error
	_ = s.db.WithContext(ctx).Model(&database.AgentEnrollment{}).Where("node_id = ?", id).Update("last_error", "").Error
	s.invalidate(id)
	return nil
}
