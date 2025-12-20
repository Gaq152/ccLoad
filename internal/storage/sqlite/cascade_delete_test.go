package sqlite_test

import (
	"ccLoad/internal/model"
	"ccLoad/internal/storage"
	"context"
	"testing"
)

// TestDeleteConfig_CascadeTokenChannels 验证删除渠道时级联删除 token_channels 关联数据
// 防止孤儿数据产生
func TestDeleteConfig_CascadeTokenChannels(t *testing.T) {
	tmpDB := t.TempDir() + "/test-cascade-delete.db"
	store, err := storage.CreateSQLiteStore(tmpDB, nil)
	if err != nil {
		t.Fatalf("创建测试数据库失败: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. 创建测试渠道
	channel := &model.Config{
		Name:    "cascade-test-channel",
		URL:     "https://api.example.com",
		Models:  []string{"claude-3-opus"},
		Enabled: true,
	}
	createdChannel, err := store.CreateConfig(ctx, channel)
	if err != nil {
		t.Fatalf("创建渠道失败: %v", err)
	}
	channelID := createdChannel.ID

	// 2. 创建测试令牌
	token := &model.AuthToken{
		Description: "cascade-test-token",
		Token:       "sk-test-cascade-" + t.Name(),
		IsActive:    true,
		AllChannels: false, // 非全渠道，需要关联
	}
	err = store.CreateAuthToken(ctx, token)
	if err != nil {
		t.Fatalf("创建令牌失败: %v", err)
	}
	// 获取创建后的令牌以获得 ID
	createdToken, err := store.GetAuthTokenByValue(ctx, token.Token)
	if err != nil {
		t.Fatalf("获取创建的令牌失败: %v", err)
	}
	tokenID := createdToken.ID

	// 3. 关联令牌和渠道
	err = store.SetTokenChannels(ctx, tokenID, []int64{channelID})
	if err != nil {
		t.Fatalf("设置令牌渠道关联失败: %v", err)
	}

	// 4. 验证关联已创建
	channels, err := store.GetTokenChannels(ctx, tokenID)
	if err != nil {
		t.Fatalf("获取令牌渠道关联失败: %v", err)
	}
	if len(channels) != 1 || channels[0] != channelID {
		t.Fatalf("令牌渠道关联不正确: 期望 [%d]，实际 %v", channelID, channels)
	}

	// 5. 删除渠道
	err = store.DeleteConfig(ctx, channelID)
	if err != nil {
		t.Fatalf("删除渠道失败: %v", err)
	}

	// 6. 验证渠道已删除
	_, err = store.GetConfig(ctx, channelID)
	if err == nil {
		t.Fatal("渠道应该已被删除，但仍然存在")
	}

	// 7. 验证 token_channels 关联已被级联删除
	channelsAfter, err := store.GetTokenChannels(ctx, tokenID)
	if err != nil {
		t.Fatalf("删除后获取令牌渠道关联失败: %v", err)
	}
	if len(channelsAfter) != 0 {
		t.Errorf("❌ 孤儿数据检测: token_channels 未被级联删除，仍有 %d 条关联", len(channelsAfter))
	}

	// 8. 验证令牌本身未被影响
	tokenAfter, err := store.GetAuthToken(ctx, tokenID)
	if err != nil {
		t.Fatalf("删除渠道后获取令牌失败: %v", err)
	}
	if tokenAfter.ID != tokenID {
		t.Error("令牌应该保持不变")
	}

	t.Log("✅ DeleteConfig 级联删除 token_channels 测试通过")
}

// TestDeleteConfig_CascadeAPIKeys 验证删除渠道时级联删除 api_keys 关联数据
func TestDeleteConfig_CascadeAPIKeys(t *testing.T) {
	tmpDB := t.TempDir() + "/test-cascade-apikeys.db"
	store, err := storage.CreateSQLiteStore(tmpDB, nil)
	if err != nil {
		t.Fatalf("创建测试数据库失败: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. 创建测试渠道
	channel := &model.Config{
		Name:    "cascade-apikey-test",
		URL:     "https://api.example.com",
		Models:  []string{"claude-3-opus", "claude-3-sonnet"},
		Enabled: true,
	}
	createdChannel, err := store.CreateConfig(ctx, channel)
	if err != nil {
		t.Fatalf("创建渠道失败: %v", err)
	}
	channelID := createdChannel.ID

	// 2. 创建关联的 API Key
	apiKey := &model.APIKey{
		ChannelID:   channelID,
		KeyIndex:    0,
		APIKey:      "sk-test-key-" + t.Name(),
		KeyStrategy: model.KeyStrategySequential,
	}
	err = store.CreateAPIKey(ctx, apiKey)
	if err != nil {
		t.Fatalf("创建 API Key 失败: %v", err)
	}

	// 3. 验证 API Key 已创建
	keys, err := store.GetAPIKeys(ctx, channelID)
	if err != nil {
		t.Fatalf("获取 API Keys 失败: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("API Key 应该已创建")
	}

	// 4. 删除渠道
	err = store.DeleteConfig(ctx, channelID)
	if err != nil {
		t.Fatalf("删除渠道失败: %v", err)
	}

	// 5. 验证 API Keys 已被级联删除
	keysAfter, err := store.GetAPIKeys(ctx, channelID)
	if err != nil {
		t.Fatalf("删除后获取 API Keys 失败: %v", err)
	}
	if len(keysAfter) != 0 {
		t.Errorf("❌ api_keys 未被级联删除，仍有 %d 条记录", len(keysAfter))
	}

	t.Log("✅ DeleteConfig 级联删除 api_keys 测试通过")
}

// TestDeleteConfig_MultipleTokenChannels 验证删除渠道时正确处理多个令牌关联
func TestDeleteConfig_MultipleTokenChannels(t *testing.T) {
	tmpDB := t.TempDir() + "/test-cascade-multi.db"
	store, err := storage.CreateSQLiteStore(tmpDB, nil)
	if err != nil {
		t.Fatalf("创建测试数据库失败: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. 创建两个测试渠道
	channel1, err := store.CreateConfig(ctx, &model.Config{
		Name: "channel-1", URL: "https://api1.example.com", Models: []string{"model-a"}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("创建渠道1失败: %v", err)
	}

	channel2, err := store.CreateConfig(ctx, &model.Config{
		Name: "channel-2", URL: "https://api2.example.com", Models: []string{"model-b"}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("创建渠道2失败: %v", err)
	}

	// 2. 创建令牌并关联两个渠道
	token := &model.AuthToken{
		Description: "multi-channel-token",
		Token:       "sk-multi-" + t.Name(),
		IsActive:    true,
		AllChannels: false,
	}
	err = store.CreateAuthToken(ctx, token)
	if err != nil {
		t.Fatalf("创建令牌失败: %v", err)
	}
	createdToken, _ := store.GetAuthTokenByValue(ctx, token.Token)
	tokenID := createdToken.ID

	// 关联两个渠道
	err = store.SetTokenChannels(ctx, tokenID, []int64{channel1.ID, channel2.ID})
	if err != nil {
		t.Fatalf("设置令牌渠道关联失败: %v", err)
	}

	// 3. 验证关联已创建
	channels, _ := store.GetTokenChannels(ctx, tokenID)
	if len(channels) != 2 {
		t.Fatalf("应该有2个渠道关联，实际 %d", len(channels))
	}

	// 4. 删除渠道1
	err = store.DeleteConfig(ctx, channel1.ID)
	if err != nil {
		t.Fatalf("删除渠道1失败: %v", err)
	}

	// 5. 验证只删除了渠道1的关联，渠道2的关联保留
	channelsAfter, _ := store.GetTokenChannels(ctx, tokenID)
	if len(channelsAfter) != 1 {
		t.Errorf("应该只剩1个渠道关联，实际 %d", len(channelsAfter))
	}
	if len(channelsAfter) == 1 && channelsAfter[0] != channel2.ID {
		t.Errorf("应该保留渠道2的关联，实际保留的是 %d", channelsAfter[0])
	}

	t.Log("✅ DeleteConfig 多令牌关联测试通过")
}
