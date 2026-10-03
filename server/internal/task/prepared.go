package task

import (
	"context"
	"github.com/suma/suma/server/internal/database"
	domainEvent "github.com/suma/suma/server/internal/event"
	"gorm.io/gorm"
)

// Prepare inserts a pending task in the caller's transaction. Launch must only
// be called after the transaction commits; recovery never replays pending work.
func (s *Service) Prepare(tx *gorm.DB, nodeID, kind, name string) (database.Task, error) {
	id, err := randomID()
	if err != nil {
		return database.Task{}, err
	}
	row := database.Task{ID: id, Scope: ScopeNode, NodeID: nodeID, Type: kind, Name: name, Status: StatusPending}
	return row, tx.Create(&row).Error
}
func (s *Service) Launch(row database.Task, work Work) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if _, exists := s.cancels[row.ID]; exists {
		s.mu.Unlock()
		cancel()
		return
	}
	s.cancels[row.ID] = cancel
	s.mu.Unlock()
	go s.run(ctx, row, work)
}
func (s *Service) SetEventSink(sink domainEvent.Sink) { s.mu.Lock(); s.sink = sink; s.mu.Unlock() }
