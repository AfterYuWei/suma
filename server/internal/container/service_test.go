package container

import "testing"

func TestMaskEnvironment(t *testing.T) {
	values := MaskEnvironment([]string{"MODE=production", "DATABASE_PASSWORD=secret", "API_TOKEN=value"})
	if values[0].Value != "production" || values[0].Sensitive {
		t.Fatal("ordinary value should be visible")
	}
	if values[1].Value != "" || !values[1].Sensitive || values[2].Value != "" {
		t.Fatal("secrets should be masked")
	}
}
