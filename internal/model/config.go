package model

import (
	"strings"
	"time"
)

// Config 渠道配置
type Config struct {
	ID             int64             `json:"id"`
	Name           string            `json:"name"`
	ChannelType    string            `json:"channel_type"` // 渠道类型: "anthropic" | "codex" | "gemini"，默认anthropic
	URL            string            `json:"url"`
	Priority       int               `json:"priority"`
	SortOrder      int               `json:"sort_order"` // 同优先级内的排序顺序（拖拽排序用）
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

	// Codex预设类型（2025-12新增）
	Preset string `json:"preset,omitempty"` // "official"=官方预设, "custom"=自定义, ""=非Codex渠道

	// OpenAI兼容模式（2025-12新增）
	OpenAICompat bool `json:"openai_compat"` // 使用/v1/chat/completions格式（Anthropic/Gemini/Codex自定义渠道支持）

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

	// OAuth Token 专用字段（仅Codex官方预设使用，2025-12新增）
	AccessToken    string `json:"access_token,omitempty"`
	IDToken        string `json:"id_token,omitempty"`
	RefreshToken   string `json:"refresh_token,omitempty"`
	TokenExpiresAt int64  `json:"token_expires_at,omitempty"` // Unix时间戳

	// Kiro 设备指纹（用于 AWS CodeWhisperer 认证）
	DeviceFingerprint string `json:"device_fingerprint,omitempty"`

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
	Enabled         bool              `json:"enabled"`          // 是否启用用量监控
	RequestURL      string            `json:"request_url"`      // 请求URL
	RequestMethod   string            `json:"request_method"`   // HTTP方法: GET/POST
	RequestHeaders  map[string]string `json:"request_headers"`  // 请求头（如Authorization）
	RequestBody     string            `json:"request_body"`     // POST请求体（可选）
	ExtractorScript string            `json:"extractor_script"` // JS提取器脚本（在前端执行）
	IntervalSeconds int               `json:"interval_seconds"` // 轮询间隔（秒），默认300
	ChallengeMode   string            `json:"challenge_mode"`   // 反爬挑战模式: "" (无) | "acw_sc__v2" (anyrouter)
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

// ChannelSortUpdate 渠道排序更新项（用于拖拽排序）
type ChannelSortUpdate struct {
	ID        int64
	Priority  int
	SortOrder int
}

// ModelEntry 模型条目（用于模糊匹配）
type ModelEntry struct {
	Model string
}

// SupportsModel 检查渠道是否支持指定模型
func (c *Config) SupportsModel(model string) bool {
	for _, m := range c.Models {
		if m == model {
			return true
		}
	}
	return false
}

// ModelEntries 返回模型条目列表（用于模糊匹配）
func (c *Config) ModelEntries() []ModelEntry {
	entries := make([]ModelEntry, len(c.Models))
	for i, m := range c.Models {
		entries[i] = ModelEntry{Model: m}
	}
	return entries
}

// FuzzyMatchModel 模糊匹配模型名称
// 当精确匹配失败时，查找包含 query 子串的模型，按版本排序返回最新的
// 返回 (匹配到的模型名, 是否匹配成功)
func (c *Config) FuzzyMatchModel(query string) (string, bool) {
	if query == "" {
		return "", false
	}

	queryLower := strings.ToLower(query)
	var matches []string

	for _, model := range c.Models {
		if strings.Contains(strings.ToLower(model), queryLower) {
			matches = append(matches, model)
		}
	}

	if len(matches) == 0 {
		return "", false
	}
	if len(matches) == 1 {
		return matches[0], true
	}

	// 多个匹配：按版本排序，取最新
	sortModelsByVersion(matches)
	return matches[0], true
}

// sortModelsByVersion 按版本排序模型列表（最新优先）
// 排序优先级：1.日期后缀 2.版本数字 3.字典序
func sortModelsByVersion(models []string) {
	for i := 0; i < len(models)-1; i++ {
		for j := i + 1; j < len(models); j++ {
			if compareModelVersion(models[i], models[j]) < 0 {
				models[i], models[j] = models[j], models[i]
			}
		}
	}
}

// compareModelVersion 比较两个模型版本
// 返回 >0 表示 a 更新，<0 表示 b 更新，0 表示相同
func compareModelVersion(a, b string) int {
	// 1. 日期后缀优先（YYYYMMDD）
	dateA := extractDateSuffix(a)
	dateB := extractDateSuffix(b)
	if dateA != dateB {
		if dateA > dateB {
			return 1
		}
		return -1
	}

	// 2. 版本数字序列比较
	verA := extractVersionNumbers(a)
	verB := extractVersionNumbers(b)
	maxLen := len(verA)
	if len(verB) > maxLen {
		maxLen = len(verB)
	}
	for i := 0; i < maxLen; i++ {
		va, vb := 0, 0
		if i < len(verA) {
			va = verA[i]
		}
		if i < len(verB) {
			vb = verB[i]
		}
		if va != vb {
			return va - vb
		}
	}

	// 3. 兜底：字典序
	if a > b {
		return 1
	} else if a < b {
		return -1
	}
	return 0
}

// extractDateSuffix 提取模型名称末尾的日期后缀（YYYYMMDD）
// 返回日期字符串，无日期返回空串
func extractDateSuffix(model string) string {
	// 查找最后一个分隔符
	lastDash := strings.LastIndexByte(model, '-')
	lastDot := strings.LastIndexByte(model, '.')
	lastSep := lastDash
	if lastDot > lastSep {
		lastSep = lastDot
	}
	if lastSep < 0 {
		return ""
	}

	suffix := model[lastSep+1:]
	if len(suffix) != 8 {
		return ""
	}

	// 验证是否全数字
	for i := 0; i < len(suffix); i++ {
		if suffix[i] < '0' || suffix[i] > '9' {
			return ""
		}
	}

	// 简单验证年份范围
	year := (int(suffix[0]-'0') * 1000) + (int(suffix[1]-'0') * 100) +
		(int(suffix[2]-'0') * 10) + int(suffix[3]-'0')
	if year < 2000 || year > 2100 {
		return ""
	}

	return suffix
}

// extractVersionNumbers 提取模型名称中的版本数字
// 例如：gpt-5.2 → [5,2], claude-sonnet-4-5-20250929 → [4,5]
func extractVersionNumbers(model string) []int {
	// 移除日期后缀避免干扰
	if date := extractDateSuffix(model); date != "" {
		model = model[:len(model)-len(date)-1]
	}

	var nums []int
	var current int
	inNumber := false

	for i := 0; i < len(model); i++ {
		c := model[i]
		if c >= '0' && c <= '9' {
			current = current*10 + int(c-'0')
			inNumber = true
		} else {
			if inNumber {
				nums = append(nums, current)
				current = 0
				inNumber = false
			}
		}
	}
	if inNumber {
		nums = append(nums, current)
	}

	return nums
}
