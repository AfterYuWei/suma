package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/suma/suma/server/internal/database"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	totpPeriod             = 30 * time.Second
	twoFactorChallengeTTL  = 5 * time.Minute
	twoFactorEnrollmentTTL = 10 * time.Minute
	maxTwoFactorAttempts   = 5
	recoveryCodeCount      = 10
	recoveryCodeRawLength  = 12
)

var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

type LoginResult struct {
	Token             string
	User              User
	RequiresTwoFactor bool
	ChallengeToken    string
}

type TwoFactorStatus struct {
	Enabled                bool  `json:"enabled"`
	RecoveryCodesRemaining int64 `json:"recovery_codes_remaining"`
}

type TwoFactorSetup struct {
	Secret        string `json:"secret"`
	OTPAuthURI    string `json:"otpauth_uri"`
	QRCodeDataURL string `json:"qr_code_data_url"`
}

type RecoveryCodes struct {
	Codes []string `json:"recovery_codes"`
}

func (s *Service) TwoFactorStatus(ctx context.Context, userID uint) (TwoFactorStatus, error) {
	var row database.User
	if err := s.db.WithContext(ctx).Select("totp_enabled").First(&row, userID).Error; err != nil {
		return TwoFactorStatus{}, err
	}
	status := TwoFactorStatus{Enabled: row.TOTPEnabled}
	if row.TOTPEnabled {
		if err := s.db.WithContext(ctx).Model(&database.TwoFactorRecoveryCode{}).Where("user_id = ?", userID).Count(&status.RecoveryCodesRemaining).Error; err != nil {
			return TwoFactorStatus{}, err
		}
	}
	return status, nil
}

func (s *Service) BeginTwoFactorSetup(ctx context.Context, userID uint, currentPassword string) (TwoFactorSetup, error) {
	if s.secrets == nil {
		return TwoFactorSetup{}, ErrTwoFactorStore
	}
	var row database.User
	if err := s.db.WithContext(ctx).Select("id", "username", "email", "password_hash", "totp_enabled").First(&row, userID).Error; err != nil {
		return TwoFactorSetup{}, err
	}
	if row.TOTPEnabled {
		return TwoFactorSetup{}, ErrTwoFactorEnabled
	}
	if bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(currentPassword)) != nil {
		return TwoFactorSetup{}, ErrCurrentPassword
	}
	secretBytes := make([]byte, 20)
	if _, err := rand.Read(secretBytes); err != nil {
		return TwoFactorSetup{}, fmt.Errorf("generate two-factor secret: %w", err)
	}
	secretValue := base32NoPadding.EncodeToString(secretBytes)
	ciphertext, err := s.secrets.Encrypt(secretValue)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	enrollment := database.TwoFactorEnrollment{UserID: userID, SecretCipher: ciphertext, ExpiresAt: time.Now().Add(twoFactorEnrollmentTTL)}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"secret_cipher", "expires_at", "updated_at"}),
	}).Create(&enrollment).Error; err != nil {
		return TwoFactorSetup{}, err
	}
	label := row.Email
	if label == "" {
		label = row.Username
	}
	parameters := url.Values{"secret": {secretValue}, "issuer": {"SUMA"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	otpauthURI := "otpauth://totp/" + url.PathEscape("SUMA:"+label) + "?" + parameters.Encode()
	png, err := qrcode.Encode(otpauthURI, qrcode.Medium, 224)
	if err != nil {
		return TwoFactorSetup{}, fmt.Errorf("create two-factor QR code: %w", err)
	}
	return TwoFactorSetup{Secret: secretValue, OTPAuthURI: otpauthURI, QRCodeDataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)}, nil
}

func (s *Service) ConfirmTwoFactorSetup(ctx context.Context, userID uint, currentToken, code string) (RecoveryCodes, error) {
	if s.secrets == nil {
		return RecoveryCodes{}, ErrTwoFactorStore
	}
	var enrollment database.TwoFactorEnrollment
	if err := s.db.WithContext(ctx).Where("user_id = ? AND expires_at > ?", userID, time.Now()).First(&enrollment).Error; err != nil {
		return RecoveryCodes{}, ErrInvalidTwoFactor
	}
	secretValue, err := s.secrets.Decrypt(enrollment.SecretCipher)
	if err != nil {
		return RecoveryCodes{}, err
	}
	counter, valid := validateTOTP(secretValue, code, time.Now(), -1)
	if !valid {
		return RecoveryCodes{}, ErrInvalidTwoFactor
	}
	codes, hashes, err := generateRecoveryCodes()
	if err != nil {
		return RecoveryCodes{}, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&database.User{}).Where("id = ? AND totp_enabled = ?", userID, false).Updates(map[string]any{
			"totp_enabled": true, "totp_secret": enrollment.SecretCipher, "totp_last_counter": counter,
			"totp_failed_attempts": 0, "totp_locked_until": nil,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrTwoFactorEnabled
		}
		if err := tx.Where("user_id = ?", userID).Delete(&database.TwoFactorRecoveryCode{}).Error; err != nil {
			return err
		}
		for _, hash := range hashes {
			if err := tx.Create(&database.TwoFactorRecoveryCode{UserID: userID, CodeHash: hash}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("user_id = ?", userID).Delete(&database.TwoFactorEnrollment{}).Error; err != nil {
			return err
		}
		return revokeOtherSessions(tx, userID, currentToken)
	})
	if err != nil {
		return RecoveryCodes{}, err
	}
	return RecoveryCodes{Codes: codes}, nil
}

func (s *Service) CompleteTwoFactorLogin(ctx context.Context, challengeToken, code, ip string) (string, User, error) {
	if s.secrets == nil {
		return "", User{}, ErrTwoFactorStore
	}
	if strings.TrimSpace(challengeToken) == "" {
		return "", User{}, ErrInvalidTwoFactor
	}
	var token string
	var authenticated User
	var authFailure error
	var loginName string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var challenge database.LoginChallenge
		if err := tx.Where("token_hash = ?", tokenHash(challengeToken)).First(&challenge).Error; err != nil {
			authFailure = ErrInvalidTwoFactor
			return nil
		}
		loginName = challenge.Username
		if !challenge.ExpiresAt.After(time.Now()) || challenge.Attempts >= maxTwoFactorAttempts {
			_ = tx.Delete(&challenge).Error
			authFailure = ErrInvalidTwoFactor
			return nil
		}
		var row database.User
		if err := tx.Select(twoFactorUserColumns).First(&row, challenge.UserID).Error; err != nil {
			return err
		}
		if !row.TOTPEnabled {
			_ = tx.Delete(&challenge).Error
			authFailure = ErrInvalidTwoFactor
			return nil
		}
		if row.TOTPLockedUntil != nil && row.TOTPLockedUntil.After(time.Now()) {
			_ = tx.Delete(&challenge).Error
			authFailure = ErrInvalidTwoFactor
			return nil
		}
		counter, recoveryID, valid, verifyErr := s.verifyFactor(tx, row, code, true)
		if verifyErr != nil {
			return verifyErr
		}
		if !valid {
			challenge.Attempts++
			failedAttempts := row.TOTPFailedAttempts + 1
			userUpdates := map[string]any{"totp_failed_attempts": failedAttempts}
			if failedAttempts >= maxTwoFactorAttempts {
				userUpdates["totp_failed_attempts"] = 0
				userUpdates["totp_locked_until"] = time.Now().Add(5 * time.Minute)
			}
			if err := tx.Model(&database.User{}).Where("id = ?", row.ID).Updates(userUpdates).Error; err != nil {
				return err
			}
			if challenge.Attempts >= maxTwoFactorAttempts {
				if err := tx.Delete(&challenge).Error; err != nil {
					return err
				}
			} else if err := tx.Model(&challenge).Update("attempts", challenge.Attempts).Error; err != nil {
				return err
			}
			authFailure = ErrInvalidTwoFactor
			return nil
		}
		if recoveryID != 0 {
			result := tx.Where("id = ? AND user_id = ?", recoveryID, row.ID).Delete(&database.TwoFactorRecoveryCode{})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				authFailure = ErrInvalidTwoFactor
				return nil
			}
		} else {
			result := tx.Model(&database.User{}).Where("id = ? AND totp_last_counter < ?", row.ID, counter).Update("totp_last_counter", counter)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				authFailure = ErrInvalidTwoFactor
				return nil
			}
			row.TOTPLastCounter = counter
		}
		if err := tx.Model(&database.User{}).Where("id = ?", row.ID).Updates(map[string]any{"totp_failed_attempts": 0, "totp_locked_until": nil}).Error; err != nil {
			return err
		}
		rawToken, tokenHashValue, err := newToken()
		if err != nil {
			return err
		}
		if err := tx.Create(&database.Session{TokenHash: tokenHashValue, UserID: row.ID, ExpiresAt: time.Now().Add(s.sessionTTL)}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", row.ID).Delete(&database.LoginChallenge{}).Error; err != nil {
			return err
		}
		token, authenticated = rawToken, userView(row)
		return nil
	})
	if err != nil {
		return "", User{}, err
	}
	if authFailure != nil {
		_ = s.db.WithContext(ctx).Create(&database.LoginLog{Username: loginName, IP: ip, Success: false}).Error
		return "", User{}, authFailure
	}
	_ = s.db.WithContext(ctx).Create(&database.LoginLog{Username: loginName, IP: ip, Success: true}).Error
	return token, authenticated, nil
}

func (s *Service) DisableTwoFactor(ctx context.Context, userID uint, currentToken, currentPassword, code string) error {
	row, err := s.twoFactorUser(ctx, userID, currentPassword)
	if err != nil {
		return err
	}
	if !row.TOTPEnabled {
		return ErrTwoFactorDisabled
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, recoveryID, valid, err := s.verifyFactor(tx, row, code, true)
		if err != nil {
			return err
		}
		if !valid {
			return ErrInvalidTwoFactor
		}
		if recoveryID != 0 {
			if result := tx.Where("id = ? AND user_id = ?", recoveryID, userID).Delete(&database.TwoFactorRecoveryCode{}); result.Error != nil || result.RowsAffected != 1 {
				if result.Error != nil {
					return result.Error
				}
				return ErrInvalidTwoFactor
			}
		}
		if err := tx.Model(&database.User{}).Where("id = ?", userID).Updates(map[string]any{"totp_enabled": false, "totp_secret": nil, "totp_last_counter": 0, "totp_failed_attempts": 0, "totp_locked_until": nil}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&database.TwoFactorRecoveryCode{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&database.TwoFactorEnrollment{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&database.LoginChallenge{}).Error; err != nil {
			return err
		}
		return revokeOtherSessions(tx, userID, currentToken)
	})
}

func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID uint, currentToken, currentPassword, code string) (RecoveryCodes, error) {
	row, err := s.twoFactorUser(ctx, userID, currentPassword)
	if err != nil {
		return RecoveryCodes{}, err
	}
	if !row.TOTPEnabled {
		return RecoveryCodes{}, ErrTwoFactorDisabled
	}
	codes, hashes, err := generateRecoveryCodes()
	if err != nil {
		return RecoveryCodes{}, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		counter, _, valid, err := s.verifyFactor(tx, row, code, true)
		if err != nil {
			return err
		}
		if !valid {
			return ErrInvalidTwoFactor
		}
		if counter != 0 {
			result := tx.Model(&database.User{}).Where("id = ? AND totp_last_counter < ?", userID, counter).Update("totp_last_counter", counter)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrInvalidTwoFactor
			}
		}
		if err := tx.Where("user_id = ?", userID).Delete(&database.TwoFactorRecoveryCode{}).Error; err != nil {
			return err
		}
		for _, hash := range hashes {
			if err := tx.Create(&database.TwoFactorRecoveryCode{UserID: userID, CodeHash: hash}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("user_id = ?", userID).Delete(&database.LoginChallenge{}).Error; err != nil {
			return err
		}
		return revokeOtherSessions(tx, userID, currentToken)
	})
	if err != nil {
		return RecoveryCodes{}, err
	}
	return RecoveryCodes{Codes: codes}, nil
}

func (s *Service) twoFactorUser(ctx context.Context, userID uint, currentPassword string) (database.User, error) {
	if s.secrets == nil {
		return database.User{}, ErrTwoFactorStore
	}
	var row database.User
	if err := s.db.WithContext(ctx).Select(twoFactorUserColumns).First(&row, userID).Error; err != nil {
		return database.User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(currentPassword)) != nil {
		return database.User{}, ErrCurrentPassword
	}
	return row, nil
}

func (s *Service) verifyFactor(tx *gorm.DB, row database.User, code string, preventReplay bool) (int64, uint, bool, error) {
	secretValue, err := s.secrets.Decrypt(row.TOTPSecret)
	if err != nil {
		return 0, 0, false, err
	}
	minimum := int64(-1)
	if preventReplay {
		minimum = row.TOTPLastCounter
	}
	if counter, valid := validateTOTP(secretValue, code, time.Now(), minimum); valid {
		return counter, 0, true, nil
	}
	normalized := normalizeRecoveryCode(code)
	if len(normalized) != recoveryCodeRawLength {
		return 0, 0, false, nil
	}
	var rows []database.TwoFactorRecoveryCode
	if err := tx.Where("user_id = ?", row.ID).Find(&rows).Error; err != nil {
		return 0, 0, false, err
	}
	for _, recovery := range rows {
		if bcrypt.CompareHashAndPassword([]byte(recovery.CodeHash), []byte(normalized)) == nil {
			return 0, recovery.ID, true, nil
		}
	}
	return 0, 0, false, nil
}

func validateTOTP(secretValue, code string, now time.Time, minimumCounter int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	for _, character := range code {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	secretBytes, err := base32NoPadding.DecodeString(strings.ToUpper(strings.TrimSpace(secretValue)))
	if err != nil || len(secretBytes) == 0 {
		return 0, false
	}
	current := now.Unix() / int64(totpPeriod/time.Second)
	for drift := int64(-1); drift <= 1; drift++ {
		counter := current + drift
		if counter <= minimumCounter || counter < 0 {
			continue
		}
		expected := totpCodeForCounter(secretBytes, counter)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return counter, true
		}
	}
	return 0, false
}

func totpCode(secretValue string, at time.Time) string {
	secretBytes, err := base32NoPadding.DecodeString(strings.ToUpper(strings.TrimSpace(secretValue)))
	if err != nil {
		return ""
	}
	return totpCodeForCounter(secretBytes, at.Unix()/int64(totpPeriod/time.Second))
}

func totpCodeForCounter(secretBytes []byte, counter int64) string {
	message := make([]byte, 8)
	binary.BigEndian.PutUint64(message, uint64(counter))
	mac := hmac.New(sha1.New, secretBytes)
	_, _ = mac.Write(message)
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := (uint32(digest[offset])&0x7f)<<24 | uint32(digest[offset+1])<<16 | uint32(digest[offset+2])<<8 | uint32(digest[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000)
}

func generateRecoveryCodes() ([]string, []string, error) {
	codes := make([]string, 0, recoveryCodeCount)
	hashes := make([]string, 0, recoveryCodeCount)
	for len(codes) < recoveryCodeCount {
		random := make([]byte, 8)
		if _, err := rand.Read(random); err != nil {
			return nil, nil, fmt.Errorf("generate recovery code: %w", err)
		}
		raw := base32NoPadding.EncodeToString(random)[:recoveryCodeRawLength]
		hash, err := bcrypt.GenerateFromPassword([]byte(raw), bcrypt.DefaultCost)
		if err != nil {
			return nil, nil, fmt.Errorf("hash recovery code: %w", err)
		}
		codes = append(codes, raw[:4]+"-"+raw[4:8]+"-"+raw[8:])
		hashes = append(hashes, string(hash))
	}
	return codes, hashes, nil
}

func normalizeRecoveryCode(value string) string {
	replacer := strings.NewReplacer("-", "", " ", "", "\t", "", "\r", "", "\n", "")
	return strings.ToUpper(replacer.Replace(value))
}

func revokeOtherSessions(tx *gorm.DB, userID uint, currentToken string) error {
	query := tx.Where("user_id = ?", userID)
	if currentToken != "" {
		query = query.Where("token_hash <> ?", tokenHash(currentToken))
	}
	return query.Delete(&database.Session{}).Error
}

var twoFactorUserColumns = []string{
	"id", "username", "nickname", "email", "password_hash", "avatar_mime", "avatar_updated_at",
	"totp_enabled", "totp_secret", "totp_last_counter", "totp_failed_attempts", "totp_locked_until",
}
