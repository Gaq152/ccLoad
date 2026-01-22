package app

import (
	modelpkg "ccLoad/internal/model"
	"ccLoad/internal/util"

	"context"
	"log"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

// selectCandidatesByChannelType 根据渠道类型选择候选渠道
// 性能优化：使用缓存层，内存查询 < 2ms vs 数据库查询 50ms+
func (s *Server) selectCandidatesByChannelType(ctx context.Context, channelType string) ([]*modelpkg.Config, error) {
	// 缓存可用时走缓存，否则退化到存储层
	channels, err := s.GetEnabledChannelsByType(ctx, channelType)
	if err != nil {
		return nil, err
	}
	return s.filterCooldownChannels(ctx, s.maybeShuffleChannels(channels))
}

// selectCandidates 选择支持指定模型的候选渠道
// 性能优化：使用缓存层，消除JSON查询和聚合操作的性能杀手
func (s *Server) selectCandidates(ctx context.Context, model string) ([]*modelpkg.Config, error) {
	// 缓存优先查询（自动60秒TTL刷新，避免重复的数据库性能灾难）
	return s.GetEnabledChannelsByModel(ctx, model)
}

// selectCandidatesByModelAndType 根据模型和渠道类型筛选候选渠道
// 遵循SRP：数据库负责返回满足模型的渠道，本函数仅负责类型过滤
func (s *Server) selectCandidatesByModelAndType(ctx context.Context, model string, channelType string) ([]*modelpkg.Config, error) {
	configs, err := s.selectCandidates(ctx, model)
	if err != nil {
		return nil, err
	}

	if channelType == "" {
		return s.filterCooldownChannels(ctx, s.maybeShuffleChannels(configs))
	}

	normalizedType := util.NormalizeChannelType(channelType)
	filtered := make([]*modelpkg.Config, 0, len(configs))
	for _, cfg := range configs {
		if cfg.GetChannelType() == normalizedType {
			filtered = append(filtered, cfg)
		}
	}

	return s.filterCooldownChannels(ctx, s.maybeShuffleChannels(filtered))
}

// maybeShuffleChannels 根据设置决定是否打乱渠道顺序
// 开启负载均衡：同优先级的渠道随机打乱
// 关闭负载均衡：保持原始顺序（按优先级排序）
func (s *Server) maybeShuffleChannels(channels []*modelpkg.Config) []*modelpkg.Config {
	// 防御性检查：configService 为 nil 时默认开启负载均衡
	if s.configService == nil || s.configService.GetBool("channel_load_balance", true) {
		return shuffleSamePriorityChannels(channels)
	}
	return channels
}

// configSupportsModel 检查渠道是否支持指定模型
func (s *Server) configSupportsModel(cfg *modelpkg.Config, model string) bool {
	if model == "*" {
		return true
	}
	return cfg.SupportsModel(model)
}

// configSupportsModelWithDateFallback 检查渠道是否支持指定模型（支持日期后缀回退和模糊匹配）
func (s *Server) configSupportsModelWithDateFallback(cfg *modelpkg.Config, model string) bool {
	if s.configSupportsModel(cfg, model) {
		return true
	}
	if model == "*" {
		return false
	}

	// 日期后缀回退
	if s.modelLookupStripDateSuffix {
		// 请求带日期：claude-3-5-sonnet-20241022 -> claude-3-5-sonnet
		if stripped, ok := stripTrailingYYYYMMDD(model); ok && stripped != model {
			if cfg.SupportsModel(stripped) {
				return true
			}
		}

		// 请求无日期：claude-sonnet-4-5 -> claude-sonnet-4-5-20250929
		for _, entry := range cfg.ModelEntries() {
			if entry.Model == "" {
				continue
			}
			if stripped, ok := stripTrailingYYYYMMDD(entry.Model); ok && stripped == model {
				return true
			}
		}
	}

	// 模糊匹配：sonnet -> claude-sonnet-4-5-20250929
	if s.modelFuzzyMatch {
		if _, ok := cfg.FuzzyMatchModel(model); ok {
			return true
		}
	}

	return false
}

// stripTrailingYYYYMMDD 去除模型名称末尾的日期后缀（YYYYMMDD）
func stripTrailingYYYYMMDD(model string) (string, bool) {
	dash := strings.LastIndexByte(model, '-')
	if dash < 0 {
		return model, false
	}
	suffix := model[dash+1:]
	if len(suffix) != 8 {
		return model, false
	}
	for i := 0; i < len(suffix); i++ {
		if suffix[i] < '0' || suffix[i] > '9' {
			return model, false
		}
	}
	year, _ := strconv.Atoi(suffix[:4])
	month, _ := strconv.Atoi(suffix[4:6])
	day, _ := strconv.Atoi(suffix[6:8])
	if year < 2000 || year > 2100 {
		return model, false
	}
	if month < 1 || month > 12 {
		return model, false
	}
	lastDay := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day < 1 || day > lastDay {
		return model, false
	}
	return model[:dash], true
}

// filterCooldownChannels 过滤掉冷却中的渠道
// [INFO] 修复 (2025-12-09): 在渠道选择阶段就过滤冷却渠道，避免无效尝试
// 过滤规则:
//  1. 渠道级冷却 → 直接过滤
//  2. 所有Key都在冷却 → 过滤
//  3. 至少有一个Key可用 → 保留
func (s *Server) filterCooldownChannels(ctx context.Context, channels []*modelpkg.Config) ([]*modelpkg.Config, error) {
	if len(channels) == 0 {
		return channels, nil
	}

	now := time.Now()

	// 批量查询冷却状态（使用缓存层，性能优化）
	channelCooldowns, err := s.getAllChannelCooldowns(ctx)
	if err != nil {
		// 降级处理：查询失败时不过滤，避免阻塞请求
		log.Printf("[WARN] Failed to get channel cooldowns (degraded mode): %v", err)
		return channels, nil
	}

	keyCooldowns, err := s.getAllKeyCooldowns(ctx)
	if err != nil {
		// 降级处理：查询失败时不过滤
		log.Printf("[WARN] Failed to get key cooldowns (degraded mode): %v", err)
		return channels, nil
	}

	// 过滤冷却中的渠道
	filtered := make([]*modelpkg.Config, 0, len(channels))
	for _, cfg := range channels {
		// 1. 检查渠道级冷却
		if cooldownUntil, exists := channelCooldowns[cfg.ID]; exists {
			if cooldownUntil.After(now) {
				continue // 渠道冷却中，跳过
			}
		}

		// 2. 检查是否所有Key都在冷却
		// keyCooldowns 只包含"正在冷却"的Key；未出错/未冷却的Key不会出现在这里。
		// 只有当我们确认该渠道的所有Key都处于冷却中时，才跳过整个渠道。
		keyMap, hasCooldownKeys := keyCooldowns[cfg.ID]
		if hasCooldownKeys && cfg.KeyCount > 0 {
			// 若冷却记录数量小于Key总数，说明至少有一个Key未进入冷却映射，渠道仍可用。
			if len(keyMap) >= cfg.KeyCount {
				hasAvailableKey := false
				for _, cooldownUntil := range keyMap {
					if !cooldownUntil.After(now) {
						hasAvailableKey = true
						break
					}
				}
				if !hasAvailableKey {
					continue // 所有Key都冷却中，跳过
				}
			}
		}

		// 渠道可用
		filtered = append(filtered, cfg)
	}

	return filtered, nil
}

// shuffleSamePriorityChannels 随机打乱相同优先级的渠道，实现负载均衡
// 设计原则：KISS、无状态、保持优先级排序
func shuffleSamePriorityChannels(channels []*modelpkg.Config) []*modelpkg.Config {
	n := len(channels)
	if n <= 1 {
		return channels
	}

	result := make([]*modelpkg.Config, n)
	copy(result, channels)

	// 单次遍历：识别优先级边界并就地打乱
	groupStart := 0
	for i := 1; i <= n; i++ {
		// 检测分组边界（优先级变化）
		if i == n || result[i].Priority != result[groupStart].Priority {
			// 打乱 [groupStart, i) 区间
			if i-groupStart > 1 {
				rand.Shuffle(i-groupStart, func(a, b int) {
					result[groupStart+a], result[groupStart+b] = result[groupStart+b], result[groupStart+a]
				})
			}
			groupStart = i
		}
	}

	return result
}

// filterByTokenChannels 根据令牌的渠道访问配置过滤候选渠道
// 参数:
//   - channels: 候选渠道列表
//   - tokenID: API令牌ID（从context获取）
//
// 返回值:
//   - 过滤后的渠道列表（只包含令牌允许访问的渠道）
//
// 设计说明:
//   - 如果tokenID为0或令牌配置为AllChannels，则不过滤
//   - 否则只返回令牌允许的渠道
func (s *Server) filterByTokenChannels(channels []*modelpkg.Config, tokenID int64) []*modelpkg.Config {
	// tokenID为0表示没有令牌认证（理论上不应该发生，因为API需要认证）
	if tokenID == 0 || len(channels) == 0 {
		return channels
	}

	// 获取令牌的渠道配置
	cfg, exists := s.authService.GetTokenChannelConfig(tokenID)
	if !exists || cfg == nil {
		// 令牌配置不存在，拒绝所有渠道访问（fail-closed，安全优先）
		log.Printf("[ERROR] 令牌ID=%d的渠道配置不存在，拒绝访问（fail-closed）", tokenID)
		return []*modelpkg.Config{}
	}

	// AllChannels=true 表示允许所有渠道
	if cfg.AllChannels {
		return channels
	}

	// 构建允许的渠道ID集合（O(1)查找）
	allowedSet := make(map[int64]struct{}, len(cfg.ChannelIDs))
	for _, id := range cfg.ChannelIDs {
		allowedSet[id] = struct{}{}
	}

	// 过滤渠道
	filtered := make([]*modelpkg.Config, 0, len(channels))
	for _, ch := range channels {
		if _, allowed := allowedSet[ch.ID]; allowed {
			filtered = append(filtered, ch)
		}
	}

	return filtered
}
