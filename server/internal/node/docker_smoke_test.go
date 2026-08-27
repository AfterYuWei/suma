//go:build dockersmoke

package node

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
)

// TestRealMTLSDockerNode is opt-in because it requires an independently
// configured Docker Engine with client-certificate authentication.
func TestRealMTLSDockerNode(t *testing.T) {
	host := os.Getenv("SUMA_SMOKE_TCP_HOST")
	certDir := os.Getenv("SUMA_SMOKE_TLS_DIR")
	if host == "" || certDir == "" { t.Skip("set SUMA_SMOKE_TCP_HOST and SUMA_SMOKE_TLS_DIR for the mTLS Docker smoke test") }
	read := func(name string) string { value, err := os.ReadFile(filepath.Join(certDir, name)); if err != nil { t.Fatal(err) }; return string(value) }
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "suma.db")); if err != nil { t.Fatal(err) }
	secrets, err := secret.Open(filepath.Join(root, "secret.key")); if err != nil { t.Fatal(err) }
	service, err := NewService(db, secrets, "unix:///var/run/docker.sock"); if err != nil { t.Fatal(err) }
	credential, err := service.CreateTLSCredential(context.Background(), TLSCredentialInput{Name: "smoke-mtls", CA: read("ca.pem"), Certificate: read("cert.pem"), PrivateKey: read("key.pem")}); if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second); defer cancel()
	node, err := service.Create(ctx, Input{Name: "Smoke mTLS", ConnectionType: ConnectionTCP, Endpoint: host, TLSMode: TLSRequired, TLSCredentialID: &credential.ID, Enabled: true}); if err != nil { t.Fatal(err) }
	adapter, err := service.Runtime(ctx, node.ID); if err != nil { t.Fatal(err) }
	if err := adapter.Ping(ctx); err != nil { t.Fatal(err) }
	if _, err := adapter.Info(ctx); err != nil { t.Fatal(err) }
	if _, err := adapter.List(ctx); err != nil { t.Fatal(err) }
}
