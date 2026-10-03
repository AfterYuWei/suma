package app

import (
	"context"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"path/filepath"
	"testing"
	"time"
)

func TestOperationRuntimeFingerprint(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := secret.Open(filepath.Join(root, "key"))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := node.NewService(db, secrets, "unix:///tmp/suma-unopened-test.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.Close()
	identity := imageUpdateNode(nodes, db)
	initial, err := identity(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.Node{}).Where("id = ?", "local").UpdateColumns(map[string]any{"last_latency_ms": 2, "last_checked_at": time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	probed, _ := identity(ctx, "local")
	if initial.RuntimeKey != probed.RuntimeKey {
		t.Fatal("routine probe invalidated observations")
	}
	cid := uint(42)
	db.Create(&database.DockerTLSCredential{ID: cid, Name: "fixture"})
	db.Model(&database.Node{}).Where("id = ?", "local").UpdateColumn("tls_credential_id", cid)
	before, _ := identity(ctx, "local")
	db.Model(&database.DockerTLSCredential{}).Where("id = ?", cid).UpdateColumn("updated_at", time.Now().Add(time.Minute))
	rotated, _ := identity(ctx, "local")
	if before.RuntimeKey == rotated.RuntimeKey {
		t.Fatal("TLS material rotation reused observations")
	}
	db.Model(&database.Node{}).Where("id = ?", "local").UpdateColumn("agent_connected_at", time.Now())
	reconnected, _ := identity(ctx, "local")
	if rotated.RuntimeKey == reconnected.RuntimeKey {
		t.Fatal("Agent reconnection reused observations")
	}
}
