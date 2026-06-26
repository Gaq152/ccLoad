package sqlite_test

import (
	"context"
	"testing"

	"ccLoad/internal/model"
	"ccLoad/internal/storage"
)

// TestImportChannelBatch_KiroSeedsBothEndpoints 验证 CSV 导入新建 Kiro 渠道时
// 直接把主端点 + 备用端点落库（数据库为端点的唯一数据源）。
func TestImportChannelBatch_KiroSeedsBothEndpoints(t *testing.T) {
	tmpDB := t.TempDir() + "/test-import-kiro.db"
	store, err := storage.CreateSQLiteStore(tmpDB, nil)
	if err != nil {
		t.Fatalf("创建测试数据库失败: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	const primary = "https://q.us-east-1.amazonaws.com"
	const backup = "https://codewhisperer.us-east-1.amazonaws.com"

	created, _, err := store.ImportChannelBatch(ctx, []*model.ChannelWithKeys{
		{
			Config: &model.Config{
				Name:        "kiro-import-test",
				URL:         primary,
				Models:      []string{"claude-opus-4-8"},
				ChannelType: "anthropic",
				Preset:      "kiro",
				Enabled:     true,
			},
			APIKeys: []model.APIKey{{KeyIndex: 0, RefreshToken: "rt-test"}},
		},
	})
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if created != 1 {
		t.Fatalf("期望创建 1 个渠道，实际 %d", created)
	}

	// 找到导入的渠道
	configs, err := store.ListConfigs(ctx)
	if err != nil {
		t.Fatalf("列出渠道失败: %v", err)
	}
	var channelID int64
	for _, c := range configs {
		if c.Name == "kiro-import-test" {
			channelID = c.ID
		}
	}
	if channelID == 0 {
		t.Fatal("未找到导入的渠道")
	}

	// 验证端点：主端点（激活）+ 备用端点（非激活）
	eps, err := store.ListEndpoints(ctx, channelID)
	if err != nil {
		t.Fatalf("获取端点失败: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("期望 2 个端点，实际 %d 个: %+v", len(eps), eps)
	}
	byURL := make(map[string]model.ChannelEndpoint, 2)
	for _, ep := range eps {
		byURL[ep.URL] = ep
	}
	p, ok := byURL[primary]
	if !ok || !p.IsActive {
		t.Fatalf("主端点应存在且激活: %+v", byURL)
	}
	b, ok := byURL[backup]
	if !ok || b.IsActive {
		t.Fatalf("备用端点应存在且非激活: %+v", byURL)
	}
}

// TestMigrateKiroBackupEndpoints_Backfill 验证启动迁移会为只剩主端点的 Kiro 渠道补回备用端点。
func TestMigrateKiroBackupEndpoints_Backfill(t *testing.T) {
	tmpDB := t.TempDir() + "/test-kiro-backfill.db"

	const primary = "https://q.us-east-1.amazonaws.com"
	const backup = "https://codewhisperer.us-east-1.amazonaws.com"

	// 第一次打开：建库 + 造一个只有单端点的 Kiro 渠道（模拟历史损坏数据）
	store, err := storage.CreateSQLiteStore(tmpDB, nil)
	if err != nil {
		t.Fatalf("创建测试数据库失败: %v", err)
	}
	ctx := context.Background()

	ch, err := store.CreateConfig(ctx, &model.Config{
		Name:        "kiro-backfill-test",
		URL:         primary,
		Models:      []string{"claude-opus-4-8"},
		ChannelType: "anthropic",
		Preset:      "kiro",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("创建渠道失败: %v", err)
	}
	// 只落主端点
	if err := store.SaveEndpoints(ctx, ch.ID, []model.ChannelEndpoint{
		{ChannelID: ch.ID, URL: primary, IsActive: true, SortOrder: 0},
	}); err != nil {
		t.Fatalf("保存端点失败: %v", err)
	}
	eps, _ := store.ListEndpoints(ctx, ch.ID)
	if len(eps) != 1 {
		t.Fatalf("前置条件应为 1 个端点，实际 %d", len(eps))
	}
	store.Close()

	// 第二次打开同一个库：迁移应补回备用端点
	store2, err := storage.CreateSQLiteStore(tmpDB, nil)
	if err != nil {
		t.Fatalf("重新打开数据库失败: %v", err)
	}

	eps, err = store2.ListEndpoints(ctx, ch.ID)
	if err != nil {
		t.Fatalf("获取端点失败: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("迁移后期望 2 个端点，实际 %d 个: %+v", len(eps), eps)
	}
	hasBackup := false
	for _, ep := range eps {
		if ep.URL == backup {
			hasBackup = true
			if ep.IsActive {
				t.Fatal("补回的备用端点应为非激活")
			}
		}
	}
	if !hasBackup {
		t.Fatalf("迁移后应包含备用端点 %s: %+v", backup, eps)
	}

	// 幂等性：第三次打开不应再重复补充
	store2.Close()
	store3, err := storage.CreateSQLiteStore(tmpDB, nil)
	if err != nil {
		t.Fatalf("第三次打开失败: %v", err)
	}
	defer store3.Close()
	eps, _ = store3.ListEndpoints(ctx, ch.ID)
	if len(eps) != 2 {
		t.Fatalf("迁移应幂等，期望仍为 2 个端点，实际 %d 个", len(eps))
	}
}
