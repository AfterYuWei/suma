package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dockport/dockport/server/internal/database"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrAlreadyInitialized = errors.New("administrator already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
)

type Service struct {
	db         *gorm.DB
	sessionTTL time.Duration
}
type User struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
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

func (s *Service) Initialize(ctx context.Context, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	if len(username) < 3 || len(username) > 64 {
		return User{}, fmt.Errorf("username must contain 3 to 64 characters")
	}
	if len(password) < 8 || len(password) > 128 {
		return User{}, fmt.Errorf("password must contain 8 to 128 characters")
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
		created = database.User{Username: username, PasswordHash: string(hash)}
		return tx.Create(&created).Error
	})
	return User{ID: created.ID, Username: created.Username}, err
}

func (s *Service) Login(ctx context.Context, username, password, ip string) (string, User, error) {
	username = strings.TrimSpace(username)
	var row database.User
	err := s.db.WithContext(ctx).Where("username = ?", username).First(&row).Error
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
	return token, User{ID: row.ID, Username: row.Username}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrUnauthorized
	}
	var session database.Session
	if err := s.db.WithContext(ctx).Preload("User").Where("token_hash = ? AND expires_at > ?", tokenHash(token), time.Now()).First(&session).Error; err != nil {
		return User{}, ErrUnauthorized
	}
	return User{ID: session.User.ID, Username: session.User.Username}, nil
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
