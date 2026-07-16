package storage

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestEnsurePricingHighPriceThresholdSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE model_pricing (
			id INTEGER PRIMARY KEY,
			model TEXT NOT NULL,
			channel_type TEXT NOT NULL
		);
		INSERT INTO model_pricing (model, channel_type) VALUES
			('gpt-custom', 'openai'),
			('gemini-custom', 'gemini');
	`); err != nil {
		t.Fatal(err)
	}

	if err := ensurePricingHighPriceThresholdSQLite(ctx, db); err != nil {
		t.Fatal(err)
	}
	// 迁移必须可重复执行。
	if err := ensurePricingHighPriceThresholdSQLite(ctx, db); err != nil {
		t.Fatal(err)
	}

	thresholds := map[string]int64{}
	rows, err := db.QueryContext(ctx, "SELECT model, high_price_threshold FROM model_pricing")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		var threshold int64
		if err := rows.Scan(&model, &threshold); err != nil {
			t.Fatal(err)
		}
		thresholds[model] = threshold
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if got := thresholds["gpt-custom"]; got != 272_000 {
		t.Fatalf("GPT迁移阈值 = %d, want 272000", got)
	}
	if got := thresholds["gemini-custom"]; got != 200_000 {
		t.Fatalf("Gemini迁移阈值 = %d, want 200000", got)
	}
}
