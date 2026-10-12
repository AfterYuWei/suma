package container

import (
	"context"
	"errors"
	"testing"
	"time"
)

type overviewProviderFunc func(context.Context, string) (Overview, error)

func (f overviewProviderFunc) ContainerOverview(ctx context.Context, id string) (Overview, error) {
	return f(ctx, id)
}

func TestOverviewValidationDeadlineAndCancellation(t *testing.T) {
	calls := 0
	p := overviewProviderFunc(func(ctx context.Context, id string) (Overview, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > RuntimeReadTimeout {
			t.Fatal("overview has no bounded deadline")
		}
		return Overview{ID: id}, ctx.Err()
	})
	for _, id := range []string{"", "../container", "a/b", "a?query", "a\n"} {
		if _, err := ReadOverview(context.Background(), p, id); !errors.Is(err, ErrObservationInput) {
			t.Fatal("invalid identifier accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid input reached provider")
	}
	if out, err := ReadOverview(context.Background(), p, "postgres"); err != nil || out.ID != "postgres" {
		t.Fatalf("overview failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadOverview(ctx, p, "postgres"); !errors.Is(err, context.Canceled) {
		t.Fatal("parent cancellation lost")
	}
}
