package sql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"ccLoad/internal/model"
)

// AggregateDailyStats 聚合指定日期的统计数据到 daily_stats 表
// 从 logs 表聚合数据，按 channel_id + model + auth_token_id 维度
func (s *SQLStore) AggregateDailyStats(ctx context.Context, date time.Time) error {
	// 计算日期范围（当天 00:00:00 到 23:59:59.999）
	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	endOfDay := time.Date(date.Year(), date.Month(), date.Day(), 23, 59, 59, 999999999, date.Location())
	startMs := startOfDay.UnixMilli()
	endMs := endOfDay.UnixMilli()
	dateStr := startOfDay.Format("2006-01-02")

	// 先删除该日期已有的统计（支持重新聚合）
	deleteQuery := "DELETE FROM daily_stats WHERE date = ?"
	if _, err := s.db.ExecContext(ctx, deleteQuery, dateStr); err != nil {
		return fmt.Errorf("delete existing daily stats: %w", err)
	}

	// 聚合查询：从 logs 表按维度聚合
	// 需要 JOIN channels 表获取 channel_type
	aggregateQuery := `
		INSERT INTO daily_stats (
			date, channel_id, channel_type, model, auth_token_id,
			success_count, error_count, total_count,
			input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens,
			total_cost, avg_duration, avg_first_byte_time,
			stream_count, non_stream_count, created_at
		)
		SELECT
			? as date,
			l.channel_id,
			COALESCE(c.channel_type, '') as channel_type,
			COALESCE(l.model, '') as model,
			COALESCE(l.auth_token_id, 0) as auth_token_id,
			SUM(CASE WHEN l.status_code >= 200 AND l.status_code < 300 THEN 1 ELSE 0 END) as success_count,
			SUM(CASE WHEN l.status_code < 200 OR l.status_code >= 300 THEN 1 ELSE 0 END) as error_count,
			COUNT(*) as total_count,
			SUM(COALESCE(l.input_tokens, 0)) as input_tokens,
			SUM(COALESCE(l.output_tokens, 0)) as output_tokens,
			SUM(COALESCE(l.cache_read_input_tokens, 0)) as cache_read_tokens,
			SUM(COALESCE(l.cache_creation_input_tokens, 0)) as cache_creation_tokens,
			SUM(COALESCE(l.cost, 0.0)) as total_cost,
			COALESCE(AVG(CASE WHEN l.duration > 0 THEN l.duration ELSE NULL END), 0.0) as avg_duration,
			COALESCE(AVG(CASE WHEN l.is_streaming = 1 AND l.first_byte_time > 0 AND l.status_code >= 200 AND l.status_code < 300 THEN l.first_byte_time ELSE NULL END), 0.0) as avg_first_byte_time,
			SUM(CASE WHEN l.is_streaming = 1 THEN 1 ELSE 0 END) as stream_count,
			SUM(CASE WHEN l.is_streaming = 0 THEN 1 ELSE 0 END) as non_stream_count,
			? as created_at
		FROM logs l
		LEFT JOIN channels c ON l.channel_id = c.id
		WHERE l.time >= ? AND l.time <= ? AND l.channel_id > 0
		GROUP BY l.channel_id, c.channel_type, l.model, l.auth_token_id
		HAVING total_count > 0
	`

	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, aggregateQuery, dateStr, now, startMs, endMs)
	if err != nil {
		return fmt.Errorf("aggregate daily stats: %w", err)
	}

	return nil
}

// GetDailyStats 查询日期范围内的每日统计记录
func (s *SQLStore) GetDailyStats(ctx context.Context, startDate, endDate time.Time) ([]*model.DailyStat, error) {
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")

	query := `
		SELECT id, date, channel_id, channel_type, model, auth_token_id,
		       success_count, error_count, total_count,
		       input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens,
		       total_cost, avg_duration, avg_first_byte_time,
		       stream_count, non_stream_count, created_at
		FROM daily_stats
		WHERE date >= ? AND date <= ?
		ORDER BY date DESC, channel_id ASC, model ASC
	`

	rows, err := s.db.QueryContext(ctx, query, startStr, endStr)
	if err != nil {
		return nil, fmt.Errorf("query daily stats: %w", err)
	}
	defer rows.Close()

	var stats []*model.DailyStat
	for rows.Next() {
		var stat model.DailyStat
		err := rows.Scan(
			&stat.ID, &stat.Date, &stat.ChannelID, &stat.ChannelType, &stat.Model, &stat.AuthTokenID,
			&stat.SuccessCount, &stat.ErrorCount, &stat.TotalCount,
			&stat.InputTokens, &stat.OutputTokens, &stat.CacheReadTokens, &stat.CacheCreationTokens,
			&stat.TotalCost, &stat.AvgDuration, &stat.AvgFirstByteTime,
			&stat.StreamCount, &stat.NonStreamCount, &stat.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan daily stat: %w", err)
		}
		stats = append(stats, &stat)
	}

	return stats, rows.Err()
}

// GetDailyStatsSummary 汇总日期范围内的统计数据，返回与 GetStats 兼容的格式
// 用于替代从 logs 表实时聚合，支持查询已清理日志的历史数据
func (s *SQLStore) GetDailyStatsSummary(ctx context.Context, startDate, endDate time.Time, filter *model.LogFilter) ([]model.StatsEntry, error) {
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")

	// 基础查询：按 channel_id + model 汇总
	baseQuery := `
		SELECT
			channel_id,
			model,
			SUM(success_count) as success,
			SUM(error_count) as error,
			SUM(total_count) as total,
			SUM(input_tokens) as total_input_tokens,
			SUM(output_tokens) as total_output_tokens,
			SUM(cache_read_tokens) as total_cache_read_tokens,
			SUM(cache_creation_tokens) as total_cache_creation_tokens,
			SUM(total_cost) as total_cost,
			SUM(avg_duration * total_count) / NULLIF(SUM(total_count), 0) as avg_duration,
			SUM(avg_first_byte_time * stream_count) / NULLIF(SUM(stream_count), 0) as avg_first_byte_time
		FROM daily_stats
		WHERE date >= ? AND date <= ?
	`

	args := []any{startStr, endStr}

	// 应用过滤条件
	if filter != nil {
		if filter.ChannelType != "" {
			baseQuery += " AND channel_type = ?"
			args = append(args, filter.ChannelType)
		}
		if filter.Model != "" {
			baseQuery += " AND model = ?"
			args = append(args, filter.Model)
		}
		if filter.AuthTokenID != nil && *filter.AuthTokenID > 0 {
			baseQuery += " AND auth_token_id = ?"
			args = append(args, *filter.AuthTokenID)
		}
	}

	baseQuery += " GROUP BY channel_id, model ORDER BY channel_id ASC, model ASC"

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query daily stats summary: %w", err)
	}
	defer rows.Close()

	var stats []model.StatsEntry
	channelIDsToFetch := make(map[int64]bool)

	for rows.Next() {
		var entry model.StatsEntry
		var channelID int64
		var avgDuration, avgFirstByteTime sql.NullFloat64
		var totalInputTokens, totalOutputTokens, totalCacheReadTokens, totalCacheCreationTokens sql.NullInt64
		var totalCost sql.NullFloat64

		err := rows.Scan(
			&channelID, &entry.Model,
			&entry.Success, &entry.Error, &entry.Total,
			&totalInputTokens, &totalOutputTokens, &totalCacheReadTokens, &totalCacheCreationTokens,
			&totalCost, &avgDuration, &avgFirstByteTime,
		)
		if err != nil {
			return nil, fmt.Errorf("scan daily stats summary: %w", err)
		}

		chID := int(channelID)
		entry.ChannelID = &chID
		channelIDsToFetch[channelID] = true

		if avgDuration.Valid && avgDuration.Float64 > 0 {
			entry.AvgDurationSeconds = &avgDuration.Float64
		}
		if avgFirstByteTime.Valid && avgFirstByteTime.Float64 > 0 {
			entry.AvgFirstByteTimeSeconds = &avgFirstByteTime.Float64
		}
		if totalInputTokens.Valid && totalInputTokens.Int64 > 0 {
			entry.TotalInputTokens = &totalInputTokens.Int64
		}
		if totalOutputTokens.Valid && totalOutputTokens.Int64 > 0 {
			entry.TotalOutputTokens = &totalOutputTokens.Int64
		}
		if totalCacheReadTokens.Valid && totalCacheReadTokens.Int64 > 0 {
			entry.TotalCacheReadInputTokens = &totalCacheReadTokens.Int64
		}
		if totalCacheCreationTokens.Valid && totalCacheCreationTokens.Int64 > 0 {
			entry.TotalCacheCreationInputTokens = &totalCacheCreationTokens.Int64
		}
		if totalCost.Valid && totalCost.Float64 > 0 {
			entry.TotalCost = &totalCost.Float64
		}

		stats = append(stats, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 批量获取渠道名称
	if len(channelIDsToFetch) > 0 {
		channelNames, err := s.batchGetChannelNames(ctx, channelIDsToFetch)
		if err != nil {
			return nil, fmt.Errorf("batch get channel names: %w", err)
		}
		for i := range stats {
			if stats[i].ChannelID != nil {
				if name, ok := channelNames[int64(*stats[i].ChannelID)]; ok {
					stats[i].ChannelName = name
				}
			}
		}
	}

	return stats, nil
}

// CleanupDailyStatsBefore 清理指定日期之前的统计数据
func (s *SQLStore) CleanupDailyStatsBefore(ctx context.Context, cutoff time.Time) error {
	cutoffStr := cutoff.Format("2006-01-02")
	query := "DELETE FROM daily_stats WHERE date < ?"
	_, err := s.db.ExecContext(ctx, query, cutoffStr)
	if err != nil {
		return fmt.Errorf("cleanup daily stats: %w", err)
	}
	return nil
}

// GetLatestDailyStatsDate 获取最新的统计日期
// 如果没有统计数据，返回零值时间
func (s *SQLStore) GetLatestDailyStatsDate(ctx context.Context) (time.Time, error) {
	query := "SELECT MAX(date) FROM daily_stats"
	var dateStr sql.NullString
	err := s.db.QueryRowContext(ctx, query).Scan(&dateStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("get latest daily stats date: %w", err)
	}

	if !dateStr.Valid || dateStr.String == "" {
		return time.Time{}, nil // 没有统计数据
	}

	date, err := time.Parse("2006-01-02", dateStr.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse date: %w", err)
	}

	return date, nil
}

// GetDailyStatsMetrics 从 daily_stats 表查询趋势图数据（每天一个数据点）
// 用于历史数据查询，支持渠道类型、模型和令牌过滤
func (s *SQLStore) GetDailyStatsMetrics(ctx context.Context, startDate, endDate time.Time, channelType, modelFilter string, authTokenID int64) ([]model.MetricPoint, error) {
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")

	// 基础查询：按日期和渠道聚合
	query := `
		SELECT
			date,
			channel_id,
			SUM(success_count) AS success,
			SUM(error_count) AS error,
			SUM(total_cost) AS total_cost,
			SUM(input_tokens) AS input_tokens,
			SUM(output_tokens) AS output_tokens,
			SUM(cache_read_tokens) AS cache_read_tokens,
			SUM(cache_creation_tokens) AS cache_creation_tokens,
			SUM(avg_first_byte_time * stream_count) / NULLIF(SUM(stream_count), 0) AS avg_first_byte_time,
			SUM(avg_duration * total_count) / NULLIF(SUM(total_count), 0) AS avg_duration,
			SUM(stream_count) AS stream_count,
			SUM(total_count) AS total_count
		FROM daily_stats
		WHERE date >= ? AND date <= ?
	`

	args := []any{startStr, endStr}

	// 添加过滤条件
	if channelType != "" {
		query += " AND channel_type = ?"
		args = append(args, channelType)
	}
	if modelFilter != "" {
		query += " AND model = ?"
		args = append(args, modelFilter)
	}
	if authTokenID > 0 {
		query += " AND auth_token_id = ?"
		args = append(args, authTokenID)
	}

	query += " GROUP BY date, channel_id ORDER BY date ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query daily stats metrics: %w", err)
	}
	defer rows.Close()

	// 按日期聚合数据
	dateMap := make(map[string]*model.MetricPoint)
	channelIDsToFetch := make(map[int64]bool)

	for rows.Next() {
		var dateStr string
		var channelID sql.NullInt64
		var success, errorCount int
		var totalCost float64
		var inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens int64
		var avgFirstByteTime, avgDuration sql.NullFloat64
		var streamCount, totalCount int

		if err := rows.Scan(&dateStr, &channelID, &success, &errorCount, &totalCost,
			&inputTokens, &outputTokens, &cacheReadTokens, &cacheCreationTokens,
			&avgFirstByteTime, &avgDuration, &streamCount, &totalCount); err != nil {
			return nil, fmt.Errorf("scan daily stats metrics: %w", err)
		}

		// 统一日期格式为 "2006-01-02"（数据库可能返回带时间的格式）
		if len(dateStr) > 10 {
			dateStr = dateStr[:10]
		}

		// 获取或创建该日期的 MetricPoint
		mp, ok := dateMap[dateStr]
		if !ok {
			// 解析日期为时间（设置为当天中午12点，避免时区问题）
			date, _ := time.Parse("2006-01-02", dateStr)
			date = date.Add(12 * time.Hour)
			mp = &model.MetricPoint{
				Ts:       date,
				Channels: make(map[string]model.ChannelMetric),
			}
			dateMap[dateStr] = mp
		}

		// 累加总体统计
		mp.Success += success
		mp.Error += errorCount
		mp.InputTokens += inputTokens
		mp.OutputTokens += outputTokens
		mp.CacheReadTokens += cacheReadTokens
		mp.CacheCreationTokens += cacheCreationTokens

		if mp.TotalCost == nil {
			mp.TotalCost = new(float64)
		}
		*mp.TotalCost += totalCost

		// 渠道级别统计
		if channelID.Valid && channelID.Int64 > 0 {
			channelIDsToFetch[channelID.Int64] = true
			channelKey := fmt.Sprintf("ch_%d", channelID.Int64)

			var avgFBT *float64
			if avgFirstByteTime.Valid && avgFirstByteTime.Float64 > 0 {
				avgFBT = new(float64)
				*avgFBT = avgFirstByteTime.Float64
			}
			var avgDur *float64
			if avgDuration.Valid && avgDuration.Float64 > 0 {
				avgDur = new(float64)
				*avgDur = avgDuration.Float64
			}
			var chCost *float64
			if totalCost > 0 {
				chCost = new(float64)
				*chCost = totalCost
			}

			// 合并同一天同一渠道的数据
			if existing, ok := mp.Channels[channelKey]; ok {
				existing.Success += success
				existing.Error += errorCount
				existing.InputTokens += inputTokens
				existing.OutputTokens += outputTokens
				existing.CacheReadTokens += cacheReadTokens
				existing.CacheCreationTokens += cacheCreationTokens
				if existing.TotalCost != nil && chCost != nil {
					*existing.TotalCost += *chCost
				} else if chCost != nil {
					existing.TotalCost = chCost
				}
				// 平均值取最新的
				if avgFBT != nil {
					existing.AvgFirstByteTimeSeconds = avgFBT
				}
				if avgDur != nil {
					existing.AvgDurationSeconds = avgDur
				}
				mp.Channels[channelKey] = existing
			} else {
				mp.Channels[channelKey] = model.ChannelMetric{
					Success:                 success,
					Error:                   errorCount,
					AvgFirstByteTimeSeconds: avgFBT,
					AvgDurationSeconds:      avgDur,
					TotalCost:               chCost,
					InputTokens:             inputTokens,
					OutputTokens:            outputTokens,
					CacheReadTokens:         cacheReadTokens,
					CacheCreationTokens:     cacheCreationTokens,
				}
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 批量获取渠道名称
	channelNames := make(map[int64]string)
	if len(channelIDsToFetch) > 0 {
		var err error
		channelNames, err = s.batchGetChannelNames(ctx, channelIDsToFetch)
		if err != nil {
			return nil, fmt.Errorf("batch get channel names: %w", err)
		}
	}

	// 替换渠道ID为渠道名称
	for _, mp := range dateMap {
		newChannels := make(map[string]model.ChannelMetric)
		for key, metric := range mp.Channels {
			var channelID int64
			fmt.Sscanf(key, "ch_%d", &channelID)
			if name, ok := channelNames[channelID]; ok {
				newChannels[name] = metric
			} else {
				newChannels["未知渠道"] = metric
			}
		}
		mp.Channels = newChannels

		// 计算总体平均值（简化处理：使用加权平均）
		// 这里需要额外查询来获取准确的平均值，暂时跳过
	}

	// 按日期排序输出
	result := make([]model.MetricPoint, 0, len(dateMap))
	for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		if mp, ok := dateMap[dateStr]; ok {
			result = append(result, *mp)
		} else {
			// 该日期无数据，添加空数据点
			date := d.Add(12 * time.Hour)
			result = append(result, model.MetricPoint{
				Ts:       date,
				Channels: make(map[string]model.ChannelMetric),
			})
		}
	}

	return result, nil
}

// batchGetChannelNames 批量获取渠道名称
func (s *SQLStore) batchGetChannelNames(ctx context.Context, channelIDs map[int64]bool) (map[int64]string, error) {
	if len(channelIDs) == 0 {
		return nil, nil
	}

	// 构建 IN 查询
	ids := make([]any, 0, len(channelIDs))
	placeholders := ""
	for id := range channelIDs {
		ids = append(ids, id)
		if placeholders != "" {
			placeholders += ","
		}
		placeholders += "?"
	}

	query := fmt.Sprintf("SELECT id, name FROM channels WHERE id IN (%s)", placeholders)
	rows, err := s.db.QueryContext(ctx, query, ids...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := make(map[int64]string)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
	}

	return names, rows.Err()
}
