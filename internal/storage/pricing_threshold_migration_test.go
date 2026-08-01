package storage

import (
	"context"
	"database/sql"
	"math"
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
			channel_type TEXT NOT NULL,
			input_price_high DOUBLE NOT NULL DEFAULT 0
		);
		INSERT INTO model_pricing (model, channel_type, input_price_high) VALUES
			('gpt-no-tier', 'openai', 0),
			('gpt-tiered', 'openai', 4),
			('gemini-tiered', 'gemini', 2.5);
	`); err != nil {
		t.Fatal(err)
	}

	if err := ensurePricingHighPriceThresholdSQLite(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := normalizePricingHighPriceThreshold(ctx, db); err != nil {
		t.Fatal(err)
	}
	// 迁移必须可重复执行。
	if err := ensurePricingHighPriceThresholdSQLite(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := normalizePricingHighPriceThreshold(ctx, db); err != nil {
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

	if got := thresholds["gpt-no-tier"]; got != 0 {
		t.Fatalf("无分档 GPT 迁移阈值 = %d, want 0", got)
	}
	if got := thresholds["gpt-tiered"]; got != 272_000 {
		t.Fatalf("分档 GPT 迁移阈值 = %d, want 272000", got)
	}
	if got := thresholds["gemini-tiered"]; got != 200_000 {
		t.Fatalf("Gemini迁移阈值 = %d, want 200000", got)
	}
}

func TestMigratePricingCacheMultipliersAndDefaultFlagSQLite(t *testing.T) {
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
			channel_type TEXT NOT NULL,
			input_price DOUBLE NOT NULL,
			output_price DOUBLE NOT NULL,
			input_price_high DOUBLE NOT NULL DEFAULT 0,
			output_price_high DOUBLE NOT NULL DEFAULT 0,
			cache_read_multiplier DOUBLE NOT NULL DEFAULT 0,
			cache_write_multiplier DOUBLE NOT NULL DEFAULT 0,
			is_predefined INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO model_pricing
			(id, model, channel_type, input_price, output_price, input_price_high, output_price_high,
			 cache_read_multiplier, cache_write_multiplier, is_predefined)
		VALUES
			(1, 'claude-custom', 'anthropic', 3, 15, 6, 22.5, 0.2, 1.5, 1),
			(2, 'gpt-4.1-custom', 'openai', 2, 8, 4, 12, 0, 0, 0);
	`); err != nil {
		t.Fatal(err)
	}

	if err := ensurePricingDefault(ctx, db, DialectSQLite); err != nil {
		t.Fatal(err)
	}
	if err := ensurePricingAbsoluteCachePrices(ctx, db, DialectSQLite); err != nil {
		t.Fatal(err)
	}
	// 所有迁移都必须可重复执行，且不能再次乘倍率。
	if err := ensurePricingDefault(ctx, db, DialectSQLite); err != nil {
		t.Fatal(err)
	}
	if err := ensurePricingAbsoluteCachePrices(ctx, db, DialectSQLite); err != nil {
		t.Fatal(err)
	}

	type migratedPrices struct {
		read, write, readHigh, writeHigh float64
		isDefault                        int
	}
	got := map[string]migratedPrices{}
	rows, err := db.QueryContext(ctx, `SELECT model, cache_read_price, cache_write_price,
		cache_read_price_high, cache_write_price_high, is_default FROM model_pricing`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var model string
		var prices migratedPrices
		if err := rows.Scan(&model, &prices.read, &prices.write, &prices.readHigh, &prices.writeHigh, &prices.isDefault); err != nil {
			t.Fatal(err)
		}
		got[model] = prices
	}

	claude := got["claude-custom"]
	if !near(claude.read, 0.6) || !near(claude.write, 4.5) || !near(claude.readHigh, 1.2) || !near(claude.writeHigh, 9) || claude.isDefault != 1 {
		t.Fatalf("Claude 迁移结果 = %+v", claude)
	}
	gpt := got["gpt-4.1-custom"]
	if !near(gpt.read, 0.5) || !near(gpt.write, 2.5) || !near(gpt.readHigh, 1) || !near(gpt.writeHigh, 5) || gpt.isDefault != 0 {
		t.Fatalf("GPT 迁移结果 = %+v", gpt)
	}
}

func TestNormalizeModelsDevPricingPrefixesSQLite(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE model_pricing (
			id INTEGER PRIMARY KEY,
			model TEXT NOT NULL UNIQUE,
			aliases TEXT
		);
		INSERT INTO model_pricing (id, model, aliases) VALUES
			(1, 'anthropic.claude-opus-5', ''),
			(2, 'gpt-5.2', 'manual-alias'),
			(3, 'openai.gpt-5.2', 'old-openai-alias'),
			(4, 'google.gemini-2.5-pro', NULL),
			(5, 'gpt-4.1', '');
	`); err != nil {
		t.Fatal(err)
	}

	if err := normalizeModelsDevPricingPrefixes(ctx, db); err != nil {
		t.Fatal(err)
	}
	// 必须幂等，第二次执行不能重复添加别名或删除正常模型。
	if err := normalizeModelsDevPricingPrefixes(ctx, db); err != nil {
		t.Fatal(err)
	}

	type migratedRow struct {
		model   string
		aliases string
	}
	got := make(map[string]migratedRow)
	rows, err := db.QueryContext(ctx, "SELECT model, COALESCE(aliases, '') FROM model_pricing ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var row migratedRow
		if err := rows.Scan(&row.model, &row.aliases); err != nil {
			t.Fatal(err)
		}
		got[row.model] = row
	}
	if len(got) != 4 {
		t.Fatalf("迁移后模型数 = %d, want 4: %#v", len(got), got)
	}
	if got["claude-opus-5"].aliases != "anthropic.claude-opus-5" {
		t.Fatalf("Claude 迁移结果 = %#v", got["claude-opus-5"])
	}
	if got["gpt-5.2"].aliases != "manual-alias,old-openai-alias,openai.gpt-5.2" {
		t.Fatalf("OpenAI 重复项合并结果 = %#v", got["gpt-5.2"])
	}
	if got["gemini-2.5-pro"].aliases != "google.gemini-2.5-pro" {
		t.Fatalf("Gemini 迁移结果 = %#v", got["gemini-2.5-pro"])
	}
	if _, ok := got["gpt-4.1"]; !ok {
		t.Fatal("合法模型 gpt-4.1 被错误改写")
	}
}

func near(got, want float64) bool {
	return math.Abs(got-want) < 1e-12
}
