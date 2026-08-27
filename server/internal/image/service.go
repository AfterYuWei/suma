package image

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	return s.tasks.StartForNode(nodeID, nodeName, "image.pull", "Pull "+reference, func(ctx context.Context, report task.Reporter) error {
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
				return fmt.Errorf("%s", event.Error)
			}
			if event.ProgressDetail.Total > 0 {
				progress = int(float64(event.ProgressDetail.Current) / float64(event.ProgressDetail.Total) * 90)
			}
			message := event.Status
			if event.ID != "" {
				message = event.ID + ": " + message
			}
			report(progress, message)
		}
		if err := scanner.Err(); err != nil {
			return err
		}
		report(100, "Pull complete")
		return nil
	})
}
