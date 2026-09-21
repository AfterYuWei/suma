package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/suma/suma/server/internal/database"
)

func TestNormalizeWebAuthnOrigin(t *testing.T) {
	tests := []struct {
		value, origin, rpID string
		valid               bool
	}{
		{"https://suma.example.com", "https://suma.example.com", "suma.example.com", true},
		{"https://SUMA.example.com:8443", "https://suma.example.com:8443", "suma.example.com", true},
		{"http://localhost:5173", "http://localhost:5173", "localhost", true},
		{"http://127.0.0.1:8080", "", "", false},
		{"http://suma.example.com", "", "", false},
		{"https://suma.example.com/path", "", "", false},
		{"", "", "", false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			origin, rpID, err := NormalizeWebAuthnOrigin(test.value)
			if test.valid && (err != nil || origin != test.origin || rpID != test.rpID) {
				t.Fatalf("got (%q, %q, %v), want (%q, %q, nil)", origin, rpID, err, test.origin, test.rpID)
			}
			if !test.valid && !errors.Is(err, ErrInvalidWebAuthnOrigin) {
				t.Fatalf("error = %v, want ErrInvalidWebAuthnOrigin", err)
			}
		})
	}
}

func TestPasskeyCeremoniesAreSecureAndOneTime(t *testing.T) {
	ctx := context.Background()
	service := testService(t)
	user, err := service.Initialize(ctx, "admin", "admin@example.test", "Administrator", "long-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginPasskeyRegistration(ctx, user.ID, "wrong-password", "", "Laptop", "https://suma.example.test", "suma.example.test"); !errors.Is(err, ErrCurrentPassword) {
		t.Fatalf("wrong password error = %v", err)
	}
	result, err := service.BeginPasskeyRegistration(ctx, user.ID, "long-password", "", "Laptop", "https://suma.example.test", "suma.example.test")
	if err != nil {
		t.Fatal(err)
	}
	options, ok := result.Options.(*protocol.CredentialCreation)
	if !ok {
		t.Fatalf("options type = %T", result.Options)
	}
	if options.Response.RelyingParty.ID != "suma.example.test" || options.Response.AuthenticatorSelection.ResidentKey != protocol.ResidentKeyRequirementRequired || options.Response.AuthenticatorSelection.UserVerification != protocol.VerificationRequired {
		t.Fatalf("unsafe registration options: %#v", options.Response)
	}
	var row database.User
	if err := service.db.Select("passkey_user_handle").First(&row, user.ID).Error; err != nil || len(row.PasskeyUserHandle) != 32 {
		t.Fatalf("user handle length = %d, error = %v", len(row.PasskeyUserHandle), err)
	}
	var ceremony database.WebAuthnCeremony
	if err := service.db.Where("kind = ?", passkeyKindRegister).First(&ceremony).Error; err != nil {
		t.Fatal(err)
	}
	if ceremony.TokenHash == result.CeremonyToken || ceremony.Origin != "https://suma.example.test" {
		t.Fatalf("ceremony was not hashed or origin-bound: %#v", ceremony)
	}
	if _, _, err := service.consumeWebAuthnCeremony(ctx, result.CeremonyToken, passkeyKindRegister, user.ID); err != nil {
		t.Fatalf("consume ceremony: %v", err)
	}
	if _, _, err := service.consumeWebAuthnCeremony(ctx, result.CeremonyToken, passkeyKindRegister, user.ID); !errors.Is(err, ErrInvalidCeremony) {
		t.Fatalf("reused ceremony error = %v", err)
	}

	login, err := service.BeginPasskeyLogin(ctx, "https://suma.example.test", "suma.example.test")
	if err != nil {
		t.Fatal(err)
	}
	assertion, ok := login.Options.(*protocol.CredentialAssertion)
	if !ok || assertion.Response.UserVerification != protocol.VerificationRequired || len(assertion.Response.AllowedCredentials) != 0 {
		t.Fatalf("unsafe discoverable login options: %#v", login.Options)
	}
}
