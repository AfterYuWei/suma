package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const keySize = 32

type Store struct {
	aead cipher.AEAD
}

func Open(path string) (*Store, error) {
	key, err := loadOrCreateKey(path)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create secret store: %w", err)
	}
	return &Store{aead: aead}, nil
}

func (s *Store) Encrypt(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("create secret nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, []byte(value), nil), nil
}

func (s *Store) Decrypt(value []byte) (string, error) {
	if len(value) == 0 {
		return "", nil
	}
	if len(value) < s.aead.NonceSize() {
		return "", errors.New("invalid encrypted secret")
	}
	nonce, ciphertext := value[:s.aead.NonceSize()], value[s.aead.NonceSize():]
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("unable to decrypt secret")
	}
	return string(plaintext), nil
}

func Fingerprint(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

func loadOrCreateKey(path string) ([]byte, error) {
	value, err := os.ReadFile(path)
	if err == nil {
		if len(value) != keySize {
			return nil, fmt.Errorf("secret key must contain exactly %d bytes", keySize)
		}
		return value, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read secret key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create secret key directory: %w", err)
	}
	value = make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return nil, fmt.Errorf("generate secret key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return loadOrCreateKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("create secret key: %w", err)
	}
	if _, err := file.Write(value); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write secret key: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("sync secret key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close secret key: %w", err)
	}
	return value, nil
}
