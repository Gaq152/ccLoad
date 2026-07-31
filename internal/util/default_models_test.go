package util

import "testing"

func TestDefaultModelsDatabaseListIsAuthoritative(t *testing.T) {
	ClearDBDefaultModels()
	t.Cleanup(ClearDBDefaultModels)

	builtIn := DefaultModels(ChannelTypeCodex)
	if !containsModel(builtIn, "gpt-5.6") {
		t.Fatalf("内置 Codex 默认列表未同步最新模型: %#v", builtIn)
	}

	SetDBDefaultModels(ChannelTypeCodex, []string{"custom-default"})
	if got := DefaultModels(ChannelTypeCodex); len(got) != 1 || got[0] != "custom-default" {
		t.Fatalf("数据库默认列表 = %#v", got)
	}

	SetDBDefaultModels(ChannelTypeCodex, nil)
	if got := DefaultModels(ChannelTypeCodex); len(got) != 0 {
		t.Fatalf("数据库空默认列表不应回退到内置列表: %#v", got)
	}
}

func TestBuiltInDefaultModelsHaveMatchingPricingEntries(t *testing.T) {
	pricing := make(map[string]DBPricingEntry)
	for _, entry := range GetDefaultPricing() {
		pricing[entry.Model] = entry
	}
	for channelType, models := range GetDefaultModelSets() {
		for _, model := range models {
			entry, ok := pricing[model]
			if !ok {
				t.Errorf("默认模型 %s/%s 缺少内置定价", channelType, model)
				continue
			}
			if entry.ChannelType != channelType {
				t.Errorf("默认模型 %s 的定价渠道 = %s, want %s", model, entry.ChannelType, channelType)
			}
		}
	}

	gpt := pricing["gpt-5.6"]
	if gpt.InputPrice != 5 || gpt.CacheReadPrice != 0.5 || gpt.InputPriceHigh != 10 || gpt.CacheReadPriceHigh != 1 || gpt.HighPriceThreshold != 272_000 {
		t.Fatalf("gpt-5.6 内置分档定价未同步: %+v", gpt)
	}
}

func containsModel(models []string, want string) bool {
	for _, model := range models {
		if model == want {
			return true
		}
	}
	return false
}
