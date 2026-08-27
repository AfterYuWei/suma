package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
)

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(db, time.Hour)
}

func TestInitializeLoginSessionLogout(t *testing.T) {
	ctx := context.Background()
	service := testService(t)
	needsSetup, err := service.NeedsSetup(ctx)
	if err != nil || !needsSetup {
		t.Fatalf("expected setup: %v", err)
	}
	if _, err := service.Initialize(ctx, "admin", "long-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Initialize(ctx, "other", "long-password"); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("expected already initialized, got %v", err)
	}
	token, user, err := service.Login(ctx, "admin", "long-password", "127.0.0.1")
	if err != nil || user.Username != "admin" || token == "" {
		t.Fatalf("login failed: %v", err)
	}
	if _, err := service.Authenticate(ctx, token); err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("session should be invalid: %v", err)
	}
}

func TestRejectsInvalidCredentials(t *testing.T) {
	ctx := context.Background()
	service := testService(t)
	if _, err := service.Initialize(ctx, "admin", "long-password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Login(ctx, "admin", "wrong-password", "127.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials: %v", err)
	}
}

func TestInitializePasswordLength(t *testing.T) {
	ctx := context.Background()
	service := testService(t)
	if _, err := service.Initialize(ctx, "admin", "1234567"); err == nil {
		t.Fatal("expected a seven-character password to be rejected")
	}
	if _, err := service.Initialize(ctx, "admin", "12345678"); err != nil {
		t.Fatalf("expected an eight-character password to be accepted: %v", err)
	}
}
