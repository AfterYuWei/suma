package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dockport/dockport/server/internal/database"
	"github.com/dockport/dockport/server/internal/secret"
	"gorm.io/gorm"
)

const (
	AuthNone      = "none"
	AuthHTTPToken = "http_token"
	AuthHTTPBasic = "http_basic"
	AuthSSHKey    = "ssh_key"
)

type CredentialInput struct {
	Name              string   `json:"name"`
	AuthType          string   `json:"auth_type"`
	Username          string   `json:"username"`
	Secret            string   `json:"secret"`
	PrivateKey        string   `json:"private_key"`
	Passphrase        string   `json:"passphrase"`
	KnownHosts        string   `json:"known_hosts"`
	CustomCA          string   `json:"custom_ca"`
	Fingerprint       string   `json:"fingerprint,omitempty"`
	AuthorizedNodeIDs []string `json:"authorized_node_ids,omitempty"`
}

type CredentialMaterial struct {
	AuthType   string
	Username   string
	Secret     string
	PrivateKey string
	Passphrase string
	KnownHosts string
	CustomCA   string
}

type CredentialService struct {
	db      *gorm.DB
	secrets *secret.Store
}

func NewCredentialService(db *gorm.DB, secrets *secret.Store) *CredentialService {
	return &CredentialService{db: db, secrets: secrets}
}

func (s *CredentialService) List(ctx context.Context) ([]database.GitCredential, error) {
	var rows []database.GitCredential
	if err := s.db.WithContext(ctx).Order("name ASC").Find(&rows).Error; err != nil {
		return rows, err
	}
	for index := range rows {
		if err := s.db.WithContext(ctx).Model(&database.DeliveryProject{}).Where("git_credential_id = ?", rows[index].ID).Count(&rows[index].UsedBy).Error; err != nil {
			return nil, err
		}
		var grants []database.GitCredentialNode
		if err := s.db.WithContext(ctx).Where("credential_id = ?", rows[index].ID).Order("node_id ASC").Find(&grants).Error; err != nil {
			return nil, err
		}
		for _, grant := range grants {
			rows[index].AuthorizedNodeIDs = append(rows[index].AuthorizedNodeIDs, grant.NodeID)
		}
	}
	return rows, nil
}

func (s *CredentialService) Create(ctx context.Context, input CredentialInput) (database.GitCredential, error) {
	return s.CreateWithDB(ctx, s.db, input)
}

func (s *CredentialService) CreateWithDB(ctx context.Context, db *gorm.DB, input CredentialInput) (database.GitCredential, error) {
	if err := validateCredential(input); err != nil {
		return database.GitCredential{}, err
	}
	row, err := s.encrypt(input)
	if err != nil {
		return row, err
	}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return replaceGitGrants(ctx, tx, row.ID, input.AuthorizedNodeIDs)
	}); err != nil {
		return row, err
	}
	return row, nil
}

func (s *CredentialService) UpsertProject(ctx context.Context, db *gorm.DB, projectID uint, input CredentialInput) (database.DeliveryProjectGitCredential, error) {
	var current database.DeliveryProjectGitCredential
	err := db.WithContext(ctx).Where("project_id = ?", projectID).First(&current).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := validateCredential(input); err != nil {
			return current, err
		}
		global, err := s.encrypt(input)
		if err != nil {
			return current, err
		}
		row := database.DeliveryProjectGitCredential{ProjectID: projectID, Name: global.Name, AuthType: global.AuthType, Username: global.Username, SecretCiphertext: global.SecretCiphertext, PrivateKeyCiphertext: global.PrivateKeyCiphertext, PassphraseCiphertext: global.PassphraseCiphertext, KnownHostsCiphertext: global.KnownHostsCiphertext, CACiphertext: global.CACiphertext, Fingerprint: global.Fingerprint}
		if err := db.WithContext(ctx).Create(&row).Error; err != nil {
			return row, err
		}
		return row, nil
	}
	if err != nil {
		return current, err
	}
	return s.updateProject(ctx, db, current, input)
}

func (s *CredentialService) ProjectSummary(ctx context.Context, projectID uint) (database.DeliveryProjectGitCredential, error) {
	var row database.DeliveryProjectGitCredential
	return row, s.db.WithContext(ctx).Where("project_id = ?", projectID).First(&row).Error
}

func (s *CredentialService) DeleteProject(ctx context.Context, db *gorm.DB, projectID uint) error {
	return db.WithContext(ctx).Where("project_id = ?", projectID).Delete(&database.DeliveryProjectGitCredential{}).Error
}

func (s *CredentialService) Update(ctx context.Context, id uint, input CredentialInput) (database.GitCredential, error) {
	var current database.GitCredential
	if err := s.db.WithContext(ctx).First(&current, id).Error; err != nil {
		return current, err
	}
	typeChanged := input.AuthType != "" && input.AuthType != current.AuthType
	if input.Name == "" {
		input.Name = current.Name
	}
	if input.AuthType == "" {
		input.AuthType = current.AuthType
	}
	if input.Username == "" && !typeChanged {
		input.Username = current.Username
	}
	if err := validateCredentialUpdate(input, current, typeChanged); err != nil {
		return current, err
	}
	material, err := s.Material(ctx, id)
	if err != nil {
		return current, err
	}
	if input.Secret != "" {
		material.Secret = input.Secret
	}
	if input.PrivateKey != "" {
		material.PrivateKey = input.PrivateKey
	}
	if input.Passphrase != "" {
		material.Passphrase = input.Passphrase
	}
	if input.KnownHosts != "" {
		material.KnownHosts = input.KnownHosts
	}
	if input.CustomCA != "" {
		material.CustomCA = input.CustomCA
	}
	material.AuthType = input.AuthType
	material.Username = input.Username
	if input.AuthType == AuthNone || input.AuthType == AuthSSHKey {
		material.Username = ""
	}
	switch input.AuthType {
	case AuthNone:
		material.Secret, material.PrivateKey, material.Passphrase, material.KnownHosts = "", "", "", ""
	case AuthHTTPToken, AuthHTTPBasic:
		material.PrivateKey, material.Passphrase, material.KnownHosts = "", "", ""
	case AuthSSHKey:
		material.Secret = ""
	}
	updates := map[string]any{"name": input.Name, "auth_type": material.AuthType, "username": material.Username}
	values := []struct {
		name  string
		value string
	}{
		{"secret_ciphertext", material.Secret},
		{"private_key_ciphertext", material.PrivateKey},
		{"passphrase_ciphertext", material.Passphrase},
		{"known_hosts_ciphertext", material.KnownHosts},
		{"ca_ciphertext", material.CustomCA},
	}
	for _, value := range values {
		ciphertext, err := s.secrets.Encrypt(value.value)
		if err != nil {
			return current, err
		}
		updates[value.name] = ciphertext
	}
	updates["fingerprint"] = secret.Fingerprint(material.AuthType, material.Username, material.Secret, material.PrivateKey, material.KnownHosts)
	if err := s.db.WithContext(ctx).Model(&current).Updates(updates).Error; err != nil {
		return current, err
	}
	if input.AuthorizedNodeIDs != nil {
		if err := replaceGitGrants(ctx, s.db, id, input.AuthorizedNodeIDs); err != nil {
			return current, err
		}
	}
	return current, s.db.WithContext(ctx).First(&current, id).Error
}

func (s *CredentialService) Delete(ctx context.Context, id uint) error {
	var count int64
	if err := s.db.WithContext(ctx).Model(&database.DeliveryProject{}).Where("git_credential_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("credential is used by a Compose project")
	}
	if err := s.db.WithContext(ctx).Model(&database.GitCredentialNode{}).Where("credential_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("credential still has node grants")
	}
	return s.db.WithContext(ctx).Delete(&database.GitCredential{}, id).Error
}

func (s *CredentialService) AuthorizedForNodes(ctx context.Context, id uint, nodeIDs []string) error {
	for _, nodeID := range nodeIDs {
		var count int64
		if err := s.db.WithContext(ctx).Model(&database.GitCredentialNode{}).Where("credential_id = ? AND node_id = ?", id, nodeID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("Git credential is not authorized for node %s", nodeID)
		}
	}
	return nil
}

func replaceGitGrants(ctx context.Context, db *gorm.DB, id uint, nodeIDs []string) error {
	if err := db.WithContext(ctx).Where("credential_id = ?", id).Delete(&database.GitCredentialNode{}).Error; err != nil {
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
			return fmt.Errorf("Docker node %s not found", nodeID)
		}
		if err := db.WithContext(ctx).Create(&database.GitCredentialNode{CredentialID: id, NodeID: nodeID}).Error; err != nil {
			return err
		}
		seen[nodeID] = true
	}
	return nil
}

func (s *CredentialService) Material(ctx context.Context, id uint) (CredentialMaterial, error) {
	var row database.GitCredential
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return CredentialMaterial{}, err
	}
	values := []*string{}
	material := CredentialMaterial{AuthType: row.AuthType, Username: row.Username}
	values = append(values, &material.Secret, &material.PrivateKey, &material.Passphrase, &material.KnownHosts, &material.CustomCA)
	ciphertexts := [][]byte{row.SecretCiphertext, row.PrivateKeyCiphertext, row.PassphraseCiphertext, row.KnownHostsCiphertext, row.CACiphertext}
	for index, ciphertext := range ciphertexts {
		value, err := s.secrets.Decrypt(ciphertext)
		if err != nil {
			return CredentialMaterial{}, err
		}
		*values[index] = value
	}
	now := time.Now()
	_ = s.db.WithContext(ctx).Model(&row).Update("last_used_at", now).Error
	return material, nil
}

func (s *CredentialService) ProjectMaterial(ctx context.Context, projectID uint) (CredentialMaterial, error) {
	var row database.DeliveryProjectGitCredential
	if err := s.db.WithContext(ctx).Where("project_id = ?", projectID).First(&row).Error; err != nil {
		return CredentialMaterial{}, err
	}
	return s.decrypt(row.AuthType, row.Username, row.SecretCiphertext, row.PrivateKeyCiphertext, row.PassphraseCiphertext, row.KnownHostsCiphertext, row.CACiphertext)
}

func (s *CredentialService) decrypt(authType, username string, ciphertexts ...[]byte) (CredentialMaterial, error) {
	material := CredentialMaterial{AuthType: authType, Username: username}
	values := []*string{&material.Secret, &material.PrivateKey, &material.Passphrase, &material.KnownHosts, &material.CustomCA}
	for index, ciphertext := range ciphertexts {
		value, err := s.secrets.Decrypt(ciphertext)
		if err != nil {
			return CredentialMaterial{}, err
		}
		*values[index] = value
	}
	return material, nil
}

func (s *CredentialService) updateProject(ctx context.Context, db *gorm.DB, current database.DeliveryProjectGitCredential, input CredentialInput) (database.DeliveryProjectGitCredential, error) {
	typeChanged := input.AuthType != "" && input.AuthType != current.AuthType
	if input.Name == "" {
		input.Name = current.Name
	}
	if input.AuthType == "" {
		input.AuthType = current.AuthType
	}
	if input.Username == "" && !typeChanged {
		input.Username = current.Username
	}
	shadow := database.GitCredential{AuthType: current.AuthType, SecretCiphertext: current.SecretCiphertext, PrivateKeyCiphertext: current.PrivateKeyCiphertext, KnownHostsCiphertext: current.KnownHostsCiphertext}
	if err := validateCredentialUpdate(input, shadow, typeChanged); err != nil {
		return current, err
	}
	material, err := s.decrypt(current.AuthType, current.Username, current.SecretCiphertext, current.PrivateKeyCiphertext, current.PassphraseCiphertext, current.KnownHostsCiphertext, current.CACiphertext)
	if err != nil {
		return current, err
	}
	if input.Secret != "" {
		material.Secret = input.Secret
	}
	if input.PrivateKey != "" {
		material.PrivateKey = input.PrivateKey
	}
	if input.Passphrase != "" {
		material.Passphrase = input.Passphrase
	}
	if input.KnownHosts != "" {
		material.KnownHosts = input.KnownHosts
	}
	if input.CustomCA != "" {
		material.CustomCA = input.CustomCA
	}
	material.AuthType, material.Username = input.AuthType, input.Username
	if input.AuthType == AuthNone || input.AuthType == AuthSSHKey {
		material.Username = ""
	}
	switch input.AuthType {
	case AuthNone:
		material.Secret, material.PrivateKey, material.Passphrase, material.KnownHosts = "", "", "", ""
	case AuthHTTPToken, AuthHTTPBasic:
		material.PrivateKey, material.Passphrase, material.KnownHosts = "", "", ""
	case AuthSSHKey:
		material.Secret = ""
	}
	updates := map[string]any{"name": input.Name, "auth_type": material.AuthType, "username": material.Username, "fingerprint": secret.Fingerprint(material.AuthType, material.Username, material.Secret, material.PrivateKey, material.KnownHosts)}
	for _, value := range []struct{ name, value string }{{"secret_ciphertext", material.Secret}, {"private_key_ciphertext", material.PrivateKey}, {"passphrase_ciphertext", material.Passphrase}, {"known_hosts_ciphertext", material.KnownHosts}, {"ca_ciphertext", material.CustomCA}} {
		ciphertext, err := s.secrets.Encrypt(value.value)
		if err != nil {
			return current, err
		}
		updates[value.name] = ciphertext
	}
	if err := db.WithContext(ctx).Model(&current).Updates(updates).Error; err != nil {
		return current, err
	}
	return current, db.WithContext(ctx).First(&current, current.ID).Error
}

func (s *CredentialService) encrypt(input CredentialInput) (database.GitCredential, error) {
	row := database.GitCredential{Name: input.Name, AuthType: input.AuthType, Username: input.Username}
	values := []struct {
		target *[]byte
		value  string
	}{{&row.SecretCiphertext, input.Secret}, {&row.PrivateKeyCiphertext, input.PrivateKey}, {&row.PassphraseCiphertext, input.Passphrase}, {&row.KnownHostsCiphertext, input.KnownHosts}, {&row.CACiphertext, input.CustomCA}}
	for _, value := range values {
		ciphertext, err := s.secrets.Encrypt(value.value)
		if err != nil {
			return row, err
		}
		*value.target = ciphertext
	}
	row.Fingerprint = secret.Fingerprint(input.AuthType, input.Username, input.Secret, input.PrivateKey, input.KnownHosts)
	return row, nil
}

func validateCredential(input CredentialInput) error {
	if input.Name == "" || len(input.Name) > 128 || strings.TrimSpace(input.Name) != input.Name || strings.ContainsAny(input.Name, "\r\n\x00") {
		return errors.New("credential name must be 1-128 characters without surrounding whitespace or control characters")
	}
	if len(input.Username) > 256 || strings.ContainsAny(input.Username, "\r\n\x00") {
		return errors.New("credential username is invalid")
	}
	if len(input.Secret) > 64<<10 || len(input.Passphrase) > 64<<10 || len(input.PrivateKey) > 1<<20 || len(input.KnownHosts) > 1<<20 || len(input.CustomCA) > 1<<20 {
		return errors.New("credential material exceeds the allowed size")
	}
	switch input.AuthType {
	case AuthNone:
	case AuthHTTPToken:
		if input.Secret == "" {
			return errors.New("token is required")
		}
	case AuthHTTPBasic:
		if input.Username == "" || input.Secret == "" {
			return errors.New("username and password are required")
		}
	case AuthSSHKey:
		if input.PrivateKey == "" || input.KnownHosts == "" {
			return errors.New("SSH private key and known_hosts are required")
		}
	default:
		return fmt.Errorf("unsupported credential type %q", input.AuthType)
	}
	return nil
}

func validateCredentialUpdate(input CredentialInput, current database.GitCredential, typeChanged bool) error {
	copy := input
	if !typeChanged && copy.Secret == "" && len(current.SecretCiphertext) > 0 {
		copy.Secret = "unchanged"
	}
	if !typeChanged && copy.PrivateKey == "" && len(current.PrivateKeyCiphertext) > 0 {
		copy.PrivateKey = "unchanged"
	}
	if !typeChanged && copy.KnownHosts == "" && len(current.KnownHostsCiphertext) > 0 {
		copy.KnownHosts = "unchanged"
	}
	return validateCredential(copy)
}
