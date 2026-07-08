package storage

import (
	modelpkg "ccLoad/internal/model"
	"ccLoad/internal/util"
	"context"
	"log"
	"maps"
	"sync"
	"sync/atomic"
	"time"
)

// ChannelCache 高性能渠道缓存层
// 内存查询比数据库查询快 1000 倍+
type ChannelCache struct {
	store           Store
	channelsByModel map[string][]*modelpkg.Config // model → channels
	channelsByType  map[string][]*modelpkg.Config // type → channels
	allChannels     []*modelpkg.Config            // 所有渠道
	lastUpdate      time.Time
	mutex           sync.RWMutex
	ttl             time.Duration

	// 扩展缓存支持更多关键查询
	apiKeysByChannelID map[int64][]*modelpkg.APIKey // channelID → API keys
	cooldownCache      struct {
		channels   map[int64]time.Time         // channelID → cooldown until
		keys       map[int64]map[int]time.Time // channelID→keyIndex→cooldown until
		lastUpdate time.Time
		ttl        time.Duration
	}

	// Metrics counters
	channelCounters        cacheCounters
	channelTypeCounters    cacheCounters
	apiKeyCounters         cacheCounters
	channelCooldownCounter cacheCounters
	keyCooldownCounter     cacheCounters
}

type cacheCounters struct {
	hits          atomic.Uint64
	misses        atomic.Uint64
	invalidations atomic.Uint64
}

func (c *cacheCounters) addHit() {
	c.hits.Add(1)
}

func (c *cacheCounters) addMiss() {
	c.misses.Add(1)
}

func (c *cacheCounters) addInvalidation() {
	c.invalidations.Add(1)
}

func (c *cacheCounters) snapshot() (hits, misses, invalidations uint64) {
	return c.hits.Load(), c.misses.Load(), c.invalidations.Load()
}

// NewChannelCache 创建渠道缓存实例
func NewChannelCache(store Store, ttl time.Duration) *ChannelCache {
	return &ChannelCache{
		store:           store,
		channelsByModel: make(map[string][]*modelpkg.Config),
		channelsByType:  make(map[string][]*modelpkg.Config),
		allChannels:     make([]*modelpkg.Config, 0),
		ttl:             ttl,

		// 初始化扩展缓存
		apiKeysByChannelID: make(map[int64][]*modelpkg.APIKey),
		cooldownCache: struct {
			channels   map[int64]time.Time
			keys       map[int64]map[int]time.Time
			lastUpdate time.Time
			ttl        time.Duration
		}{
			channels: make(map[int64]time.Time),
			keys:     make(map[int64]map[int]time.Time),
			ttl:      30 * time.Second, // 冷却状态缓存30秒
		},
	}
}

// deepCopyConfig 深拷贝 Config 对象（包括 slice/map）
// [FIX] P0-2: 防止调用方修改污染缓存
// 设计：拷贝所有可变字段（Models, ModelRedirects），其他字段为值类型或不可变类型
func deepCopyConfig(src *modelpkg.Config) *modelpkg.Config {
	if src == nil {
		return nil
	}

	// 浅拷贝对象本身
	dst := *src

	// 深拷贝 Models slice
	if src.Models != nil {
		dst.Models = make([]string, len(src.Models))
		copy(dst.Models, src.Models)
	}

	// 深拷贝 ModelRedirects map
	if src.ModelRedirects != nil {
		dst.ModelRedirects = make(map[string]string, len(src.ModelRedirects))
		for k, v := range src.ModelRedirects {
			dst.ModelRedirects[k] = v
		}
	}

	if src.FastBillingConfig != nil {
		dst.FastBillingConfig = &modelpkg.FastBillingConfig{}
		if src.FastBillingConfig.Multipliers != nil {
			dst.FastBillingConfig.Multipliers = make(map[string]float64, len(src.FastBillingConfig.Multipliers))
			for k, v := range src.FastBillingConfig.Multipliers {
				dst.FastBillingConfig.Multipliers[k] = v
			}
		}
	}

	return &dst
}

// deepCopyConfigs 批量深拷贝 Config 对象
// [FIX] P0-2: 缓存边界隔离，避免共享指针污染
func deepCopyConfigs(src []*modelpkg.Config) []*modelpkg.Config {
	if src == nil {
		return nil
	}

	result := make([]*modelpkg.Config, len(src))
	for i, cfg := range src {
		result[i] = deepCopyConfig(cfg)
	}
	return result
}

// GetEnabledChannelsByModel 缓存优先的模型查询
// 性能：内存查询 < 2ms vs 数据库查询 50ms+
// [FIX] P0-2: 返回深拷贝，防止调用方污染缓存
func (c *ChannelCache) GetEnabledChannelsByModel(ctx context.Context, model string) ([]*modelpkg.Config, error) {
	if err := c.refreshIfNeeded(ctx); err != nil {
		c.channelCounters.addMiss()
		// 缓存失败时降级到数据库查询
		return c.store.GetEnabledChannelsByModel(ctx, model)
	}

	c.mutex.RLock()
	defer c.mutex.RUnlock()

	c.channelCounters.addHit()

	if model == "*" {
		// 返回所有渠道的深拷贝（隔离可变字段：Models, ModelRedirects）
		return deepCopyConfigs(c.allChannels), nil
	}

	// 返回指定模型的渠道深拷贝
	channels, exists := c.channelsByModel[model]
	if !exists {
		return []*modelpkg.Config{}, nil
	}

	return deepCopyConfigs(channels), nil
}

// GetEnabledChannelsByType 缓存优先的类型查询
// [FIX] P0-2: 返回深拷贝，防止调用方污染缓存
func (c *ChannelCache) GetEnabledChannelsByType(ctx context.Context, channelType string) ([]*modelpkg.Config, error) {
	if err := c.refreshIfNeeded(ctx); err != nil {
		c.channelTypeCounters.addMiss()
		// 缓存失败时降级到数据库查询
		return c.store.GetEnabledChannelsByType(ctx, channelType)
	}

	c.mutex.RLock()
	defer c.mutex.RUnlock()

	c.channelTypeCounters.addHit()

	normalizedType := util.NormalizeChannelType(channelType)
	channels, exists := c.channelsByType[normalizedType]
	if !exists {
		return []*modelpkg.Config{}, nil
	}

	// 返回深拷贝（隔离可变字段：Models, ModelRedirects）
	return deepCopyConfigs(channels), nil
}

// GetConfig 获取指定ID的渠道配置
// 直接查询数据库，保证数据永远是最新的
func (c *ChannelCache) GetConfig(ctx context.Context, channelID int64) (*modelpkg.Config, error) {
	// 🔧 修复 (2025-11-16): 直接查询数据库,删除复杂的缓存逻辑
	//
	// 原问题: 缓存失效后仍可能返回旧数据,且缓存只包含enabled=true的渠道
	//
	// Linus风格: "Talk is cheap. Show me the code."
	// - 缓存是过早优化,增加复杂度却收益甚微(1-2ms vs 0.1ms)
	// - 单个渠道查询有主键索引,性能已经足够好
	// - 直接查数据库保证数据永远是最新的,简单可靠
	//
	// 保留的缓存: GetEnabledChannelsByModel/Type (批量查询,真正的热路径)
	// 删除的缓存: GetConfig的allChannels遍历(过度设计)

	return c.store.GetConfig(ctx, channelID)
}

// refreshIfNeeded 智能缓存刷新
func (c *ChannelCache) refreshIfNeeded(ctx context.Context) error {
	c.mutex.RLock()
	needsRefresh := time.Since(c.lastUpdate) > c.ttl
	c.mutex.RUnlock()

	if !needsRefresh {
		return nil
	}

	// 使用写锁保护刷新操作
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// 双重检查，防止并发刷新
	if time.Since(c.lastUpdate) <= c.ttl {
		return nil
	}

	return c.refreshCache(ctx)
}

// refreshCache 刷新缓存数据
// [INFO] 内部共享指针设计说明：
//   - allChannels, byModel, byType 三个索引共享同一批 *Config 指针（节省内存）
//   - 这是**安全的**：缓存内部实现，外部无法访问
//   - 对外防御：GetEnabledChannelsByModel/Type 返回深拷贝，完全隔离
//   - 刷新安全：整体替换缓存（原子更新），不修改单个对象
func (c *ChannelCache) refreshCache(ctx context.Context) error {
	start := time.Now()

	allChannels, err := c.store.GetEnabledChannelsByModel(ctx, "*")
	if err != nil {
		return err
	}

	// 构建按类型分组的索引（内部共享指针，对外深拷贝隔离）
	byModel := make(map[string][]*modelpkg.Config)
	byType := make(map[string][]*modelpkg.Config)

	for _, channel := range allChannels {
		channelType := channel.GetChannelType()
		byType[channelType] = append(byType[channelType], channel) // 内部共享

		// 同时填充模型索引
		for _, model := range channel.Models {
			byModel[model] = append(byModel[model], channel) // 内部共享
		}
	}

	// 原子性更新缓存（整体替换，不修改单个对象）
	c.allChannels = allChannels
	c.channelsByModel = byModel
	c.channelsByType = byType
	c.lastUpdate = time.Now()

	// 性能日志
	refreshDuration := time.Since(start)
	if refreshDuration > 5*time.Second {
		log.Printf("[WARN]  缓存刷新过慢: %v, 渠道数: %d, 模型数: %d, 类型数: %d",
			refreshDuration, len(allChannels), len(byModel), len(byType))
	}

	return nil
}

// InvalidateCache 手动失效缓存
func (c *ChannelCache) InvalidateCache() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.lastUpdate = time.Time{} // 重置为0时间，强制刷新
	c.channelCounters.addInvalidation()
	c.channelTypeCounters.addInvalidation()
}

// GetCacheStats 获取缓存统计信息
func (c *ChannelCache) GetCacheStats() map[string]any {
	c.mutex.RLock()
	lastUpdate := c.lastUpdate
	ageSeconds := time.Since(c.lastUpdate).Seconds()
	totalChannels := len(c.allChannels)
	totalModels := len(c.channelsByModel)
	totalTypes := len(c.channelsByType)
	ttlSeconds := c.ttl.Seconds()
	c.mutex.RUnlock()

	channelHits, channelMisses, channelInvalidations := c.channelCounters.snapshot()
	channelTypeHits, channelTypeMisses, channelTypeInvalidations := c.channelTypeCounters.snapshot()
	apiHits, apiMisses, apiInvalidations := c.apiKeyCounters.snapshot()
	chanCooldownHits, chanCooldownMisses, chanCooldownInvalidations := c.channelCooldownCounter.snapshot()
	keyCooldownHits, keyCooldownMisses, keyCooldownInvalidations := c.keyCooldownCounter.snapshot()

	return map[string]any{
		"last_update":                    lastUpdate,
		"age_seconds":                    ageSeconds,
		"total_channels":                 totalChannels,
		"total_models":                   totalModels,
		"total_types":                    totalTypes,
		"ttl_seconds":                    ttlSeconds,
		"channels_hits":                  channelHits,
		"channels_misses":                channelMisses,
		"channels_invalidations":         channelInvalidations,
		"channel_type_hits":              channelTypeHits,
		"channel_type_misses":            channelTypeMisses,
		"channel_type_invalidations":     channelTypeInvalidations,
		"api_keys_hits":                  apiHits,
		"api_keys_misses":                apiMisses,
		"api_keys_invalidations":         apiInvalidations,
		"channel_cooldown_hits":          chanCooldownHits,
		"channel_cooldown_misses":        chanCooldownMisses,
		"channel_cooldown_invalidations": chanCooldownInvalidations,
		"key_cooldown_hits":              keyCooldownHits,
		"key_cooldown_misses":            keyCooldownMisses,
		"key_cooldown_invalidations":     keyCooldownInvalidations,
	}
}

// GetAPIKeys 缓存优先的API Keys查询
// 性能：内存查询 <1ms vs 数据库查询 10-20ms
func (c *ChannelCache) GetAPIKeys(ctx context.Context, channelID int64) ([]*modelpkg.APIKey, error) {
	// 检查缓存
	c.mutex.RLock()
	if keys, exists := c.apiKeysByChannelID[channelID]; exists {
		c.mutex.RUnlock()
		c.apiKeyCounters.addHit()
		// 深拷贝: 防止调用方修改污染缓存
		result := make([]*modelpkg.APIKey, len(keys))
		for i, key := range keys {
			keyCopy := *key // 拷贝对象本身
			result[i] = &keyCopy
		}
		return result, nil
	}
	c.mutex.RUnlock()

	// 缓存未命中，从数据库加载
	keys, err := c.store.GetAPIKeys(ctx, channelID)
	c.apiKeyCounters.addMiss()
	if err != nil {
		return nil, err
	}

	// 存储到缓存（只存 slice 本身；对外总是返回深拷贝，避免污染缓存）
	c.mutex.Lock()
	c.apiKeysByChannelID[channelID] = keys
	c.mutex.Unlock()

	result := make([]*modelpkg.APIKey, len(keys))
	for i, key := range keys {
		keyCopy := *key // 拷贝对象本身
		result[i] = &keyCopy
	}
	return result, nil
}

// GetAllChannelCooldowns 缓存优先的渠道冷却查询
// 性能：内存查询 <1ms vs 数据库查询 5-10ms
func (c *ChannelCache) GetAllChannelCooldowns(ctx context.Context) (map[int64]time.Time, error) {
	// 检查冷却缓存是否有效
	c.mutex.RLock()
	if time.Since(c.cooldownCache.lastUpdate) <= c.cooldownCache.ttl {
		// 有效缓存，返回副本
		result := make(map[int64]time.Time, len(c.cooldownCache.channels))
		maps.Copy(result, c.cooldownCache.channels)
		c.mutex.RUnlock()
		c.channelCooldownCounter.addHit()
		return result, nil
	}
	c.mutex.RUnlock()

	// 缓存过期，从数据库加载
	cooldowns, err := c.store.GetAllChannelCooldowns(ctx)
	c.channelCooldownCounter.addMiss()
	if err != nil {
		return nil, err
	}

	// 存到缓存；对外总是返回副本，避免调用方修改污染缓存。
	c.mutex.Lock()
	c.cooldownCache.channels = cooldowns
	c.cooldownCache.lastUpdate = time.Now()
	c.mutex.Unlock()

	result := make(map[int64]time.Time, len(cooldowns))
	maps.Copy(result, cooldowns)
	return result, nil
}

// GetAllKeyCooldowns 缓存优先的Key冷却查询
// 性能：内存查询 <1ms vs 数据库查询 5-10ms
func (c *ChannelCache) GetAllKeyCooldowns(ctx context.Context) (map[int64]map[int]time.Time, error) {
	// 检查冷却缓存是否有效
	c.mutex.RLock()
	if time.Since(c.cooldownCache.lastUpdate) <= c.cooldownCache.ttl {
		// 有效缓存，返回副本
		result := make(map[int64]map[int]time.Time)
		for k, v := range c.cooldownCache.keys {
			keyMap := make(map[int]time.Time)
			maps.Copy(keyMap, v)
			result[k] = keyMap
		}
		c.mutex.RUnlock()
		c.keyCooldownCounter.addHit()
		return result, nil
	}
	c.mutex.RUnlock()

	// 缓存过期，从数据库加载
	cooldowns, err := c.store.GetAllKeyCooldowns(ctx)
	c.keyCooldownCounter.addMiss()
	if err != nil {
		return nil, err
	}

	// 存到缓存；对外总是返回深拷贝，避免调用方修改污染缓存。
	c.mutex.Lock()
	c.cooldownCache.keys = cooldowns
	c.cooldownCache.lastUpdate = time.Now()
	c.mutex.Unlock()

	result := make(map[int64]map[int]time.Time, len(cooldowns))
	for k, v := range cooldowns {
		keyMap := make(map[int]time.Time, len(v))
		maps.Copy(keyMap, v)
		result[k] = keyMap
	}
	return result, nil
}

// InvalidateAPIKeysCache 手动失效API Keys缓存
func (c *ChannelCache) InvalidateAPIKeysCache(channelID int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.apiKeysByChannelID, channelID)
	c.apiKeyCounters.addInvalidation()
}

// InvalidateAllAPIKeysCache 清空所有API Key缓存（批量操作后使用）
func (c *ChannelCache) InvalidateAllAPIKeysCache() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.apiKeysByChannelID = make(map[int64][]*modelpkg.APIKey)
	c.apiKeyCounters.addInvalidation()
}

// InvalidateCooldownCache 手动失效冷却缓存
func (c *ChannelCache) InvalidateCooldownCache() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.cooldownCache.lastUpdate = time.Time{}
	c.channelCooldownCounter.addInvalidation()
	c.keyCooldownCounter.addInvalidation()
}
