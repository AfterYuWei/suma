package container

import (
	"context"
	"time"
)

const RuntimeReadTimeout = 5 * time.Second

// Nil values mean the Engine could not provide that field. A zero memory
// limit means unlimited, and health status "none" means no healthcheck.
type RuntimeMetadata struct {
	ID               string     `json:"id"`
	HealthStatus     *string    `json:"health_status"`
	RestartCount     *int       `json:"restart_count"`
	MemoryLimitBytes *int64     `json:"memory_limit_bytes"`
	State            *string    `json:"state"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
	ExitCode         *int       `json:"exit_code"`
	OOMKilled        *bool      `json:"oom_killed"`
	StateError       *string    `json:"state_error"`
	ObservedAt       *time.Time `json:"observed_at"`
}

type RuntimeProvider interface {
	RuntimeMetadata(context.Context) ([]RuntimeMetadata, error)
}

func ReadRuntime(ctx context.Context, runtime RuntimeProvider) ([]RuntimeMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, RuntimeReadTimeout)
	defer cancel()
	return runtime.RuntimeMetadata(ctx)
}
