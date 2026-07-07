package model

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// AuthToken 表示一个API访问令牌
// 用于代理API (/v1/*) 的认证授权
type AuthToken struct {
	ID          int64     `json:"id"`
	Token       string    `json:"token"`                  // SHA256哈希值(存储时)或明文(创建时返回)
	Description string    `json:"description"`            // 令牌用途描述
	CreatedAt   time.Time `json:"created_at"`             // 创建时间
	ExpiresAt   *int64    `json:"expires_at,omitempty"`   // 过期时间(Unix毫秒时间戳)，nil表示永不过期
	LastUsedAt  *int64    `json:"last_used_at,omitempty"` // 最后使用时间(Unix毫秒时间戳)
	IsActive    bool      `json:"is_active"`              // 是否启用

	// 渠道访问控制（2025-12新增）
	AllChannels bool    `json:"all_channels"`          // 是否允许使用所有渠道（true=全部，false=仅指定渠道）
	ChannelIDs  []int64 `json:"channel_ids,omitempty"` // 允许使用的渠道ID列表（仅当 AllChannels=false 时有效）

	// 统计字段（2025-11新增）
	SuccessCount   int64   `json:"success_count"`     // 成功调用次数
	FailureCount   int64   `json:"failure_count"`     // 失败调用次数
	StreamAvgTTFB  float64 `json:"stream_avg_ttfb"`   // 流式请求平均首字节时间(秒)
	NonStreamAvgRT float64 `json:"non_stream_avg_rt"` // 非流式请求平均响应时间(秒)
	StreamCount    int64   `json:"stream_count"`      // 流式请求计数(用于计算平均值)
	NonStreamCount int64   `json:"non_stream_count"`  // 非流式请求计数(用于计算平均值)

	// Token成本统计（2025-12新增）
	PromptTokensTotal        int64    `json:"prompt_tokens_total"`         // 累计输入Token数
	CompletionTokensTotal    int64    `json:"completion_tokens_total"`     // 累计输出Token数
	CacheReadTokensTotal     int64    `json:"cache_read_tokens_total"`     // 累计缓存读Token数
	CacheCreationTokensTotal int64    `json:"cache_creation_tokens_total"` // 累计缓存写Token数
	TotalCostUSD             float64  `json:"total_cost_usd"`              // 累计成本(美元)
	QuotaLimitUSD            *float64 `json:"quota_limit_usd"`             // 额度上限(美元)，nil表示无限
	QuotaUsedUSD             float64  `json:"quota_used_usd"`              // 额度累计已用(美元)，API响应字段

	// Token 加密存储（AES-256-GCM，用于再次查看）
	TokenEncrypted *string `json:"-"`                    // 加密后的明文（不暴露到 API）
	TokenHint      *string `json:"token_hint,omitempty"` // 明文掩码（如 sk-ccl-abcd****wxyz），用于列表展示
	HasEncrypted   bool    `json:"has_encrypted"`        // 是否有加密存储（前端判断是否显示查看按钮）

	// API 响应计算字段（不存储到数据库）
	IsExpiredFlag bool `json:"is_expired"` // 是否已过期（API响应时计算）
}

// AuthTokenRangeStats 某个时间范围内的token统计（从logs表聚合，2025-12新增）
type AuthTokenRangeStats struct {
	SuccessCount        int64   `json:"success_count"`         // 成功次数
	FailureCount        int64   `json:"failure_count"`         // 失败次数
	PromptTokens        int64   `json:"prompt_tokens"`         // 输入Token总数
	CompletionTokens    int64   `json:"completion_tokens"`     // 输出Token总数
	CacheReadTokens     int64   `json:"cache_read_tokens"`     // 缓存读Token总数
	CacheCreationTokens int64   `json:"cache_creation_tokens"` // 缓存写Token总数
	TotalCost           float64 `json:"total_cost"`            // 总费用(美元)
	StreamAvgTTFB       float64 `json:"stream_avg_ttfb"`       // 流式请求平均首字节时间
	NonStreamAvgRT      float64 `json:"non_stream_avg_rt"`     // 非流式请求平均响应时间
	StreamCount         int64   `json:"stream_count"`          // 流式请求计数
	NonStreamCount      int64   `json:"non_stream_count"`      // 非流式请求计数
}

// HashToken 计算令牌的SHA256哈希值
// 用于安全存储令牌到数据库
func HashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// IsExpired 检查令牌是否已过期
// expires_at == nil 或 *expires_at == 0 表示永不过期
func (t *AuthToken) IsExpired() bool {
	if t.ExpiresAt == nil || *t.ExpiresAt == 0 {
		return false
	}
	return time.Now().UnixMilli() > *t.ExpiresAt
}

// IsValid 检查令牌是否有效(启用且未过期)
func (t *AuthToken) IsValid() bool {
	return t.IsActive && !t.IsExpired()
}

// MaskToken 脱敏显示令牌(仅显示前4后4字符)
// 例如: "sk-ant-1234567890abcdef" -> "sk-a****cdef"
func MaskToken(token string) string {
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "****" + token[len(token)-4:]
}

// BuildTokenHint 从明文生成更可读的掩码提示（保留前缀 + 首4 + 尾4）
// 例如: "sk-ccl-abcd...1234xyz" -> "sk-ccl-abcd****1xyz"
func BuildTokenHint(plaintext string) string {
	if plaintext == "" {
		return ""
	}
	// 识别常见前缀：sk-ccl-、sk-ant-、sk- 等
	prefix := ""
	for _, p := range []string{"sk-ccl-", "sk-ant-", "sk-"} {
		if len(plaintext) > len(p) && plaintext[:len(p)] == p {
			prefix = p
			break
		}
	}
	rest := plaintext[len(prefix):]
	if len(rest) <= 8 {
		return prefix + "****"
	}
	return prefix + rest[:4] + "****" + rest[len(rest)-4:]
}

// UpdateLastUsed 更新最后使用时间为当前时间
func (t *AuthToken) UpdateLastUsed() {
	now := time.Now().UnixMilli()
	t.LastUsedAt = &now
}
