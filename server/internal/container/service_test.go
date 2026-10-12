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

func TestMaskEnvironmentCredentialVariants(t *testing.T) {
	values := MaskEnvironment([]string{"DB_PASS=private-value", "CREDENTIAL=private-value", "DATABASE_URL=postgres://user:private-value@localhost/app", "APIKEY=private-value", "MODE=production"})
	for _, value := range values[:4] {
		if !value.Sensitive || value.Value != "" {
			t.Fatal("credential variant was exposed")
		}
	}
	if values[4].Sensitive || values[4].Value != "production" {
		t.Fatal("ordinary environment value was hidden")
	}
}
