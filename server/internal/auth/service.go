package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/suma/suma/server/internal/database"
	"golang.org/x/crypto/bcrypt"
	_ "golang.org/x/image/webp"
	"gorm.io/gorm"
)

var (
	ErrAlreadyInitialized = errors.New("administrator already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrCurrentPassword    = errors.New("current password is invalid")
	ErrIdentityConflict   = errors.New("username or email is already in use")
	ErrAvatarNotFound     = errors.New("avatar not found")
)

const MaxAvatarBytes = 2 << 20

type Service struct {
	db         *gorm.DB
	sessionTTL time.Duration
}
type User struct {
	ID        uint   `json:"id"`
	Username  string `json:"username"`
	Nickname  string `json:"nickname"`
	Email     string `json:"email"`
	HasAvatar bool   `json:"has_avatar"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

type ProfileInput struct{ Username, Nickname, Email, CurrentPassword string }
type PasswordInput struct{ CurrentPassword, NewPassword string }
type Avatar struct {
	Data       []byte
	MIME, ETag string
}

func NewService(db *gorm.DB, sessionTTL time.Duration) *Service {
	return &Service{db: db, sessionTTL: sessionTTL}
}

func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&database.User{}).Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}

func (s *Service) Initialize(ctx context.Context, username, email, nickname, password string) (User, error) {
	username, nickname, email = strings.TrimSpace(username), strings.TrimSpace(nickname), strings.ToLower(strings.TrimSpace(email))
	if err := validateProfile(username, nickname, email, true); err != nil {
		return User{}, err
	}
	if err := validatePassword(password); err != nil {
		return User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	var created database.User
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.User{}).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrAlreadyInitialized
		}
		created = database.User{Username: username, Nickname: nickname, Email: email, PasswordHash: string(hash)}
		return tx.Create(&created).Error
	})
	return userView(created), err
}

func (s *Service) Login(ctx context.Context, username, password, ip string) (string, User, error) {
	username = strings.TrimSpace(username)
	var row database.User
	err := s.db.WithContext(ctx).Select(userColumns).Where("username = ? OR lower(email) = ?", username, strings.ToLower(username)).First(&row).Error
	success := err == nil && bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(password)) == nil
	_ = s.db.WithContext(ctx).Create(&database.LoginLog{Username: username, IP: ip, Success: success}).Error
	if !success {
		return "", User{}, ErrInvalidCredentials
	}
	token, hash, err := newToken()
	if err != nil {
		return "", User{}, err
	}
	if err := s.db.WithContext(ctx).Create(&database.Session{TokenHash: hash, UserID: row.ID, ExpiresAt: time.Now().Add(s.sessionTTL)}).Error; err != nil {
		return "", User{}, err
	}
	return token, userView(row), nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrUnauthorized
	}
	var session database.Session
	if err := s.db.WithContext(ctx).Preload("User", func(db *gorm.DB) *gorm.DB { return db.Select(userColumns) }).Where("token_hash = ? AND expires_at > ?", tokenHash(token), time.Now()).First(&session).Error; err != nil {
		return User{}, ErrUnauthorized
	}
	return userView(session.User), nil
}

func (s *Service) UpdateProfile(ctx context.Context, userID uint, input ProfileInput) (User, error) {
	input.Username = strings.TrimSpace(input.Username)
	input.Nickname = strings.TrimSpace(input.Nickname)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if err := validateProfile(input.Username, input.Nickname, input.Email, true); err != nil {
		return User{}, err
	}
	var updated database.User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Select(userColumns).First(&updated, userID).Error; err != nil {
			return err
		}
		identityChanged := updated.Username != input.Username || !strings.EqualFold(updated.Email, input.Email)
		if identityChanged && bcrypt.CompareHashAndPassword([]byte(updated.PasswordHash), []byte(input.CurrentPassword)) != nil {
			return ErrCurrentPassword
		}
		var conflicts int64
		if err := tx.Model(&database.User{}).Where("id <> ? AND (username = ? OR lower(email) = ?)", userID, input.Username, input.Email).Count(&conflicts).Error; err != nil {
			return err
		}
		if conflicts > 0 {
			return ErrIdentityConflict
		}
		updated.Username, updated.Nickname, updated.Email = input.Username, input.Nickname, input.Email
		return tx.Model(&updated).Updates(map[string]any{"username": updated.Username, "nickname": updated.Nickname, "email": updated.Email}).Error
	})
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		err = ErrIdentityConflict
	}
	return userView(updated), err
}

func (s *Service) ChangePassword(ctx context.Context, userID uint, currentToken string, input PasswordInput) error {
	if err := validatePassword(input.NewPassword); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row database.User
		if err := tx.Select(userColumns).First(&row, userID).Error; err != nil {
			return err
		}
		if bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(input.CurrentPassword)) != nil {
			return ErrCurrentPassword
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		if err := tx.Model(&row).Update("password_hash", string(hash)).Error; err != nil {
			return err
		}
		query := tx.Where("user_id = ?", userID)
		if currentToken != "" {
			query = query.Where("token_hash <> ?", tokenHash(currentToken))
		}
		return query.Delete(&database.Session{}).Error
	})
}

func (s *Service) UpdateAvatar(ctx context.Context, userID uint, data []byte) (User, error) {
	mime, err := validateAvatar(data)
	if err != nil {
		return User{}, err
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Model(&database.User{}).Where("id = ?", userID).Updates(map[string]any{"avatar_data": data, "avatar_mime": mime, "avatar_updated_at": now}).Error; err != nil {
		return User{}, err
	}
	var row database.User
	if err := s.db.WithContext(ctx).Select(userColumns).First(&row, userID).Error; err != nil {
		return User{}, err
	}
	return userView(row), nil
}

func (s *Service) Avatar(ctx context.Context, userID uint) (Avatar, error) {
	var row database.User
	if err := s.db.WithContext(ctx).Select("avatar_data", "avatar_mime").First(&row, userID).Error; err != nil {
		return Avatar{}, err
	}
	if len(row.AvatarData) == 0 {
		return Avatar{}, ErrAvatarNotFound
	}
	hash := sha256.Sum256(row.AvatarData)
	return Avatar{Data: row.AvatarData, MIME: row.AvatarMIME, ETag: `"` + hex.EncodeToString(hash[:]) + `"`}, nil
}

func (s *Service) DeleteAvatar(ctx context.Context, userID uint) (User, error) {
	if err := s.db.WithContext(ctx).Model(&database.User{}).Where("id = ?", userID).Updates(map[string]any{"avatar_data": nil, "avatar_mime": "", "avatar_updated_at": nil}).Error; err != nil {
		return User{}, err
	}
	var row database.User
	if err := s.db.WithContext(ctx).Select(userColumns).First(&row, userID).Error; err != nil {
		return User{}, err
	}
	return userView(row), nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.db.WithContext(ctx).Where("token_hash = ?", tokenHash(token)).Delete(&database.Session{}).Error
}

func newToken() (string, string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", fmt.Errorf("generate session: %w", err)
	}
	token := hex.EncodeToString(bytes)
	return token, tokenHash(token), nil
}

func tokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func userView(row database.User) User {
	view := User{ID: row.ID, Username: row.Username, Nickname: row.Nickname, Email: row.Email, HasAvatar: row.AvatarMIME != ""}
	if view.HasAvatar && row.AvatarUpdatedAt != nil {
		view.AvatarURL = fmt.Sprintf("/api/v1/account/avatar?v=%d", row.AvatarUpdatedAt.UnixNano())
	}
	return view
}

func validateProfile(username, nickname, email string, requireEmail bool) error {
	if len(username) < 3 || len(username) > 64 {
		return fmt.Errorf("username must contain 3 to 64 characters")
	}
	if utf8.RuneCountInString(nickname) > 64 {
		return fmt.Errorf("nickname must contain at most 64 characters")
	}
	if strings.ContainsAny(username+nickname, "\r\n\x00") {
		return fmt.Errorf("username or nickname contains invalid characters")
	}
	if email == "" && !requireEmail {
		return nil
	}
	if len(email) > 254 {
		return fmt.Errorf("email must contain at most 254 characters")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || strings.ContainsAny(email, "\r\n\x00") {
		return fmt.Errorf("email address is invalid")
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) < 8 || len(password) > 128 {
		return fmt.Errorf("password must contain 8 to 128 characters")
	}
	return nil
}

func validateAvatar(data []byte) (string, error) {
	if len(data) == 0 || len(data) > MaxAvatarBytes {
		return "", fmt.Errorf("avatar must be a WebP image no larger than 2 MB")
	}
	if animatedWebP(data) {
		return "", fmt.Errorf("animated avatars are not supported")
	}
	contentType := http.DetectContentType(data)
	if contentType != "image/webp" {
		return "", fmt.Errorf("cropped avatar must be a WebP image")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "webp" {
		return "", fmt.Errorf("avatar image is invalid")
	}
	if config.Width != 512 || config.Height != 512 || int64(config.Width)*int64(config.Height) > 25_000_000 {
		return "", fmt.Errorf("cropped avatar must be 512 by 512 pixels")
	}
	if _, format, err = image.Decode(bytes.NewReader(data)); err != nil || format != "webp" {
		return "", fmt.Errorf("avatar image is invalid")
	}
	return "image/webp", nil
}

var userColumns = []string{"id", "username", "nickname", "email", "password_hash", "avatar_mime", "avatar_updated_at"}

func animatedWebP(data []byte) bool {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return false
	}
	for offset := 12; offset+8 <= len(data); {
		name := string(data[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		if name == "ANIM" {
			return true
		}
		if size < 0 || offset+8+size > len(data) {
			return false
		}
		offset += 8 + size + size%2
	}
	return false
}
