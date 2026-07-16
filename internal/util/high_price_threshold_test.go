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

func TestDefaultHighPriceThresholdForChannel(t *testing.T) {
	if got := DefaultHighPriceThresholdForChannel("openai"); got != 272_000 {
		t.Fatalf("OpenAI默认阈值 = %d, want 272000", got)
	}
	if got := DefaultHighPriceThresholdForChannel("gemini"); got != 200_000 {
		t.Fatalf("Gemini默认阈值 = %d, want 200000", got)
	}
}
