package sqlite_test

import (
	"context"
	"testing"
	"time"

	"ccLoad/internal/model"
)

func TestFastBillingConfigRoundTrip(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()

	ctx := context.Background()
	created, err := store.CreateConfig(ctx, &model.Config{
		Name:        "fast-billing-custom",
		URL:         "https://api.example.com",
		Models:      []string{"gpt-5.5"},
		ChannelType: "codex",
		Enabled:     true,
		FastBillingConfig: &model.FastBillingConfig{
			Multipliers: map[string]float64{
				"gpt-5.5": 3,
				"gpt-5.6": 4,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateConfig failed: %v", err)
	}

	got, err := store.GetConfig(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if got.FastBillingConfig == nil {
		t.Fatal("FastBillingConfig = nil, want persisted config")
	}
	if got.FastBillingConfig.Multipliers["gpt-5.5"] != 3 {
		t.Fatalf("gpt-5.5 multiplier = %v, want 3", got.FastBillingConfig.Multipliers["gpt-5.5"])
	}
	if got.FastBillingConfig.Multipliers["gpt-5.6"] != 4 {
		t.Fatalf("gpt-5.6 multiplier = %v, want 4", got.FastBillingConfig.Multipliers["gpt-5.6"])
	}
}

func TestFastBillingConfigDefaultsForNilStoredConfig(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()

	ctx := context.Background()
	created, err := store.CreateConfig(ctx, &model.Config{
		Name:        "fast-billing-default",
		URL:         "https://api.example.com",
		Models:      []string{"gpt-5.5"},
		ChannelType: "codex",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("CreateConfig failed: %v", err)
	}

	got, err := store.GetConfig(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if got.FastBillingConfig == nil {
		t.Fatal("FastBillingConfig = nil, want default config")
	}
	if got.FastBillingConfig.ResolveMultiplier("gpt-5.5-20260701") != 2.5 {
		t.Fatalf("default gpt-5.5 multiplier = %v, want 2.5", got.FastBillingConfig.ResolveMultiplier("gpt-5.5-20260701"))
	}
}

func TestFastBillingLogRoundTrip(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()

	ctx := context.Background()
	err := store.AddLog(ctx, &model.LogEntry{
		Time:           model.JSONTime{Time: time.Now()},
		Model:          "gpt-5.5",
		RequestType:    "compact_v2",
		StatusCode:     200,
		Message:        "ok",
		IsFast:         true,
		ServiceTier:    "priority",
		FastMultiplier: 2.5,
		Cost:           0.025,
	})
	if err != nil {
		t.Fatalf("AddLog failed: %v", err)
	}

	logs, err := store.ListLogs(ctx, time.Time{}, 10, 0, nil)
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs len = %d, want 1", len(logs))
	}
	if !logs[0].IsFast {
		t.Fatal("IsFast = false, want true")
	}
	if logs[0].ServiceTier != "priority" {
		t.Fatalf("ServiceTier = %q, want priority", logs[0].ServiceTier)
	}
	if logs[0].FastMultiplier != 2.5 {
		t.Fatalf("FastMultiplier = %v, want 2.5", logs[0].FastMultiplier)
	}
	if logs[0].RequestType != "compact_v2" {
		t.Fatalf("RequestType = %q, want compact_v2", logs[0].RequestType)
	}

	rangeLogs, err := store.ListLogsRange(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), 10, 0, nil)
	if err != nil {
		t.Fatalf("ListLogsRange failed: %v", err)
	}
	if len(rangeLogs) != 1 || !rangeLogs[0].IsFast || rangeLogs[0].FastMultiplier != 2.5 || rangeLogs[0].RequestType != "compact_v2" {
		t.Fatalf("ListLogsRange fast metadata = %+v, want one fast log with multiplier 2.5", rangeLogs)
	}
}
