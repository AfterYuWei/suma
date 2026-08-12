package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/dockport/dockport/server/internal/database"
	"gorm.io/gorm"
)

const (
	StatusPending  = "pending"
	StatusRunning  = "running"
	StatusSuccess  = "success"
	StatusFailed   = "failed"
	StatusCanceled = "canceled"
)

type Event struct {
	Type     string    `json:"type"`
	Status   string    `json:"status,omitempty"`
	Progress int       `json:"progress,omitempty"`
	Message  string    `json:"message,omitempty"`
	Time     time.Time `json:"time"`
}
type Reporter func(progress int, message string)
type Work func(context.Context, Reporter) error
type Service struct {
	db          *gorm.DB
	mu          sync.RWMutex
	subscribers map[string]map[chan Event]struct{}
	cancels     map[string]context.CancelFunc
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db, subscribers: map[string]map[chan Event]struct{}{}, cancels: map[string]context.CancelFunc{}}
}

func (s *Service) Start(taskType, name string, work Work) (database.Task, error) {
	id, err := randomID()
	if err != nil {
		return database.Task{}, err
	}
	row := database.Task{ID: id, NodeID: "local", Type: taskType, Name: name, Status: StatusPending}
	if err := s.db.Create(&row).Error; err != nil {
		return row, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()
	go s.run(ctx, row, work)
	return row, nil
}

func (s *Service) run(ctx context.Context, row database.Task, work Work) {
	now := time.Now()
	s.db.Model(&database.Task{}).Where("id = ?", row.ID).Updates(map[string]any{"status": StatusRunning, "started_at": now})
	s.publish(row.ID, Event{Type: "status", Status: StatusRunning, Time: now})
	reporter := func(progress int, message string) {
		if progress < 0 {
			progress = 0
		}
		if progress > 100 {
			progress = 100
		}
		s.db.Model(&database.Task{}).Where("id = ?", row.ID).Updates(map[string]any{"progress": progress, "message": message})
		s.db.Create(&database.TaskLog{TaskID: row.ID, Level: "info", Message: message})
		s.publish(row.ID, Event{Type: "progress", Progress: progress, Message: message, Time: time.Now()})
	}
	err := work(ctx, reporter)
	status, message := StatusSuccess, "Completed"
	if err != nil {
		status, message = StatusFailed, err.Error()
	}
	if ctx.Err() != nil {
		status, message = StatusCanceled, "Canceled"
	}
	finished := time.Now()
	progress := 100
	if status != StatusSuccess {
		progress = 0
	}
	s.db.Model(&database.Task{}).Where("id = ?", row.ID).Updates(map[string]any{"status": status, "progress": progress, "message": message, "finished_at": finished})
	s.db.Create(&database.TaskLog{TaskID: row.ID, Level: map[bool]string{true: "info", false: "error"}[status == StatusSuccess], Message: message})
	s.publish(row.ID, Event{Type: "status", Status: status, Progress: progress, Message: message, Time: finished})
	s.mu.Lock()
	delete(s.cancels, row.ID)
	s.mu.Unlock()
}

func (s *Service) List(ctx context.Context) ([]database.Task, error) {
	var rows []database.Task
	return rows, s.db.WithContext(ctx).Order("created_at DESC").Limit(200).Find(&rows).Error
}
func (s *Service) Logs(ctx context.Context, id string) ([]database.TaskLog, error) {
	var rows []database.TaskLog
	return rows, s.db.WithContext(ctx).Where("task_id = ?", id).Order("created_at ASC").Find(&rows).Error
}
func (s *Service) Cancel(id string) bool {
	s.mu.RLock()
	cancel, ok := s.cancels[id]
	s.mu.RUnlock()
	if ok {
		cancel()
	}
	return ok
}
func (s *Service) Subscribe(id string) (<-chan Event, func()) {
	channel := make(chan Event, 32)
	s.mu.Lock()
	if s.subscribers[id] == nil {
		s.subscribers[id] = map[chan Event]struct{}{}
	}
	s.subscribers[id][channel] = struct{}{}
	s.mu.Unlock()
	return channel, func() { s.mu.Lock(); delete(s.subscribers[id], channel); close(channel); s.mu.Unlock() }
}
func (s *Service) publish(id string, event Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for channel := range s.subscribers[id] {
		select {
		case channel <- event:
		default:
		}
	}
}
func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate task ID: %w", err)
	}
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
