package container

import (
	"context"
	"errors"
	"testing"
	"time"
)

type runtimeProviderFunc func(context.Context) ([]RuntimeMetadata, error)

func (f runtimeProviderFunc) RuntimeMetadata(ctx context.Context) ([]RuntimeMetadata, error) {
	return f(ctx)
}

func TestRuntimeReadDeadlineAndUnknownFields(t *testing.T) {
	rows, err := ReadRuntime(context.Background(), runtimeProviderFunc(func(ctx context.Context) ([]RuntimeMetadata, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > RuntimeReadTimeout || time.Until(deadline) < 4*time.Second {
			t.Fatal("runtime read must have a five-second deadline")
		}
		return []RuntimeMetadata{{ID: "unavailable"}}, nil
	}))
	if err != nil || len(rows) != 1 || rows[0].HealthStatus != nil || rows[0].RestartCount != nil || rows[0].MemoryLimitBytes != nil {
		t.Fatalf("unknown fields were not retained: %#v %v", rows, err)
	}
}

func TestRuntimeReadPreservesParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReadRuntime(ctx, runtimeProviderFunc(func(ctx context.Context) ([]RuntimeMetadata, error) { return nil, ctx.Err() }))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
