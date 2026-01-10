package app

// ============================================================================
// Kiro (AWS CodeWhisperer) 类型定义
// 参考: https://github.com/nineyuanz/kiro2api
// ============================================================================

// Kiro 常量
const (
	// Kiro Social 方式 Token 刷新 URL
	KiroRefreshTokenURL = "https://prod.us-east-1.auth.desktop.kiro.dev/refreshToken"

	// Kiro IdC 方式 Token 刷新 URL
	KiroIdCRefreshTokenURL = "https://oidc.us-east-1.amazonaws.com/token"

	// Kiro API 端点
	KiroAPIEndpoint = "https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse"

	// Token 提前刷新时间（过期前 5 分钟刷新）
	KiroTokenRefreshBuffer = 5 * 60 // 秒

	// 认证方式常量
	KiroAuthMethodSocial = "Social"
	KiroAuthMethodIdC    = "IdC"
)

// KiroModelMap 模型映射表 (Anthropic 模型名 -> CodeWhisperer 模型 ID)
var KiroModelMap = map[string]string{
	// 完整模型名
	"claude-opus-4-5-20251101":   "CLAUDE_OPUS_4_5_20251101_V1_0",
	"claude-sonnet-4-5-20250929": "CLAUDE_SONNET_4_5_20250929_V1_0",
	"claude-sonnet-4-20250514":   "CLAUDE_SONNET_4_20250514_V1_0",
	"claude-3-7-sonnet-20250219": "CLAUDE_3_7_SONNET_20250219_V1_0",
	"claude-3-5-haiku-20241022":  "auto",
	"claude-haiku-4-5-20251001":  "auto",

	// 常用别名
	"claude-3-7-sonnet": "CLAUDE_3_7_SONNET_20250219_V1_0",
	"claude-sonnet-4":   "CLAUDE_SONNET_4_20250514_V1_0",
	"claude-sonnet-4-5": "CLAUDE_SONNET_4_5_20250929_V1_0",
	"claude-opus-4-5":   "CLAUDE_OPUS_4_5_20251101_V1_0",
}

// ============================================================================
// Kiro Token 配置结构
// ============================================================================

// KiroAuthConfig Kiro 认证配置（用户粘贴的 JSON 格式）
type KiroAuthConfig struct {
	AuthType     string `json:"auth"`                    // "Social" 或 "IdC"
	RefreshToken string `json:"refreshToken"`            // 刷新令牌
	ClientID     string `json:"clientId,omitempty"`      // IdC 方式需要
	ClientSecret string `json:"clientSecret,omitempty"`  // IdC 方式需要
	Disabled     bool   `json:"disabled,omitempty"`      // 是否禁用
}

// KiroTokenInfo Kiro Token 信息（刷新后获得）
type KiroTokenInfo struct {
	AccessToken string `json:"accessToken"` // 访问令牌
	ExpiresAt   int64  `json:"expiresAt"`   // 过期时间 Unix 时间戳（毫秒）
}

// ============================================================================
// CodeWhisperer 请求结构
// ============================================================================

// KiroRequest CodeWhisperer 请求结构
type KiroRequest struct {
	ConversationState      KiroConversationState       `json:"conversationState"`
	InferenceConfiguration *KiroInferenceConfiguration `json:"inferenceConfiguration,omitempty"`
}

// KiroConversationState 会话状态
type KiroConversationState struct {
	AgentContinuationId string             `json:"agentContinuationId"`
	AgentTaskType       string             `json:"agentTaskType"`   // 固定 "vibe"
	ChatTriggerType     string             `json:"chatTriggerType"` // "MANUAL" 或 "AUTO"
	ConversationId      string             `json:"conversationId"`
	CurrentMessage      KiroCurrentMessage `json:"currentMessage"`
	History             []any              `json:"history,omitempty"`
}

// KiroCurrentMessage 当前消息
type KiroCurrentMessage struct {
	UserInputMessage KiroUserInputMessage `json:"userInputMessage"`
}

// KiroUserInputMessage 用户输入消息
type KiroUserInputMessage struct {
	Content                 string                      `json:"content"`
	ModelId                 string                      `json:"modelId"`
	Origin                  string                      `json:"origin"` // 固定 "AI_EDITOR"
	Images                  []KiroImage                 `json:"images,omitempty"`
	UserInputMessageContext KiroUserInputMessageContext `json:"userInputMessageContext,omitempty"`
}

// KiroImage 图片数据 (CodeWhisperer 格式)
// 参考: https://github.com/nineyuanz/kiro2api/blob/main/types/codewhisperer.go
type KiroImage struct {
	Format string `json:"format"` // 图片格式: "jpeg", "png", "gif", "webp"
	Source struct {
		Bytes string `json:"bytes"` // base64 编码的图片数据
	} `json:"source"`
}

// KiroUserInputMessageContext 用户输入消息上下文
type KiroUserInputMessageContext struct {
	Tools       []KiroTool       `json:"tools,omitempty"`
	ToolResults []KiroToolResult `json:"toolResults,omitempty"`
}

// KiroTool 工具定义
type KiroTool struct {
	ToolSpecification KiroToolSpecification `json:"toolSpecification"`
}

// KiroToolSpecification 工具规格
type KiroToolSpecification struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema KiroInputSchema `json:"inputSchema"`
}

// KiroInputSchema 输入模式
type KiroInputSchema struct {
	Json any `json:"json"` // JSON Schema
}

// KiroToolResult 工具结果
type KiroToolResult struct {
	ToolUseId string           `json:"toolUseId"`
	Content   []map[string]any `json:"content"`
	Status    string           `json:"status"` // "success" 或 "error"
	IsError   bool             `json:"isError,omitempty"`
}

// KiroToolUseEntry 工具调用条目（用于历史消息中的 assistant 响应）
type KiroToolUseEntry struct {
	ToolUseId string         `json:"toolUseId"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input"`
}

// ============================================================================
// CodeWhisperer 历史消息结构
// ============================================================================

// KiroHistoryUserMessage 历史用户消息
type KiroHistoryUserMessage struct {
	UserInputMessage KiroUserInputMessage `json:"userInputMessage"`
}

// KiroHistoryAssistantMessage 历史助手消息
type KiroHistoryAssistantMessage struct {
	AssistantResponseMessage KiroAssistantResponseMessage `json:"assistantResponseMessage"`
}

// KiroAssistantResponseMessage 助手响应消息
type KiroAssistantResponseMessage struct {
	Content  string             `json:"content"`
	ToolUses []KiroToolUseEntry `json:"toolUses,omitempty"`
}

// ============================================================================
// CodeWhisperer 推理配置
// ============================================================================

// KiroInferenceConfiguration 推理配置
type KiroInferenceConfiguration struct {
	MaxTokens   int            `json:"maxTokens,omitempty"`
	Temperature *float64       `json:"temperature,omitempty"`
	Thinking    *KiroThinking  `json:"thinking,omitempty"`
}

// KiroThinking Thinking 配置
type KiroThinking struct {
	Type         string `json:"type"`         // "enabled"
	BudgetTokens int    `json:"budgetTokens"` // 思考预算 token 数
}

// ============================================================================
// Token 刷新请求/响应结构
// ============================================================================

// KiroSocialRefreshRequest Social 方式刷新请求
type KiroSocialRefreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

// KiroSocialRefreshResponse Social 方式刷新响应
type KiroSocialRefreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresIn    int64  `json:"expiresIn"` // 秒
}

// KiroIdCRefreshResponse IdC 方式刷新响应
type KiroIdCRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int64  `json:"expires_in"` // 秒
	TokenType    string `json:"token_type"`
}
