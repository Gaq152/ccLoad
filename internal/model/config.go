package model

import (
	"time"
)

// Config 渠道配置
type Config struct {
	ID             int64             `json:"id"`
	Name           string            `json:"name"`
	ChannelType    string            `json:"channel_type"` // 渠道类型: "anthropic" | "codex" | "openai" | "gemini"，默认anthropic
	URL            string            `json:"url"`
	Priority       int               `json:"priority"`
	Models         []string          `json:"models"`
	ModelRedirects map[string]string `json:"model_redirects,omitempty"` // 模型重定向映射：请求模型 -> 实际转发模型
	Enabled        bool              `json:"enabled"`

	// 渠道级冷却（从cooldowns表迁移）
	CooldownUntil      int64 `json:"cooldown_until"`       // Unix秒时间戳，0表示无冷却
	CooldownDurationMs int64 `json:"cooldown_duration_ms"` // 冷却持续时间（毫秒）

	// 多端点管理（2025-12新增）
	AutoSelectEndpoint bool              `json:"auto_select_endpoint"` // 自动选择最快端点
	Endpoints          []ChannelEndpoint `json:"endpoints,omitempty"`  // 端点列表（查询时填充）

	// 用量监控配置（2025-12新增）
	QuotaConfig *QuotaConfig `json:"quota_config,omitempty"` // 用量查询配置

	CreatedAt JSONTime `json:"created_at"` // 使用JSONTime确保序列化格式一致（RFC3339）
	UpdatedAt JSONTime `json:"updated_at"` // 使用JSONTime确保序列化格式一致（RFC3339）

	// 缓存Key数量，避免冷却判断时的N+1查询
	KeyCount int `json:"key_count"` // API Key数量（查询时JOIN计算）
}

// ChannelEndpoint 渠道端点（多URL支持）
type ChannelEndpoint struct {
	ID         int64  `json:"id"`
	ChannelID  int64  `json:"channel_id"`
	URL        string `json:"url"`
	IsActive   bool   `json:"is_active"`    // 当前选中的端点
	LatencyMs  *int   `json:"latency_ms"`   // 最近测速延迟(ms)，nil表示未测试
	StatusCode *int   `json:"status_code"`  // 最近测速HTTP状态码，nil表示未测试
	LastTestAt int64  `json:"last_test_at"` // 最后测速时间戳
	SortOrder  int    `json:"sort_order"`   // 排序顺序
	CreatedAt  int64  `json:"created_at"`
}

// EndpointTestResult 端点测速结果（用于批量更新）
type EndpointTestResult struct {
	LatencyMs  int
	StatusCode int
}

// GetChannelType 默认返回"anthropic"（Claude API）
func (c *Config) GetChannelType() string {
	if c.ChannelType == "" {
		return "anthropic"
	}
	return c.ChannelType
}

func (c *Config) IsCoolingDown(now time.Time) bool {
	return c.CooldownUntil > now.Unix()
}

// KeyStrategy 常量定义
const (
	KeyStrategySequential = "sequential"  // 顺序选择：按索引顺序尝试Key
	KeyStrategyRoundRobin = "round_robin" // 轮询选择：均匀分布请求到各个Key
)

// IsValidKeyStrategy 验证KeyStrategy是否有效
func IsValidKeyStrategy(s string) bool {
	return s == "" || s == KeyStrategySequential || s == KeyStrategyRoundRobin
}

// DefaultKeyStrategy 返回默认策略
func DefaultKeyStrategy() string {
	return KeyStrategySequential
}

type APIKey struct {
	ID        int64  `json:"id"`
	ChannelID int64  `json:"channel_id"`
	KeyIndex  int    `json:"key_index"`
	APIKey    string `json:"api_key"`

	KeyStrategy string `json:"key_strategy"` // "sequential" | "round_robin"

	// Key级冷却（从key_cooldowns表迁移）
	CooldownUntil      int64 `json:"cooldown_until"`
	CooldownDurationMs int64 `json:"cooldown_duration_ms"`

	CreatedAt JSONTime `json:"created_at"`
	UpdatedAt JSONTime `json:"updated_at"`
}

func (k *APIKey) IsCoolingDown(now time.Time) bool {
	return k.CooldownUntil > now.Unix()
}

// ChannelWithKeys 用于Redis完整同步
// 设计目标：解决Redis恢复后渠道缺少API Keys的问题
type ChannelWithKeys struct {
	Config  *Config  `json:"config"`
	APIKeys []APIKey `json:"api_keys"` // 不使用指针避免额外分配
}

// QuotaConfig 渠道用量查询配置
// 用于定期从外部API获取渠道的剩余额度/用量信息
type QuotaConfig struct {
	Enabled         bool              `json:"enabled"`           // 是否启用用量监控
	RequestURL      string            `json:"request_url"`       // 请求URL
	RequestMethod   string            `json:"request_method"`    // HTTP方法: GET/POST
	RequestHeaders  map[string]string `json:"request_headers"`   // 请求头（如Authorization）
	RequestBody     string            `json:"request_body"`      // POST请求体（可选）
	ExtractorScript string            `json:"extractor_script"`  // JS提取器脚本（在前端执行）
	IntervalSeconds int               `json:"interval_seconds"`  // 轮询间隔（秒），默认300
}

// GetIntervalSeconds 返回轮询间隔，默认300秒（5分钟）
func (q *QuotaConfig) GetIntervalSeconds() int {
	if q.IntervalSeconds <= 0 {
		return 300
	}
	return q.IntervalSeconds
}

// GetRequestMethod 返回HTTP方法，默认GET
func (q *QuotaConfig) GetRequestMethod() string {
	if q.RequestMethod == "" {
		return "GET"
	}
	return q.RequestMethod
}
