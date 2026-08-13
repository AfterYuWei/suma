package node

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/dockport/dockport/server/internal/database"
	"github.com/dockport/dockport/server/internal/secret"
	"gorm.io/gorm"
)

func (s *Service) ListTLSCredentials(ctx context.Context) ([]TLSCredentialView, error) {
	var rows []database.DockerTLSCredential
	if err := s.db.WithContext(ctx).Order("name ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]TLSCredentialView, 0, len(rows))
	for _, row := range rows {
		value, err := s.tlsCredentialView(ctx, row)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *Service) CreateTLSCredential(ctx context.Context, input TLSCredentialInput) (TLSCredentialView, error) {
	if err := validateTLSInput(input, true); err != nil {
		return TLSCredentialView{}, err
	}
	ca, err := s.secrets.Encrypt(input.CA)
	if err != nil {
		return TLSCredentialView{}, err
	}
	certificate, err := s.secrets.Encrypt(input.Certificate)
	if err != nil {
		return TLSCredentialView{}, err
	}
	privateKey, err := s.secrets.Encrypt(input.PrivateKey)
	if err != nil {
		return TLSCredentialView{}, err
	}
	row := database.DockerTLSCredential{Name: strings.TrimSpace(input.Name), CACiphertext: ca, CertificateCiphertext: certificate, PrivateKeyCiphertext: privateKey, Fingerprint: secret.Fingerprint(input.CA, input.Certificate)}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return replaceTLSGrants(ctx, tx, row.ID, input.AuthorizedNodeIDs)
	}); err != nil {
		return TLSCredentialView{}, err
	}
	return s.tlsCredentialView(ctx, row)
}

func (s *Service) UpdateTLSCredential(ctx context.Context, id uint, input TLSCredentialInput) (TLSCredentialView, error) {
	var row database.DockerTLSCredential
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return TLSCredentialView{}, err
	}
	if err := validateTLSInput(input, false); err != nil {
		return TLSCredentialView{}, err
	}
	updates := map[string]any{"name": strings.TrimSpace(input.Name)}
	if input.CA != "" || input.Certificate != "" || input.PrivateKey != "" {
		if input.CA == "" || input.Certificate == "" || input.PrivateKey == "" {
			return TLSCredentialView{}, errors.New("CA, certificate, and private key must be replaced together")
		}
		ca, err := s.secrets.Encrypt(input.CA)
		if err != nil {
			return TLSCredentialView{}, err
		}
		certificate, err := s.secrets.Encrypt(input.Certificate)
		if err != nil {
			return TLSCredentialView{}, err
		}
		privateKey, err := s.secrets.Encrypt(input.PrivateKey)
		if err != nil {
			return TLSCredentialView{}, err
		}
		updates["ca_ciphertext"], updates["certificate_ciphertext"], updates["private_key_ciphertext"], updates["fingerprint"] = ca, certificate, privateKey, secret.Fingerprint(input.CA, input.Certificate)
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.DockerTLSCredential{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		return replaceTLSGrants(ctx, tx, id, input.AuthorizedNodeIDs)
	}); err != nil {
		return TLSCredentialView{}, err
	}
	s.invalidateNodesForTLS(id)
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return TLSCredentialView{}, err
	}
	return s.tlsCredentialView(ctx, row)
}

func (s *Service) DeleteTLSCredential(ctx context.Context, id uint) error {
	var references int64
	if err := s.db.WithContext(ctx).Model(&database.Node{}).Where("tls_credential_id = ?", id).Count(&references).Error; err != nil {
		return err
	}
	if references > 0 {
		return errors.New("TLS credential is still used by a node")
	}
	if err := s.db.WithContext(ctx).Model(&database.DockerTLSCredentialNode{}).Where("credential_id = ?", id).Count(&references).Error; err != nil {
		return err
	}
	if references > 0 {
		return errors.New("TLS credential still has node grants")
	}
	return s.db.WithContext(ctx).Delete(&database.DockerTLSCredential{}, id).Error
}

func validateTLSInput(input TLSCredentialInput, requireMaterial bool) error {
	if strings.TrimSpace(input.Name) == "" || len(strings.TrimSpace(input.Name)) > 128 {
		return errors.New("credential name is required")
	}
	if requireMaterial && (strings.TrimSpace(input.CA) == "" || strings.TrimSpace(input.Certificate) == "" || strings.TrimSpace(input.PrivateKey) == "") {
		return errors.New("CA, certificate, and private key are required")
	}
	return nil
}

func replaceTLSGrants(ctx context.Context, tx *gorm.DB, credentialID uint, nodeIDs []string) error {
	unique := map[string]bool{}
	for _, nodeID := range nodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if !validID.MatchString(nodeID) || unique[nodeID] {
			continue
		}
		var count int64
		if err := tx.WithContext(ctx).Model(&database.Node{}).Where("id = ?", nodeID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return errors.New("authorized Docker node not found")
		}
		unique[nodeID] = true
	}
	// A credential cannot be revoked from a node while that node references it.
	var used []database.Node
	if err := tx.WithContext(ctx).Where("tls_credential_id = ?", credentialID).Find(&used).Error; err != nil {
		return err
	}
	for _, node := range used {
		if !unique[node.ID] {
			return errors.New("credential cannot be revoked while a node uses it")
		}
	}
	if err := tx.WithContext(ctx).Where("credential_id = ?", credentialID).Delete(&database.DockerTLSCredentialNode{}).Error; err != nil {
		return err
	}
	for nodeID := range unique {
		if err := tx.WithContext(ctx).Create(&database.DockerTLSCredentialNode{CredentialID: credentialID, NodeID: nodeID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) tlsCredentialView(ctx context.Context, row database.DockerTLSCredential) (TLSCredentialView, error) {
	var grants []database.DockerTLSCredentialNode
	if err := s.db.WithContext(ctx).Where("credential_id = ?", row.ID).Find(&grants).Error; err != nil {
		return TLSCredentialView{}, err
	}
	ids := make([]string, 0, len(grants))
	for _, grant := range grants {
		ids = append(ids, grant.NodeID)
	}
	sort.Strings(ids)
	return TLSCredentialView{ID: row.ID, Name: row.Name, Fingerprint: row.Fingerprint, AuthorizedNodeIDs: ids, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, LastUsedAt: row.LastUsedAt}, nil
}

func (s *Service) invalidateNodesForTLS(credentialID uint) {
	var rows []database.Node
	_ = s.db.Where("tls_credential_id = ?", credentialID).Find(&rows).Error
	for _, row := range rows {
		s.invalidate(row.ID)
	}
	_ = s.db.Model(&database.DockerTLSCredential{}).Where("id = ?", credentialID).Update("last_used_at", time.Now()).Error
}
