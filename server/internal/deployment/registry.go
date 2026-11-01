package deployment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/suma/suma/server/internal/credential"
)

// RegistryConfiguration resolves the same explicitly selected, node-authorized
// credentials for project CD and each batch delivery target.
func RegistryConfiguration(ctx context.Context, service *credential.RegistryService, node string, ids []uint) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}
	if service == nil {
		return "", errors.New("registry credential service is unavailable")
	}
	auths := map[string]map[string]string{}
	for _, id := range ids {
		if err := service.AuthorizedForNode(ctx, id, node); err != nil {
			return "", err
		}
		material, err := service.Material(ctx, id)
		if err != nil {
			return "", err
		}
		if _, exists := auths[material.ServerAddress]; exists {
			return "", errors.New("multiple selected credentials address the same registry")
		}
		entry := map[string]string{}
		if material.AuthType == credential.RegistryToken {
			entry["identitytoken"] = material.Secret
		} else {
			entry["auth"] = base64.StdEncoding.EncodeToString([]byte(material.Username + ":" + material.Secret))
		}
		auths[material.ServerAddress] = entry
	}
	raw, err := json.Marshal(map[string]any{"auths": auths})
	return string(raw), err
}
