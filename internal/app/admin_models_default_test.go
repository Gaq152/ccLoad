package app

import (
	"context"
	"testing"

	"ccLoad/internal/model"
	"ccLoad/internal/storage"
	"ccLoad/internal/util"
)

func TestFetchModelsForcedDefaultUsesPricingDefaultList(t *testing.T) {
	util.ClearDBDefaultModels()
	t.Cleanup(util.ClearDBDefaultModels)
	util.SetDBDefaultModels(util.ChannelTypeCodex, []string{"gpt-custom-default"})

	response, err := fetchModelsForConfig(context.Background(), util.ChannelTypeCodex, "https://example.com", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if response.Source != "default" || len(response.Models) != 1 || response.Models[0] != "gpt-custom-default" {
		t.Fatalf("response = %+v", response)
	}
}

func TestFetchModelsFailureFallsBackToSameDefaultList(t *testing.T) {
	util.ClearDBDefaultModels()
	t.Cleanup(util.ClearDBDefaultModels)
	util.SetDBDefaultModels(util.ChannelTypeGemini, []string{"gemini-custom-default"})

	response, err := fetchModelsForConfig(context.Background(), util.ChannelTypeGemini, "http://127.0.0.1:1", "test-key", false)
	if err != nil {
		t.Fatal(err)
	}
	if response.Source != "default_fallback" || len(response.Models) != 1 || response.Models[0] != "gemini-custom-default" {
		t.Fatalf("response = %+v", response)
	}
}

func TestRefreshPricingCacheDrivesChannelDefaultList(t *testing.T) {
	util.ClearDBDefaultModels()
	util.ClearDBPricing()
	t.Cleanup(func() {
		util.ClearDBDefaultModels()
		util.ClearDBPricing()
		util.SetDBAliases(nil)
	})

	store, err := storage.CreateSQLiteStore(t.TempDir()+"/pricing-default.db", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	entry := &model.ModelPricingEntry{
		Model:       "gpt-user-default",
		DisplayName: "User Default",
		ChannelType: util.ChannelTypeCodex,
		InputPrice:  1,
		OutputPrice: 2,
		IsDefault:   true,
	}
	if err := store.CreateModelPricing(context.Background(), entry); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: store}
	server.refreshPricingCache()
	if got := util.DefaultModels(util.ChannelTypeCodex); len(got) != 1 || got[0] != entry.Model {
		t.Fatalf("勾选后的渠道默认列表 = %#v", got)
	}

	entry.IsDefault = false
	if err := store.UpdateModelPricing(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	server.refreshPricingCache()
	if got := util.DefaultModels(util.ChannelTypeCodex); len(got) != 0 {
		t.Fatalf("取消勾选后默认列表应为空且不回退内置值: %#v", got)
	}
}
