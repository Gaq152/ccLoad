package sql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"ccLoad/internal/model"
)

// ==================== ChannelEndpoints CRUD 实现 ====================

// ListEndpoints 获取渠道的所有端点
func (s *SQLStore) ListEndpoints(ctx context.Context, channelID int64) ([]model.ChannelEndpoint, error) {
	query := `
		SELECT id, channel_id, url, is_active, latency_ms, status_code, last_test_at, sort_order, created_at
		FROM channel_endpoints
		WHERE channel_id = ?
		ORDER BY sort_order ASC, id ASC
	`
	rows, err := s.db.QueryContext(ctx, query, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var endpoints []model.ChannelEndpoint
	for rows.Next() {
		var ep model.ChannelEndpoint
		var latencyMs, statusCode sql.NullInt64
		err := rows.Scan(
			&ep.ID, &ep.ChannelID, &ep.URL, &ep.IsActive,
			&latencyMs, &statusCode, &ep.LastTestAt, &ep.SortOrder, &ep.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		if latencyMs.Valid {
			v := int(latencyMs.Int64)
			ep.LatencyMs = &v
		}
		if statusCode.Valid {
			v := int(statusCode.Int64)
			ep.StatusCode = &v
		}
		endpoints = append(endpoints, ep)
	}

	return endpoints, rows.Err()
}

// GetActiveEndpoint 获取渠道的激活端点
func (s *SQLStore) GetActiveEndpoint(ctx context.Context, channelID int64) (*model.ChannelEndpoint, error) {
	// 按 sort_order 和 id 排序，确保多个 active 时返回确定的结果
	query := `
		SELECT id, channel_id, url, is_active, latency_ms, status_code, last_test_at, sort_order, created_at
		FROM channel_endpoints
		WHERE channel_id = ? AND is_active = 1
		ORDER BY sort_order ASC, id ASC
		LIMIT 1
	`
	row := s.db.QueryRowContext(ctx, query, channelID)

	var ep model.ChannelEndpoint
	var latencyMs, statusCode sql.NullInt64
	err := row.Scan(
		&ep.ID, &ep.ChannelID, &ep.URL, &ep.IsActive,
		&latencyMs, &statusCode, &ep.LastTestAt, &ep.SortOrder, &ep.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // 没有激活端点
		}
		return nil, err
	}
	if latencyMs.Valid {
		v := int(latencyMs.Int64)
		ep.LatencyMs = &v
	}
	if statusCode.Valid {
		v := int(statusCode.Int64)
		ep.StatusCode = &v
	}
	return &ep, nil
}

// SaveEndpoints 批量保存端点（删除旧的，插入新的）
func (s *SQLStore) SaveEndpoints(ctx context.Context, channelID int64, endpoints []model.ChannelEndpoint) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 删除旧端点
	_, err = tx.ExecContext(ctx, "DELETE FROM channel_endpoints WHERE channel_id = ?", channelID)
	if err != nil {
		return err
	}

	// 去重：同一渠道下 URL 完全相同的端点只保留一个（优先保留激活项），
	// 避免端点管理弹窗出现两个相同端点。channel_endpoints 表无 (channel_id,url) 唯一约束，
	// 此处在写入前做最后一道防线。
	if len(endpoints) > 0 {
		seen := make(map[string]int, len(endpoints))
		deduped := make([]model.ChannelEndpoint, 0, len(endpoints))
		for _, ep := range endpoints {
			if idx, ok := seen[ep.URL]; ok {
				// 已存在同 URL 端点：若新项是激活态，则继承激活标记
				if ep.IsActive {
					deduped[idx].IsActive = true
				}
				continue
			}
			seen[ep.URL] = len(deduped)
			deduped = append(deduped, ep)
		}
		endpoints = deduped
	}

	// 插入新端点
	if len(endpoints) > 0 {
		// 确保至少有一个端点是激活的（如果没有激活的，默认第一个）
		hasActive := false
		for _, ep := range endpoints {
			if ep.IsActive {
				hasActive = true
				break
			}
		}
		if !hasActive {
			endpoints[0].IsActive = true
		}

		now := time.Now().Unix()
		insertQuery := `
			INSERT INTO channel_endpoints (channel_id, url, is_active, latency_ms, status_code, last_test_at, sort_order, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`
		// 记录 active 端点的 URL，用于同步到 channels.url
		var activeURL string
		for i, ep := range endpoints {
			var latencyMs, statusCode any = nil, nil
			if ep.LatencyMs != nil {
				latencyMs = *ep.LatencyMs
			}
			if ep.StatusCode != nil {
				statusCode = *ep.StatusCode
			}
			_, err = tx.ExecContext(ctx, insertQuery,
				channelID, ep.URL, ep.IsActive, latencyMs, statusCode, ep.LastTestAt, i, now,
			)
			if err != nil {
				return err
			}
			if ep.IsActive {
				activeURL = ep.URL
			}
		}

		// 同步更新 channels.url（以 active 端点为准）
		if activeURL != "" {
			_, err = tx.ExecContext(ctx,
				"UPDATE channels SET url = ?, updated_at = ? WHERE id = ?",
				activeURL, now, channelID,
			)
			if err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// SetActiveEndpoint 设置激活的端点（同时更新 channels.url）
func (s *SQLStore) SetActiveEndpoint(ctx context.Context, channelID int64, endpointID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 取消所有端点的激活状态
	_, err = tx.ExecContext(ctx,
		"UPDATE channel_endpoints SET is_active = 0 WHERE channel_id = ?",
		channelID,
	)
	if err != nil {
		return err
	}

	// 激活指定端点
	_, err = tx.ExecContext(ctx,
		"UPDATE channel_endpoints SET is_active = 1 WHERE id = ? AND channel_id = ?",
		endpointID, channelID,
	)
	if err != nil {
		return err
	}

	// 获取新激活端点的URL
	var url string
	err = tx.QueryRowContext(ctx,
		"SELECT url FROM channel_endpoints WHERE id = ?",
		endpointID,
	).Scan(&url)
	if err != nil {
		return err
	}

	// 更新 channels.url 字段（保持兼容性）
	_, err = tx.ExecContext(ctx,
		"UPDATE channels SET url = ?, updated_at = ? WHERE id = ?",
		url, time.Now().Unix(), channelID,
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// UpdateEndpointLatency 更新端点延迟测试结果
func (s *SQLStore) UpdateEndpointLatency(ctx context.Context, endpointID int64, latencyMs int, statusCode int) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		"UPDATE channel_endpoints SET latency_ms = ?, status_code = ?, last_test_at = ? WHERE id = ?",
		latencyMs, statusCode, now, endpointID,
	)
	return err
}

// UpdateEndpointsLatency 批量更新端点延迟和状态码（测速后调用）
func (s *SQLStore) UpdateEndpointsLatency(ctx context.Context, results map[int64]model.EndpointTestResult) error {
	if len(results) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	for endpointID, result := range results {
		_, err = tx.ExecContext(ctx,
			"UPDATE channel_endpoints SET latency_ms = ?, status_code = ?, last_test_at = ? WHERE id = ?",
			result.LatencyMs, result.StatusCode, now, endpointID,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetChannelAutoSelectEndpoint 获取渠道的自动选择端点设置
func (s *SQLStore) GetChannelAutoSelectEndpoint(ctx context.Context, channelID int64) (bool, error) {
	var autoSelect bool
	err := s.db.QueryRowContext(ctx,
		"SELECT auto_select_endpoint FROM channels WHERE id = ?",
		channelID,
	).Scan(&autoSelect)
	if err != nil {
		return false, err
	}
	return autoSelect, nil
}

// SetChannelAutoSelectEndpoint 设置渠道的自动选择端点开关
func (s *SQLStore) SetChannelAutoSelectEndpoint(ctx context.Context, channelID int64, autoSelect bool) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE channels SET auto_select_endpoint = ?, updated_at = ? WHERE id = ?",
		autoSelect, time.Now().Unix(), channelID,
	)
	return err
}

// SelectFastestEndpoint 自动选择最快端点并激活
func (s *SQLStore) SelectFastestEndpoint(ctx context.Context, channelID int64) error {
	// 查找延迟最小的端点（排除超时的，latency_ms >= 0）
	var fastestID int64
	var fastestURL string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, url FROM channel_endpoints
		WHERE channel_id = ? AND latency_ms IS NOT NULL AND latency_ms >= 0
		ORDER BY latency_ms ASC
		LIMIT 1
	`, channelID).Scan(&fastestID, &fastestURL)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // 没有测速结果或全部超时，不做任何操作
		}
		return err
	}

	// 设置为激活端点
	return s.SetActiveEndpoint(ctx, channelID, fastestID)
}

// SyncActiveEndpointURL 同步更新 active endpoint 的 URL（当 channels.url 变更时调用）
// 关键不变量：同一渠道下不允许出现两个 URL 完全相同的端点。
//   - newURL 已存在于某端点：仅切换激活端点到该端点，绝不改写其他端点的 URL（否则产生重复行）
//   - newURL 不存在：把当前激活端点的 URL 改为 newURL
//   - 没有任何端点：创建一个新的激活端点
func (s *SQLStore) SyncActiveEndpointURL(ctx context.Context, channelID int64, newURL string) error {
	if newURL == "" {
		return nil
	}

	endpoints, err := s.ListEndpoints(ctx, channelID)
	if err != nil {
		return err
	}

	// 没有任何端点，创建一个新的激活端点
	if len(endpoints) == 0 {
		now := time.Now().Unix()
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO channel_endpoints (channel_id, url, is_active, sort_order, created_at)
			VALUES (?, ?, 1, 0, ?)
		`, channelID, newURL, now)
		return err
	}

	// 查找匹配 newURL 的端点与当前激活端点
	var matched, activeEp *model.ChannelEndpoint
	for i := range endpoints {
		ep := &endpoints[i]
		if matched == nil && ep.URL == newURL {
			matched = ep
		}
		if activeEp == nil && ep.IsActive {
			activeEp = ep
		}
	}

	// 情况1：newURL 已存在 —— 仅切换激活端点到匹配端点，不改写任何 URL（避免重复）
	if matched != nil {
		if matched.IsActive {
			return nil // 已是激活端点，无需任何操作
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx,
			"UPDATE channel_endpoints SET is_active = 0 WHERE channel_id = ?", channelID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx,
			"UPDATE channel_endpoints SET is_active = 1 WHERE id = ?", matched.ID); err != nil {
			return err
		}
		return tx.Commit()
	}

	// 情况2：newURL 不存在 —— 把激活端点的 URL 改为 newURL
	if activeEp != nil {
		_, err = s.db.ExecContext(ctx,
			"UPDATE channel_endpoints SET url = ? WHERE id = ?", newURL, activeEp.ID)
		return err
	}

	// 有端点但无激活端点：把第一个设为激活并更新其 URL
	firstEp := endpoints[0]
	_, err = s.db.ExecContext(ctx, `
		UPDATE channel_endpoints SET url = ?, is_active = 1 WHERE id = ?
	`, newURL, firstEp.ID)
	return err
}

// GetChannelsWithAutoSelect 获取所有开启自动选择且有端点的渠道ID列表
func (s *SQLStore) GetChannelsWithAutoSelect(ctx context.Context) ([]*model.Config, error) {
	// 查询已启用、开启自动选择、且有端点的渠道
	query := `
		SELECT DISTINCT c.id, c.name
		FROM channels c
		INNER JOIN channel_endpoints e ON c.id = e.channel_id
		WHERE c.enabled = 1 AND c.auto_select_endpoint = 1
		GROUP BY c.id
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []*model.Config
	for rows.Next() {
		var ch model.Config
		if err := rows.Scan(&ch.ID, &ch.Name); err != nil {
			return nil, err
		}
		channels = append(channels, &ch)
	}

	return channels, rows.Err()
}
