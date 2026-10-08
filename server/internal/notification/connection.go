package notification

import (
	"context"
	"fmt"
	"strings"

	"github.com/suma/suma/server/internal/redact"
)

type connectionEntry struct {
	generation string
	ConnectionStatus
}

func needsConnection(c Channel) bool {
	return c.Enabled && (c.Provider == "feishu_app" || c.Provider == "telegram" && c.Config.Interactive)
}

func (s *Service) Connection(ctx context.Context, id string) (ConnectionStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channel, err := s.Channel(ctx, id)
	if err != nil {
		return ConnectionStatus{}, err
	}
	if channel.Provider != "feishu_app" {
		return ConnectionStatus{}, fmt.Errorf("%w: connection status is available for Feishu applications", ErrInvalid)
	}
	if entry, ok := s.connections[id]; ok {
		return entry.ConnectionStatus, nil
	}
	return ConnectionStatus{State: "stopped"}, nil
}

func (s *Service) connectionState(id, generation string, state ConnectionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.connections[id]
	if !ok || entry.generation != generation {
		return
	}
	state.Error = redact.Bounded(state.Error, 512)
	entry.ConnectionStatus = state
	s.connections[id] = entry
}

func (s *Service) stopChatLocked(id string) {
	if cancel := s.chatCancel[id]; cancel != nil {
		cancel()
		delete(s.chatCancel, id)
	}
	s.connections[id] = connectionEntry{ConnectionStatus: ConnectionStatus{State: "stopped"}}
}

func validChatID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func validateTargets(targets []Target) error {
	if len(targets) > 50 {
		return fmt.Errorf("%w: at most 50 recipients per channel", ErrInvalid)
	}
	seen := map[string]bool{}
	for i := range targets {
		targets[i].ChatID = strings.TrimSpace(targets[i].ChatID)
		targets[i].Name = strings.TrimSpace(targets[i].Name)
		if !validChatID(targets[i].ChatID) || seen[targets[i].ChatID] || len(targets[i].Name) > 128 {
			return fmt.Errorf("%w: invalid or duplicate recipient", ErrInvalid)
		}
		if targets[i].Name == "" {
			targets[i].Name = targets[i].ChatID
		}
		seen[targets[i].ChatID] = true
	}
	return nil
}
func hasTarget(targets []Target, id string) bool {
	for _, target := range targets {
		if target.ChatID == id {
			return true
		}
	}
	return false
}
func targetIDs(targets []Target) []string {
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.ChatID)
	}
	return ids
}
