package credential

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/testutil"
)

func TestComposeRegistryConfigUsesOnlyMatchingNodeGrants(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewRegistryService(db, store)
	ctx := context.Background()
	for _, id := range []string{"edge", "other"} {
		if err := db.Create(&database.Node{ID: id, Name: id, ConnectionType: "unix", Endpoint: "unix:///fixture", Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	create := func(host, kind, user, password, node string) database.RegistryCredential {
		t.Helper()
		row, err := s.Create(ctx, RegistryInput{Name: host + node, ServerAddress: host, AuthType: kind, Username: user, Secret: password, AuthorizedNodeIDs: []string{node}})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	if has, err := s.HasNodeCredentials(ctx, "edge"); err != nil || has {
		t.Fatal("empty node has credentials", err)
	}
	cnb := create("docker.cnb.cool", RegistryBasic, "cnb", "fixture-cnb-secret", "edge")
	create("docker.cnb.cool", RegistryBasic, "other-user", "other-node-secret", "other")
	unused := create("unused.example", RegistryBasic, "user", "unrelated-secret", "edge")
	// Unrelated material is not even decrypted.
	if err := db.Model(&database.RegistryCredential{}).Where("id = ?", unused.ID).Update("secret_ciphertext", []byte("corrupt")).Error; err != nil {
		t.Fatal(err)
	}
	config, err := s.DockerConfigForNode(ctx, "edge", []string{"docker.cnb.cool/yuweinfo/eizhu-admin:latest", "nginx:stable"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Auths map[string]map[string]string `json:"auths"`
	}
	if err := json.Unmarshal([]byte(config), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Auths) != 1 || decoded.Auths["docker.cnb.cool"]["auth"] != base64.StdEncoding.EncodeToString([]byte("cnb:fixture-cnb-secret")) || decoded.Auths["docker.cnb.cool"]["identitytoken"] != "" {
		t.Fatal("matching basic credential was not used")
	}
	if strings.Contains(config, "other-node-secret") || strings.Contains(config, "unrelated-secret") {
		t.Fatal("unrelated credential leaked")
	}
	if _, err := s.Update(ctx, cnb.ID, RegistryInput{Secret: "rotated-secret"}); err != nil {
		t.Fatal(err)
	}
	config, err = s.DockerConfigForNode(ctx, "edge", []string{"docker.cnb.cool/team/app"})
	if err != nil || !strings.Contains(config, base64.StdEncoding.EncodeToString([]byte("cnb:rotated-secret"))) {
		t.Fatal("rotation was not read at execution", err)
	}
	if _, err := s.Update(ctx, cnb.ID, RegistryInput{AuthorizedNodeIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	if config, err := s.DockerConfigForNode(ctx, "edge", []string{"docker.cnb.cool/team/app"}); err != nil || config != "" {
		t.Fatal("revoked grant still used", err)
	}
}

func TestComposeRegistryConfigRejectsAmbiguityAndPreservesIdentityTokens(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "key"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewRegistryService(db, store)
	ctx := context.Background()
	if err := db.Create(&database.Node{ID: "edge", Name: "edge", ConnectionType: "unix", Endpoint: "unix:///fixture", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	for _, in := range []RegistryInput{
		{Name: "hub", ServerAddress: "index.docker.io", AuthType: RegistryBasic, Username: "hub-user", Secret: "hub-secret"},
		{Name: "token", ServerAddress: "registry.example:5000", AuthType: RegistryToken, Secret: "identity-token"},
		{Name: "conflict-a", ServerAddress: "conflict.example", AuthType: RegistryBasic, Username: "a", Secret: "secret-a"},
		{Name: "conflict-b", ServerAddress: "conflict.example", AuthType: RegistryBasic, Username: "b", Secret: "secret-b"},
	} {
		in.AuthorizedNodeIDs = []string{"edge"}
		if _, err := s.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	config, err := s.DockerConfigForNode(ctx, "edge", []string{"nginx:stable", "registry.example:5000/team/app"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Auths map[string]map[string]string `json:"auths"`
	}
	if err := json.Unmarshal([]byte(config), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Auths) != 2 || decoded.Auths["https://index.docker.io/v1/"]["auth"] == "" || decoded.Auths["registry.example:5000"]["identitytoken"] != "identity-token" {
		t.Fatal("registry aliases or token type changed")
	}
	if _, err := s.DockerConfigForNode(ctx, "edge", []string{"conflict.example/team/app"}); err == nil || !strings.Contains(err.Error(), "multiple registry credentials") {
		t.Fatal("ambiguous identities silently selected", err)
	}
	if _, err := s.DockerConfigForNode(ctx, "edge", []string{"invalid image ref"}); err == nil {
		t.Fatal("invalid image accepted")
	}
}
