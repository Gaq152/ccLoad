package sqlite_test

import (
	"ccLoad/internal/model"
	"ccLoad/internal/util"
	"context"
	"testing"
	"time"
)

func TestLogAPIKeyHashRoundTrip(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()

	ctx := context.Background()
	expectedHash := util.HashAPIKey("sk-test-key")

	err := store.AddLog(ctx, &model.LogEntry{
		Time:       model.JSONTime{Time: time.Now()},
		Model:      "claude-3",
		StatusCode: 200,
		Message:    "ok",
		APIKeyUsed: "sk-test-key",
		APIKeyHash: expectedHash,
	})
	if err != nil {
		t.Fatalf("AddLog failed: %v", err)
	}

	logs, err := store.ListLogs(ctx, time.Time{}, 10, 0, nil)
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}

	if logs[0].APIKeyHash != expectedHash {
		t.Fatalf("expected api_key_hash %q, got %q", expectedHash, logs[0].APIKeyHash)
	}
	if logs[0].APIKeyUsed == "sk-test-key" {
		t.Fatalf("expected stored API key to stay masked, got %q", logs[0].APIKeyUsed)
	}
}

func TestLogOAuthMarkerKeepsEmptyHash(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()

	ctx := context.Background()

	err := store.AddLog(ctx, &model.LogEntry{
		Time:       model.JSONTime{Time: time.Now()},
		Model:      "claude-3",
		StatusCode: 200,
		Message:    "ok",
		APIKeyUsed: "[OAuth]",
	})
	if err != nil {
		t.Fatalf("AddLog failed: %v", err)
	}

	logs, err := store.ListLogs(ctx, time.Time{}, 10, 0, nil)
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}

	if logs[0].APIKeyUsed != "[OAuth]" {
		t.Fatalf("expected OAuth marker, got %q", logs[0].APIKeyUsed)
	}
	if logs[0].APIKeyHash != "" {
		t.Fatalf("expected empty api_key_hash for OAuth marker, got %q", logs[0].APIKeyHash)
	}
}
