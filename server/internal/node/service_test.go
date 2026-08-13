package node

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dockport/dockport/server/internal/database"
	"github.com/dockport/dockport/server/internal/secret"
)

func TestValidateEndpointSecurity(t *testing.T) {
	tests := []struct {
		name, connection, endpoint, tls string
		credential                      *uint
		want                            string
	}{
		{"unix absolute", ConnectionUnix, "unix:///var/run/docker.sock", TLSDisabled, nil, ""},
		{"unix host rejected", ConnectionUnix, "unix://remote/var/run/docker.sock", TLSDisabled, nil, "absolute unix"},
		{"tcp plaintext loopback", ConnectionTCP, "tcp://127.0.0.1:2375", TLSDisabled, nil, ""},
		{"tcp plaintext network rejected", ConnectionTCP, "tcp://192.168.1.10:2375", TLSDisabled, nil, "loopback"},
		{"tcp missing port", ConnectionTCP, "tcp://localhost", TLSDisabled, nil, "explicit port"},
		{"tcp tls credential required", ConnectionTCP, "tcp://docker.example:2376", TLSRequired, nil, "credential"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateEndpoint(test.connection, test.endpoint, test.tls, test.credential)
			if test.want == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestNormalizeRootsRejectsProtectedPaths(t *testing.T) {
	for _, value := range []string{"relative", "/", "/etc/dockport", "/var/run/docker"} {
		if _, _, err := normalizeRoots([]string{value}); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
	roots, encoded, err := normalizeRoots([]string{"/srv/apps", "/srv/apps", "/opt/stacks"})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || encoded != `["/opt/stacks","/srv/apps"]` {
		t.Fatalf("unexpected normalized roots: %#v %s", roots, encoded)
	}
}

func TestTLSCredentialIsEncryptedAndRedacted(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "dockport.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(db, store, "unix:///var/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	input := TLSCredentialInput{Name: "remote", CA: "SECRET-CA", Certificate: "SECRET-CERT", PrivateKey: "SECRET-KEY", AuthorizedNodeIDs: []string{"local"}}
	view, err := service.CreateTLSCredential(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var row database.DockerTLSCredential
	if err := db.First(&row, view.ID).Error; err != nil {
		t.Fatal(err)
	}
	for _, ciphertext := range [][]byte{row.CACiphertext, row.CertificateCiphertext, row.PrivateKeyCiphertext} {
		if len(ciphertext) == 0 || strings.Contains(string(ciphertext), "SECRET-") {
			t.Fatal("credential material was not encrypted")
		}
	}
	response, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(response), "SECRET-") || strings.Contains(string(response), "private_key") {
		t.Fatalf("credential material leaked in response: %s", response)
	}
}
