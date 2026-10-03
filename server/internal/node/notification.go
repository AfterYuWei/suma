package node

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"github.com/suma/suma/server/internal/database"
	"time"
)

func (s *Service) TLSExpiry(ctx context.Context, id uint) (time.Time, string, error) {
	var row database.DockerTLSCredential
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return time.Time{}, "", err
	}
	raw, err := s.secrets.Decrypt(row.CertificateCiphertext)
	if err != nil {
		return time.Time{}, "", err
	}
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return time.Time{}, "", errors.New("invalid TLS certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, "", err
	}
	return cert.NotAfter, row.Fingerprint, nil
}
