package app

import (
	"context"
	"github.com/suma/suma/server/internal/testutil"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
)

func TestAIDigestPullUsesFrozenAuthorizedRegistryMapping(t *testing.T) {
	root := t.TempDir()
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	db.Create(&database.Node{ID: "local", Name: "Local", Enabled: true})
	store, err := secret.Open(filepath.Join(root, "key"))
	if err != nil {
		t.Fatal(err)
	}
	registries := credential.NewRegistryService(db, store)
	row, err := registries.Create(context.Background(), credential.RegistryInput{Name: "Private registry", ServerAddress: "registry.example.com", AuthType: credential.RegistryBasic, Username: "robot", Secret: "private-registry-secret", AuthorizedNodeIDs: []string{"local"}})
	if err != nil {
		t.Fatal(err)
	}
	db.Create(&database.ImageUpdateRegistryCredential{NodeID: "local", Registry: row.ServerAddress, CredentialID: row.ID})
	runtime := aiRuntime{db: db, registries: registries}
	ref := "registry.example.com/app@sha256:" + strings.Repeat("a", 64)
	before, material, err := runtime.pullConfiguration(context.Background(), "local", ref, false)
	if err != nil || material.Secret != "" || strings.Contains(jsonText(before), "private-registry-secret") {
		t.Fatal("preview exposed registry secret", err)
	}
	after, material, err := runtime.pullConfiguration(context.Background(), "local", ref, true)
	if err != nil || stateHash(before) != stateHash(after) || material.Secret != "private-registry-secret" {
		t.Fatal("approved mapping unavailable", err)
	}
	db.Model(&database.RegistryCredential{}).Where("id = ?", row.ID).Update("fingerprint", "changed")
	changed, _, err := runtime.pullConfiguration(context.Background(), "local", ref, false)
	if err != nil || stateHash(before) == stateHash(changed) {
		t.Fatal("credential change did not invalidate snapshot")
	}
	db.Where("credential_id = ? AND node_id = ?", row.ID, "local").Delete(&database.RegistryCredentialNode{})
	if _, _, err := runtime.pullConfiguration(context.Background(), "local", ref, true); err == nil {
		t.Fatal("revoked registry grant used")
	}
}
