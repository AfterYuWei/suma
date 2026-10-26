package git

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
)

func TestCredentialPreviewRetainsMaskedSecretsAndExplicitUsername(t *testing.T) {
	store, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewCredentialService(nil, store)
	original := CredentialInput{Name: "stored", AuthType: AuthHTTPToken, Username: "supplied-user", Secret: "stored-secret", CustomCA: "stored-ca"}
	stored, err := service.encrypt(original)
	if err != nil {
		t.Fatal(err)
	}
	current := database.DeliveryProjectGitCredential{Name: stored.Name, AuthType: stored.AuthType, Username: stored.Username, SecretCiphertext: stored.SecretCiphertext, CACiphertext: stored.CACiphertext}
	before := current
	_, material, err := service.resolveProjectMaterial(current, CredentialInput{Name: "draft", AuthType: AuthHTTPToken, Username: "edited-user"})
	if err != nil || material.Username != "edited-user" || material.Secret != original.Secret || material.CustomCA != original.CustomCA {
		t.Fatal("masked credential did not retain secret and use draft username", err)
	}
	if !reflect.DeepEqual(before, current) {
		t.Fatal("preview changed stored credential")
	}
	_, material, err = service.resolveProjectMaterial(current, CredentialInput{AuthType: AuthHTTPToken, Secret: "replacement"})
	if err != nil || material.Username != original.Username || material.Secret != "replacement" {
		t.Fatal("preview did not retain saved username when omitted", err)
	}
	if _, _, err := service.resolveProjectMaterial(current, CredentialInput{AuthType: AuthSSHKey}); err == nil {
		t.Fatal("changing credential type reused incompatible secrets")
	}
	// A new credential preview works without a database and cannot persist.
	material, err = service.PreviewProjectMaterial(context.Background(), 0, &original)
	if err != nil || material.Username != original.Username || material.Secret != original.Secret {
		t.Fatal("new draft credential was not resolved", err)
	}
}
