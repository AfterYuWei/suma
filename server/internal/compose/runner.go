package compose

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type Runner interface {
	Up(context.Context, string, io.Writer) error
	Down(context.Context, string, io.Writer) error
	Start(context.Context, string, io.Writer) error
	Stop(context.Context, string, io.Writer) error
	Restart(context.Context, string, io.Writer) error
	Pull(context.Context, string, io.Writer) error
	Build(context.Context, string, io.Writer) error
	Validate(context.Context, string, io.Writer) error
	Logs(context.Context, string, io.Writer) error
}

type CLIRunner struct {
	command string
	prefix  []string
}

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
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		return fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
func (r *CLIRunner) Up(ctx context.Context, project string, output io.Writer) error {
	return r.run(ctx, project, output, "up", "-d")
}
func (r *CLIRunner) Down(ctx context.Context, project string, output io.Writer) error {
	return r.run(ctx, project, output, "down")
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
