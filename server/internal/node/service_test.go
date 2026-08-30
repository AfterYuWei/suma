package node

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
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
		{"tcp plaintext private 10", ConnectionTCP, "tcp://10.0.0.10:2375", TLSDisabled, nil, ""},
		{"tcp plaintext private 172", ConnectionTCP, "tcp://172.16.1.10:2375", TLSDisabled, nil, ""},
		{"tcp plaintext private 192", ConnectionTCP, "tcp://192.168.1.10:2375", TLSDisabled, nil, ""},
		{"tcp plaintext Tailscale IPv4", ConnectionTCP, "tcp://100.100.10.20:2375", TLSDisabled, nil, ""},
		{"tcp plaintext Tailscale IPv6", ConnectionTCP, "tcp://[fd7a:115c:a1e0::1]:2375", TLSDisabled, nil, ""},
		{"tcp plaintext outside Tailscale range rejected", ConnectionTCP, "tcp://100.128.0.1:2375", TLSDisabled, nil, "Tailscale"},
		{"tcp plaintext public rejected", ConnectionTCP, "tcp://203.0.113.10:2375", TLSDisabled, nil, "private network"},
		{"tcp plaintext hostname rejected", ConnectionTCP, "tcp://docker.internal:2375", TLSDisabled, nil, "private network"},
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

func TestPrepareRequiresPlaintextEndpointConfirmation(t *testing.T) {
	service := &Service{}
	input := Input{Name: "LAN", ConnectionType: ConnectionTCP, Endpoint: "tcp://192.168.1.10:2375", TLSMode: TLSDisabled, Enabled: true}
	if _, err := service.prepare(context.Background(), "lan", input); err == nil || !strings.Contains(err.Error(), "192.168.1.10") {
		t.Fatalf("expected plaintext confirmation error, got %v", err)
	}
	input.PlaintextConfirmation = "192.168.1.10"
	if _, err := service.prepare(context.Background(), "lan", input); err != nil {
		t.Fatalf("matching plaintext confirmation was rejected: %v", err)
	}
}

func TestTLSCredentialIsEncryptedAndRedacted(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "suma.db"))
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
