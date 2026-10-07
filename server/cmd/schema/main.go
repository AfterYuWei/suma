// Command schema regenerates the development PostgreSQL baseline offline.
package main

import (
	"context"
	"fmt"
	"github.com/suma/suma/server/internal/database"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"strings"
	"time"
)

type schemaLogger struct{ statements *[]string }

func (l schemaLogger) LogMode(logger.LogLevel) logger.Interface { return l }
func (schemaLogger) Info(context.Context, string, ...any)       {}
func (schemaLogger) Warn(context.Context, string, ...any)       {}
func (schemaLogger) Error(context.Context, string, ...any)      {}
func (l schemaLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	s, _ := fc()
	*l.statements = append(*l.statements, s+";")
}
func main() {
	var statements []string
	db, err := gorm.Open(postgres.Open("host=localhost user=schema dbname=schema sslmode=disable"), &gorm.Config{DryRun: true, DisableAutomaticPing: true, Logger: schemaLogger{&statements}})
	if err != nil {
		panic(err)
	}
	if err = db.Migrator().CreateTable(database.Models()...); err != nil {
		panic(err)
	}
	statements = append(statements, "CREATE UNIQUE INDEX idx_users_email_lower ON users (lower(email)) WHERE email <> '';", "CREATE UNIQUE INDEX idx_node_groups_name_lower ON node_groups (lower(name));", "INSERT INTO node_groups (name, description, is_default, created_at, updated_at) VALUES ('Default', '', true, now(), now());")
	content := "-- PostgreSQL v1 business schema. No historical database import.\n" + strings.Join(statements, "\n") + "\n"
	if err = os.WriteFile("internal/database/migrations/0001_initial.sql", []byte(content), 0644); err != nil {
		panic(err)
	}
	fmt.Printf("Generated %d PostgreSQL schema statements\n", len(statements))
}
