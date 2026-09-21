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

func TestNodeGroupsSupportMultipleAndNoGroupMemberships(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "groups.db"))
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
	t.Cleanup(func() { _ = service.Close() })

	groups, err := service.ListGroups(context.Background())
	if err != nil || len(groups) != 1 || !groups[0].IsDefault || groups[0].NodeCount != 1 {
		t.Fatalf("bootstrapped groups = %#v, err = %v", groups, err)
	}
	edge, err := service.CreateGroup(context.Background(), GroupInput{Name: "Edge", Description: "Remote engines"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateGroup(context.Background(), GroupInput{Name: "edge"}); err == nil {
		t.Fatal("expected case-insensitive duplicate group name rejection")
	}
	normalized, err := service.validateGroupIDs(context.Background(), []uint{edge.ID, groups[0].ID, edge.ID})
	if err != nil || len(normalized) != 2 || normalized[0] >= normalized[1] {
		t.Fatalf("normalized group IDs = %#v, err = %v", normalized, err)
	}
	if _, err := service.validateGroupIDs(context.Background(), []uint{999999}); err == nil {
		t.Fatal("expected unknown group ID rejection")
	}
	if err := db.Create(&database.Node{ID: "remote", Name: "Remote", ConnectionType: "unix", Endpoint: "unix:///remote.sock", TLSMode: "disabled", AllowedBindRootsJSON: "[]", Enabled: false}).Error; err != nil {
		t.Fatal(err)
	}
	if err := replaceNodeGroups(db, "remote", []uint{groups[0].ID, edge.ID}); err != nil {
		t.Fatal(err)
	}
	filtered, err := service.ListFiltered(context.Background(), &edge.ID)
	if err != nil || len(filtered) != 1 || filtered[0].ID != "remote" || len(filtered[0].GroupIDs) != 2 {
		t.Fatalf("edge filter = %#v, err = %v", filtered, err)
	}
	if err := replaceNodeGroups(db, "remote", []uint{}); err != nil {
		t.Fatal(err)
	}
	allNodes, err := service.ListFiltered(context.Background(), nil)
	if err != nil || len(allNodes) != 2 || len(allNodes[1].GroupIDs) != 0 {
		t.Fatalf("all-node filter with no-group node = %#v, err = %v", allNodes, err)
	}
	if err := service.DeleteGroup(context.Background(), edge.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetGroup(context.Background(), edge.ID); err == nil {
		t.Fatal("deleted group is still readable")
	}
	if err := replaceNodeGroups(db, "remote", []uint{groups[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), "remote"); err != nil {
		t.Fatal(err)
	}
	var membershipCount int64
	if err := db.Model(&database.NodeGroupNode{}).Where("node_id = ?", "remote").Count(&membershipCount).Error; err != nil || membershipCount != 0 {
		t.Fatalf("deleted node memberships = %d, err = %v", membershipCount, err)
	}
}
