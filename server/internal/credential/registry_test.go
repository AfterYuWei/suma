package credential

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/suma/suma/server/internal/testutil"
	"path/filepath"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
)

func TestRegistryCredentialLifecycleEncryptsSecrets(t *testing.T) {
	root := t.TempDir()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewRegistryService(db, store)
	created, err := service.Create(context.Background(), RegistryInput{Name: "production", ServerAddress: "registry.example.com:5000", AuthType: RegistryBasic, Username: "robot", Secret: "secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	var stored database.RegistryCredential
	if err := db.First(&stored, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored.SecretCiphertext, []byte("secret-value")) {
		t.Fatal("registry secret was stored in plaintext")
	}
	encoded, _ := json.Marshal(created)
	if bytes.Contains(encoded, []byte("secret-value")) || bytes.Contains(encoded, []byte("SecretCiphertext")) {
		t.Fatalf("response leaked secret: %s", encoded)
	}
	if _, err := service.Update(context.Background(), created.ID, RegistryInput{Name: "production-renamed"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryCredentialValidation(t *testing.T) {
	invalid := []RegistryInput{
		{Name: "x", ServerAddress: "https://registry.example.com", AuthType: RegistryToken, Secret: "x"},
		{Name: "x", ServerAddress: "registry.example.com/path", AuthType: RegistryToken, Secret: "x"},
		{Name: "x", ServerAddress: "registry.example.com", AuthType: RegistryBasic, Secret: "x"},
		{Name: "x", ServerAddress: "registry.example.com", AuthType: RegistryToken},
	}
	for _, input := range invalid {
		if validateRegistry(input, true) == nil {
			t.Fatalf("input unexpectedly valid: %#v", input)
		}
	}
}

func TestImageUpdatePolicyProtectsCredentialAndGrants(t *testing.T) {
	root := t.TempDir()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(root, "key"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewRegistryService(db, store)
	ctx := context.Background()
	if err := db.Create(&database.Node{ID: "edge", Name: "Edge", ConnectionType: "unix", Endpoint: "unix:///test", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	cred, err := service.Create(ctx, RegistryInput{Name: "reg", ServerAddress: "ghcr.io", AuthType: RegistryBasic, Username: "user", Secret: "private", AuthorizedNodeIDs: []string{"edge"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ImageUpdateRegistryCredential{NodeID: "edge", Registry: "ghcr.io", CredentialID: cred.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if service.Delete(ctx, cred.ID) == nil {
		t.Fatal("deleted referenced credential")
	}
	if _, err := service.Update(ctx, cred.ID, RegistryInput{AuthorizedNodeIDs: []string{}}); err == nil {
		t.Fatal("removed referenced grant")
	}
	if _, err := service.Update(ctx, cred.ID, RegistryInput{ServerAddress: "another.example"}); err == nil {
		t.Fatal("changed referenced registry")
	}
	if _, err := service.Update(ctx, cred.ID, RegistryInput{Secret: "rotated"}); err != nil {
		t.Fatalf("safe rotation: %v", err)
	}
	if err := service.AuthorizedForNode(ctx, cred.ID, "edge"); err != nil {
		t.Fatal("failed mutation was not atomic")
	}
}
