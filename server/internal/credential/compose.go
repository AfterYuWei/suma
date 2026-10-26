package credential

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/distribution/reference"
	"github.com/suma/suma/server/internal/database"
)

func (s *RegistryService) HasNodeCredentials(ctx context.Context, nodeID string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&database.RegistryCredentialNode{}).Where("node_id = ?", nodeID).Count(&count).Error
	return count > 0, err
}

// Resolve only registries present in the rendered Compose configuration. Grants
// authorize use on a node; multiple identities for one registry are ambiguous.
// Never silently choose an account or fall back after a credential read failure.
func (s *RegistryService) DockerConfigForNode(ctx context.Context, nodeID string, images []string) (string, error) {
	needed := map[string]bool{}
	for _, image := range images {
		name, err := reference.ParseNormalizedNamed(image)
		if err != nil {
			return "", errors.New("cannot resolve a Compose image registry")
		}
		needed[canonicalRegistry(reference.Domain(name))] = true
	}
	var rows []database.RegistryCredential
	if err := s.db.WithContext(ctx).Select("registry_credentials.id", "registry_credentials.server_address").
		Joins("JOIN registry_credential_nodes AS node_grant ON node_grant.credential_id = registry_credentials.id").
		Where("node_grant.node_id = ?", nodeID).Order("registry_credentials.id").Find(&rows).Error; err != nil {
		return "", err
	}
	selected := map[string]uint{}
	for _, row := range rows {
		host := canonicalRegistry(row.ServerAddress)
		if !needed[host] {
			continue
		}
		if _, exists := selected[host]; exists {
			return "", fmt.Errorf("multiple registry credentials are authorized for %s; keep one matching node grant", host)
		}
		selected[host] = row.ID
	}
	if len(selected) == 0 {
		return "", nil
	}
	auths := map[string]map[string]string{}
	for host, id := range selected {
		if err := s.AuthorizedForNode(ctx, id, nodeID); err != nil {
			return "", err
		}
		material, err := s.Material(ctx, id)
		if err != nil {
			return "", errors.New("unable to read authorized registry credential")
		}
		if canonicalRegistry(material.ServerAddress) != host {
			return "", errors.New("registry credential changed; retry the operation")
		}
		entry := map[string]string{}
		switch material.AuthType {
		case RegistryToken:
			entry["identitytoken"] = material.Secret
		case RegistryBasic:
			entry["auth"] = base64.StdEncoding.EncodeToString([]byte(material.Username + ":" + material.Secret))
		default:
			return "", errors.New("unsupported registry authentication type")
		}
		key := host
		if key == "docker.io" {
			key = "https://index.docker.io/v1/"
		}
		auths[key] = entry
	}
	value, err := json.Marshal(map[string]any{"auths": auths})
	return string(value), err
}

func canonicalRegistry(value string) string {
	value = strings.ToLower(value)
	if value == "index.docker.io" || value == "registry-1.docker.io" {
		return "docker.io"
	}
	return value
}
