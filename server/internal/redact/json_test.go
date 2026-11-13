package redact

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestJSONPreservesStructureAndNumbersWhileRedacting(t *testing.T) {
	raw := json.RawMessage(`{"id":9007199254740993,"healthy":true,"password":{"nested":"private"},"registry_credential_ids":[3,9],"items":[{"token":false,"text":"中文\nquoted \" text"}],"value":"https://user:password@example.com"}`)
	safe, err := JSON(raw, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(safe) || !strings.Contains(string(safe), `9007199254740993`) || !strings.Contains(string(safe), `"registry_credential_ids":[3,9]`) || !strings.Contains(string(safe), `"healthy":true`) {
		t.Fatalf("structure or evidence changed: %s", safe)
	}
	for _, secret := range []string{"private", "user:password", `"token":false`} {
		if strings.Contains(string(safe), secret) {
			t.Fatalf("sensitive field survived: %s", safe)
		}
	}
}

func TestJSONRejectsSizeInsteadOfTruncating(t *testing.T) {
	value := map[string]string{"output": strings.Repeat("中文", 30_000)}
	if _, err := JSON(value, 65_536); !errors.Is(err, ErrJSONTooLarge) {
		t.Fatalf("expected an explicit limit error, got %v", err)
	}
	if raw, err := JSON(value, 1<<20); err != nil || !json.Valid(raw) {
		t.Fatalf("large evidence must be retained: %v", err)
	}
	if _, err := JSON(make(chan int), 4096); err == nil {
		t.Fatal("unsupported result must return an encoding error")
	}
}
