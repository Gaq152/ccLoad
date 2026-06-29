package app

import (
	"encoding/base64"
	"net/http"
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

func TestNewCodexExtraHeadersPreservesIncomingSessionHeaders(t *testing.T) {
	incoming := http.Header{}
	incoming.Set("conversation_id", "conv-original")
	incoming.Set("session_id", "sess-original")

	headers := NewCodexExtraHeaders("acct-test", incoming)

	if headers.AccountID != "acct-test" {
		t.Fatalf("AccountID = %q, want acct-test", headers.AccountID)
	}
	if headers.ConversationID != "conv-original" {
		t.Fatalf("ConversationID = %q, want conv-original", headers.ConversationID)
	}
	if headers.SessionID != "sess-original" {
		t.Fatalf("SessionID = %q, want sess-original", headers.SessionID)
	}
}

func TestNewCodexExtraHeadersGeneratesMissingSessionHeaders(t *testing.T) {
	headers := NewCodexExtraHeaders("acct-test", http.Header{})

	if headers.ConversationID == "" {
		t.Fatal("expected generated ConversationID")
	}
	if headers.SessionID == "" {
		t.Fatal("expected generated SessionID")
	}
}
