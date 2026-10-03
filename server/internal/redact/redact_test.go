package redact

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRedactionAndUTF8Limit(t *testing.T) {
	raw := "PASSWORD=private-password Authorization: Bearer private-token\nhttps://user:password@example.com/x\nhttps://api.telegram.org/bot12345:PRIVATE/getMe\nhttps://open.feishu.cn/open-apis/bot/v2/hook/PRIVATE\n-----BEGIN PRIVATE KEY-----\nPRIVATE\n-----END PRIVATE KEY-----"
	safe := Text(raw)
	for _, secret := range []string{"private-password", "private-token", "user:password", "PRIVATE\n"} {
		if strings.Contains(safe, secret) {
			t.Fatalf("leaked %q: %s", secret, safe)
		}
	}
	if !utf8.ValidString(Bounded(strings.Repeat("中文", 200), 31)) {
		t.Fatal("split UTF8")
	}
}

func TestJSONNestedAndStandaloneSecretsAreNotReturned(t *testing.T) {
	raw := `{"environment":{"DATABASE_PASSWORD":"quoted-private-value","app_secret":"escaped\"private-value","apiKey":"short-secret","access_key":"access-private-value","Cookie":"session=private-cookie"},"healthy":true}`
	safe := Text(raw)
	if !json.Valid([]byte(safe)) {
		t.Fatal("redaction broke JSON", safe)
	}
	for _, value := range []string{"quoted-private-value", "escaped", "short-secret", "access-private-value", "private-cookie"} {
		if strings.Contains(safe, value) {
			t.Fatal("nested JSON secret survived")
		}
	}
	for _, value := range []string{"sk-proj-" + strings.Repeat("a", 30), "AKIA" + strings.Repeat("A", 16), "123456789:" + strings.Repeat("a", 35), "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature"} {
		if strings.Contains(Text("unlabelled value "+value), value) {
			t.Fatal("standalone credential survived")
		}
	}
}
