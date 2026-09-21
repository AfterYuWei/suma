package auth

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
)

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(db, time.Hour)
}

func TestTOTPMatchesRFC6238SHA1Vector(t *testing.T) {
	encoded := base32NoPadding.EncodeToString([]byte("12345678901234567890"))
	if code := totpCode(encoded, time.Unix(59, 0)); code != "287082" {
		t.Fatalf("TOTP code = %s, want 287082", code)
	}
}

func TestTwoFactorSetupLoginRecoveryAndDisable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db, time.Hour, store)
	user, err := service.Initialize(ctx, "admin", "admin@example.test", "Administrator", "long-password")
	if err != nil {
		t.Fatal(err)
	}
	currentToken, _, err := service.Login(ctx, "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginTwoFactorSetup(ctx, user.ID, "wrong-password"); !errors.Is(err, ErrCurrentPassword) {
		t.Fatalf("setup without current password = %v", err)
	}
	setup, err := service.BeginTwoFactorSetup(ctx, user.ID, "long-password")
	if err != nil || setup.Secret == "" || !strings.HasPrefix(setup.OTPAuthURI, "otpauth://totp/") || !strings.HasPrefix(setup.QRCodeDataURL, "data:image/png;base64,") {
		t.Fatalf("setup = %#v, %v", setup, err)
	}
	var enrollment database.TwoFactorEnrollment
	if err := db.First(&enrollment, "user_id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enrollment.SecretCipher, []byte(setup.Secret)) {
		t.Fatal("TOTP secret was stored in plaintext")
	}
	confirmationCode := totpCode(setup.Secret, time.Now().Add(-totpPeriod))
	recovery, err := service.ConfirmTwoFactorSetup(ctx, user.ID, currentToken, confirmationCode)
	if err != nil || len(recovery.Codes) != recoveryCodeCount {
		t.Fatalf("confirm = %#v, %v", recovery, err)
	}
	status, err := service.TwoFactorStatus(ctx, user.ID)
	if err != nil || !status.Enabled || status.RecoveryCodesRemaining != recoveryCodeCount {
		t.Fatalf("status = %#v, %v", status, err)
	}
	var stored database.User
	if err := db.Select("totp_secret", "totp_enabled").First(&stored, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.TOTPEnabled || bytes.Contains(stored.TOTPSecret, []byte(setup.Secret)) {
		t.Fatal("enabled TOTP secret was not encrypted")
	}
	var storedRecovery database.TwoFactorRecoveryCode
	if err := db.Where("user_id = ?", user.ID).First(&storedRecovery).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedRecovery.CodeHash, normalizeRecoveryCode(recovery.Codes[0])) {
		t.Fatal("recovery code was stored in plaintext")
	}
	for attempt := 0; attempt < maxTwoFactorAttempts; attempt++ {
		failedChallenge, err := service.StartLogin(ctx, "admin", "long-password", "127.0.0.9")
		if err != nil {
			t.Fatalf("start failed challenge %d: %v", attempt, err)
		}
		if _, _, err := service.CompleteTwoFactorLogin(ctx, failedChallenge.ChallengeToken, "invalid", "127.0.0.9"); !errors.Is(err, ErrInvalidTwoFactor) {
			t.Fatalf("invalid factor attempt %d = %v", attempt, err)
		}
	}
	if _, err := service.StartLogin(ctx, "admin", "long-password", "127.0.0.9"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("2FA account was not temporarily locked: %v", err)
	}
	if err := db.Model(&database.User{}).Where("id = ?", user.ID).Updates(map[string]any{"totp_failed_attempts": 0, "totp_locked_until": nil}).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Login(ctx, "admin", "long-password", "127.0.0.2"); !errors.Is(err, ErrTwoFactorRequired) {
		t.Fatalf("password-only login bypassed 2FA: %v", err)
	}
	challenge, err := service.StartLogin(ctx, "admin@example.test", "long-password", "127.0.0.2")
	if err != nil || !challenge.RequiresTwoFactor || challenge.ChallengeToken == "" || challenge.Token != "" {
		t.Fatalf("challenge = %#v, %v", challenge, err)
	}
	loginCode := totpCode(setup.Secret, time.Now())
	token, loggedIn, err := service.CompleteTwoFactorLogin(ctx, challenge.ChallengeToken, loginCode, "127.0.0.2")
	if err != nil || token == "" || !loggedIn.TwoFactorEnabled {
		t.Fatalf("complete login = %#v, %v", loggedIn, err)
	}
	if _, err := service.Authenticate(ctx, token); err != nil {
		t.Fatalf("2FA session = %v", err)
	}
	replay, err := service.StartLogin(ctx, "admin", "long-password", "127.0.0.3")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CompleteTwoFactorLogin(ctx, replay.ChallengeToken, loginCode, "127.0.0.3"); !errors.Is(err, ErrInvalidTwoFactor) {
		t.Fatalf("replayed TOTP code = %v", err)
	}
	recoveryLogin, err := service.StartLogin(ctx, "admin", "long-password", "127.0.0.4")
	if err != nil {
		t.Fatal(err)
	}
	recoveryToken, _, err := service.CompleteTwoFactorLogin(ctx, recoveryLogin.ChallengeToken, recovery.Codes[0], "127.0.0.4")
	if err != nil {
		t.Fatalf("recovery login = %v", err)
	}
	status, err = service.TwoFactorStatus(ctx, user.ID)
	if err != nil || status.RecoveryCodesRemaining != recoveryCodeCount-1 {
		t.Fatalf("recovery status = %#v, %v", status, err)
	}
	regenerated, err := service.RegenerateRecoveryCodes(ctx, user.ID, recoveryToken, "long-password", recovery.Codes[1])
	if err != nil || len(regenerated.Codes) != recoveryCodeCount {
		t.Fatalf("regenerate recovery codes = %#v, %v", regenerated, err)
	}
	oldRecoveryLogin, err := service.StartLogin(ctx, "admin", "long-password", "127.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.CompleteTwoFactorLogin(ctx, oldRecoveryLogin.ChallengeToken, recovery.Codes[2], "127.0.0.5"); !errors.Is(err, ErrInvalidTwoFactor) {
		t.Fatalf("old recovery code survived regeneration: %v", err)
	}
	newRecoveryLogin, err := service.StartLogin(ctx, "admin", "long-password", "127.0.0.6")
	if err != nil {
		t.Fatal(err)
	}
	newRecoveryToken, _, err := service.CompleteTwoFactorLogin(ctx, newRecoveryLogin.ChallengeToken, regenerated.Codes[0], "127.0.0.6")
	if err != nil {
		t.Fatalf("new recovery login = %v", err)
	}
	if err := service.DisableTwoFactor(ctx, user.ID, newRecoveryToken, "long-password", regenerated.Codes[1]); err != nil {
		t.Fatalf("disable = %v", err)
	}
	status, err = service.TwoFactorStatus(ctx, user.ID)
	if err != nil || status.Enabled || status.RecoveryCodesRemaining != 0 {
		t.Fatalf("disabled status = %#v, %v", status, err)
	}
	if _, _, err := service.Login(ctx, "admin", "long-password", "127.0.0.7"); err != nil {
		t.Fatalf("password login after disable = %v", err)
	}
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
