package database

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestOpenMigratesDatabase(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&User{}) || !db.Migrator().HasTable(&Task{}) {
		t.Fatal("expected core tables to be migrated")
	}
}

func TestOpenReplacesLegacyComposeNameUniqueness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "suma.db")
	legacy, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec(`CREATE TABLE compose_projects (id integer primary key autoincrement, name text, path text); CREATE UNIQUE INDEX idx_compose_projects_name ON compose_projects(name); INSERT INTO compose_projects(name,path) VALUES ('app','/old');`).Error; err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if db.Migrator().HasIndex(&ComposeProject{}, "idx_compose_projects_name") {
		t.Fatal("legacy global name index still exists")
	}
	if err := db.Create(&ComposeProject{NodeID: "remote", Name: "app", Path: "/new"}).Error; err != nil {
		t.Fatalf("same name on another node should be allowed: %v", err)
	}
}
