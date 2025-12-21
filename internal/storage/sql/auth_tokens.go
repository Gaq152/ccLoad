package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"ccLoad/internal/model"
)

// ============================================================================
// Auth Tokens Management - API访问令牌管理
// ============================================================================

// CreateAuthToken 创建新的API访问令牌
// 注意: token字段存储的是SHA256哈希值，而非明文
func (s *SQLStore) CreateAuthToken(ctx context.Context, token *model.AuthToken) error {
	token.CreatedAt = time.Now()

	// 处理可空字段：SQLite NOT NULL DEFAULT 0 需要传入 0 而不是 nil
	var expiresAt int64 = 0
	if token.ExpiresAt != nil {
		expiresAt = *token.ExpiresAt
	}

	var lastUsedAt int64 = 0
	if token.LastUsedAt != nil {
		lastUsedAt = *token.LastUsedAt
	}

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_tokens (
			token, description, created_at, expires_at, last_used_at, is_active, all_channels,
			success_count, failure_count, stream_avg_ttfb, non_stream_avg_rt, stream_count, non_stream_count,
			prompt_tokens_total, completion_tokens_total, total_cost_usd
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, 0.0, 0.0, 0, 0, 0, 0, 0.0)
	`, token.Token, token.Description, token.CreatedAt.UnixMilli(), expiresAt, lastUsedAt,
		boolToInt(token.IsActive), boolToInt(token.AllChannels))

	if err != nil {
		return fmt.Errorf("create auth token: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}

	token.ID = id

	// 触发异步Redis同步 (新增 2025-11)
	s.triggerAsyncSync(syncAuthTokens)

	return nil
}

// GetAuthToken 根据ID获取令牌
func (s *SQLStore) GetAuthToken(ctx context.Context, id int64) (*model.AuthToken, error) {
	token := &model.AuthToken{}
	var createdAtMs int64
	var expiresAt, lastUsedAt sql.NullInt64
	var isActive, allChannels int

	err := s.db.QueryRowContext(ctx, `
		SELECT
			id, token, description, created_at, expires_at, last_used_at, is_active, all_channels,
			success_count, failure_count, stream_avg_ttfb, non_stream_avg_rt, stream_count, non_stream_count,
			prompt_tokens_total, completion_tokens_total, cache_read_tokens_total, cache_creation_tokens_total, total_cost_usd
		FROM auth_tokens
		WHERE id = ?
	`, id).Scan(
		&token.ID,
		&token.Token,
		&token.Description,
		&createdAtMs,
		&expiresAt,
		&lastUsedAt,
		&isActive,
		&allChannels,
		&token.SuccessCount,
		&token.FailureCount,
		&token.StreamAvgTTFB,
		&token.NonStreamAvgRT,
		&token.StreamCount,
		&token.NonStreamCount,
		&token.PromptTokensTotal,
		&token.CompletionTokensTotal,
		&token.CacheReadTokensTotal,
		&token.CacheCreationTokensTotal,
		&token.TotalCostUSD,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("auth token not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get auth token: %w", err)
	}

	// 转换时间戳
	token.CreatedAt = time.UnixMilli(createdAtMs)
	if expiresAt.Valid {
		token.ExpiresAt = &expiresAt.Int64
	}
	if lastUsedAt.Valid {
		token.LastUsedAt = &lastUsedAt.Int64
	}
	token.IsActive = isActive != 0
	token.AllChannels = allChannels != 0

	return token, nil
}

// GetAuthTokenByValue 根据令牌哈希值获取令牌信息
// 用于认证时快速查找令牌
func (s *SQLStore) GetAuthTokenByValue(ctx context.Context, tokenHash string) (*model.AuthToken, error) {
	token := &model.AuthToken{}
	var createdAtMs int64
	var expiresAt, lastUsedAt sql.NullInt64
	var isActive, allChannels int

	err := s.db.QueryRowContext(ctx, `
		SELECT
			id, token, description, created_at, expires_at, last_used_at, is_active, all_channels,
			success_count, failure_count, stream_avg_ttfb, non_stream_avg_rt, stream_count, non_stream_count,
			prompt_tokens_total, completion_tokens_total, cache_read_tokens_total, cache_creation_tokens_total, total_cost_usd
		FROM auth_tokens
		WHERE token = ?
	`, tokenHash).Scan(
		&token.ID,
		&token.Token,
		&token.Description,
		&createdAtMs,
		&expiresAt,
		&lastUsedAt,
		&isActive,
		&allChannels,
		&token.SuccessCount,
		&token.FailureCount,
		&token.StreamAvgTTFB,
		&token.NonStreamAvgRT,
		&token.StreamCount,
		&token.NonStreamCount,
		&token.PromptTokensTotal,
		&token.CompletionTokensTotal,
		&token.CacheReadTokensTotal,
		&token.CacheCreationTokensTotal,
		&token.TotalCostUSD,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("auth token not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get auth token by value: %w", err)
	}

	// 转换时间戳
	token.CreatedAt = time.UnixMilli(createdAtMs)
	if expiresAt.Valid {
		token.ExpiresAt = &expiresAt.Int64
	}
	if lastUsedAt.Valid {
		token.LastUsedAt = &lastUsedAt.Int64
	}
	token.IsActive = isActive != 0
	token.AllChannels = allChannels != 0

	return token, nil
}

// ListAuthTokens 列出所有令牌
func (s *SQLStore) ListAuthTokens(ctx context.Context) ([]*model.AuthToken, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			id, token, description, created_at, expires_at, last_used_at, is_active, all_channels,
			success_count, failure_count, stream_avg_ttfb, non_stream_avg_rt, stream_count, non_stream_count,
			prompt_tokens_total, completion_tokens_total, cache_read_tokens_total, cache_creation_tokens_total, total_cost_usd
		FROM auth_tokens
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list auth tokens: %w", err)
	}
	defer rows.Close()

	var tokens []*model.AuthToken
	for rows.Next() {
		token := &model.AuthToken{}
		var createdAtMs int64
		var expiresAt, lastUsedAt sql.NullInt64
		var isActive, allChannels int

		if err := rows.Scan(
			&token.ID,
			&token.Token,
			&token.Description,
			&createdAtMs,
			&expiresAt,
			&lastUsedAt,
			&isActive,
			&allChannels,
			&token.SuccessCount,
			&token.FailureCount,
			&token.StreamAvgTTFB,
			&token.NonStreamAvgRT,
			&token.StreamCount,
			&token.NonStreamCount,
			&token.PromptTokensTotal,
			&token.CompletionTokensTotal,
			&token.CacheReadTokensTotal,
			&token.CacheCreationTokensTotal,
			&token.TotalCostUSD,
		); err != nil {
			return nil, fmt.Errorf("scan auth token: %w", err)
		}

		// 转换时间戳
		token.CreatedAt = time.UnixMilli(createdAtMs)
		if expiresAt.Valid {
			token.ExpiresAt = &expiresAt.Int64
		}
		if lastUsedAt.Valid {
			token.LastUsedAt = &lastUsedAt.Int64
		}
		token.IsActive = isActive != 0
		token.AllChannels = allChannels != 0

		tokens = append(tokens, token)
	}

	return tokens, rows.Err()
}

// ListActiveAuthTokens 列出所有有效的令牌
// 用于热更新AuthService的令牌缓存
func (s *SQLStore) ListActiveAuthTokens(ctx context.Context) ([]*model.AuthToken, error) {
	now := time.Now().UnixMilli()

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			id, token, description, created_at, expires_at, last_used_at, is_active, all_channels,
			success_count, failure_count, stream_avg_ttfb, non_stream_avg_rt, stream_count, non_stream_count,
			prompt_tokens_total, completion_tokens_total, cache_read_tokens_total, cache_creation_tokens_total, total_cost_usd
		FROM auth_tokens
		WHERE is_active = 1 AND (expires_at = 0 OR expires_at > ?)
		ORDER BY created_at DESC
	`, now)
	if err != nil {
		return nil, fmt.Errorf("list active auth tokens: %w", err)
	}
	defer rows.Close()

	var tokens []*model.AuthToken
	for rows.Next() {
		token := &model.AuthToken{}
		var createdAtMs int64
		var expiresAt, lastUsedAt sql.NullInt64
		var isActive, allChannels int

		if err := rows.Scan(
			&token.ID,
			&token.Token,
			&token.Description,
			&createdAtMs,
			&expiresAt,
			&lastUsedAt,
			&isActive,
			&allChannels,
			&token.SuccessCount,
			&token.FailureCount,
			&token.StreamAvgTTFB,
			&token.NonStreamAvgRT,
			&token.StreamCount,
			&token.NonStreamCount,
			&token.PromptTokensTotal,
			&token.CompletionTokensTotal,
			&token.CacheReadTokensTotal,
			&token.CacheCreationTokensTotal,
			&token.TotalCostUSD,
		); err != nil {
			return nil, fmt.Errorf("scan auth token: %w", err)
		}

		// 转换时间戳
		token.CreatedAt = time.UnixMilli(createdAtMs)
		if expiresAt.Valid {
			token.ExpiresAt = &expiresAt.Int64
		}
		if lastUsedAt.Valid {
			token.LastUsedAt = &lastUsedAt.Int64
		}
		token.IsActive = isActive != 0
		token.AllChannels = allChannels != 0

		tokens = append(tokens, token)
	}

	return tokens, rows.Err()
}

// UpdateAuthToken 更新令牌信息
func (s *SQLStore) UpdateAuthToken(ctx context.Context, token *model.AuthToken) error {
	var expiresAt any
	if token.ExpiresAt != nil {
		expiresAt = *token.ExpiresAt
	}

	var lastUsedAt any
	if token.LastUsedAt != nil {
		lastUsedAt = *token.LastUsedAt
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE auth_tokens
		SET description = ?,
		    expires_at = ?,
		    last_used_at = ?,
		    is_active = ?,
		    all_channels = ?
		WHERE id = ?
	`, token.Description, expiresAt, lastUsedAt, boolToInt(token.IsActive), boolToInt(token.AllChannels),
		token.ID)

	if err != nil {
		return fmt.Errorf("update auth token: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("auth token not found")
	}

	// 触发异步Redis同步 (新增 2025-11)
	s.triggerAsyncSync(syncAuthTokens)

	return nil
}

// DeleteAuthToken 删除令牌
func (s *SQLStore) DeleteAuthToken(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM auth_tokens WHERE id = ?
	`, id)

	if err != nil {
		return fmt.Errorf("delete auth token: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("auth token not found")
	}

	// 触发异步Redis同步 (新增 2025-11)
	s.triggerAsyncSync(syncAuthTokens)

	return nil
}

// UpdateTokenLastUsed 更新令牌最后使用时间
// 异步调用，性能优化
func (s *SQLStore) UpdateTokenLastUsed(ctx context.Context, tokenHash string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE auth_tokens
		SET last_used_at = ?
		WHERE token = ?
	`, now.UnixMilli(), tokenHash)

	if err != nil {
		return fmt.Errorf("update token last used: %w", err)
	}

	return nil
}

// UpdateTokenStats 增量更新Token统计信息
// 使用事务保证原子性，采用增量计算公式避免扫描历史数据
// 参数:
//   - tokenHash: Token的SHA256哈希值
//   - isSuccess: 本次请求是否成功(2xx状态码)
//   - duration: 总响应时间(秒)
//   - isStreaming: 是否为流式请求
//   - firstByteTime: 流式请求的首字节时间(秒)，非流式时为0
//   - promptTokens: 输入token数量
//   - completionTokens: 输出token数量
//   - costUSD: 本次请求费用(美元)
func (s *SQLStore) UpdateTokenStats(
	ctx context.Context,
	tokenHash string,
	isSuccess bool,
	duration float64,
	isStreaming bool,
	firstByteTime float64,
	promptTokens int64,
	completionTokens int64,
	cacheReadTokens int64,
	cacheCreationTokens int64,
	costUSD float64,
) error {
	// 使用事务保证原子性（读-计算-写）
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // 失败时自动回滚

	// 1. 查询当前统计数据
	var stats struct {
		SuccessCount             int64
		FailureCount             int64
		StreamAvgTTFB            float64
		NonStreamAvgRT           float64
		StreamCount              int64
		NonStreamCount           int64
		PromptTokensTotal        int64
		CompletionTokensTotal    int64
		CacheReadTokensTotal     int64
		CacheCreationTokensTotal int64
		TotalCostUSD             float64
	}

	err = tx.QueryRowContext(ctx, `
		SELECT
			success_count, failure_count,
			stream_avg_ttfb, non_stream_avg_rt,
			stream_count, non_stream_count,
			prompt_tokens_total, completion_tokens_total,
			cache_read_tokens_total, cache_creation_tokens_total,
			total_cost_usd
		FROM auth_tokens
		WHERE token = ?
	`, tokenHash).Scan(
		&stats.SuccessCount,
		&stats.FailureCount,
		&stats.StreamAvgTTFB,
		&stats.NonStreamAvgRT,
		&stats.StreamCount,
		&stats.NonStreamCount,
		&stats.PromptTokensTotal,
		&stats.CompletionTokensTotal,
		&stats.CacheReadTokensTotal,
		&stats.CacheCreationTokensTotal,
		&stats.TotalCostUSD,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("token not found: %s", tokenHash)
	}
	if err != nil {
		return fmt.Errorf("query current stats: %w", err)
	}

	// 2. 增量更新计数器
	if isSuccess {
		stats.SuccessCount++
		// 只有成功请求才累加token和费用
		stats.PromptTokensTotal += promptTokens
		stats.CompletionTokensTotal += completionTokens
		stats.CacheReadTokensTotal += cacheReadTokens
		stats.CacheCreationTokensTotal += cacheCreationTokens
		stats.TotalCostUSD += costUSD
	} else {
		stats.FailureCount++
	}

	// 3. 增量更新平均值（使用累加公式避免扫描历史数据）
	// 公式: new_avg = (old_avg * old_count + new_value) / (old_count + 1)
	if isStreaming && firstByteTime > 0 {
		// 流式请求：更新平均首字节时间
		stats.StreamAvgTTFB = ((stats.StreamAvgTTFB * float64(stats.StreamCount)) + firstByteTime) / float64(stats.StreamCount+1)
		stats.StreamCount++
	} else if !isStreaming {
		// 非流式请求：更新平均响应时间
		stats.NonStreamAvgRT = ((stats.NonStreamAvgRT * float64(stats.NonStreamCount)) + duration) / float64(stats.NonStreamCount+1)
		stats.NonStreamCount++
	}

	// 4. 写回数据库
	now := time.Now()
	_, err = tx.ExecContext(ctx, `
		UPDATE auth_tokens
		SET
			success_count = ?,
			failure_count = ?,
			stream_avg_ttfb = ?,
			non_stream_avg_rt = ?,
			stream_count = ?,
			non_stream_count = ?,
			prompt_tokens_total = ?,
			completion_tokens_total = ?,
			cache_read_tokens_total = ?,
			cache_creation_tokens_total = ?,
			total_cost_usd = ?,
			last_used_at = ?
		WHERE token = ?
	`,
		stats.SuccessCount,
		stats.FailureCount,
		stats.StreamAvgTTFB,
		stats.NonStreamAvgRT,
		stats.StreamCount,
		stats.NonStreamCount,
		stats.PromptTokensTotal,
		stats.CompletionTokensTotal,
		stats.CacheReadTokensTotal,
		stats.CacheCreationTokensTotal,
		stats.TotalCostUSD,
		now.UnixMilli(),
		tokenHash,
	)

	if err != nil {
		return fmt.Errorf("update stats: %w", err)
	}

	// 5. 提交事务
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// ============================================================================
// Token-Channel 关联管理（令牌渠道访问控制）
// ============================================================================

// GetTokenChannels 获取令牌允许使用的渠道ID列表
// 仅当令牌的 all_channels=false 时有意义
func (s *SQLStore) GetTokenChannels(ctx context.Context, tokenID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT channel_id FROM token_channels
		WHERE token_id = ?
		ORDER BY channel_id ASC
	`, tokenID)
	if err != nil {
		return nil, fmt.Errorf("get token channels: %w", err)
	}
	defer rows.Close()

	var channelIDs []int64
	for rows.Next() {
		var channelID int64
		if err := rows.Scan(&channelID); err != nil {
			return nil, fmt.Errorf("scan channel id: %w", err)
		}
		channelIDs = append(channelIDs, channelID)
	}

	return channelIDs, rows.Err()
}

// SetTokenChannels 设置令牌允许使用的渠道列表（覆盖式更新）
// 先删除旧关联，再插入新关联
func (s *SQLStore) SetTokenChannels(ctx context.Context, tokenID int64, channelIDs []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 删除旧关联
	_, err = tx.ExecContext(ctx, "DELETE FROM token_channels WHERE token_id = ?", tokenID)
	if err != nil {
		return fmt.Errorf("delete old token channels: %w", err)
	}

	// 插入新关联
	if len(channelIDs) > 0 {
		now := time.Now().UnixMilli()
		for _, channelID := range channelIDs {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO token_channels (token_id, channel_id, created_at)
				VALUES (?, ?, ?)
			`, tokenID, channelID, now)
			if err != nil {
				return fmt.Errorf("insert token channel: %w", err)
			}
		}
	}

	// 触发异步Redis同步
	s.triggerAsyncSync(syncAuthTokens)

	return tx.Commit()
}

// LoadTokenChannelsMap 批量加载多个令牌的渠道关联
// 用于列表展示时一次性获取所有令牌的渠道配置
// 返回 map[tokenID][]channelID
func (s *SQLStore) LoadTokenChannelsMap(ctx context.Context, tokenIDs []int64) (map[int64][]int64, error) {
	if len(tokenIDs) == 0 {
		return make(map[int64][]int64), nil
	}

	// 构建 IN 查询
	placeholders := make([]string, len(tokenIDs))
	args := make([]any, len(tokenIDs))
	for i, id := range tokenIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT token_id, channel_id FROM token_channels
		WHERE token_id IN (%s)
		ORDER BY token_id, channel_id
	`, joinStrings(placeholders, ","))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load token channels map: %w", err)
	}
	defer rows.Close()

	result := make(map[int64][]int64)
	for rows.Next() {
		var tokenID, channelID int64
		if err := rows.Scan(&tokenID, &channelID); err != nil {
			return nil, fmt.Errorf("scan token channel: %w", err)
		}
		result[tokenID] = append(result[tokenID], channelID)
	}

	return result, rows.Err()
}

// joinStrings 辅助函数：连接字符串切片
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
