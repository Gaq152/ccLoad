package app

import (
	"fmt"
	neturl "net/url"
	"strings"
	"time"

	"ccLoad/internal/model"
	"ccLoad/internal/util"
)

// ==================== 共享数据结构 ====================
// 从admin.go提取共享类型,遵循SRP原则

// ChannelRequest 渠道创建/更新请求结构
type ChannelRequest struct {
	Name           string             `json:"name" binding:"required"`
	APIKey         string             `json:"api_key"`                // 非官方预设时必填
	ChannelType    string             `json:"channel_type,omitempty"` // 渠道类型:anthropic, codex, gemini
	KeyStrategy    string             `json:"key_strategy,omitempty"` // Key使用策略:sequential, round_robin
	URL            string             `json:"url" binding:"required,url"`
	Priority       int                `json:"priority"`
	Models         []string           `json:"models" binding:"required,min=1"`
	ModelRedirects map[string]string  `json:"model_redirects,omitempty"` // 可选的模型重定向映射
	Enabled        bool               `json:"enabled"`
	QuotaConfig    *model.QuotaConfig `json:"quota_config,omitempty"` // 用量监控配置（可选）

	// Codex/Gemini 预设相关字段（2025-12新增）
	Preset       string `json:"preset,omitempty"`        // "official"=官方预设, "custom"=自定义, ""=非OAuth渠道
	OpenAICompat bool   `json:"openai_compat,omitempty"` // OpenAI兼容模式（Anthropic/Gemini/Codex自定义渠道支持）

	// OAuth Token 专用字段（仅官方预设使用）
	AccessToken    string `json:"access_token,omitempty"`
	IDToken        string `json:"id_token,omitempty"`
	RefreshToken   string `json:"refresh_token,omitempty"`
	TokenExpiresAt int64  `json:"token_expires_at,omitempty"` // Unix时间戳

	// Kiro 专用字段
	DeviceFingerprint string `json:"device_fingerprint,omitempty"` // Kiro 设备指纹
}

func validateChannelBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("url cannot be empty")
	}

	u, err := neturl.Parse(raw)
	if err != nil || u == nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid url: %q", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid url scheme: %q (allowed: http, https)", u.Scheme)
	}
	if u.User != nil {
		return "", fmt.Errorf("url must not contain user info")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("url must not contain query or fragment")
	}

	// [FIX] 只禁止看起来像完整 API 端点的路径（防止误填如 /v1/messages, /v1/chat/completions）
	// 允许以下场景：
	// - /v1 单独结尾（如 https://proxy.example.com/v1 作为基础 URL）
	// - /openai/v1 结尾（反向代理或 API gateway）
	// 禁止：/v1/ 后面还有内容（如 /v1/messages, /v1/chat/completions）
	if strings.HasPrefix(u.Path, "/v1/") {
		return "", fmt.Errorf("url should not contain API endpoint path like /v1/... (current path: %q), please use base URL only", u.Path)
	}

	// 强制返回标准化格式（scheme://host+path，移除 trailing slash）
	// 例如: "https://example.com/api/" → "https://example.com/api"
	normalizedPath := strings.TrimSuffix(u.Path, "/")
	return u.Scheme + "://" + u.Host + normalizedPath, nil
}

// Validate 实现RequestValidator接口
// [FIX] P0-1: 添加白名单校验和标准化（Fail-Fast + 边界防御）
func (cr *ChannelRequest) Validate() error {
	// 必填字段校验（现有逻辑保留）
	if strings.TrimSpace(cr.Name) == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if len(cr.Models) == 0 {
		return fmt.Errorf("models cannot be empty")
	}

	// URL 验证规则（Fail-Fast 边界防御）：
	// - 必须包含 scheme+host（http/https）
	// - 禁止 userinfo、query、fragment
	// - 禁止 /v1/ 开头的 path（防止误填完整端点如 /v1/messages）
	// - 允许 /v1 结尾（如 https://proxy.example.com/v1 作为基础 URL）
	normalizedURL, err := validateChannelBaseURL(cr.URL)
	if err != nil {
		return err
	}
	cr.URL = normalizedURL

	// [FIX] channel_type 白名单校验 + 标准化
	// 设计：空值允许（使用默认值anthropic），非空值必须合法
	cr.ChannelType = strings.TrimSpace(cr.ChannelType)
	if cr.ChannelType != "" {
		// 先标准化（小写化）
		normalized := util.NormalizeChannelType(cr.ChannelType)
		// 再白名单校验
		if !util.IsValidChannelType(normalized) {
			return fmt.Errorf("invalid channel_type: %q (allowed: anthropic, gemini, codex)", cr.ChannelType)
		}
		cr.ChannelType = normalized // 应用标准化结果
	}

	// [FIX] OAuth 预设验证（2025-12新增，支持 Codex 和 Gemini）
	// 设计：Codex/Gemini 渠道支持预设类型，官方预设只能用 OAuth，自定义预设只能用 API Key
	cr.Preset = strings.TrimSpace(cr.Preset)
	isOAuthChannel := cr.ChannelType == "codex" || cr.ChannelType == "gemini"

	if isOAuthChannel {
		// Codex/Gemini 渠道必须指定预设
		if cr.Preset != "official" && cr.Preset != "custom" {
			return fmt.Errorf("%s渠道必须选择预设类型 (official 或 custom)", cr.ChannelType)
		}
		if cr.Preset == "official" {
			// 官方预设：必须有 OAuth Token
			if strings.TrimSpace(cr.AccessToken) == "" {
				return fmt.Errorf("官方预设必须完成OAuth授权")
			}
			// 官方预设不需要 api_key，清空以防误传
			cr.APIKey = ""
		} else {
			// 自定义预设：必须有 API Key
			if strings.TrimSpace(cr.APIKey) == "" {
				return fmt.Errorf("自定义预设必须填写API Key")
			}
			// 自定义预设不需要 OAuth Token，清空以防误传
			cr.AccessToken = ""
			cr.IDToken = ""
			cr.RefreshToken = ""
			cr.TokenExpiresAt = 0
		}
	} else if cr.ChannelType == "anthropic" {
		// Anthropic 渠道：支持 antigravity 和 kiro 预设
		if cr.Preset == "kiro" {
			// Kiro 预设：使用 OAuth Token 认证，不需要 API Key
			if strings.TrimSpace(cr.RefreshToken) == "" {
				return fmt.Errorf("kiro预设必须提供 refresh_token")
			}
		} else {
			// 其他预设（custom/antigravity）：需要 API Key
			if strings.TrimSpace(cr.APIKey) == "" {
				return fmt.Errorf("api_key cannot be empty")
			}
		}
		// 只允许 antigravity、kiro 或空/custom 预设
		if cr.Preset != "" && cr.Preset != "custom" && cr.Preset != "antigravity" && cr.Preset != "kiro" {
			return fmt.Errorf("anthropic渠道只支持 custom、antigravity 或 kiro 预设")
		}
	} else {
		// 其他非 OAuth 渠道：必须有 API Key，不使用预设
		if strings.TrimSpace(cr.APIKey) == "" {
			return fmt.Errorf("api_key cannot be empty")
		}
		cr.Preset = "" // 非 OAuth 渠道清空预设
	}

	// [FIX] key_strategy 白名单校验 + 标准化
	// 设计：空值允许（使用默认值sequential），非空值必须合法
	cr.KeyStrategy = strings.TrimSpace(cr.KeyStrategy)
	if cr.KeyStrategy != "" {
		// 先标准化（小写化）
		normalized := strings.ToLower(cr.KeyStrategy)
		// 再白名单校验
		if !model.IsValidKeyStrategy(normalized) {
			return fmt.Errorf("invalid key_strategy: %q (allowed: sequential, round_robin)", cr.KeyStrategy)
		}
		cr.KeyStrategy = normalized // 应用标准化结果
	}

	// QuotaConfig 验证（如果启用）
	if cr.QuotaConfig != nil && cr.QuotaConfig.Enabled {
		if err := validateQuotaConfig(cr.QuotaConfig); err != nil {
			return fmt.Errorf("invalid quota_config: %w", err)
		}
	}

	// Kiro 设备指纹验证（可选字段，但如果填写必须是 64 位 hex 字符串）
	cr.DeviceFingerprint = strings.TrimSpace(cr.DeviceFingerprint)
	if cr.DeviceFingerprint != "" {
		if !isValidDeviceFingerprint(cr.DeviceFingerprint) {
			return fmt.Errorf("invalid device_fingerprint: 必须是64位十六进制字符串")
		}
	}

	return nil
}

// validateQuotaConfig 验证用量监控配置
func validateQuotaConfig(qc *model.QuotaConfig) error {
	// 验证 URL（复用 SSRF 防护逻辑）
	if qc.RequestURL == "" {
		return fmt.Errorf("request_url is required")
	}
	if err := validateQuotaURL(qc.RequestURL); err != nil {
		return fmt.Errorf("invalid request_url: %w", err)
	}

	// 验证方法
	method := strings.ToUpper(qc.GetRequestMethod())
	if method != "GET" && method != "POST" {
		return fmt.Errorf("invalid request_method: %q (allowed: GET, POST)", qc.RequestMethod)
	}

	// 验证请求体大小
	if len(qc.RequestBody) > 64*1024 {
		return fmt.Errorf("request_body too large (max 64KB)")
	}

	// 验证提取器脚本
	if qc.ExtractorScript == "" {
		return fmt.Errorf("extractor_script is required")
	}
	if len(qc.ExtractorScript) > 64*1024 {
		return fmt.Errorf("extractor_script too large (max 64KB)")
	}

	// 验证轮询间隔（最少 60 秒，最多 1 天）
	if qc.IntervalSeconds < 60 {
		qc.IntervalSeconds = 60
	} else if qc.IntervalSeconds > 86400 {
		qc.IntervalSeconds = 86400
	}

	// 验证请求头数量和大小
	if len(qc.RequestHeaders) > 20 {
		return fmt.Errorf("too many request_headers (max 20)")
	}
	for key, value := range qc.RequestHeaders {
		if len(key) > 256 || len(value) > 4096 {
			return fmt.Errorf("request_header too large (key max 256, value max 4096)")
		}
	}

	return nil
}

// isValidDeviceFingerprint 验证设备指纹格式（64位十六进制字符串）
func isValidDeviceFingerprint(fp string) bool {
	if len(fp) != 64 {
		return false
	}
	for _, c := range fp {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// ToConfig 转换为Config结构(不包含API Key,API Key单独处理)
func (cr *ChannelRequest) ToConfig() *model.Config {
	return &model.Config{
		Name:           strings.TrimSpace(cr.Name),
		ChannelType:    strings.TrimSpace(cr.ChannelType), // 传递渠道类型
		URL:            strings.TrimSpace(cr.URL),
		Priority:       cr.Priority,
		Models:         cr.Models,
		ModelRedirects: cr.ModelRedirects,
		Enabled:        cr.Enabled,
		QuotaConfig:    cr.QuotaConfig,  // 用量监控配置
		Preset:         cr.Preset,       // Codex/Gemini预设类型
		OpenAICompat:   cr.OpenAICompat, // OpenAI兼容模式
	}
}

// ToAPIKey 转换为APIKey结构（支持OAuth Token）
func (cr *ChannelRequest) ToAPIKey(channelID int64) *model.APIKey {
	return &model.APIKey{
		ChannelID:         channelID,
		KeyIndex:          0,
		APIKey:            strings.TrimSpace(cr.APIKey),
		KeyStrategy:       cr.KeyStrategy,
		AccessToken:       strings.TrimSpace(cr.AccessToken),
		IDToken:           strings.TrimSpace(cr.IDToken),
		RefreshToken:      strings.TrimSpace(cr.RefreshToken),
		TokenExpiresAt:    cr.TokenExpiresAt,
		DeviceFingerprint: strings.TrimSpace(cr.DeviceFingerprint),
	}
}

// KeyCooldownInfo Key级别冷却信息
type KeyCooldownInfo struct {
	KeyIndex            int        `json:"key_index"`
	CooldownUntil       *time.Time `json:"cooldown_until,omitempty"`
	CooldownRemainingMS int64      `json:"cooldown_remaining_ms,omitempty"`
}

// ChannelWithCooldown 带冷却状态的渠道响应结构
type ChannelWithCooldown struct {
	*model.Config
	KeyStrategy           string            `json:"key_strategy,omitempty"` // [INFO] 修复 (2025-10-11): 添加key_strategy字段
	CooldownUntil         *time.Time        `json:"cooldown_until,omitempty"`
	CooldownRemainingMS   int64             `json:"cooldown_remaining_ms,omitempty"`
	KeyCooldowns          []KeyCooldownInfo `json:"key_cooldowns,omitempty"`
	ActiveEndpointLatency *int              `json:"active_endpoint_latency,omitempty"` // 当前激活端点延迟(ms)
	ActiveEndpointStatus  *int              `json:"active_endpoint_status,omitempty"`  // 当前激活端点状态码
}

// ChannelImportSummary 导入结果统计
type ChannelImportSummary struct {
	Created   int      `json:"created"`
	Updated   int      `json:"updated"`
	Skipped   int      `json:"skipped"`
	Processed int      `json:"processed"`
	Errors    []string `json:"errors,omitempty"`
	// Redis同步相关字段 (OCP: 开放扩展)
	RedisSyncEnabled    bool   `json:"redis_sync_enabled"`              // Redis同步是否启用
	RedisSyncSuccess    bool   `json:"redis_sync_success,omitempty"`    // Redis同步是否成功
	RedisSyncError      string `json:"redis_sync_error,omitempty"`      // Redis同步错误信息
	RedisSyncedChannels int    `json:"redis_synced_channels,omitempty"` // 成功同步到Redis的渠道数量
}

// CooldownRequest 冷却设置请求
type CooldownRequest struct {
	DurationMs int64 `json:"duration_ms" binding:"required,min=1000"` // 最少1秒
}

// SettingUpdateRequest 系统配置更新请求
type SettingUpdateRequest struct {
	Value string `json:"value" binding:"required"`
}
