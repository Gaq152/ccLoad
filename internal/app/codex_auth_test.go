package app

import (
	"encoding/base64"
	"testing"
)

func makeTestJWT(payload string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return header + "." + body + ".signature"
}

func TestExtractAccountIDFromJWT(t *testing.T) {
	token := makeTestJWT(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct_test_123"}}`)

	if got := ExtractAccountIDFromJWT(token); got != "acct_test_123" {
		t.Fatalf("expected account id acct_test_123, got %q", got)
	}
}

func TestExtractEmailFromIDToken(t *testing.T) {
	token := makeTestJWT(`{"email":"user@example.com","sub":"auth0|123"}`)

	if got := ExtractEmailFromIDToken(token); got != "user@example.com" {
		t.Fatalf("expected email user@example.com, got %q", got)
	}
}
