package auth

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
)

func TestInitializationKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(filepath.Join(t.TempDir(), "setup.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	first := NewService(db, time.Hour)
	first.setupNow = func() time.Time { return now }
	oldToken, expires, err := first.PrepareSetup(ctx)
	if err != nil || len(oldToken) != 43 || !expires.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("prepare setup: token length %d, expiry %v, error %v", len(oldToken), expires, err)
	}
	if _, err := first.InitializeWithToken(ctx, "wrong", "admin", "admin@example.test", "", "long-password"); !errors.Is(err, ErrSetupKeyInvalid) {
		t.Fatalf("wrong token: %v", err)
	}
	now = expires
	if _, err := first.InitializeWithToken(ctx, oldToken, "admin", "admin@example.test", "", "long-password"); !errors.Is(err, ErrSetupKeyExpired) {
		t.Fatalf("expired token: %v", err)
	}
	second := NewService(db, time.Hour)
	second.setupNow = func() time.Time { return now }
	newToken, _, err := second.PrepareSetup(ctx)
	if err != nil || newToken == oldToken {
		t.Fatalf("restart token was not rotated: %v", err)
	}
	if _, err := second.InitializeWithToken(ctx, oldToken, "admin", "admin@example.test", "", "long-password"); !errors.Is(err, ErrSetupKeyInvalid) {
		t.Fatalf("old token after restart: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := second.InitializeWithToken(ctx, newToken, "admin", "admin@example.test", "", "long-password")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAlreadyInitialized):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent setup: successes=%d conflicts=%d", successes, conflicts)
	}
	if token, _, err := NewService(db, time.Hour).PrepareSetup(ctx); err != nil || token != "" {
		t.Fatalf("existing instance issued setup token: %q, %v", token, err)
	}
}
