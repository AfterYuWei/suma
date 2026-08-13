package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTripAndPersistsKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := first.Encrypt("private-token")
	if err != nil {
		t.Fatal(err)
	}
	if string(ciphertext) == "private-token" {
		t.Fatal("secret was not encrypted")
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := second.Decrypt(ciphertext)
	if err != nil || plaintext != "private-token" {
		t.Fatalf("decrypt = %q, %v", plaintext, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o", info.Mode().Perm())
	}
}

func TestStoreRejectsTampering(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := store.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := store.Decrypt(ciphertext); err == nil {
		t.Fatal("expected authentication failure")
	}
}
