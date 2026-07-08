package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestTraceStoreFastBillingRoundTrip(t *testing.T) {
	store, err := NewTraceStore(filepath.Join(t.TempDir(), "debug_traces.db"))
	if err != nil {
		t.Fatalf("NewTraceStore failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	id, err := store.Save(ctx, &Trace{
		Time:           time.Now().UnixMilli(),
		ChannelID:      1,
		ChannelName:    "fast-channel",
		ChannelType:    "codex",
		Model:          "gpt-5.5",
		StatusCode:     200,
		IsFast:         true,
		ServiceTier:    "priority",
		FastMultiplier: 2.5,
	})
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	items, err := store.List(ctx, 10, 0, "")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	if !items[0].IsFast || items[0].ServiceTier != "priority" || items[0].FastMultiplier != 2.5 {
		t.Fatalf("list fast metadata = %+v, want fast priority 2.5", items[0])
	}

	trace, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !trace.IsFast || trace.ServiceTier != "priority" || trace.FastMultiplier != 2.5 {
		t.Fatalf("detail fast metadata = %+v, want fast priority 2.5", trace)
	}
}
