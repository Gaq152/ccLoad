package app

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"ccLoad/internal/model"
	"ccLoad/internal/util"

	"github.com/gin-gonic/gin"
)

// ==================== 统计和监控 ====================
// 从admin.go拆分统计监控,遵循SRP原则

// handleErrors 获取错误日志列表
// GET /admin/logs?range=today&limit=100&offset=0
func (s *Server) HandleErrors(c *gin.Context) {
	params := ParsePaginationParams(c)
	lf := BuildLogFilter(c)
	since, until := params.GetTimeRange()

	// 并行查询日志列表和总数（优化性能）
	logs, err := s.store.ListLogsRange(c.Request.Context(), since, until, params.Limit, params.Offset, &lf)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	total, err := s.store.CountLogsRange(c.Request.Context(), since, until, &lf)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 返回包含总数的响应（支持前端精确分页）
	RespondJSONWithCount(c, http.StatusOK, logs, total)
}

// handleMetrics 获取聚合指标数据
// GET /admin/metrics?range=today&bucket_min=5&channel_type=anthropic&model=claude-3-5-sonnet-20241022
//
// 数据源策略：
// - 今天的数据：从 logs 表实时查询（按 bucket_min 分桶）
// - 历史数据（昨天及之前）：从 daily_stats 聚合表查询（每天一个数据点）
// - 跨天查询：合并两个数据源
func (s *Server) HandleMetrics(c *gin.Context) {
	params := ParsePaginationParams(c)
	bucketMin, _ := strconv.Atoi(c.DefaultQuery("bucket_min", "5"))
	if bucketMin <= 0 {
		bucketMin = 5
	}

	// 支持按渠道类型、模型和 API Token 过滤
	channelType := c.Query("channel_type")
	modelFilter := c.Query("model")
	authTokenID, _ := strconv.ParseInt(c.Query("auth_token_id"), 10, 64)

	since, until := params.GetTimeRange()
	ctx := c.Request.Context()

	// 判断查询范围是否包含今天
	now := time.Now()
	todayStart := beginningOfDay(now)

	var pts []model.MetricPoint
	var err error

	// 策略：根据时间范围选择数据源
	if until.Before(todayStart) {
		// 纯历史查询（不包含今天）：从 daily_stats 查询，每天一个数据点
		pts, err = s.store.GetDailyStatsMetrics(ctx, since, until, channelType, modelFilter, authTokenID)
	} else if since.After(todayStart.Add(-time.Second)) || since.Equal(todayStart) {
		// 纯今天查询：从 logs 表实时查询
		pts, err = s.store.AggregateRangeWithFilter(ctx, since, until, time.Duration(bucketMin)*time.Minute, channelType, modelFilter, authTokenID)
	} else {
		// 跨天查询（历史 + 今天）：合并两个数据源
		pts, err = s.getMergedMetrics(ctx, since, until, todayStart, bucketMin, channelType, modelFilter, authTokenID)
	}

	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 添加调试信息
	totalReqs := 0
	for _, pt := range pts {
		totalReqs += pt.Success + pt.Error
	}

	c.Header("X-Debug-Since", since.Format(time.RFC3339))
	c.Header("X-Debug-Points", fmt.Sprintf("%d", len(pts)))
	c.Header("X-Debug-Total", fmt.Sprintf("%d", totalReqs))

	RespondJSON(c, http.StatusOK, pts)
}

// getMergedMetrics 合并历史聚合数据和今天的实时数据（用于趋势图）
func (s *Server) getMergedMetrics(ctx context.Context, startTime, endTime, todayStart time.Time, bucketMin int, channelType, modelFilter string, authTokenID int64) ([]model.MetricPoint, error) {
	// 1. 从 daily_stats 查询历史数据（startTime 到 昨天）
	yesterdayEnd := todayStart.Add(-time.Nanosecond)
	historyPts, err := s.store.GetDailyStatsMetrics(ctx, startTime, yesterdayEnd, channelType, modelFilter, authTokenID)
	if err != nil {
		return nil, fmt.Errorf("query history metrics: %w", err)
	}

	// 2. 从 logs 表查询今天的实时数据
	todayPts, err := s.store.AggregateRangeWithFilter(ctx, todayStart, endTime, time.Duration(bucketMin)*time.Minute, channelType, modelFilter, authTokenID)
	if err != nil {
		return nil, fmt.Errorf("query today metrics: %w", err)
	}

	// 3. 合并两个数据源（历史在前，今天在后）
	result := make([]model.MetricPoint, 0, len(historyPts)+len(todayPts))
	result = append(result, historyPts...)
	result = append(result, todayPts...)

	return result, nil
}

// handleStats 获取渠道和模型统计
// GET /admin/stats?range=today&channel_name_like=xxx&model_like=xxx
//
// 数据源策略：
// - 今天的数据：从 logs 表实时查询（数据最新）
// - 历史数据（昨天及之前）：优先从 daily_stats 聚合表查询（支持日志清理后的历史统计）
// - 跨天查询（如 this_week）：聚合表 + 实时查询合并
func (s *Server) HandleStats(c *gin.Context) {
	params := ParsePaginationParams(c)
	lf := BuildLogFilter(c)

	startTime, endTime := params.GetTimeRange()
	ctx := c.Request.Context()

	// 判断查询范围是否包含今天
	now := time.Now()
	todayStart := beginningOfDay(now)
	yesterdayEnd := todayStart.Add(-time.Nanosecond) // 昨天 23:59:59.999999999

	var stats []model.StatsEntry
	var err error

	// 策略：根据时间范围选择数据源
	if endTime.Before(todayStart) {
		// 纯历史查询（不包含今天）：直接从聚合表查询
		stats, err = s.store.GetDailyStatsSummary(ctx, startTime, endTime, &lf)
	} else if startTime.After(yesterdayEnd) || startTime.Equal(todayStart) {
		// 纯今天查询：从 logs 表实时查询
		stats, err = s.store.GetStats(ctx, startTime, endTime, &lf)
	} else {
		// 跨天查询（历史 + 今天）：合并两个数据源
		stats, err = s.getMergedStats(ctx, startTime, endTime, todayStart, &lf)
	}

	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	RespondJSON(c, http.StatusOK, gin.H{"stats": stats})
}

// handlePublicSummary 获取基础统计摘要(公开端点,无需认证)
// GET /public/summary?range=today
// 按渠道类型分组统计，Claude和Codex类型包含Token和成本信息
//
// [SECURITY NOTE] 该端点故意设计为公开访问，用于首页仪表盘展示。
// 如需隐藏运营数据，可在 server.go:SetupRoutes 中添加 RequireTokenAuth 中间件。
//
// 数据源策略（与 HandleStats 一致）：
// - 今天的数据：从 logs 表实时查询
// - 历史数据：从 daily_stats 聚合表查询
func (s *Server) HandlePublicSummary(c *gin.Context) {
	params := ParsePaginationParams(c)
	startTime, endTime := params.GetTimeRange()
	ctx := c.Request.Context()

	// 判断查询范围是否包含今天
	now := time.Now()
	todayStart := beginningOfDay(now)
	yesterdayEnd := todayStart.Add(-time.Nanosecond)

	var stats []model.StatsEntry
	var err error

	// 策略：根据时间范围选择数据源
	if endTime.Before(todayStart) {
		// 纯历史查询
		stats, err = s.store.GetDailyStatsSummary(ctx, startTime, endTime, nil)
	} else if startTime.After(yesterdayEnd) || startTime.Equal(todayStart) {
		// 纯今天查询
		stats, err = s.store.GetStats(ctx, startTime, endTime, nil)
	} else {
		// 跨天查询
		stats, err = s.getMergedStats(ctx, startTime, endTime, todayStart, nil)
	}

	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 查询所有渠道的类型映射(channel_id -> channel_type)
	channelTypes, err := s.fetchChannelTypesMap(ctx)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 按渠道类型分组统计
	typeStats := make(map[string]*TypeSummary)
	totalSuccess := 0
	totalError := 0

	for _, stat := range stats {
		totalSuccess += stat.Success
		totalError += stat.Error

		// 获取渠道类型(默认anthropic)
		channelType := "anthropic"
		if stat.ChannelID != nil {
			if ct, ok := channelTypes[int64(*stat.ChannelID)]; ok {
				channelType = ct
			}
		}

		// 初始化类型统计
		if _, exists := typeStats[channelType]; !exists {
			typeStats[channelType] = &TypeSummary{
				ChannelType:     channelType,
				TotalRequests:   0,
				SuccessRequests: 0,
				ErrorRequests:   0,
			}
		}

		ts := typeStats[channelType]
		ts.TotalRequests += stat.Success + stat.Error
		ts.SuccessRequests += stat.Success
		ts.ErrorRequests += stat.Error

		// 所有渠道类型都统计Token和成本
		if stat.TotalInputTokens != nil {
			ts.TotalInputTokens += *stat.TotalInputTokens
		}
		if stat.TotalOutputTokens != nil {
			ts.TotalOutputTokens += *stat.TotalOutputTokens
		}
		if stat.TotalCost != nil {
			ts.TotalCost += *stat.TotalCost
		}

		// Claude和Codex类型额外统计缓存（其他类型不支持prompt caching）
		if channelType == "anthropic" || channelType == "codex" {
			if stat.TotalCacheReadInputTokens != nil {
				ts.TotalCacheReadTokens += *stat.TotalCacheReadInputTokens
			}
			if stat.TotalCacheCreationInputTokens != nil {
				ts.TotalCacheCreationTokens += *stat.TotalCacheCreationInputTokens
			}
		}
	}

	response := gin.H{
		"total_requests":   totalSuccess + totalError,
		"success_requests": totalSuccess,
		"error_requests":   totalError,
		"range":            params.Range,
		"by_type":          typeStats, // 按渠道类型分组的统计
	}

	RespondJSON(c, http.StatusOK, response)
}

// TypeSummary 按渠道类型的统计摘要
type TypeSummary struct {
	ChannelType              string  `json:"channel_type"`
	TotalRequests            int     `json:"total_requests"`
	SuccessRequests          int     `json:"success_requests"`
	ErrorRequests            int     `json:"error_requests"`
	TotalInputTokens         int64   `json:"total_input_tokens,omitempty"`          // 所有类型
	TotalOutputTokens        int64   `json:"total_output_tokens,omitempty"`         // 所有类型
	TotalCacheReadTokens     int64   `json:"total_cache_read_tokens,omitempty"`     // Claude/Codex专用（prompt caching）
	TotalCacheCreationTokens int64   `json:"total_cache_creation_tokens,omitempty"` // Claude/Codex专用（prompt caching）
	TotalCost                float64 `json:"total_cost,omitempty"`                  // 所有类型
}

// fetchChannelTypesMap 查询所有渠道的类型映射
func (s *Server) fetchChannelTypesMap(ctx context.Context) (map[int64]string, error) {
	configs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return nil, err
	}

	channelTypes := make(map[int64]string, len(configs))
	for _, cfg := range configs {
		channelTypes[cfg.ID] = cfg.ChannelType
	}
	return channelTypes, nil
}

// handleCooldownStats 获取当前冷却状态监控指标
// GET /admin/cooldown/stats
// [INFO] Linus风格:按需查询,简单直接
func (s *Server) HandleCooldownStats(c *gin.Context) {
	// 使用缓存层查询（<1ms vs 数据库查询5-10ms），若缓存不可用自动退化
	channelCooldowns, _ := s.getAllChannelCooldowns(c.Request.Context())
	keyCooldowns, _ := s.getAllKeyCooldowns(c.Request.Context())

	var keyCount int
	for _, m := range keyCooldowns {
		keyCount += len(m)
	}

	response := gin.H{
		"channel_cooldowns": len(channelCooldowns),
		"key_cooldowns":     keyCount,
	}
	RespondJSON(c, http.StatusOK, response)
}

// handleCacheStats 暴露缓存命中率等指标，方便监控采集
// GET /admin/cache/stats
func (s *Server) HandleCacheStats(c *gin.Context) {
	cache := s.getChannelCache()
	if cache == nil {
		RespondJSON(c, http.StatusOK, gin.H{
			"cache_enabled": false,
			"stats":         gin.H{},
		})
		return
	}

	stats := cache.GetCacheStats()
	RespondJSON(c, http.StatusOK, gin.H{
		"cache_enabled": true,
		"stats":         stats,
	})
}

// handleGetChannelTypes 获取渠道类型配置(公开端点,前端动态加载)
// GET /public/channel-types
func (s *Server) HandleGetChannelTypes(c *gin.Context) {
	RespondJSON(c, http.StatusOK, util.ChannelTypes)
}

// HandleGetModels 获取数据库中存在的所有模型列表（去重）
// GET /admin/models
func (s *Server) HandleGetModels(c *gin.Context) {
	// 获取时间范围（默认最近30天）
	rangeParam := c.DefaultQuery("range", "this_month")
	params := ParsePaginationParams(c)
	params.Range = rangeParam
	since, until := params.GetTimeRange()

	// 查询模型列表
	models, err := s.store.GetDistinctModels(c.Request.Context(), since, until)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	RespondJSON(c, http.StatusOK, models)
}

// HandleHealth 健康检查端点(公开访问,无需认证)
// GET /health
// 仅检查数据库连接是否活跃（<5ms，适用于K8s liveness/readiness probe）
func (s *Server) HandleHealth(c *gin.Context) {
	// 设置100ms超时，避免慢查询阻塞healthcheck
	ctx, cancel := context.WithTimeout(c.Request.Context(), 100*time.Millisecond)
	defer cancel()

	if err := s.store.Ping(ctx); err != nil {
		RespondError(c, http.StatusServiceUnavailable, err)
		return
	}

	RespondJSON(c, http.StatusOK, gin.H{"status": "ok"})
}

// getMergedStats 合并历史聚合数据和今天的实时数据
// 用于跨天查询（如 this_week、this_month）
func (s *Server) getMergedStats(ctx context.Context, startTime, endTime, todayStart time.Time, filter *model.LogFilter) ([]model.StatsEntry, error) {
	// 1. 从聚合表查询历史数据（startTime 到 昨天）
	yesterdayEnd := todayStart.Add(-time.Nanosecond)
	historyStats, err := s.store.GetDailyStatsSummary(ctx, startTime, yesterdayEnd, filter)
	if err != nil {
		return nil, fmt.Errorf("query history stats: %w", err)
	}

	// 2. 从 logs 表查询今天的实时数据
	todayStats, err := s.store.GetStats(ctx, todayStart, endTime, filter)
	if err != nil {
		return nil, fmt.Errorf("query today stats: %w", err)
	}

	// 3. 合并两个数据源（按 channel_id + model 聚合）
	return mergeStatsEntries(historyStats, todayStats), nil
}

// mergeStatsEntries 合并两组统计数据
// 按 channel_id + model 维度聚合，累加各项指标
func mergeStatsEntries(history, today []model.StatsEntry) []model.StatsEntry {
	// 使用 map 按 channel_id + model 聚合
	type statsKey struct {
		channelID int
		model     string
	}
	merged := make(map[statsKey]*model.StatsEntry)

	// 辅助函数：累加统计项
	addEntry := func(entry model.StatsEntry) {
		chID := 0
		if entry.ChannelID != nil {
			chID = *entry.ChannelID
		}
		key := statsKey{channelID: chID, model: entry.Model}

		if existing, ok := merged[key]; ok {
			// 累加基础计数
			existing.Success += entry.Success
			existing.Error += entry.Error
			existing.Total += entry.Total

			// 累加 Token 统计（需要处理 nil）
			existing.TotalInputTokens = addInt64Ptr(existing.TotalInputTokens, entry.TotalInputTokens)
			existing.TotalOutputTokens = addInt64Ptr(existing.TotalOutputTokens, entry.TotalOutputTokens)
			existing.TotalCacheReadInputTokens = addInt64Ptr(existing.TotalCacheReadInputTokens, entry.TotalCacheReadInputTokens)
			existing.TotalCacheCreationInputTokens = addInt64Ptr(existing.TotalCacheCreationInputTokens, entry.TotalCacheCreationInputTokens)
			existing.TotalCost = addFloat64Ptr(existing.TotalCost, entry.TotalCost)

			// 平均值需要重新计算（简化处理：取较新的值，即 today 的值）
			// 因为历史数据的平均值已经是聚合后的，无法精确合并
			if entry.AvgDurationSeconds != nil {
				existing.AvgDurationSeconds = entry.AvgDurationSeconds
			}
			if entry.AvgFirstByteTimeSeconds != nil {
				existing.AvgFirstByteTimeSeconds = entry.AvgFirstByteTimeSeconds
			}
		} else {
			// 新条目，复制一份
			entryCopy := entry
			merged[key] = &entryCopy
		}
	}

	// 先添加历史数据
	for _, entry := range history {
		addEntry(entry)
	}
	// 再添加今天的数据（会累加到已有条目）
	for _, entry := range today {
		addEntry(entry)
	}

	// 转换为切片返回
	result := make([]model.StatsEntry, 0, len(merged))
	for _, entry := range merged {
		result = append(result, *entry)
	}

	return result
}

// addInt64Ptr 累加两个 *int64 指针的值
func addInt64Ptr(a, b *int64) *int64 {
	if a == nil && b == nil {
		return nil
	}
	var sum int64
	if a != nil {
		sum += *a
	}
	if b != nil {
		sum += *b
	}
	return &sum
}

// addFloat64Ptr 累加两个 *float64 指针的值
func addFloat64Ptr(a, b *float64) *float64 {
	if a == nil && b == nil {
		return nil
	}
	var sum float64
	if a != nil {
		sum += *a
	}
	if b != nil {
		sum += *b
	}
	return &sum
}
