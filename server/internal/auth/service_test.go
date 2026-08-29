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
	if _, err := service.Initialize(ctx, "admin", "admin@example.test", "Administrator", "long-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Initialize(ctx, "other", "other@example.test", "", "long-password"); !errors.Is(err, ErrAlreadyInitialized) {
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
	if _, err := service.Initialize(ctx, "admin", "admin@example.test", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Login(ctx, "admin", "wrong-password", "127.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials: %v", err)
	}
}

func TestInitializePasswordLength(t *testing.T) {
	ctx := context.Background()
	service := testService(t)
	if _, err := service.Initialize(ctx, "admin", "admin@example.test", "", "1234567"); err == nil {
		t.Fatal("expected a seven-character password to be rejected")
	}
	if _, err := service.Initialize(ctx, "admin", "admin@example.test", "", "12345678"); err != nil {
		t.Fatalf("expected an eight-character password to be accepted: %v", err)
	}
}

func TestProfileEmailLoginAndPasswordSessionRevocation(t *testing.T) {
	ctx := context.Background()
	service := testService(t)
	created, err := service.Initialize(ctx, "admin", "Admin@Example.Test", "Operator", "long-password")
	if err != nil || created.Email != "admin@example.test" || created.Nickname != "Operator" {
		t.Fatalf("initialize profile = %#v, %v", created, err)
	}
	firstToken, _, err := service.Login(ctx, "ADMIN@EXAMPLE.TEST", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	secondToken, _, err := service.Login(ctx, "admin", "long-password", "127.0.0.2")
	if err != nil {
		t.Fatal(err)
	}

	updated, err := service.UpdateProfile(ctx, created.ID, ProfileInput{Username: "admin", Nickname: "New name", Email: "admin@example.test"})
	if err != nil || updated.Nickname != "New name" {
		t.Fatalf("nickname update = %#v, %v", updated, err)
	}
	if _, err := service.UpdateProfile(ctx, created.ID, ProfileInput{Username: "operator", Nickname: updated.Nickname, Email: "ops@example.test", CurrentPassword: "wrong-password"}); !errors.Is(err, ErrCurrentPassword) {
		t.Fatalf("identity update should require password: %v", err)
	}
	updated, err = service.UpdateProfile(ctx, created.ID, ProfileInput{Username: "operator", Nickname: updated.Nickname, Email: "OPS@EXAMPLE.TEST", CurrentPassword: "long-password"})
	if err != nil || updated.Username != "operator" || updated.Email != "ops@example.test" {
		t.Fatalf("identity update = %#v, %v", updated, err)
	}
	if err := service.ChangePassword(ctx, created.ID, firstToken, PasswordInput{CurrentPassword: "long-password", NewPassword: "new-password"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, firstToken); err != nil {
		t.Fatalf("current session was revoked: %v", err)
	}
	if _, err := service.Authenticate(ctx, secondToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("other session survived: %v", err)
	}
	if _, _, err := service.Login(ctx, "operator", "long-password", "127.0.0.1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password survived: %v", err)
	}
	if _, _, err := service.Login(ctx, "ops@example.test", "new-password", "127.0.0.1"); err != nil {
		t.Fatalf("new email/password login: %v", err)
	}
}

func TestLegacyUserWithoutEmailCanStillLogin(t *testing.T) {
	ctx := context.Background()
	service := testService(t)
	user, err := service.Initialize(ctx, "admin", "admin@example.test", "", "long-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&database.User{}).Where("id = ?", user.ID).Update("email", "").Error; err != nil {
		t.Fatal(err)
	}
	_, loggedIn, err := service.Login(ctx, "admin", "long-password", "127.0.0.1")
	if err != nil || loggedIn.Email != "" {
		t.Fatalf("legacy login = %#v, %v", loggedIn, err)
	}
}

func TestAvatarValidationAndLifecycle(t *testing.T) {
	ctx := context.Background()
	service := testService(t)
	user, err := service.Initialize(ctx, "admin", "admin@example.test", "", "long-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateAvatar(ctx, user.ID, []byte("<svg></svg>")); err == nil {
		t.Fatal("expected SVG avatar rejection")
	}
	if _, err := service.Avatar(ctx, user.ID); !errors.Is(err, ErrAvatarNotFound) {
		t.Fatalf("missing avatar = %v", err)
	}
	now := time.Now().UTC()
	data := []byte("stored-avatar")
	if err := service.db.Model(&database.User{}).Where("id = ?", user.ID).Updates(map[string]any{"avatar_data": data, "avatar_mime": "image/webp", "avatar_updated_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	avatar, err := service.Avatar(ctx, user.ID)
	if err != nil || string(avatar.Data) != string(data) || avatar.MIME != "image/webp" || avatar.ETag == "" {
		t.Fatalf("avatar = %#v, %v", avatar, err)
	}
	deleted, err := service.DeleteAvatar(ctx, user.ID)
	if err != nil || deleted.HasAvatar {
		t.Fatalf("delete avatar = %#v, %v", deleted, err)
	}
}
