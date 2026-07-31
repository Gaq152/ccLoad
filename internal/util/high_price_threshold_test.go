package util

import "testing"

func TestCalculateCost_ConfigurableHighPriceThreshold(t *testing.T) {
	ClearDBPricing()
	t.Cleanup(ClearDBPricing)

	SetDBPricing([]DBPricingEntry{
		{
			Model:              "gpt-threshold-test",
			ChannelType:        "openai",
			InputPrice:         1,
			OutputPrice:        2,
			InputPriceHigh:     3,
			OutputPriceHigh:    4,
			HighPriceThreshold: 272_000,
		},
	})

	standardCost := CalculateCost("gpt-threshold-test", 272_000, 1_000, 0, 0)
	wantStandard := 272_000.0/1_000_000 + 2.0*1_000/1_000_000
	if !floatEquals(standardCost, wantStandard, 0.000001) {
		t.Fatalf("阈值边界应使用标准价: got %.6f, want %.6f", standardCost, wantStandard)
	}

	highCost := CalculateCost("gpt-threshold-test", 272_001, 1_000, 0, 0)
	wantHigh := 3.0*272_001/1_000_000 + 4.0*1_000/1_000_000
	if !floatEquals(highCost, wantHigh, 0.000001) {
		t.Fatalf("超过272K应使用高价档: got %.6f, want %.6f", highCost, wantHigh)
	}
}

func TestCalculateCostHighTierUsesFullInputContextAndAbsoluteCachePrices(t *testing.T) {
	ClearDBPricing()
	t.Cleanup(ClearDBPricing)
	SetDBPricing([]DBPricingEntry{{
		Model:               "tier-cache-test",
		InputPrice:          1,
		OutputPrice:         2,
		CacheReadPrice:      0.1,
		CacheWritePrice:     1.25,
		InputPriceHigh:      3,
		OutputPriceHigh:     4,
		CacheReadPriceHigh:  0.3,
		CacheWritePriceHigh: 3.75,
		HighPriceThreshold:  200_000,
	}})

	// 普通输入只有 100K，但加上缓存读取后完整上下文超过 200K，应整单使用高档绝对价格。
	cost := CalculateCost("tier-cache-test", 100_000, 10_000, 100_001, 1_000)
	want := 100_000*3.0/1_000_000 + 10_000*4.0/1_000_000 + 100_001*0.3/1_000_000 + 1_000*3.75/1_000_000
	if diff := cost - want; diff < -1e-12 || diff > 1e-12 {
		t.Fatalf("cost = %.12f, want %.12f", cost, want)
	}
}

func TestDefaultHighPriceThresholdForChannel(t *testing.T) {
	if got := DefaultHighPriceThresholdForChannel("openai"); got != 272_000 {
		t.Fatalf("OpenAI默认阈值 = %d, want 272000", got)
	}
	if got := DefaultHighPriceThresholdForChannel("gemini"); got != 200_000 {
		t.Fatalf("Gemini默认阈值 = %d, want 200000", got)
	}
}
