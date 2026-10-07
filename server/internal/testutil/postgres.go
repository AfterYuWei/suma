package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/suma/suma/server/internal/config"
	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
)

// DSN creates a private PostgreSQL schema. No database means a failed gate.
func DSN(t *testing.T) string {
	t.Helper()
	values, err := config.ReadEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	base := values.Get("SUMA_TEST_DATABASE_DSN", "")
	if base == "" {
		t.Fatal("SUMA_TEST_DATABASE_DSN is required; set it in .env.local or the test environment")
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal("open PostgreSQL test connection failed")
	}
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	schema := "suma_test_" + hex.EncodeToString(raw)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		_ = admin.Close()
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) {
			t.Fatalf("create isolated PostgreSQL test schema failed (SQLSTATE %s); check test database credentials and CREATE permission", databaseError.Code)
		}
		t.Fatal("create isolated PostgreSQL test schema failed")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, `DROP SCHEMA "`+schema+`" CASCADE`); err != nil {
			t.Error("clean up PostgreSQL test schema failed")
		}
		_ = admin.Close()
	})
	if strings.HasPrefix(base, "postgres://") || strings.HasPrefix(base, "postgresql://") {
		u, err := url.Parse(base)
		if err != nil {
			t.Fatal("invalid PostgreSQL test URL")
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		return u.String()
	}
	return fmt.Sprintf("%s search_path=%s", base, schema)
}
func Open(t *testing.T) (*gorm.DB, error) {
	t.Helper()
	db, err := database.Open(DSN(t))
	if err == nil {
		t.Cleanup(func() {
			pool, _ := db.DB()
			if pool != nil {
				_ = pool.Close()
			}
		})
	}
	return db, err
}
