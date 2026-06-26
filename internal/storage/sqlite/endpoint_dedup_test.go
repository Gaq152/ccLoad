package sqlite_test

import (
	"context"
	"testing"

	"ccLoad/internal/model"
	"ccLoad/internal/storage"
)

// TestSyncActiveEndpointURL_NoDuplicate 验证当 channels.url 被改写成同渠道另一端点已有的 URL 时，
// 不会产生两个 URL 完全相同的端点（修复端点管理弹窗出现重复端点的 BUG）。
func TestSyncActiveEndpointURL_NoDuplicate(t *testing.T) {
	tmpDB := t.TempDir() + "/test-endpoint-dedup.db"
	store, err := storage.CreateSQLiteStore(tmpDB, nil)
	if err != nil {
		t.Fatalf("创建测试数据库失败: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	const urlA = "https://a.example.com"
	const urlB = "https://b.example.com"

	// 1. 创建渠道，主 URL = A
	ch, err := store.CreateConfig(ctx, &model.Config{
		Name:    "dedup-test",
		URL:     urlA,
		Models:  []string{"claude-3-opus"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("创建渠道失败: %v", err)
	}

	// 2. 配置两个不同端点：A(激活) + B
	if err := store.SaveEndpoints(ctx, ch.ID, []model.ChannelEndpoint{
		{ChannelID: ch.ID, URL: urlA, IsActive: true, SortOrder: 0},
		{ChannelID: ch.ID, URL: urlB, IsActive: false, SortOrder: 1},
	}); err != nil {
		t.Fatalf("保存端点失败: %v", err)
	}

	// 3. 切换激活端点到 B（模拟自动测速选中最快端点），此时 channels.url 同步为 B
	eps, err := store.ListEndpoints(ctx, ch.ID)
	if err != nil {
		t.Fatalf("获取端点失败: %v", err)
	}
	var bID int64
	for _, ep := range eps {
		if ep.URL == urlB {
			bID = ep.ID
		}
	}
	if bID == 0 {
		t.Fatal("未找到端点 B")
	}
	if err := store.SetActiveEndpoint(ctx, ch.ID, bID); err != nil {
		t.Fatalf("切换激活端点失败: %v", err)
	}

	// 4. 用旧的主 URL=A 更新渠道（模拟陈旧表单保存 / 后台 Token 刷新等只调 UpdateConfig 的路径），
	//    这会触发 SyncActiveEndpointURL(A)。旧实现会把激活端点 B 的 URL 改写成 A → 产生两个 A。
	updated := *ch
	updated.URL = urlA
	if _, err := store.UpdateConfig(ctx, ch.ID, &updated); err != nil {
		t.Fatalf("更新渠道失败: %v", err)
	}

	// 5. 断言：不存在 URL 完全相同的重复端点
	eps, err = store.ListEndpoints(ctx, ch.ID)
	if err != nil {
		t.Fatalf("获取端点失败: %v", err)
	}
	seen := make(map[string]int)
	for _, ep := range eps {
		seen[ep.URL]++
	}
	if len(eps) != 2 {
		t.Fatalf("期望 2 个端点，实际 %d 个: %+v", len(eps), eps)
	}
	for url, cnt := range seen {
		if cnt > 1 {
			t.Fatalf("出现重复端点: %s 共 %d 个", url, cnt)
		}
	}

	// 6. 断言：URL=A 的端点应被激活（匹配已存在端点时切换激活，而非改写）
	var activeURL string
	for _, ep := range eps {
		if ep.IsActive {
			activeURL = ep.URL
		}
	}
	if activeURL != urlA {
		t.Fatalf("期望激活端点为 %s，实际 %s", urlA, activeURL)
	}
}

// TestSaveEndpoints_DedupeInput 验证 SaveEndpoints 对重复 URL 的输入做去重防御。
func TestSaveEndpoints_DedupeInput(t *testing.T) {
	tmpDB := t.TempDir() + "/test-save-dedup.db"
	store, err := storage.CreateSQLiteStore(tmpDB, nil)
	if err != nil {
		t.Fatalf("创建测试数据库失败: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	ch, err := store.CreateConfig(ctx, &model.Config{
		Name:    "save-dedup-test",
		URL:     "https://x.example.com",
		Models:  []string{"claude-3-opus"},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("创建渠道失败: %v", err)
	}

	const dup = "https://dup.example.com"
	if err := store.SaveEndpoints(ctx, ch.ID, []model.ChannelEndpoint{
		{ChannelID: ch.ID, URL: dup, IsActive: false, SortOrder: 0},
		{ChannelID: ch.ID, URL: dup, IsActive: true, SortOrder: 1},
	}); err != nil {
		t.Fatalf("保存端点失败: %v", err)
	}

	eps, err := store.ListEndpoints(ctx, ch.ID)
	if err != nil {
		t.Fatalf("获取端点失败: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("期望去重后 1 个端点，实际 %d 个: %+v", len(eps), eps)
	}
	if !eps[0].IsActive {
		t.Fatal("去重后应继承激活标记")
	}
}
