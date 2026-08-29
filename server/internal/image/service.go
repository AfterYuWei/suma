package image

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
)

type Summary struct {
	ID         string            `json:"id"`
	Tags       []string          `json:"tags"`
	Digests    []string          `json:"digests"`
	Size       int64             `json:"size"`
	Created    time.Time         `json:"created"`
	Containers int64             `json:"containers"`
	Labels     map[string]string `json:"labels"`
}
type Detail struct {
	Summary
	Architecture  string   `json:"architecture"`
	OS            string   `json:"os"`
	Author        string   `json:"author"`
	DockerVersion string   `json:"docker_version"`
	Layers        []string `json:"layers"`
}
type Adapter interface {
	ListImages(context.Context) ([]Summary, error)
	InspectImage(context.Context, string) (Detail, error)
	PullImage(context.Context, string) (io.ReadCloser, error)
	RemoveImage(context.Context, string, bool) error
	TagImage(context.Context, string, string) error
}
type AuthenticatedAdapter interface {
	PullImageAuthenticated(context.Context, string, string, string, string, bool) (io.ReadCloser, error)
}
type Service struct {
	adapter Adapter
	tasks   *task.Service
}

func NewService(adapter Adapter, tasks *task.Service) *Service {
	return &Service{adapter: adapter, tasks: tasks}
}
func (s *Service) List(ctx context.Context) ([]Summary, error) { return s.adapter.ListImages(ctx) }
func (s *Service) Get(ctx context.Context, id string) (Detail, error) {
	return s.adapter.InspectImage(ctx, id)
}
func (s *Service) Remove(ctx context.Context, id string, force bool) error {
	return s.adapter.RemoveImage(ctx, id, force)
}
func (s *Service) Tag(ctx context.Context, id, reference string) error {
	return s.adapter.TagImage(ctx, id, reference)
}
func (s *Service) Pull(reference string) (database.Task, error) {
	return s.PullForNode("local", "Local", reference)
}
func (s *Service) PullForNode(nodeID, nodeName, reference string) (database.Task, error) {
	return s.PullForNodeWithRegistry(nodeID, nodeName, reference, "", "", "", false)
}
func (s *Service) PullForNodeWithRegistry(nodeID, nodeName, reference, server, username, secret string, token bool) (database.Task, error) {
	return s.tasks.StartWithIDForNode(nodeID, nodeName, "image.pull", "Pull "+reference, func(ctx context.Context, taskID string, report task.Reporter) error {
		var stream io.ReadCloser
		var err error
		if server != "" {
			authenticated, ok := s.adapter.(AuthenticatedAdapter)
			if !ok {
				return fmt.Errorf("Docker adapter does not support registry authentication")
			}
			stream, err = authenticated.PullImageAuthenticated(ctx, reference, server, username, secret, token)
		} else {
			stream, err = s.adapter.PullImage(ctx, reference)
		}
		if err != nil {
			return err
		}
		defer stream.Close()
		scanner := bufio.NewScanner(stream)
		progress := 1
		layers := map[string]*pullLayer{}
		for scanner.Scan() {
			var event struct {
				Status         string `json:"status"`
				ID             string `json:"id"`
				Error          string `json:"error"`
				ProgressDetail struct {
					Current int64 `json:"current"`
					Total   int64 `json:"total"`
				} `json:"progressDetail"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) != nil {
				continue
			}
			if event.Error != "" {
				markIncompleteLayers(s.tasks, taskID, layers, "Failed", false)
				return fmt.Errorf("%s", event.Error)
			}
			if event.ID != "" && isLayerStatus(event.Status) {
				layer := layers[event.ID]
				if layer == nil {
					layer = &pullLayer{id: event.ID}
					layers[event.ID] = layer
				}
				layer.update(event.Status, event.ProgressDetail.Current, event.ProgressDetail.Total)
				s.tasks.ReportStep(ctx, taskID, layer.id, layer.status, layer.current, layer.total, layer.progress)
				progress = aggregateLayerProgress(layers)
			}
			message := event.Status
			if event.ID != "" {
				message = event.ID + ": " + message
			}
			report(progress, message)
		}
		if ctx.Err() != nil {
			markIncompleteLayers(s.tasks, taskID, layers, "Canceled", false)
			return ctx.Err()
		}
		if err := scanner.Err(); err != nil {
			markIncompleteLayers(s.tasks, taskID, layers, "Failed", false)
			return err
		}
		markIncompleteLayers(s.tasks, taskID, layers, "Pull complete", true)
		report(100, "Pull complete")
		return nil
	})
}

type pullLayer struct {
	id       string
	status   string
	current  int64
	total    int64
	progress int
}

func (l *pullLayer) update(status string, current, total int64) {
	l.status = status
	if total > 0 {
		l.current, l.total = current, total
	}
	normalized := strings.ToLower(status)
	switch {
	case normalized == "pull complete" || normalized == "already exists":
		l.progress = 100
	case normalized == "download complete" || normalized == "verifying checksum":
		if l.progress < 80 {
			l.progress = 80
		}
	case total > 0 && normalized == "downloading":
		l.progress = int(float64(current) / float64(total) * 80)
	case total > 0 && normalized == "extracting":
		l.progress = 80 + int(float64(current)/float64(total)*19)
	}
	if l.progress > 100 {
		l.progress = 100
	}
}

func isLayerStatus(status string) bool {
	switch strings.ToLower(status) {
	case "pulling fs layer", "waiting", "downloading", "verifying checksum", "download complete", "extracting", "pull complete", "already exists":
		return true
	default:
		return false
	}
}

func aggregateLayerProgress(layers map[string]*pullLayer) int {
	if len(layers) == 0 {
		return 1
	}
	total := 0
	for _, layer := range layers {
		total += layer.progress
	}
	return total / len(layers) * 95 / 100
}

func markIncompleteLayers(tasks *task.Service, taskID string, layers map[string]*pullLayer, status string, complete bool) {
	for _, layer := range layers {
		if layer.progress >= 100 {
			continue
		}
		layer.status = status
		if complete {
			layer.progress = 100
		}
		tasks.ReportStep(context.Background(), taskID, layer.id, layer.status, layer.current, layer.total, layer.progress)
	}
}
