package app

import (
	"testing"

	"ccLoad/internal/storage"
)

func TestTraceToListItemIncludesCacheTokens(t *testing.T) {
	trace := &storage.Trace{
		ID:                  42,
		Time:                123000,
		ChannelID:           7,
		ChannelName:         "codex-main",
		ChannelType:         "codex",
		Model:               "gpt-5.5",
		RequestPath:         "/v1/responses",
		StatusCode:          200,
		Duration:            1.25,
		IsStreaming:         true,
		InputTokens:         100,
		OutputTokens:        20,
		CacheReadTokens:     80,
		CacheCreationTokens: 16,
		ClientIP:            "127.0.0.1",
		APIKeyUsed:          "test-key",
		TokenID:             9,
		AuthTokenName:       "local",
	}

	item := traceToListItem(trace)

	if item.CacheReadTokens != trace.CacheReadTokens {
		t.Fatalf("CacheReadTokens = %d, want %d", item.CacheReadTokens, trace.CacheReadTokens)
	}
	if item.CacheCreationTokens != trace.CacheCreationTokens {
		t.Fatalf("CacheCreationTokens = %d, want %d", item.CacheCreationTokens, trace.CacheCreationTokens)
	}
	if item.InputTokens != trace.InputTokens || item.OutputTokens != trace.OutputTokens {
		t.Fatalf("token fields = %d/%d, want %d/%d", item.InputTokens, item.OutputTokens, trace.InputTokens, trace.OutputTokens)
	}
}
