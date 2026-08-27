package database

import (
	"path/filepath"
	"testing"
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
