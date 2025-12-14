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
		SELECT id, channel_id, url, is_active, latency_ms, last_test_at, sort_order, created_at
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
		var latencyMs sql.NullInt64
		err := rows.Scan(
			&ep.ID, &ep.ChannelID, &ep.URL, &ep.IsActive,
			&latencyMs, &ep.LastTestAt, &ep.SortOrder, &ep.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		if latencyMs.Valid {
			v := int(latencyMs.Int64)
			ep.LatencyMs = &v
		}
		endpoints = append(endpoints, ep)
	}

	return endpoints, rows.Err()
}

// GetActiveEndpoint 获取渠道的激活端点
func (s *SQLStore) GetActiveEndpoint(ctx context.Context, channelID int64) (*model.ChannelEndpoint, error) {
	query := `
		SELECT id, channel_id, url, is_active, latency_ms, last_test_at, sort_order, created_at
		FROM channel_endpoints
		WHERE channel_id = ? AND is_active = 1
		LIMIT 1
	`
	row := s.db.QueryRowContext(ctx, query, channelID)

	var ep model.ChannelEndpoint
	var latencyMs sql.NullInt64
	err := row.Scan(
		&ep.ID, &ep.ChannelID, &ep.URL, &ep.IsActive,
		&latencyMs, &ep.LastTestAt, &ep.SortOrder, &ep.CreatedAt,
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

	// 插入新端点
	if len(endpoints) > 0 {
		now := time.Now().Unix()
		insertQuery := `
			INSERT INTO channel_endpoints (channel_id, url, is_active, latency_ms, last_test_at, sort_order, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`
		for i, ep := range endpoints {
			var latencyMs any = nil
			if ep.LatencyMs != nil {
				latencyMs = *ep.LatencyMs
			}
			_, err = tx.ExecContext(ctx, insertQuery,
				channelID, ep.URL, ep.IsActive, latencyMs, ep.LastTestAt, i, now,
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
func (s *SQLStore) UpdateEndpointLatency(ctx context.Context, endpointID int64, latencyMs int) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		"UPDATE channel_endpoints SET latency_ms = ?, last_test_at = ? WHERE id = ?",
		latencyMs, now, endpointID,
	)
	return err
}

// UpdateEndpointsLatency 批量更新端点延迟（测速后调用）
func (s *SQLStore) UpdateEndpointsLatency(ctx context.Context, results map[int64]int) error {
	if len(results) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	for endpointID, latencyMs := range results {
		_, err = tx.ExecContext(ctx,
			"UPDATE channel_endpoints SET latency_ms = ?, last_test_at = ? WHERE id = ?",
			latencyMs, now, endpointID,
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
	// 查找延迟最小的端点
	var fastestID int64
	var fastestURL string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, url FROM channel_endpoints
		WHERE channel_id = ? AND latency_ms IS NOT NULL
		ORDER BY latency_ms ASC
		LIMIT 1
	`, channelID).Scan(&fastestID, &fastestURL)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // 没有测速结果，不做任何操作
		}
		return err
	}

	// 设置为激活端点
	return s.SetActiveEndpoint(ctx, channelID, fastestID)
}

// GetChannelsWithAutoSelect 获取所有开启自动选择且有端点的渠道ID列表
func (s *SQLStore) GetChannelsWithAutoSelect(ctx context.Context) ([]*model.Config, error) {
	// 只查询已启用、开启自动选择、且有多个端点的渠道
	query := `
		SELECT DISTINCT c.id, c.name
		FROM channels c
		INNER JOIN channel_endpoints e ON c.id = e.channel_id
		WHERE c.enabled = 1 AND c.auto_select_endpoint = 1
		GROUP BY c.id
		HAVING COUNT(e.id) > 1
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
