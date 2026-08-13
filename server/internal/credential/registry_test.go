package credential

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/dockport/dockport/server/internal/database"
	"github.com/dockport/dockport/server/internal/secret"
)

func TestRegistryCredentialLifecycleEncryptsSecrets(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "dockport.db"))
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
