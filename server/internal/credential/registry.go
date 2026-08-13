package credential

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/dockport/dockport/server/internal/database"
	"github.com/dockport/dockport/server/internal/secret"
	"gorm.io/gorm"
)

type RegistryMaterial struct{ ServerAddress, AuthType, Username, Secret string }

const (
	RegistryBasic = "basic"
	RegistryToken = "token"
)

type RegistryInput struct {
	Name              string   `json:"name"`
	ServerAddress     string   `json:"server_address"`
	AuthType          string   `json:"auth_type"`
	Username          string   `json:"username"`
	Secret            string   `json:"secret"`
	AuthorizedNodeIDs []string `json:"authorized_node_ids,omitempty"`
}

type RegistryService struct {
	db      *gorm.DB
	secrets *secret.Store
}

func NewRegistryService(db *gorm.DB, secrets *secret.Store) *RegistryService {
	return &RegistryService{db: db, secrets: secrets}
}

func (s *RegistryService) List(ctx context.Context) ([]database.RegistryCredential, error) {
	var rows []database.RegistryCredential
	if err := s.db.WithContext(ctx).Order("name ASC").Find(&rows).Error; err != nil {
		return rows, err
	}
	for index := range rows {
		var grants []database.RegistryCredentialNode
		if err := s.db.WithContext(ctx).Where("credential_id = ?", rows[index].ID).Order("node_id ASC").Find(&grants).Error; err != nil {
			return nil, err
		}
		for _, grant := range grants {
			rows[index].AuthorizedNodeIDs = append(rows[index].AuthorizedNodeIDs, grant.NodeID)
		}
	}
	return rows, nil
}

func (s *RegistryService) Create(ctx context.Context, input RegistryInput) (database.RegistryCredential, error) {
	if err := validateRegistry(input, true); err != nil {
		return database.RegistryCredential{}, err
	}
	if input.AuthType == RegistryToken {
		input.Username = ""
	}
	ciphertext, err := s.secrets.Encrypt(input.Secret)
	if err != nil {
		return database.RegistryCredential{}, err
	}
	row := database.RegistryCredential{Name: input.Name, ServerAddress: input.ServerAddress, AuthType: input.AuthType, Username: input.Username, SecretCiphertext: ciphertext, Fingerprint: secret.Fingerprint(input.ServerAddress, input.AuthType, input.Username, input.Secret)}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return replaceRegistryGrants(ctx, tx, row.ID, input.AuthorizedNodeIDs)
	}); err != nil {
		return row, err
	}
	return row, nil
}

func (s *RegistryService) Update(ctx context.Context, id uint, input RegistryInput) (database.RegistryCredential, error) {
	var row database.RegistryCredential
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return row, err
	}
	if input.Name == "" {
		input.Name = row.Name
	}
	if input.ServerAddress == "" {
		input.ServerAddress = row.ServerAddress
	}
	if input.AuthType == "" {
		input.AuthType = row.AuthType
	}
	typeChanged := input.AuthType != row.AuthType
	if input.Username == "" && !typeChanged {
		input.Username = row.Username
	}
	currentSecret, err := s.secrets.Decrypt(row.SecretCiphertext)
	if err != nil {
		return row, err
	}
	if input.Secret == "" && !typeChanged {
		input.Secret = currentSecret
	}
	if err := validateRegistry(input, true); err != nil {
		return row, err
	}
	if input.AuthType == RegistryToken {
		input.Username = ""
	}
	ciphertext, err := s.secrets.Encrypt(input.Secret)
	if err != nil {
		return row, err
	}
	updates := map[string]any{"name": input.Name, "server_address": input.ServerAddress, "auth_type": input.AuthType, "username": input.Username, "secret_ciphertext": ciphertext, "fingerprint": secret.Fingerprint(input.ServerAddress, input.AuthType, input.Username, input.Secret)}
	if err := s.db.WithContext(ctx).Model(&row).Updates(updates).Error; err != nil {
		return row, err
	}
	if input.AuthorizedNodeIDs != nil {
		if err := replaceRegistryGrants(ctx, s.db, id, input.AuthorizedNodeIDs); err != nil {
			return row, err
		}
	}
	return row, s.db.WithContext(ctx).First(&row, id).Error
}

func (s *RegistryService) Delete(ctx context.Context, id uint) error {
	var count int64
	if err := s.db.WithContext(ctx).Model(&database.DeliveryProjectRegistryCredential{}).Where("credential_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("credential is used by a delivery project")
	}
	if err := s.db.WithContext(ctx).Model(&database.RegistryCredentialNode{}).Where("credential_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("credential still has node grants")
	}
	return s.db.WithContext(ctx).Delete(&database.RegistryCredential{}, id).Error
}

func (s *RegistryService) AuthorizedForNode(ctx context.Context, id uint, nodeID string) error {
	var count int64
	if err := s.db.WithContext(ctx).Model(&database.RegistryCredentialNode{}).Where("credential_id = ? AND node_id = ?", id, nodeID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return errors.New("registry credential is not authorized for this node")
	}
	return nil
}

func (s *RegistryService) Material(ctx context.Context, id uint) (RegistryMaterial, error) {
	var row database.RegistryCredential
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return RegistryMaterial{}, err
	}
	value, err := s.secrets.Decrypt(row.SecretCiphertext)
	if err != nil {
		return RegistryMaterial{}, err
	}
	return RegistryMaterial{ServerAddress: row.ServerAddress, AuthType: row.AuthType, Username: row.Username, Secret: value}, nil
}

func replaceRegistryGrants(ctx context.Context, db *gorm.DB, id uint, nodeIDs []string) error {
	if err := db.WithContext(ctx).Where("credential_id = ?", id).Delete(&database.RegistryCredentialNode{}).Error; err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, nodeID := range nodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" || seen[nodeID] {
			continue
		}
		var count int64
		if err := db.WithContext(ctx).Model(&database.Node{}).Where("id = ?", nodeID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return errors.New("authorized Docker node not found")
		}
		if err := db.WithContext(ctx).Create(&database.RegistryCredentialNode{CredentialID: id, NodeID: nodeID}).Error; err != nil {
			return err
		}
		seen[nodeID] = true
	}
	return nil
}

func validateRegistry(input RegistryInput, requireSecret bool) error {
	if input.Name == "" || len(input.Name) > 128 || strings.TrimSpace(input.Name) != input.Name || strings.ContainsAny(input.Name, "\r\n\x00") {
		return errors.New("credential name must be 1-128 characters without surrounding whitespace or control characters")
	}
	if input.ServerAddress == "" || len(input.ServerAddress) > 512 || strings.TrimSpace(input.ServerAddress) != input.ServerAddress || strings.Contains(input.ServerAddress, "://") || strings.ContainsAny(input.ServerAddress, "/@?#\r\n\x00") {
		return errors.New("registry address must be a host with an optional port")
	}
	parsed, err := url.Parse("registry://" + input.ServerAddress)
	if err != nil || parsed.Host != input.ServerAddress || parsed.Hostname() == "" {
		return errors.New("registry address must be a host with an optional port")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return errors.New("registry port is invalid")
		}
	}
	if len(input.Username) > 256 || strings.ContainsAny(input.Username, "\r\n\x00") || len(input.Secret) > 64<<10 {
		return errors.New("registry credential is invalid")
	}
	switch input.AuthType {
	case RegistryBasic:
		if input.Username == "" || (requireSecret && input.Secret == "") {
			return errors.New("username and password are required")
		}
	case RegistryToken:
		if requireSecret && input.Secret == "" {
			return errors.New("token is required")
		}
	default:
		return errors.New("authentication type must be basic or token")
	}
	return nil
}
