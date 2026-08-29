package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type legacyUser struct {
	ID           uint   `gorm:"primaryKey"`
	Username     string `gorm:"uniqueIndex;size:64;not null"`
	PasswordHash string `gorm:"not null"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (legacyUser) TableName() string { return "users" }

func TestOpenMigratesDatabase(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&User{}) || !db.Migrator().HasTable(&Task{}) {
		t.Fatal("expected core tables to be migrated")
	}
}

func TestOpenMigratesLegacyUserProfileColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.AutoMigrate(&legacyUser{}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Create(&legacyUser{Username: "admin", PasswordHash: "hash"}).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := legacy.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"nickname", "email", "avatar_data", "avatar_mime", "avatar_updated_at"} {
		if !db.Migrator().HasColumn(&User{}, column) {
			t.Fatalf("missing migrated users.%s", column)
		}
	}
	var user User
	if err := db.First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if user.Username != "admin" || user.Email != "" || user.Nickname != "" {
		t.Fatalf("legacy user changed during migration: %#v", user)
	}
}
