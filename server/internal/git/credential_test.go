package git

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dockport/dockport/server/internal/database"
	secretstore "github.com/dockport/dockport/server/internal/secret"
	"gorm.io/gorm"
)

func TestCredentialValidation(t *testing.T) {
	tests := []struct {
		name  string
		input CredentialInput
		valid bool
	}{
		{"none", CredentialInput{Name: "public", AuthType: AuthNone}, true},
		{"token", CredentialInput{Name: "token", AuthType: AuthHTTPToken, Secret: "secret"}, true},
		{"basic", CredentialInput{Name: "basic", AuthType: AuthHTTPBasic, Username: "deploy", Secret: "password"}, true},
		{"SSH", CredentialInput{Name: "ssh", AuthType: AuthSSHKey, PrivateKey: "private-key", KnownHosts: "known-host"}, true},
		{"missing name", CredentialInput{AuthType: AuthNone}, false},
		{"token missing secret", CredentialInput{Name: "token", AuthType: AuthHTTPToken}, false},
		{"basic missing username", CredentialInput{Name: "basic", AuthType: AuthHTTPBasic, Secret: "password"}, false},
		{"basic missing password", CredentialInput{Name: "basic", AuthType: AuthHTTPBasic, Username: "deploy"}, false},
		{"SSH missing key", CredentialInput{Name: "ssh", AuthType: AuthSSHKey, KnownHosts: "known-host"}, false},
		{"SSH missing known hosts", CredentialInput{Name: "ssh", AuthType: AuthSSHKey, PrivateKey: "private-key"}, false},
		{"unsupported", CredentialInput{Name: "netrc", AuthType: "netrc"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCredential(test.input)
			if (err == nil) != test.valid {
				t.Fatalf("validateCredential() error = %v, valid = %v", err, test.valid)
			}
		})
	}
}

func TestCredentialServiceEncryptsSecretsAndOmitsThemFromJSON(t *testing.T) {
	db, service := newCredentialTestService(t)
	input := CredentialInput{
		Name:       "production-deploy",
		AuthType:   AuthSSHKey,
		Username:   "git",
		PrivateKey: "PRIVATE-KEY-MATERIAL-a1b2c3",
		Passphrase: "PASSPHRASE-a1b2c3",
		KnownHosts: "git.example.test ssh-ed25519 AAAATEST-a1b2c3",
		CustomCA:   "CUSTOM-CA-a1b2c3",
	}
	created, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	var stored database.GitCredential
	if err := db.First(&stored, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	secretValues := map[string][]byte{
		input.PrivateKey: stored.PrivateKeyCiphertext,
		input.Passphrase: stored.PassphraseCiphertext,
		input.KnownHosts: stored.KnownHostsCiphertext,
		input.CustomCA:   stored.CACiphertext,
	}
	for plaintext, ciphertext := range secretValues {
		if len(ciphertext) == 0 {
			t.Fatalf("ciphertext for %q is empty", plaintext)
		}
		if bytes.Equal(ciphertext, []byte(plaintext)) || bytes.Contains(ciphertext, []byte(plaintext)) {
			t.Fatalf("plaintext %q was persisted without encryption", plaintext)
		}
	}

	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	for plaintext := range secretValues {
		if bytes.Contains(encoded, []byte(plaintext)) {
			t.Fatalf("credential JSON leaked %q: %s", plaintext, encoded)
		}
	}
	for _, field := range []string{"SecretCiphertext", "PrivateKeyCiphertext", "KnownHostsCiphertext", "CACiphertext", "secret_ciphertext", "private_key_ciphertext"} {
		if bytes.Contains(encoded, []byte(field)) {
			t.Fatalf("credential JSON exposed storage field %q: %s", field, encoded)
		}
	}

	material, err := service.Material(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if material.AuthType != input.AuthType || material.Username != input.Username || material.PrivateKey != input.PrivateKey || material.Passphrase != input.Passphrase || material.KnownHosts != input.KnownHosts || material.CustomCA != input.CustomCA {
		t.Fatalf("decrypted material does not match input: %#v", material)
	}
	if err := db.First(&stored, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.LastUsedAt == nil {
		t.Fatal("credential use did not update last_used_at")
	}
}

func TestCredentialUpdatePreservesOmittedSecret(t *testing.T) {
	_, service := newCredentialTestService(t)
	created, err := service.Create(context.Background(), CredentialInput{
		Name: "deploy", AuthType: AuthHTTPToken, Username: "oauth2", Secret: "old-token", CustomCA: "old-ca",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.Update(context.Background(), created.ID, CredentialInput{Name: "renamed", CustomCA: "new-ca"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "renamed" || updated.AuthType != AuthHTTPToken || updated.Username != "oauth2" {
		t.Fatalf("unexpected updated metadata: %#v", updated)
	}
	material, err := service.Material(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if material.Secret != "old-token" || material.CustomCA != "new-ca" {
		t.Fatalf("unexpected updated material: %#v", material)
	}
}

func TestCredentialUpdateClearsSecretsFromPreviousAuthType(t *testing.T) {
	db, service := newCredentialTestService(t)
	created, err := service.Create(context.Background(), CredentialInput{
		Name: "deploy", AuthType: AuthSSHKey, PrivateKey: "private-key", Passphrase: "passphrase", KnownHosts: "git.example.test ssh-ed25519 AAAA", CustomCA: "private-ca",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(context.Background(), created.ID, CredentialInput{Name: "deploy", AuthType: AuthHTTPToken, Secret: "new-token"}); err != nil {
		t.Fatal(err)
	}
	material, err := service.Material(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if material.Secret != "new-token" || material.PrivateKey != "" || material.Passphrase != "" || material.KnownHosts != "" {
		t.Fatalf("old SSH material survived auth-type change: %#v", material)
	}
	if material.CustomCA != "private-ca" {
		t.Fatalf("transport-independent custom CA was not preserved: %#v", material)
	}
	var stored database.GitCredential
	if err := db.First(&stored, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	for name, ciphertext := range map[string][]byte{"private key": stored.PrivateKeyCiphertext, "passphrase": stored.PassphraseCiphertext, "known_hosts": stored.KnownHostsCiphertext} {
		plaintext, err := service.secrets.Decrypt(ciphertext)
		if err != nil || plaintext != "" {
			t.Fatalf("%s ciphertext was not cleared: plaintext=%q err=%v", name, plaintext, err)
		}
	}
}

func TestCredentialDeleteRejectsCredentialInUse(t *testing.T) {
	db, service := newCredentialTestService(t)
	created, err := service.Create(context.Background(), CredentialInput{Name: "deploy", AuthType: AuthHTTPToken, Secret: "token"})
	if err != nil {
		t.Fatal(err)
	}
	project := database.DeliveryProject{Name: "production", GitCredentialID: &created.ID}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), created.ID); err == nil || !strings.Contains(err.Error(), "used") {
		t.Fatalf("Delete() error = %v, want credential-in-use error", err)
	}
	if err := db.Model(&project).Update("git_credential_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), created.ID); err != nil {
		t.Fatalf("Delete() after detaching credential: %v", err)
	}
	var row database.GitCredential
	if err := db.First(&row, created.ID).Error; err == nil || !strings.Contains(err.Error(), gorm.ErrRecordNotFound.Error()) {
		t.Fatalf("credential still exists, query error = %v", err)
	}
}

func TestCredentialNamesAreUnique(t *testing.T) {
	_, service := newCredentialTestService(t)
	input := CredentialInput{Name: "production", AuthType: AuthHTTPToken, Secret: "token"}
	if _, err := service.Create(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), input); err == nil {
		t.Fatal("duplicate credential name was accepted")
	}
}

func newCredentialTestService(t *testing.T) (*gorm.DB, *CredentialService) {
	t.Helper()
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "dockport.db"))
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := secretstore.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	return db, NewCredentialService(db, secrets)
}
