package compose

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Runner interface {
	Up(context.Context, string, io.Writer) error
	Down(context.Context, string, io.Writer) error
	ForceDown(context.Context, string, bool, io.Writer) error
	Start(context.Context, string, io.Writer) error
	Stop(context.Context, string, io.Writer) error
	Restart(context.Context, string, io.Writer) error
	Pull(context.Context, string, io.Writer) error
	Build(context.Context, string, io.Writer) error
	Validate(context.Context, string, io.Writer) error
	Logs(context.Context, string, io.Writer) error
	Render(context.Context, ExecutionSpec, io.Writer) (string, error)
	ValidateRelease(context.Context, ExecutionSpec, io.Writer) error
	PullRelease(context.Context, ExecutionSpec, io.Writer) error
	UpRelease(context.Context, ExecutionSpec, int, io.Writer) error
	DownRelease(context.Context, ExecutionSpec, io.Writer) error
	ForceDownRelease(context.Context, ExecutionSpec, bool, io.Writer) error
	PS(context.Context, ExecutionSpec, io.Writer) (string, error)
	LogsRelease(context.Context, ExecutionSpec, io.Writer) error
}

type ExecutionSpec struct {
	ProjectName string
	ProjectDir  string
	Files       []string
	EnvFiles    []string
	Profiles    []string
}

type Target struct {
	NodeID       string
	NodeName     string
	Host         string
	TLSRequired  bool
	CA           string
	Certificate  string
	PrivateKey   string
	DockerConfig string
}

type CLIRunner struct {
	command string
	prefix  []string
	target  *Target
}

func (r *CLIRunner) ForTarget(target Target) *CLIRunner {
	copy := *r
	copy.target = &target
	return &copy
}
func (r *CLIRunner) Targeted(target Target) Runner { return r.ForTarget(target) }

func NewRunner(command string) (*CLIRunner, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil, fmt.Errorf("compose command is empty")
	}
	return &CLIRunner{command: parts[0], prefix: parts[1:]}, nil
}
func (r *CLIRunner) run(ctx context.Context, project string, output io.Writer, args ...string) error {
	values := append(append([]string{}, r.prefix...), "--project-directory", project)
	values = append(values, args...)
	command := exec.CommandContext(ctx, r.command, values...)
	command.Dir = project
	environment, cleanup, err := r.commandEnvironment()
	if err != nil {
		return err
	}
	defer cleanup()
	command.Env = environment
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		return fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
func (r *CLIRunner) runSpec(ctx context.Context, spec ExecutionSpec, output io.Writer, args ...string) error {
	values, err := r.arguments(spec)
	if err != nil {
		return err
	}
	values = append(values, args...)
	command := exec.CommandContext(ctx, r.command, values...)
	command.Dir = spec.ProjectDir
	environment, cleanup, err := r.commandEnvironment()
	if err != nil {
		return err
	}
	defer cleanup()
	command.Env = environment
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		return fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (r *CLIRunner) captureSpec(ctx context.Context, spec ExecutionSpec, output io.Writer, args ...string) (string, error) {
	values, err := r.arguments(spec)
	if err != nil {
		return "", err
	}
	values = append(values, args...)
	command := exec.CommandContext(ctx, r.command, values...)
	command.Dir = spec.ProjectDir
	environment, cleanup, err := r.commandEnvironment()
	if err != nil {
		return "", err
	}
	defer cleanup()
	command.Env = environment
	var stdout strings.Builder
	var stderr strings.Builder
	if output == nil {
		output = io.Discard
	}
	command.Stdout = &stdout
	command.Stderr = io.MultiWriter(&stderr, output)
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return stdout.String(), fmt.Errorf("docker compose %s: %w: %s", strings.Join(args, " "), err, message)
		}
		return stdout.String(), fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

func (r *CLIRunner) arguments(spec ExecutionSpec) ([]string, error) {
	if spec.ProjectName == "" || spec.ProjectDir == "" {
		return nil, fmt.Errorf("Compose project name and directory are required")
	}
	values := append(append([]string{}, r.prefix...), "--ansi", "never", "--project-name", spec.ProjectName, "--project-directory", spec.ProjectDir)
	for _, file := range spec.Files {
		values = append(values, "--file", file)
	}
	for _, file := range spec.EnvFiles {
		values = append(values, "--env-file", file)
	}
	for _, profile := range spec.Profiles {
		values = append(values, "--profile", profile)
	}
	return values, nil
}
func (r *CLIRunner) Up(ctx context.Context, project string, output io.Writer) error {
	return r.run(ctx, project, output, "up", "-d")
}
func (r *CLIRunner) Down(ctx context.Context, project string, output io.Writer) error {
	return r.run(ctx, project, output, "down")
}
func (r *CLIRunner) ForceDown(ctx context.Context, project string, preserveVolumes bool, output io.Writer) error {
	args := []string{"down", "--remove-orphans", "--timeout", "0"}
	if !preserveVolumes {
		args = append(args, "--volumes")
	}
	return r.run(ctx, project, output, args...)
}
func (r *CLIRunner) Start(ctx context.Context, project string, output io.Writer) error {
	return r.run(ctx, project, output, "start")
}
func (r *CLIRunner) Stop(ctx context.Context, project string, output io.Writer) error {
	return r.run(ctx, project, output, "stop")
}
func (r *CLIRunner) Restart(ctx context.Context, project string, output io.Writer) error {
	return r.run(ctx, project, output, "restart")
}
func (r *CLIRunner) Pull(ctx context.Context, project string, output io.Writer) error {
	return r.run(ctx, project, output, "pull")
}
func (r *CLIRunner) Build(ctx context.Context, project string, output io.Writer) error {
	return r.run(ctx, project, output, "build")
}
func (r *CLIRunner) Validate(ctx context.Context, project string, output io.Writer) error {
	return r.run(ctx, project, output, "config", "--quiet")
}
func (r *CLIRunner) Logs(ctx context.Context, project string, output io.Writer) error {
	return r.run(ctx, project, output, "logs", "--tail", "500", "--no-color")
}

func (r *CLIRunner) Render(ctx context.Context, spec ExecutionSpec, output io.Writer) (string, error) {
	return r.captureSpec(ctx, spec, output, "config", "--format", "json")
}
func (r *CLIRunner) ValidateRelease(ctx context.Context, spec ExecutionSpec, output io.Writer) error {
	return r.runSpec(ctx, spec, output, "config", "--quiet")
}
func (r *CLIRunner) PullRelease(ctx context.Context, spec ExecutionSpec, output io.Writer) error {
	return r.runSpec(ctx, spec, output, "pull", "--policy", "always")
}
func (r *CLIRunner) UpRelease(ctx context.Context, spec ExecutionSpec, timeout int, output io.Writer) error {
	args := []string{"up", "-d", "--remove-orphans", "--wait"}
	if timeout > 0 {
		args = append(args, "--wait-timeout", fmt.Sprintf("%d", timeout))
	}
	return r.runSpec(ctx, spec, output, args...)
}
func (r *CLIRunner) DownRelease(ctx context.Context, spec ExecutionSpec, output io.Writer) error {
	return r.runSpec(ctx, spec, output, "down")
}
func (r *CLIRunner) ForceDownRelease(ctx context.Context, spec ExecutionSpec, preserveVolumes bool, output io.Writer) error {
	args := []string{"down", "--remove-orphans", "--timeout", "0"}
	if !preserveVolumes {
		args = append(args, "--volumes")
	}
	return r.runSpec(ctx, spec, output, args...)
}
func (r *CLIRunner) PS(ctx context.Context, spec ExecutionSpec, output io.Writer) (string, error) {
	return r.captureSpec(ctx, spec, output, "ps", "--format", "json", "--all")
}
func (r *CLIRunner) LogsRelease(ctx context.Context, spec ExecutionSpec, output io.Writer) error {
	return r.runSpec(ctx, spec, output, "logs", "--tail", "500", "--no-color")
}

func releaseEnvironment() []string {
	allowed := []string{"PATH", "HOME", "DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_API_VERSION", "DOCKER_CONTEXT", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "TMPDIR", "LANG", "LC_ALL"}
	values := make([]string, 0, len(allowed))
	for _, key := range allowed {
		if value, ok := os.LookupEnv(key); ok {
			values = append(values, key+"="+value)
		}
	}
	return values
}

func (r *CLIRunner) commandEnvironment() ([]string, func(), error) {
	values := releaseEnvironment()
	if r.target == nil {
		return values, func() {}, nil
	}
	filtered := make([]string, 0, len(values)+3)
	for _, value := range values {
		if strings.HasPrefix(value, "DOCKER_HOST=") || strings.HasPrefix(value, "DOCKER_CONTEXT=") || strings.HasPrefix(value, "DOCKER_TLS_VERIFY=") || strings.HasPrefix(value, "DOCKER_CERT_PATH=") || strings.HasPrefix(value, "DOCKER_CONFIG=") {
			continue
		}
		filtered = append(filtered, value)
	}
	filtered = append(filtered, "DOCKER_HOST="+r.target.Host)
	if !r.target.TLSRequired && r.target.DockerConfig == "" {
		return filtered, func() {}, nil
	}
	directory, err := os.MkdirTemp("", "suma-compose-tls-")
	if err != nil {
		return nil, func() {}, fmt.Errorf("create Compose TLS directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	if err := os.Chmod(directory, 0o700); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	if r.target.TLSRequired {
		for _, item := range []struct{ name, value string }{{"ca.pem", r.target.CA}, {"cert.pem", r.target.Certificate}, {"key.pem", r.target.PrivateKey}} {
			if strings.TrimSpace(item.value) == "" {
				cleanup()
				return nil, func() {}, fmt.Errorf("Docker TLS %s is required", item.name)
			}
			if err := os.WriteFile(filepath.Join(directory, item.name), []byte(item.value), 0o600); err != nil {
				cleanup()
				return nil, func() {}, err
			}
		}
		filtered = append(filtered, "DOCKER_TLS_VERIFY=1", "DOCKER_CERT_PATH="+directory)
	}
	if r.target.DockerConfig != "" {
		configDir := filepath.Join(directory, "config")
		if err := os.Mkdir(configDir, 0o700); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(r.target.DockerConfig), 0o600); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		filtered = append(filtered, "DOCKER_CONFIG="+configDir)
	}
	return filtered, cleanup, nil
}
