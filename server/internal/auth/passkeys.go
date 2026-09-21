package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	wa "github.com/go-webauthn/webauthn/webauthn"
	"github.com/suma/suma/server/internal/database"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	webAuthnCeremonyTTL = 5 * time.Minute
	passkeyKindRegister = "register"
	passkeyKindLogin    = "login"
)

var (
	ErrInvalidWebAuthnOrigin = errors.New("passkeys require HTTPS, or HTTP on localhost")
	ErrInvalidCeremony       = errors.New("invalid or expired passkey ceremony")
	ErrPasskeyNotFound       = errors.New("passkey not found")
)

type Passkey struct {
	ID         uint       `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type PasskeyOptions struct {
	CeremonyToken string `json:"ceremony_token"`
	Options       any    `json:"options"`
}

type passkeyUser struct {
	row         database.User
	credentials []wa.Credential
}

func (u passkeyUser) WebAuthnID() []byte   { return u.row.PasskeyUserHandle }
func (u passkeyUser) WebAuthnName() string { return u.row.Username }
func (u passkeyUser) WebAuthnDisplayName() string {
	if u.row.Nickname != "" {
		return u.row.Nickname
	}
	return u.row.Username
}
func (u passkeyUser) WebAuthnCredentials() []wa.Credential { return u.credentials }

func NormalizeWebAuthnOrigin(value string) (origin, rpID string, err error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", ErrInvalidWebAuthnOrigin
	}
	scheme := strings.ToLower(parsed.Scheme)
	hostname := strings.ToLower(parsed.Hostname())
	if net.ParseIP(hostname) != nil || scheme != "https" && !(scheme == "http" && hostname == "localhost") {
		return "", "", ErrInvalidWebAuthnOrigin
	}
	if hostname == "" {
		return "", "", ErrInvalidWebAuthnOrigin
	}
	if err := protocol.ValidateRPID(hostname); err != nil {
		return "", "", ErrInvalidWebAuthnOrigin
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if parsed.Port() != "" {
		host = net.JoinHostPort(hostname, parsed.Port())
	}
	return scheme + "://" + host, hostname, nil
}

func (s *Service) ListPasskeys(ctx context.Context, userID uint) ([]Passkey, error) {
	var rows []database.PasskeyCredential
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("created_at asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]Passkey, len(rows))
	for i, row := range rows {
		result[i] = Passkey{ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt, LastUsedAt: row.LastUsedAt}
	}
	return result, nil
}

func (s *Service) BeginPasskeyRegistration(ctx context.Context, userID uint, currentPassword, factor, name, origin, rpID string) (PasskeyOptions, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 64 {
		return PasskeyOptions{}, errors.New("passkey name must be between 1 and 64 characters")
	}
	if err := s.verifySensitiveAction(ctx, userID, currentPassword, factor); err != nil {
		return PasskeyOptions{}, err
	}
	user, err := s.loadPasskeyUser(ctx, userID, true)
	if err != nil {
		return PasskeyOptions{}, err
	}
	webAuthn, err := newWebAuthn(origin, rpID)
	if err != nil {
		return PasskeyOptions{}, err
	}
	requireResident := true
	selection := protocol.AuthenticatorSelection{RequireResidentKey: &requireResident, ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired}
	exclusions := make([]protocol.CredentialDescriptor, len(user.credentials))
	for i := range user.credentials {
		exclusions[i] = user.credentials[i].Descriptor()
	}
	options, session, err := webAuthn.BeginRegistration(user,
		wa.WithAuthenticatorSelection(selection), wa.WithExclusions(exclusions), wa.WithRegistrationOrigin(origin))
	if err != nil {
		return PasskeyOptions{}, fmt.Errorf("begin passkey registration: %w", err)
	}
	token, err := s.saveWebAuthnCeremony(ctx, passkeyKindRegister, userID, name, origin, rpID, session)
	if err != nil {
		return PasskeyOptions{}, err
	}
	return PasskeyOptions{CeremonyToken: token, Options: options}, nil
}

func (s *Service) FinishPasskeyRegistration(ctx context.Context, userID uint, currentToken, ceremonyToken string, request *http.Request) (Passkey, error) {
	ceremony, session, err := s.consumeWebAuthnCeremony(ctx, ceremonyToken, passkeyKindRegister, userID)
	if err != nil {
		return Passkey{}, err
	}
	user, err := s.loadPasskeyUser(ctx, userID, false)
	if err != nil {
		return Passkey{}, err
	}
	webAuthn, err := newWebAuthn(ceremony.Origin, ceremony.RPID)
	if err != nil {
		return Passkey{}, err
	}
	credential, err := webAuthn.FinishRegistration(user, session, request)
	if err != nil {
		return Passkey{}, ErrInvalidCeremony
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return Passkey{}, err
	}
	row := database.PasskeyCredential{UserID: userID, CredentialID: credential.ID, Name: ceremony.Name, CredentialJSON: encoded}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return revokeOtherSessions(tx, userID, currentToken)
	})
	if err != nil {
		return Passkey{}, err
	}
	return Passkey{ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt}, nil
}

func (s *Service) BeginPasskeyLogin(ctx context.Context, origin, rpID string) (PasskeyOptions, error) {
	webAuthn, err := newWebAuthn(origin, rpID)
	if err != nil {
		return PasskeyOptions{}, err
	}
	options, session, err := webAuthn.BeginDiscoverableLogin(wa.WithLoginOrigin(origin), wa.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return PasskeyOptions{}, fmt.Errorf("begin passkey login: %w", err)
	}
	token, err := s.saveWebAuthnCeremony(ctx, passkeyKindLogin, 0, "", origin, rpID, session)
	if err != nil {
		return PasskeyOptions{}, err
	}
	return PasskeyOptions{CeremonyToken: token, Options: options}, nil
}

func (s *Service) FinishPasskeyLogin(ctx context.Context, ceremonyToken, ip string, request *http.Request) (string, User, error) {
	ceremony, session, err := s.consumeWebAuthnCeremony(ctx, ceremonyToken, passkeyKindLogin, 0)
	if err != nil {
		return "", User{}, err
	}
	webAuthn, err := newWebAuthn(ceremony.Origin, ceremony.RPID)
	if err != nil {
		return "", User{}, err
	}
	var authenticated passkeyUser
	handler := func(rawID, userHandle []byte) (wa.User, error) {
		var credential database.PasskeyCredential
		if err := s.db.WithContext(ctx).Where("credential_id = ?", rawID).First(&credential).Error; err != nil {
			return nil, ErrInvalidCeremony
		}
		user, err := s.loadPasskeyUser(ctx, credential.UserID, false)
		if err != nil || !bytes.Equal(user.row.PasskeyUserHandle, userHandle) {
			return nil, ErrInvalidCeremony
		}
		authenticated = user
		return user, nil
	}
	credential, err := webAuthn.FinishDiscoverableLogin(handler, session, request)
	if err != nil || authenticated.row.ID == 0 {
		_ = s.db.WithContext(ctx).Create(&database.LoginLog{Username: "passkey", IP: ip, Success: false}).Error
		return "", User{}, ErrInvalidCeremony
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return "", User{}, err
	}
	token, hash, err := newToken()
	if err != nil {
		return "", User{}, err
	}
	now := time.Now()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&database.PasskeyCredential{}).Where("user_id = ? AND credential_id = ?", authenticated.row.ID, credential.ID).Updates(map[string]any{"credential_json": encoded, "last_used_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			if result.Error != nil {
				return result.Error
			}
			return ErrInvalidCeremony
		}
		return tx.Create(&database.Session{TokenHash: hash, UserID: authenticated.row.ID, ExpiresAt: now.Add(s.sessionTTL)}).Error
	})
	if err != nil {
		return "", User{}, err
	}
	_ = s.db.WithContext(ctx).Create(&database.LoginLog{Username: authenticated.row.Username, IP: ip, Success: true}).Error
	return token, userView(authenticated.row), nil
}

func (s *Service) RenamePasskey(ctx context.Context, userID, passkeyID uint, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 64 {
		return errors.New("passkey name must be between 1 and 64 characters")
	}
	result := s.db.WithContext(ctx).Model(&database.PasskeyCredential{}).Where("id = ? AND user_id = ?", passkeyID, userID).Update("name", name)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrPasskeyNotFound
	}
	return nil
}

func (s *Service) DeletePasskey(ctx context.Context, userID, passkeyID uint, currentToken, currentPassword, factor string) error {
	if err := s.verifySensitiveAction(ctx, userID, currentPassword, factor); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND user_id = ?", passkeyID, userID).Delete(&database.PasskeyCredential{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrPasskeyNotFound
		}
		return revokeOtherSessions(tx, userID, currentToken)
	})
}

func newWebAuthn(origin, rpID string) (*wa.WebAuthn, error) {
	return wa.New(&wa.Config{RPID: rpID, RPDisplayName: "SUMA", RPOrigins: []string{origin}, AuthenticatorSelection: protocol.AuthenticatorSelection{UserVerification: protocol.VerificationRequired}})
}

func (s *Service) loadPasskeyUser(ctx context.Context, userID uint, ensureHandle bool) (passkeyUser, error) {
	var row database.User
	if err := s.db.WithContext(ctx).Select("id", "username", "nickname", "email", "password_hash", "avatar_mime", "avatar_updated_at", "totp_enabled", "passkey_user_handle").First(&row, userID).Error; err != nil {
		return passkeyUser{}, err
	}
	if len(row.PasskeyUserHandle) == 0 && ensureHandle {
		handle := make([]byte, 32)
		if _, err := rand.Read(handle); err != nil {
			return passkeyUser{}, err
		}
		result := s.db.WithContext(ctx).Model(&database.User{}).Where("id = ? AND (passkey_user_handle IS NULL OR length(passkey_user_handle) = 0)", userID).Update("passkey_user_handle", handle)
		if result.Error != nil {
			return passkeyUser{}, result.Error
		}
		if result.RowsAffected == 1 {
			row.PasskeyUserHandle = handle
		} else if err := s.db.WithContext(ctx).Select("passkey_user_handle").First(&row, userID).Error; err != nil {
			return passkeyUser{}, err
		}
	}
	var records []database.PasskeyCredential
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&records).Error; err != nil {
		return passkeyUser{}, err
	}
	credentials := make([]wa.Credential, 0, len(records))
	for _, record := range records {
		var credential wa.Credential
		if err := json.Unmarshal(record.CredentialJSON, &credential); err != nil {
			return passkeyUser{}, fmt.Errorf("decode passkey: %w", err)
		}
		credentials = append(credentials, credential)
	}
	return passkeyUser{row: row, credentials: credentials}, nil
}

func (s *Service) saveWebAuthnCeremony(ctx context.Context, kind string, userID uint, name, origin, rpID string, session *wa.SessionData) (string, error) {
	raw, hash, err := newToken()
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	now := time.Now()
	row := database.WebAuthnCeremony{TokenHash: hash, UserID: userID, Kind: kind, Name: name, SessionJSON: encoded, Origin: origin, RPID: rpID, ExpiresAt: now.Add(webAuthnCeremonyTTL)}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at <= ?", now).Delete(&database.WebAuthnCeremony{}).Error; err != nil {
			return err
		}
		return tx.Create(&row).Error
	}); err != nil {
		return "", err
	}
	return raw, nil
}

func (s *Service) consumeWebAuthnCeremony(ctx context.Context, token, kind string, userID uint) (database.WebAuthnCeremony, wa.SessionData, error) {
	var row database.WebAuthnCeremony
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Where("token_hash = ? AND kind = ? AND expires_at > ?", tokenHash(token), kind, time.Now())
		if userID != 0 {
			query = query.Where("user_id = ?", userID)
		}
		if err := query.First(&row).Error; err != nil {
			return err
		}
		return tx.Delete(&row).Error
	})
	if err != nil {
		return row, wa.SessionData{}, ErrInvalidCeremony
	}
	var session wa.SessionData
	if json.Unmarshal(row.SessionJSON, &session) != nil {
		return row, session, ErrInvalidCeremony
	}
	return row, session, nil
}

func (s *Service) verifySensitiveAction(ctx context.Context, userID uint, currentPassword, factor string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row database.User
		if err := tx.Select(twoFactorUserColumns).First(&row, userID).Error; err != nil {
			return err
		}
		if bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(currentPassword)) != nil {
			return ErrCurrentPassword
		}
		if !row.TOTPEnabled {
			return nil
		}
		if s.secrets == nil {
			return ErrTwoFactorStore
		}
		counter, recoveryID, valid, err := s.verifyFactor(tx, row, factor, true)
		if err != nil {
			return err
		}
		if !valid {
			return ErrInvalidTwoFactor
		}
		if recoveryID != 0 {
			result := tx.Where("id = ? AND user_id = ?", recoveryID, userID).Delete(&database.TwoFactorRecoveryCode{})
			if result.Error != nil || result.RowsAffected != 1 {
				return ErrInvalidTwoFactor
			}
		} else {
			result := tx.Model(&database.User{}).Where("id = ? AND totp_last_counter < ?", userID, counter).Update("totp_last_counter", counter)
			if result.Error != nil || result.RowsAffected != 1 {
				return ErrInvalidTwoFactor
			}
		}
		return nil
	})
}
