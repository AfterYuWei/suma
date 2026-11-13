//go:build dockersmoke

package taskcenter_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// This rehearsal restores only the randomly owned test schema and fixture files.
// It never backs up, drops or restores a production database or application root.
func TestRealTaskCenterUpgradeBackupRestore(t *testing.T) {
	if os.Getenv("SUMA_RUN_UPGRADE_SMOKE") != "1" {
		t.Skip("set SUMA_RUN_UPGRADE_SMOKE=1 with a disposable PostgreSQL container")
	}
	container := os.Getenv("SUMA_UPGRADE_SMOKE_POSTGRES")
	if !strings.HasPrefix(container, "suma-tc-pg-") {
		t.Fatal("a dedicated suma-tc-pg- PostgreSQL fixture is required")
	}
	businessUpgradeFixture(t, func(t *testing.T, dsn, root string) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatal("invalid isolated PostgreSQL configuration")
		}
		schema := cfg.RuntimeParams["search_path"]
		if !regexp.MustCompile(`^suma_test_[0-9a-f]{24}$`).MatchString(schema) {
			t.Fatal("refusing restore outside a randomly owned test schema")
		}
		inspect, err := exec.CommandContext(ctx, "docker", "inspect", "--format", `{{index .Config.Labels "suma.task-center.validation"}} {{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}`, container).Output()
		if err != nil {
			t.Fatal("inspect disposable PostgreSQL fixture failed")
		}
		identity := strings.Fields(string(inspect))
		if len(identity) != 2 || identity[0] == "<no" || identity[1] != cfg.Host {
			t.Fatal("PostgreSQL container does not own this isolated test connection")
		}
		dump, err := exec.CommandContext(ctx, "docker", "exec", container, "pg_dump", "--username", cfg.User, "--dbname", cfg.Database, "--schema", schema, "--format=custom", "--no-owner", "--no-privileges").Output()
		if err != nil || len(dump) == 0 {
			t.Fatal("back up isolated PostgreSQL schema failed")
		}
		list := exec.CommandContext(ctx, "docker", "exec", "-i", container, "pg_restore", "--list")
		list.Stdin = bytes.NewReader(dump)
		if err := list.Run(); err != nil {
			t.Fatal("verify PostgreSQL backup archive failed")
		}
		archive := filepath.Join(root, "application-files.tar.gz")
		private, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err := private.Close(); err != nil {
			t.Fatal(err)
		}
		if err := exec.CommandContext(ctx, "tar", "-czf", archive, "-C", root, "secret.key", "compose.yml").Run(); err != nil {
			t.Fatal("back up fixture application files failed")
		}
		connection, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal("open isolated schema for restoration failed")
		}
		defer connection.Close()
		if _, err := connection.ExecContext(ctx, `DROP SCHEMA "`+schema+`" CASCADE`); err != nil {
			t.Fatal("remove only owned disposable schema failed")
		}
		restore := exec.CommandContext(ctx, "docker", "exec", "-i", container, "pg_restore", "--exit-on-error", "--username", cfg.User, "--dbname", cfg.Database, "--no-owner", "--no-privileges")
		restore.Stdin = bytes.NewReader(dump)
		if err := restore.Run(); err != nil {
			t.Fatal("restore isolated PostgreSQL backup failed")
		}
		for _, name := range []string{"secret.key", "compose.yml"} {
			if err := os.Remove(filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
		}
		if err := exec.CommandContext(ctx, "tar", "-xzf", archive, "-C", root).Run(); err != nil {
			t.Fatal("restore fixture application files failed")
		}
		t.Logf("restored isolated PostgreSQL schema (%d backup bytes) and original application key/files before upgrade", len(dump))
	})
}
