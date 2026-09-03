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
	"strconv"
	"strings"
	"sync"
	"time"

	containerdomain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/database"
	projectdomain "github.com/suma/suma/server/internal/project"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type Project struct {
	projectdomain.Summary
	Path        string                  `json:"path"`
	CanManage   bool                    `json:"can_manage"`
	ConfigFiles []string                `json:"config_files"`
	Services    int                     `json:"services"`
	Containers  int                     `json:"containers"`
	Compose     string                  `json:"compose"`
	Environment string                  `json:"environment"`
	Metadata    *ManagedProjectMetadata `json:"metadata,omitempty"`
}

type BatchResult struct {
	Name    string `json:"name"`
	TaskID  string `json:"task_id,omitempty"`
	Success bool   `json:"success"`
}
type Service struct {
	root         string
	runner       Runner
	tasks        *task.Service
	containers   containerdomain.Service
	nodeID       string
	nodeName     string
	localSources bool
	instanceID   string
	projectLocks *sync.Map
}

func NewService(_ *gorm.DB, root string, runner Runner, tasks *task.Service, containers containerdomain.Service) (*Service, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}
	return &Service{root: absolute, runner: runner, tasks: tasks, containers: containers, nodeID: "local", nodeName: "Local", localSources: true, instanceID: fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano()), projectLocks: &sync.Map{}}, nil
}
func (s *Service) ForNode(nodeID, nodeName string, runner Runner, containers containerdomain.Service, localSources bool) *Service {
	return &Service{root: s.root, runner: runner, tasks: s.tasks, containers: containers, nodeID: nodeID, nodeName: nodeName, localSources: localSources, instanceID: s.instanceID, projectLocks: s.projectLocks}
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

// ListSummaries returns the backend-neutral Projects list contract. Compose
// content, environment values, source paths, and backend detail stay out of
// list responses and are loaded only from the backend-specific detail API.
func (s *Service) ListSummaries(ctx context.Context) ([]projectdomain.Summary, error) {
	projects, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]projectdomain.Summary, 0, len(projects))
	for _, project := range projects {
		summaries = append(summaries, project.Summary)
	}
	return summaries, nil
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
	composeProjectLabel     = ProjectLabel
	composeServiceLabel     = ServiceLabel
	composeWorkingDirLabel  = WorkingDirLabel
	composeConfigFilesLabel = ConfigFilesLabel
)

func (s *Service) Observe(ctx context.Context, name string) (ObservedComposeProject, error) {
	if _, err := s.findProject(ctx, name); err != nil {
		return ObservedComposeProject{}, err
	}
	inspector, ok := s.containers.(RuntimeInspector)
	if !ok {
		return ObservedComposeProject{}, errors.New("Docker runtime does not support Compose Project inspection")
	}
	snapshot, err := inspector.InspectComposeProject(ctx, name)
	if err != nil {
		return ObservedComposeProject{}, err
	}
	return ObserveRuntimeProject(snapshot), nil
}

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
	project.ServiceCount = project.Services
	project.InstanceCount = project.Containers
	if running > 0 && running == project.Containers {
		project.Status = "running"
	} else if running > 0 {
		project.Status = "degraded"
	}
	project.Managed = project.CanManage
	project.Capabilities = projectdomain.ComposeCapabilities(project.CanManage)
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
	metadata := newManagedProjectMetadata(s.effectiveNodeID(), name, "created", "", time.Now().UTC())
	if err := writeManagedProjectMetadata(path, metadata); err != nil {
		return Project{}, err
	}
	return s.Get(ctx, name)
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

const defaultLogTail = 200
const maximumLogTail = 5000

func normalizeLogTail(tail int) int {
	if tail <= 0 {
		return defaultLogTail
	}
	return min(tail, maximumLogTail)
}

func (s *Service) Logs(ctx context.Context, name string, tail int) (string, error) {
	project, err := s.managedProject(name)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	if err := s.runner.Logs(ctx, project.Path, normalizeLogTail(tail), &output); err != nil {
		return output.String(), err
	}
	return output.String(), nil
}
func (s *Service) Validate(ctx context.Context, name, content, environment string) error {
	if _, err := s.managedProject(name); err != nil {
		return err
	}
	return s.ValidateDraft(ctx, content, environment)
}

// ValidateDraft validates an unsaved Compose Project without requiring a
// managed Project directory and without mutating runtime state.
func (s *Service) ValidateDraft(ctx context.Context, content, environment string) error {
	if err := validateManagedTakeoverContent(content); err != nil {
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
	return s.validateComposeProject(ctx, temp, environment)
}

func (s *Service) validateComposeProject(ctx context.Context, directory, environment string) error {
	var output strings.Builder
	if err := s.runner.Validate(ctx, directory, &output); err != nil {
		detail := composeValidationDetail(output.String(), environment)
		if detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	return nil
}

func composeValidationDetail(output, environment string) string {
	for _, line := range strings.Split(environment, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !sensitiveEnvironmentKey(strings.TrimSpace(strings.TrimPrefix(key, "export "))) {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		if value != "" {
			if len([]rune(value)) < 4 && strings.Contains(output, value) {
				return "Compose diagnostic omitted because it may contain a short sensitive environment value"
			}
			output = strings.ReplaceAll(output, value, "***")
			output = strings.ReplaceAll(output, strings.ReplaceAll(value, "\\'", "'"), "***")
		}
	}
	output = strings.TrimSpace(output)
	const maximumDetail = 4096
	if characters := []rune(output); len(characters) > maximumDetail {
		output = string(characters[:maximumDetail]) + "…"
	}
	return output
}
func (s *Service) Action(ctx context.Context, name, action string) (database.Task, error) {
	project, err := s.managedProject(name)
	if err != nil {
		return database.Task{}, err
	}
	return s.tasks.StartForNode(s.effectiveNodeID(), s.effectiveNodeName(), "compose."+action, strings.Title(action)+" "+name, func(ctx context.Context, report task.Reporter) error {
		var err error
		report(1, "Starting docker compose "+action)
		run := func(start, end int, operation func(io.Writer) error) error {
			writer := newReportWriter(report, start, end)
			err := operation(writer)
			writer.Flush()
			return err
		}
		switch action {
		case "up":
			err = run(5, 95, func(output io.Writer) error { return s.runner.Up(ctx, project.Path, output) })
		case "down":
			err = run(5, 95, func(output io.Writer) error { return s.runner.Down(ctx, project.Path, output) })
		case "start":
			err = run(5, 95, func(output io.Writer) error { return s.runner.Start(ctx, project.Path, output) })
		case "stop":
			err = run(5, 95, func(output io.Writer) error { return s.runner.Stop(ctx, project.Path, output) })
		case "restart":
			err = run(5, 95, func(output io.Writer) error { return s.runner.Restart(ctx, project.Path, output) })
		case "pull":
			err = run(5, 95, func(output io.Writer) error { return s.runner.Pull(ctx, project.Path, output) })
		case "build":
			err = run(5, 95, func(output io.Writer) error { return s.runner.Build(ctx, project.Path, output) })
		case "update":
			if err = run(5, 55, func(output io.Writer) error { return s.runner.Pull(ctx, project.Path, output) }); err == nil {
				report(60, "Images ready; recreating Compose services")
				err = run(60, 95, func(output io.Writer) error { return s.runner.Up(ctx, project.Path, output) })
			}
		default:
			err = fmt.Errorf("unknown compose action")
		}
		if err == nil && (action == "up" || action == "update") {
			_ = s.markDeployed(project)
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
		metadata, err := readManagedProjectMetadata(path, s.effectiveNodeID(), entry.Name(), info.ModTime())
		if err != nil {
			continue
		}
		summary := projectdomain.ComposeSummary(s.effectiveNodeID(), entry.Name(), "managed", "stopped", true)
		summary.CreatedAt, summary.UpdatedAt = metadata.ClaimedAt, info.ModTime()
		projects = append(projects, Project{Summary: summary, Path: path, CanManage: true, ConfigFiles: []string{composePath}, Metadata: &metadata})
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
	metadata, err := readManagedProjectMetadata(path, s.effectiveNodeID(), name, info.ModTime())
	if err != nil {
		return Project{}, err
	}
	summary := projectdomain.ComposeSummary(s.effectiveNodeID(), name, "managed", "stopped", true)
	summary.CreatedAt, summary.UpdatedAt = metadata.ClaimedAt, info.ModTime()
	return Project{Summary: summary, Path: path, CanManage: true, ConfigFiles: []string{filepath.Join(path, "compose.yml")}, Metadata: &metadata}, nil
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
	summary := projectdomain.ComposeSummary(nodeID, name, "external", "stopped", false)
	summary.CreatedAt, summary.UpdatedAt = container.Created, container.Created
	return Project{Summary: summary, Path: path, CanManage: false, ConfigFiles: composeConfigFiles(container.Labels[composeConfigFilesLabel], path)}
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

func (s *Service) lockProject(name string) func() {
	if s.projectLocks == nil {
		s.projectLocks = &sync.Map{}
	}
	key := s.effectiveNodeID() + "\x00" + name
	value, _ := s.projectLocks.LoadOrStore(key, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	return lock.Unlock
}

func (s *Service) markDeployed(project Project) error {
	metadata, err := readManagedProjectMetadata(project.Path, s.effectiveNodeID(), project.Name, project.UpdatedAt)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	metadata.LastDeployedAt = &now
	return writeManagedProjectMetadata(project.Path, metadata)
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
	report   task.Reporter
	buffer   strings.Builder
	start    int
	progress int
	maximum  int
}

var composeOutputPercentage = regexp.MustCompile(`([0-9]{1,3})%`)

func newReportWriter(report task.Reporter, start, maximum int) *reportWriter {
	return &reportWriter{report: report, start: start, progress: start, maximum: maximum}
}

func (w *reportWriter) Write(value []byte) (int, error) {
	w.buffer.Write(value)
	for {
		text := w.buffer.String()
		index := strings.IndexAny(text, "\r\n")
		if index < 0 {
			break
		}
		line := strings.TrimSpace(text[:index])
		w.buffer.Reset()
		w.buffer.WriteString(text[index+1:])
		w.reportLine(line)
	}
	return len(value), nil
}

// Flush reports a final Compose line even when the CLI did not terminate it
// with a newline. Some Compose progress modes use carriage returns or leave a
// final status fragment behind when stdout is not attached to a terminal.
func (w *reportWriter) Flush() {
	line := strings.TrimSpace(w.buffer.String())
	w.buffer.Reset()
	w.reportLine(line)
}

func (w *reportWriter) reportLine(line string) {
	if line == "" {
		return
	}
	if match := composeOutputPercentage.FindStringSubmatch(line); len(match) == 2 {
		if percentage, err := strconv.Atoi(match[1]); err == nil && percentage <= 100 {
			mapped := w.start + (w.maximum-w.start)*percentage/100
			if mapped > w.progress {
				w.progress = mapped
			}
		}
	} else if w.progress < w.maximum {
		step := max(1, (w.maximum-w.start)/20)
		w.progress = min(w.maximum, w.progress+step)
	}
	w.report(w.progress, line)
}
