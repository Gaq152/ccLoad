package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"ccLoad/internal/model"
)

func TestFetchModelsDevCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Fatalf("Accept = %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(`{
          "anthropic": {
            "id": "anthropic",
            "name": "Anthropic",
            "models": {
            "anthropic.claude-test": {
                "id": "anthropic.claude-test",
                "name": "Claude Test",
                "release_date": "2026-07-01",
                "modalities": {"output": ["text"]},
                "cost": {
                  "input": 3,
                  "output": 15,
                  "cache_read": 0.3,
                  "cache_write": 3.75,
                  "tiers": [{"input": 6, "output": 22.5, "cache_read": 0.6, "cache_write": 7.5, "tier": {"size": 200000}}]
                }
              },
              "embed-test": {
                "name": "Embedding Test",
                "modalities": {"output": ["text"]},
                "cost": {"input": 0.1, "output": 0}
              }
            }
          }
        }`))
	}))
	defer server.Close()

	entries, err := fetchModelsDevCatalog(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchModelsDevCatalog() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	got := entries[0]
	if got.Key != "anthropic/anthropic.claude-test" || got.NormalizedModelID != "claude-test" || got.ChannelType != "anthropic" || got.LabID != "anthropic" || got.NeedsChannelType {
		t.Fatalf("identity = %#v", got)
	}
	if got.InputPrice != 3 || got.OutputPrice != 15 || got.CacheReadPrice != 0.3 || got.CacheWritePrice != 3.75 {
		t.Fatalf("base pricing = %#v", got)
	}
	if got.InputPriceHigh != 6 || got.OutputPriceHigh != 22.5 || got.HighPriceThreshold != 200000 {
		t.Fatalf("tier pricing = %#v", got)
	}
	if got.CacheReadPriceHigh != 0.6 || got.CacheWritePriceHigh != 7.5 {
		t.Fatalf("tier cache pricing = %#v", got)
	}
}

func TestFetchModelsDevCatalogResolvesLabSeparatelyFromProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
          "models": {
            "deepseek/deepseek-r1": {"id": "deepseek/deepseek-r1", "name": "DeepSeek R1"},
            "openai/gpt-test": {"id": "openai/gpt-test", "name": "GPT Test"}
          },
          "providers": {
            "openrouter": {
              "id": "openrouter",
              "name": "OpenRouter",
              "models": {
                "deepseek/deepseek-r1": {
                  "id": "deepseek/deepseek-r1",
                  "name": "DeepSeek R1",
                  "modalities": {"output": ["text"]},
                  "cost": {"input": 0.55, "output": 2.19}
                },
                "gpt-test": {
                  "id": "gpt-test",
                  "name": "GPT Test",
                  "modalities": {"output": ["text"]},
                  "cost": {"input": 1, "output": 4}
                }
              }
            }
          }
        }`))
	}))
	defer server.Close()

	entries, err := fetchModelsDevCatalog(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchModelsDevCatalog() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	byModel := make(map[string]ModelsDevCatalogEntry, len(entries))
	for _, entry := range entries {
		byModel[entry.NormalizedModelID] = entry
	}
	deepseek := byModel["deepseek-r1"]
	if deepseek.ProviderID != "openrouter" || deepseek.LabID != "deepseek" || deepseek.ChannelType != "" || !deepseek.NeedsChannelType || deepseek.AssignmentKey != "lab:deepseek" {
		t.Fatalf("deepseek entry = %#v", deepseek)
	}
	openAI := byModel["gpt-test"]
	if openAI.ProviderID != "openrouter" || openAI.LabID != "openai" || openAI.ChannelType != "codex" || openAI.NeedsChannelType {
		t.Fatalf("openai entry = %#v", openAI)
	}
}

func TestNormalizeModelsDevModelID(t *testing.T) {
	tests := map[string]string{
		"anthropic/claude-sonnet-4:beta": "claude-sonnet-4",
		"anthropic.claude-opus-5":        "claude-opus-5",
		"openai.gpt-5.2-codex":           "gpt-5.2-codex",
		"google.gemini-2.5-pro":          "gemini-2.5-pro",
		"gpt-5.2-codex@high":             "gpt-5.2-codex-high",
		"gpt-4.1":                        "gpt-4.1",
		"Claude-Test[1m]":                "claude-test",
	}
	for input, want := range tests {
		if got := normalizeModelsDevModelID(input); got != want {
			t.Errorf("normalizeModelsDevModelID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeModelsDevModelIDUsesResolvedLabNamespace(t *testing.T) {
	if got := normalizeModelsDevModelID("openrouter.deepseek.deepseek-r1", "openrouter", "deepseek"); got != "deepseek-r1" {
		t.Fatalf("normalizeModelsDevModelID() = %q, want deepseek-r1", got)
	}
}

func TestBuildModelsDevImportEntriesPreservesManualMetadata(t *testing.T) {
	catalog := []ModelsDevCatalogEntry{{
		Key:                 "anthropic/anthropic/claude-test",
		ProviderID:          "anthropic",
		ModelID:             "anthropic/claude-test",
		NormalizedModelID:   "claude-test",
		DisplayName:         "Models.dev Name",
		ChannelType:         "anthropic",
		InputPrice:          4,
		OutputPrice:         20,
		CacheReadPrice:      0.4,
		CacheWritePrice:     5,
		InputPriceHigh:      8,
		OutputPriceHigh:     30,
		CacheReadPriceHigh:  0.8,
		CacheWritePriceHigh: 10,
		HighPriceThreshold:  200000,
	}}
	existing := []*model.ModelPricingEntry{{
		Model:          "claude-test",
		DisplayName:    "Manual Name",
		ChannelType:    "codex",
		Aliases:        []string{"custom-alias"},
		IsDefault:      true,
		InputPrice:     1,
		OutputPrice:    2,
		InputPriceHigh: 3,
	}}

	entries, result, err := buildModelsDevImportEntries(catalog, existing, []string{catalog[0].Key}, true, nil)
	if err != nil {
		t.Fatalf("buildModelsDevImportEntries() error = %v", err)
	}
	if len(entries) != 1 || result.Updated != 1 || result.Created != 0 {
		t.Fatalf("entries=%d result=%+v", len(entries), result)
	}
	got := entries[0]
	if got.DisplayName != "Manual Name" || got.ChannelType != "codex" || !got.IsDefault {
		t.Fatalf("manual metadata was not preserved: %#v", got)
	}
	if got.InputPrice != 4 || got.OutputPrice != 20 || got.InputPriceHigh != 8 || got.OutputPriceHigh != 30 {
		t.Fatalf("source pricing was not applied: %#v", got)
	}
	if got.CacheReadPrice != 0.4 || got.CacheWritePrice != 5 || got.CacheReadPriceHigh != 0.8 || got.CacheWritePriceHigh != 10 {
		t.Fatalf("absolute cache prices = %#v", got)
	}
	if len(got.Aliases) != 2 || got.Aliases[0] != "custom-alias" || got.Aliases[1] != "anthropic/claude-test" {
		t.Fatalf("aliases = %#v", got.Aliases)
	}
}

func TestBuildModelsDevImportEntriesSkipsExistingWithoutOverwriteAndDuplicates(t *testing.T) {
	catalog := []ModelsDevCatalogEntry{
		{Key: "one/model-a", NormalizedModelID: "model-a", ModelID: "model-a", ChannelType: "codex"},
		{Key: "two/model-a", NormalizedModelID: "model-a", ModelID: "model-a", ChannelType: "codex"},
	}
	existing := []*model.ModelPricingEntry{{Model: "model-a"}}

	entries, result, err := buildModelsDevImportEntries(catalog, existing, []string{"one/model-a", "two/model-a", "missing"}, false, nil)
	if err != nil {
		t.Fatalf("buildModelsDevImportEntries() error = %v", err)
	}
	if len(entries) != 0 || result.Skipped != 1 || result.Duplicate != 1 || result.Missing != 1 {
		t.Fatalf("entries=%d result=%+v", len(entries), result)
	}
}

func TestBuildModelsDevImportEntriesUpdatesPrefixedExistingModel(t *testing.T) {
	catalog := []ModelsDevCatalogEntry{{
		Key:               "openrouter/anthropic/claude-test",
		ModelID:           "anthropic/claude-test",
		NormalizedModelID: "claude-test",
		DisplayName:       "Claude Test",
		ChannelType:       "codex",
		InputPrice:        3,
		OutputPrice:       15,
	}}
	existing := []*model.ModelPricingEntry{{
		Model:       "anthropic/claude-test",
		DisplayName: "Manual",
		ChannelType: "codex",
	}}

	entries, result, err := buildModelsDevImportEntries(catalog, existing, []string{catalog[0].Key}, true, nil)
	if err != nil {
		t.Fatalf("buildModelsDevImportEntries() error = %v", err)
	}
	if len(entries) != 1 || result.Updated != 1 || result.Created != 0 {
		t.Fatalf("entries=%d result=%+v", len(entries), result)
	}
	if entries[0].Model != "anthropic/claude-test" {
		t.Fatalf("model = %q, want existing prefixed model", entries[0].Model)
	}
	if len(entries[0].Aliases) != 1 || entries[0].Aliases[0] != "claude-test" {
		t.Fatalf("aliases = %#v, want normalized alias", entries[0].Aliases)
	}

	markExistingModelsDevEntries(catalog, existing)
	if !catalog[0].Exists {
		t.Fatal("prefixed existing model was not marked as existing")
	}
}

func TestBuildModelsDevImportEntriesRequiresUnknownLabAssignment(t *testing.T) {
	catalog := []ModelsDevCatalogEntry{{
		Key:               "openrouter/deepseek/deepseek-r1",
		ProviderID:        "openrouter",
		LabID:             "deepseek",
		LabName:           "DeepSeek",
		AssignmentKey:     "lab:deepseek",
		ModelID:           "deepseek/deepseek-r1",
		NormalizedModelID: "deepseek-r1",
		DisplayName:       "DeepSeek R1",
		NeedsChannelType:  true,
		InputPrice:        0.55,
		OutputPrice:       2.19,
	}}

	if _, _, err := buildModelsDevImportEntries(catalog, nil, []string{catalog[0].Key}, true, nil); err == nil {
		t.Fatal("buildModelsDevImportEntries() error = nil, want missing channel assignment")
	}

	entries, result, err := buildModelsDevImportEntries(catalog, nil, []string{catalog[0].Key}, true, map[string]string{
		"lab:deepseek": "codex",
	})
	if err != nil {
		t.Fatalf("buildModelsDevImportEntries() error = %v", err)
	}
	if len(entries) != 1 || result.Created != 1 || entries[0].ChannelType != "codex" {
		t.Fatalf("entries=%#v result=%+v", entries, result)
	}
}

func TestBuildModelsDevImportEntriesUnknownLabPreservesExistingType(t *testing.T) {
	catalog := []ModelsDevCatalogEntry{{
		Key:               "openrouter/deepseek/deepseek-r1",
		LabID:             "deepseek",
		LabName:           "DeepSeek",
		AssignmentKey:     "lab:deepseek",
		ModelID:           "deepseek/deepseek-r1",
		NormalizedModelID: "deepseek-r1",
		NeedsChannelType:  true,
		InputPrice:        0.55,
		OutputPrice:       2.19,
	}}
	existing := []*model.ModelPricingEntry{{Model: "deepseek-r1", ChannelType: "anthropic"}}

	entries, result, err := buildModelsDevImportEntries(catalog, existing, []string{catalog[0].Key}, true, nil)
	if err != nil {
		t.Fatalf("buildModelsDevImportEntries() error = %v", err)
	}
	if len(entries) != 1 || result.Updated != 1 || entries[0].ChannelType != "anthropic" {
		t.Fatalf("entries=%#v result=%+v", entries, result)
	}
}
