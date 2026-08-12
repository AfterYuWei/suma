package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	containerdomain "github.com/dockport/dockport/server/internal/container"
	"github.com/dockport/dockport/server/internal/database"
	"github.com/dockport/dockport/server/internal/task"
	"gorm.io/gorm"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type Project struct {
	ID          uint      `json:"id"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Status      string    `json:"status"`
	Services    int       `json:"services"`
	Containers  int       `json:"containers"`
	Compose     string    `json:"compose"`
	Environment string    `json:"environment"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type Service struct {
	db         *gorm.DB
	root       string
	runner     Runner
	tasks      *task.Service
	containers containerdomain.Service
}

func NewService(db *gorm.DB, root string, runner Runner, tasks *task.Service, containers containerdomain.Service) (*Service, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}
	return &Service{db: db, root: absolute, runner: runner, tasks: tasks, containers: containers}, nil
}
func (s *Service) List(ctx context.Context) ([]Project, error) {
	var rows []database.ComposeProject
	if err := s.db.WithContext(ctx).Order("updated_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	containers, _ := s.containers.List(ctx)
	result := make([]Project, 0, len(rows))
	for _, row := range rows {
		result = append(result, decorate(Project{ID: row.ID, Name: row.Name, Path: row.Path, Status: "stopped", CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, containers))
	}
	return result, nil
}
func (s *Service) Get(ctx context.Context, name string) (Project, error) {
	var row database.ComposeProject
	if err := s.db.WithContext(ctx).Where("name = ?", name).First(&row).Error; err != nil {
		return Project{}, err
	}
	compose, err := os.ReadFile(filepath.Join(row.Path, "compose.yml"))
	if err != nil {
		return Project{}, err
	}
	environment, err := os.ReadFile(filepath.Join(row.Path, ".env"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Project{}, err
	}
	project := Project{ID: row.ID, Name: row.Name, Path: row.Path, Status: "stopped", Compose: string(compose), Environment: string(environment), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	containers, _ := s.containers.List(ctx)
	return decorate(project, containers), nil
}

func decorate(project Project, containers []containerdomain.Summary) Project {
	services, running := map[string]struct{}{}, 0
	for _, row := range containers {
		if row.Labels["com.docker.compose.project"] != project.Name {
			continue
		}
		project.Containers++
		if row.State == "running" {
			running++
		}
		if name := row.Labels["com.docker.compose.service"]; name != "" {
			services[name] = struct{}{}
		}
	}
	project.Services = len(services)
	if running > 0 && running == project.Containers {
		project.Status = "running"
	} else if running > 0 {
		project.Status = "degraded"
	}
	return project
}
func (s *Service) Create(ctx context.Context, name, content, environment string) (Project, error) {
	if !validName.MatchString(name) {
		return Project{}, fmt.Errorf("invalid project name")
	}
	path, err := s.safePath(name)
	if err != nil {
		return Project{}, err
	}
	if err := os.Mkdir(path, 0o750); err != nil {
		return Project{}, fmt.Errorf("create project directory: %w", err)
	}
	if err := writeAtomic(filepath.Join(path, "compose.yml"), content); err != nil {
		return Project{}, err
	}
	if err := writeAtomic(filepath.Join(path, ".env"), environment); err != nil {
		return Project{}, err
	}
	row := database.ComposeProject{NodeID: "local", Name: name, Path: path}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return Project{}, err
	}
	return s.Get(ctx, name)
}
func (s *Service) Save(ctx context.Context, name, content, environment string) (Project, error) {
	project, err := s.Get(ctx, name)
	if err != nil {
		return Project{}, err
	}
	if err := writeAtomic(filepath.Join(project.Path, "compose.yml"), content); err != nil {
		return Project{}, err
	}
	if err := writeAtomic(filepath.Join(project.Path, ".env"), environment); err != nil {
		return Project{}, err
	}
	s.db.WithContext(ctx).Model(&database.ComposeProject{}).Where("name = ?", name).Update("updated_at", time.Now())
	return s.Get(ctx, name)
}
func (s *Service) Remove(ctx context.Context, name string) error {
	project, err := s.Get(ctx, name)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Where("name = ?", name).Delete(&database.ComposeProject{}).Error; err != nil {
		return err
	}
	return os.RemoveAll(project.Path)
}
func (s *Service) Services(ctx context.Context, name string) ([]containerdomain.Summary, error) {
	if _, err := s.Get(ctx, name); err != nil {
		return nil, err
	}
	rows, err := s.containers.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]containerdomain.Summary, 0)
	for _, row := range rows {
		if row.Labels["com.docker.compose.project"] == name {
			result = append(result, row)
		}
	}
	return result, nil
}
func (s *Service) Logs(ctx context.Context, name string) (string, error) {
	project, err := s.Get(ctx, name)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	if err := s.runner.Logs(ctx, project.Path, &output); err != nil {
		return output.String(), err
	}
	return output.String(), nil
}
func (s *Service) Validate(ctx context.Context, name, content, environment string) error {
	project, err := s.Get(ctx, name)
	if err != nil {
		return err
	}
	temp, err := os.MkdirTemp(s.root, ".validate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if err := writeAtomic(filepath.Join(temp, "compose.yml"), content); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(temp, ".env"), environment); err != nil {
		return err
	}
	_ = project
	return s.runner.Validate(ctx, temp, io.Discard)
}
func (s *Service) Action(ctx context.Context, name, action string) (database.Task, error) {
	project, err := s.Get(ctx, name)
	if err != nil {
		return database.Task{}, err
	}
	return s.tasks.Start("compose."+action, strings.Title(action)+" "+name, func(ctx context.Context, report task.Reporter) error {
		writer := &reportWriter{report: report}
		report(1, "Starting docker compose "+action)
		switch action {
		case "up":
			err = s.runner.Up(ctx, project.Path, writer)
		case "down":
			err = s.runner.Down(ctx, project.Path, writer)
		case "start":
			err = s.runner.Start(ctx, project.Path, writer)
		case "stop":
			err = s.runner.Stop(ctx, project.Path, writer)
		case "restart":
			err = s.runner.Restart(ctx, project.Path, writer)
		case "pull":
			err = s.runner.Pull(ctx, project.Path, writer)
		case "build":
			err = s.runner.Build(ctx, project.Path, writer)
		case "update":
			if err = s.runner.Pull(ctx, project.Path, writer); err == nil {
				err = s.runner.Up(ctx, project.Path, writer)
			}
		default:
			err = fmt.Errorf("unknown compose action")
		}
		return err
	})
}
func (s *Service) safePath(name string) (string, error) {
	value := filepath.Join(s.root, name)
	relative, err := filepath.Rel(s.root, value)
	if err != nil || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		return "", fmt.Errorf("project path escapes compose root")
	}
	return value, nil
}
func writeAtomic(path, content string) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".dockport-")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o640); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.WriteString(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

type reportWriter struct {
	report task.Reporter
	buffer strings.Builder
}

func (w *reportWriter) Write(value []byte) (int, error) {
	w.buffer.Write(value)
	for {
		text := w.buffer.String()
		index := strings.IndexByte(text, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSpace(text[:index])
		w.buffer.Reset()
		w.buffer.WriteString(text[index+1:])
		if line != "" {
			w.report(50, line)
		}
	}
	return len(value), nil
}
