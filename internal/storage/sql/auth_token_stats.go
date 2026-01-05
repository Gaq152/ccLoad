package sql

import (
	"context"
	"database/sql"
	"time"

	"ccLoad/internal/model"
)

// GetAuthTokenStatsInRange 查询指定时间范围内每个token的统计数据
// - 今日数据：从 logs 表实时查询
// - 历史数据：从 daily_stats 表查询（支持已清理日志的历史查询）
// 用于tokens.html页面按时间范围筛选显示（2025-12新增，2026-01修复历史数据查询）
func (s *SQLStore) GetAuthTokenStatsInRange(ctx context.Context, startTime, endTime time.Time) (map[int64]*model.AuthTokenRangeStats, error) {
	stats := make(map[int64]*model.AuthTokenRangeStats)

	// 计算今天的起始时间
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// 判断时间范围是否包含今天
	includestoday := !endTime.Before(todayStart)
	includesHistory := startTime.Before(todayStart)

	// 1. 如果包含今天，从 logs 表查询今日数据
	if includestoday {
		logsStart := startTime
		if logsStart.Before(todayStart) {
			logsStart = todayStart // 只查今天的 logs
		}
		if err := s.getAuthTokenStatsFromLogs(ctx, logsStart, endTime, stats); err != nil {
			return nil, err
		}
	}

	// 2. 如果包含历史日期，从 daily_stats 表查询
	if includesHistory {
		historyEnd := endTime
		if !endTime.Before(todayStart) {
			// 历史数据只到昨天
			historyEnd = todayStart.Add(-time.Second)
		}
		if err := s.getAuthTokenStatsFromDailyStats(ctx, startTime, historyEnd, stats); err != nil {
			return nil, err
		}
	}

	return stats, nil
}

// getAuthTokenStatsFromLogs 从 logs 表查询 token 统计
func (s *SQLStore) getAuthTokenStatsFromLogs(ctx context.Context, startTime, endTime time.Time, stats map[int64]*model.AuthTokenRangeStats) error {
	sinceMs := startTime.UnixMilli()
	untilMs := endTime.UnixMilli()

	query := `
		SELECT
			auth_token_id,
			SUM(CASE WHEN status_code >= 200 AND status_code < 300 THEN 1 ELSE 0 END) AS success_count,
			SUM(CASE WHEN status_code < 200 OR status_code >= 300 THEN 1 ELSE 0 END) AS failure_count,
			SUM(input_tokens) AS prompt_tokens,
			SUM(output_tokens) AS completion_tokens,
			SUM(cache_read_input_tokens) AS cache_read_tokens,
			SUM(cache_creation_input_tokens) AS cache_creation_tokens,
			SUM(cost) AS total_cost,
			AVG(CASE WHEN is_streaming = 1 THEN first_byte_time ELSE NULL END) AS stream_avg_ttfb,
			AVG(CASE WHEN is_streaming = 0 THEN duration ELSE NULL END) AS non_stream_avg_rt,
			SUM(CASE WHEN is_streaming = 1 THEN 1 ELSE 0 END) AS stream_count,
			SUM(CASE WHEN is_streaming = 0 THEN 1 ELSE 0 END) AS non_stream_count
		FROM logs
		WHERE time >= ? AND time <= ? AND auth_token_id > 0
		GROUP BY auth_token_id
	`

	rows, err := s.db.QueryContext(ctx, query, sinceMs, untilMs)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var tokenID int64
		var stat model.AuthTokenRangeStats
		var streamAvgTTFB, nonStreamAvgRT sql.NullFloat64

		if err := rows.Scan(&tokenID, &stat.SuccessCount, &stat.FailureCount,
			&stat.PromptTokens, &stat.CompletionTokens,
			&stat.CacheReadTokens, &stat.CacheCreationTokens,
			&stat.TotalCost,
			&streamAvgTTFB, &nonStreamAvgRT,
			&stat.StreamCount, &stat.NonStreamCount); err != nil {
			return err
		}

		if streamAvgTTFB.Valid {
			stat.StreamAvgTTFB = streamAvgTTFB.Float64
		}
		if nonStreamAvgRT.Valid {
			stat.NonStreamAvgRT = nonStreamAvgRT.Float64
		}

		stats[tokenID] = &stat
	}

	return rows.Err()
}

// getAuthTokenStatsFromDailyStats 从 daily_stats 表查询 token 统计（合并到现有结果）
func (s *SQLStore) getAuthTokenStatsFromDailyStats(ctx context.Context, startTime, endTime time.Time, stats map[int64]*model.AuthTokenRangeStats) error {
	startStr := startTime.Format("2006-01-02")
	endStr := endTime.Format("2006-01-02")

	query := `
		SELECT
			auth_token_id,
			SUM(success_count) AS success_count,
			SUM(error_count) AS failure_count,
			SUM(input_tokens) AS prompt_tokens,
			SUM(output_tokens) AS completion_tokens,
			SUM(cache_read_tokens) AS cache_read_tokens,
			SUM(cache_creation_tokens) AS cache_creation_tokens,
			SUM(total_cost) AS total_cost,
			SUM(avg_first_byte_time * stream_count) / NULLIF(SUM(stream_count), 0) AS stream_avg_ttfb,
			SUM(avg_duration * non_stream_count) / NULLIF(SUM(non_stream_count), 0) AS non_stream_avg_rt,
			SUM(stream_count) AS stream_count,
			SUM(non_stream_count) AS non_stream_count
		FROM daily_stats
		WHERE date >= ? AND date <= ? AND auth_token_id > 0
		GROUP BY auth_token_id
	`

	rows, err := s.db.QueryContext(ctx, query, startStr, endStr)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var tokenID int64
		var successCount, failureCount int64
		var promptTokens, completionTokens, cacheReadTokens, cacheCreationTokens int64
		var totalCost float64
		var streamAvgTTFB, nonStreamAvgRT sql.NullFloat64
		var streamCount, nonStreamCount int64

		if err := rows.Scan(&tokenID, &successCount, &failureCount,
			&promptTokens, &completionTokens,
			&cacheReadTokens, &cacheCreationTokens,
			&totalCost,
			&streamAvgTTFB, &nonStreamAvgRT,
			&streamCount, &nonStreamCount); err != nil {
			return err
		}

		// 合并到现有统计（如果 logs 中已有该 token 的数据，则累加）
		if existing, ok := stats[tokenID]; ok {
			existing.SuccessCount += successCount
			existing.FailureCount += failureCount
			existing.PromptTokens += promptTokens
			existing.CompletionTokens += completionTokens
			existing.CacheReadTokens += cacheReadTokens
			existing.CacheCreationTokens += cacheCreationTokens
			existing.TotalCost += totalCost
			// 平均值需要重新计算（加权平均）
			totalStreamCount := existing.StreamCount + streamCount
			if totalStreamCount > 0 && streamAvgTTFB.Valid {
				existing.StreamAvgTTFB = (existing.StreamAvgTTFB*float64(existing.StreamCount) + streamAvgTTFB.Float64*float64(streamCount)) / float64(totalStreamCount)
			}
			totalNonStreamCount := existing.NonStreamCount + nonStreamCount
			if totalNonStreamCount > 0 && nonStreamAvgRT.Valid {
				existing.NonStreamAvgRT = (existing.NonStreamAvgRT*float64(existing.NonStreamCount) + nonStreamAvgRT.Float64*float64(nonStreamCount)) / float64(totalNonStreamCount)
			}
			existing.StreamCount = totalStreamCount
			existing.NonStreamCount = totalNonStreamCount
		} else {
			// 新增该 token 的统计
			stat := &model.AuthTokenRangeStats{
				SuccessCount:        successCount,
				FailureCount:        failureCount,
				PromptTokens:        promptTokens,
				CompletionTokens:    completionTokens,
				CacheReadTokens:     cacheReadTokens,
				CacheCreationTokens: cacheCreationTokens,
				TotalCost:           totalCost,
				StreamCount:         streamCount,
				NonStreamCount:      nonStreamCount,
			}
			if streamAvgTTFB.Valid {
				stat.StreamAvgTTFB = streamAvgTTFB.Float64
			}
			if nonStreamAvgRT.Valid {
				stat.NonStreamAvgRT = nonStreamAvgRT.Float64
			}
			stats[tokenID] = stat
		}
	}

	return rows.Err()
}
