package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	containerdomain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type Project struct {
	NodeID      string    `json:"node_id"`
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Source      string    `json:"source"`
	CanManage   bool      `json:"can_manage"`
	ConfigFiles []string  `json:"config_files"`
	Status      string    `json:"status"`
	Services    int       `json:"services"`
	Containers  int       `json:"containers"`
	Compose     string    `json:"compose"`
	Environment string    `json:"environment"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type BatchResult struct {
	Name    string `json:"name"`
	TaskID  string `json:"task_id,omitempty"`
	Success bool   `json:"success"`
}
type Service struct {
	root       string
	runner     Runner
	tasks      *task.Service
	containers containerdomain.Service
	nodeID     string
	nodeName   string
}

func NewService(_ *gorm.DB, root string, runner Runner, tasks *task.Service, containers containerdomain.Service) (*Service, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}
	return &Service{root: absolute, runner: runner, tasks: tasks, containers: containers, nodeID: "local", nodeName: "Local"}, nil
}
func (s *Service) ForNode(nodeID, nodeName string, runner Runner, containers containerdomain.Service) *Service {
	return &Service{root: s.root, runner: runner, tasks: s.tasks, containers: containers, nodeID: nodeID, nodeName: nodeName}
}
func (s *Service) List(ctx context.Context) ([]Project, error) {
	containers, err := s.containers.List(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := s.managedProjects()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]Project, len(projects))
	for _, project := range projects {
		byName[project.Name] = project
	}
	for _, container := range containers {
		name := strings.TrimSpace(container.Labels[composeProjectLabel])
		if name == "" {
			continue
		}
		project, exists := byName[name]
		if !exists {
			project = externalProjectFromContainer(s.effectiveNodeID(), name, container)
		} else if project.Path == "" {
			project.Path = container.Labels[composeWorkingDirLabel]
		}
		if len(project.ConfigFiles) == 0 {
			project.ConfigFiles = composeConfigFiles(container.Labels[composeConfigFilesLabel], project.Path)
		}
		byName[name] = project
	}
	result := make([]Project, 0, len(byName))
	for _, project := range byName {
		result = append(result, decorate(project, containers))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].Name < result[j].Name
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}
func (s *Service) Get(ctx context.Context, name string) (Project, error) {
	project, err := s.findProject(ctx, name)
	if err != nil {
		return Project{}, err
	}
	if !project.CanManage {
		return project, nil
	}
	compose, err := os.ReadFile(filepath.Join(project.Path, "compose.yml"))
	if err != nil {
		return Project{}, err
	}
	project.Compose = string(compose)
	environment, err := os.ReadFile(filepath.Join(project.Path, ".env"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Project{}, err
	}
	project.Environment = string(environment)
	return project, nil
}

const (
	composeProjectLabel     = "com.docker.compose.project"
	composeServiceLabel     = "com.docker.compose.service"
	composeWorkingDirLabel  = "com.docker.compose.project.working_dir"
	composeConfigFilesLabel = "com.docker.compose.project.config_files"
)

func decorate(project Project, containers []containerdomain.Summary) Project {
	services, running := map[string]struct{}{}, 0
	for _, row := range containers {
		if row.Labels[composeProjectLabel] != project.Name {
			continue
		}
		project.Containers++
		if row.Created.After(project.UpdatedAt) {
			project.UpdatedAt = row.Created
		}
		if row.State == "running" {
			running++
		}
		if name := row.Labels[composeServiceLabel]; name != "" {
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
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
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
	return s.Get(ctx, name)
}

// Import copies a single-file local Compose project discovered through Docker
// labels into SUMA's managed Compose root. It never follows a label outside the
// reported working directory and is intentionally unavailable for remote nodes.
func (s *Service) Import(ctx context.Context, name string) (Project, error) {
	if s.effectiveNodeID() != "local" {
		return Project{}, fmt.Errorf("external Compose import is available only for the local node")
	}
	project, err := s.findProject(ctx, name)
	if err != nil {
		return Project{}, err
	}
	if project.CanManage {
		return Project{}, fmt.Errorf("Compose project is already managed")
	}
	if len(project.ConfigFiles) != 1 {
		return Project{}, fmt.Errorf("only single-file Compose projects can be imported")
	}
	compose, err := readExternalProjectFile(project.Path, project.ConfigFiles[0], true)
	if err != nil {
		return Project{}, fmt.Errorf("read external Compose file: %w", err)
	}
	environment, err := readExternalProjectFile(project.Path, filepath.Join(project.Path, ".env"), false)
	if err != nil {
		return Project{}, fmt.Errorf("read external Compose environment: %w", err)
	}
	temp, err := os.MkdirTemp(s.nodeRoot(), ".import-")
	if err != nil {
		return Project{}, err
	}
	defer os.RemoveAll(temp)
	if err := writeAtomic(filepath.Join(temp, "compose.yml"), string(compose)); err != nil {
		return Project{}, err
	}
	if err := writeAtomic(filepath.Join(temp, ".env"), string(environment)); err != nil {
		return Project{}, err
	}
	if err := s.runner.Validate(ctx, temp, io.Discard); err != nil {
		return Project{}, fmt.Errorf("validate imported Compose project: %w", err)
	}
	return s.Create(ctx, name, string(compose), string(environment))
}
func (s *Service) Save(ctx context.Context, name, content, environment string) (Project, error) {
	project, err := s.managedProject(name)
	if err != nil {
		return Project{}, err
	}
	if err := writeAtomic(filepath.Join(project.Path, "compose.yml"), content); err != nil {
		return Project{}, err
	}
	if err := writeAtomic(filepath.Join(project.Path, ".env"), environment); err != nil {
		return Project{}, err
	}
	return s.Get(ctx, name)
}
func (s *Service) Remove(ctx context.Context, name string) error {
	project, err := s.managedProject(name)
	if err != nil {
		return err
	}
	expectedPath, err := s.safePath(name)
	if err != nil || filepath.Clean(project.Path) != filepath.Clean(expectedPath) {
		return errors.New("stored Compose project path is outside the configured root")
	}
	return os.RemoveAll(project.Path)
}

// ForceRemove tears down runtime resources with no graceful-stop delay before
// removing SUMA-owned state. Named volumes are deleted unless explicitly preserved.
func (s *Service) ForceRemove(ctx context.Context, name string, preserveVolumes bool) error {
	project, err := s.managedProject(name)
	if err != nil {
		return err
	}
	expectedPath, err := s.safePath(name)
	if err != nil || filepath.Clean(project.Path) != filepath.Clean(expectedPath) {
		return errors.New("stored Compose project path is outside the configured root")
	}
	if err := s.runner.ForceDown(ctx, project.Path, preserveVolumes, io.Discard); err != nil {
		return fmt.Errorf("force down Compose project: %w", err)
	}
	return s.Remove(ctx, name)
}
func (s *Service) Services(ctx context.Context, name string) ([]containerdomain.Summary, error) {
	rows, err := s.containers.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]containerdomain.Summary, 0)
	for _, row := range rows {
		if row.Labels[composeProjectLabel] == name {
			result = append(result, row)
		}
	}
	if len(result) == 0 {
		if _, err := s.managedProject(name); err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (s *Service) Logs(ctx context.Context, name string) (string, error) {
	project, err := s.managedProject(name)
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
	if _, err := s.managedProject(name); err != nil {
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
	return s.runner.Validate(ctx, temp, io.Discard)
}
func (s *Service) Action(ctx context.Context, name, action string) (database.Task, error) {
	project, err := s.managedProject(name)
	if err != nil {
		return database.Task{}, err
	}
	return s.tasks.StartForNode(s.effectiveNodeID(), s.effectiveNodeName(), "compose."+action, strings.Title(action)+" "+name, func(ctx context.Context, report task.Reporter) error {
		var err error
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

func (s *Service) managedProjects() ([]Project, error) {
	base := s.nodeRoot()
	if err := os.MkdirAll(base, 0o750); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	projects := make([]Project, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !validName.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(base, entry.Name())
		composePath := filepath.Join(path, "compose.yml")
		info, err := os.Stat(composePath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		projects = append(projects, Project{
			NodeID: s.effectiveNodeID(), Name: entry.Name(), Path: path,
			Source: "managed", CanManage: true, ConfigFiles: []string{composePath},
			Status: "stopped", CreatedAt: info.ModTime(), UpdatedAt: info.ModTime(),
		})
	}
	return projects, nil
}

func (s *Service) managedProject(name string) (Project, error) {
	if !validName.MatchString(name) {
		return Project{}, fmt.Errorf("invalid project name")
	}
	path, err := s.safePath(name)
	if err != nil {
		return Project{}, err
	}
	info, err := os.Stat(filepath.Join(path, "compose.yml"))
	if err != nil || !info.Mode().IsRegular() {
		return Project{}, fmt.Errorf("Compose project is external or unavailable")
	}
	return Project{
		NodeID: s.effectiveNodeID(), Name: name, Path: path,
		Source: "managed", CanManage: true, ConfigFiles: []string{filepath.Join(path, "compose.yml")},
		Status: "stopped", CreatedAt: info.ModTime(), UpdatedAt: info.ModTime(),
	}, nil
}

func (s *Service) findProject(ctx context.Context, name string) (Project, error) {
	projects, err := s.List(ctx)
	if err != nil {
		return Project{}, err
	}
	for _, project := range projects {
		if project.Name == name {
			return project, nil
		}
	}
	return Project{}, fmt.Errorf("Compose project not found")
}

func (s *Service) nodeRoot() string {
	if s.effectiveNodeID() == "local" {
		return s.root
	}
	return filepath.Join(s.root, s.effectiveNodeID())
}

func externalProjectFromContainer(nodeID, name string, container containerdomain.Summary) Project {
	path := strings.TrimSpace(container.Labels[composeWorkingDirLabel])
	return Project{
		NodeID: nodeID, Name: name, Path: path, Source: "external", CanManage: false,
		ConfigFiles: composeConfigFiles(container.Labels[composeConfigFilesLabel], path),
		Status:      "stopped", CreatedAt: container.Created, UpdatedAt: container.Created,
	}
}

func composeConfigFiles(value, workingDir string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !filepath.IsAbs(part) && workingDir != "" {
			part = filepath.Join(workingDir, part)
		}
		result = append(result, filepath.Clean(part))
	}
	return result
}

func readExternalProjectFile(workingDir, name string, required bool) ([]byte, error) {
	if !filepath.IsAbs(workingDir) || !filepath.IsAbs(name) {
		return nil, fmt.Errorf("Compose working directory and file must be absolute")
	}
	base, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		return nil, fmt.Errorf("working directory is not mounted into SUMA")
	}
	path, err := filepath.EvalSymlinks(name)
	if err != nil {
		if !required && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("file is not mounted into SUMA")
	}
	relative, err := filepath.Rel(base, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, fmt.Errorf("file is outside the Compose working directory")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("file is not regular")
	}
	if info.Size() > 2<<20 {
		return nil, fmt.Errorf("file exceeds 2 MiB")
	}
	return os.ReadFile(path)
}

func (s *Service) BatchAction(ctx context.Context, names []string, action string) []BatchResult {
	results := make([]BatchResult, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			results = append(results, BatchResult{Name: name, Success: false})
			continue
		}
		row, err := s.Action(ctx, name, action)
		results = append(results, BatchResult{Name: name, TaskID: row.ID, Success: err == nil})
	}
	return results
}

func (s *Service) safePath(name string) (string, error) {
	base := s.nodeRoot()
	value := filepath.Join(base, name)
	relative, err := filepath.Rel(base, value)
	if err != nil || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		return "", fmt.Errorf("project path escapes compose root")
	}
	return value, nil
}
func (s *Service) effectiveNodeID() string {
	if s.nodeID == "" {
		return "local"
	}
	return s.nodeID
}
func (s *Service) effectiveNodeName() string {
	if s.nodeName == "" {
		return "Local"
	}
	return s.nodeName
}
func writeAtomic(path, content string) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".suma-")
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
