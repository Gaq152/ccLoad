package testutil

import (
	"crypto/rand"
	"encoding/base64"
	_ "embed"
	"fmt"
	"net/http"
	"strings"

	"ccLoad/internal/model"

	"github.com/bytedance/sonic"
)

//go:embed codex_instructions.txt
var codexDefaultInstructions string

// ChannelTester 定义不同渠道类型的测试协议（OCP：新增类型无需修改调用方）
type ChannelTester interface {
	// Build 构造完整请求：URL、基础请求头、请求体
	// apiKey: 实际使用的API Key字符串（由调用方从数据库查询）
	Build(cfg *model.Config, apiKey string, req *TestChannelRequest) (fullURL string, headers http.Header, body []byte, err error)
	// Parse 解析响应体，返回通用结果字段（如 response_text、usage、api_response/api_error/raw_response）
	Parse(statusCode int, respBody []byte) map[string]any
}

// === 泛型类型安全工具函数 ===

// getTypedValue 从map中安全获取指定类型的值（消除类型断言嵌套）
func getTypedValue[T any](m map[string]any, key string) (T, bool) {
	var zero T
	v, ok := m[key]
	if !ok {
		return zero, false
	}
	typed, ok := v.(T)
	return typed, ok
}

// getSliceItem 从切片中安全获取指定索引的指定类型元素
func getSliceItem[T any](slice []any, index int) (T, bool) {
	var zero T
	if index < 0 || index >= len(slice) {
		return zero, false
	}
	typed, ok := slice[index].(T)
	return typed, ok
}

// CodexTester 兼容 Codex 风格（渠道类型: codex）
type CodexTester struct{}

// codexDefaultTools Codex 测试用的最小工具集
var codexDefaultTools = []map[string]any{
	{
		"type":        "function",
		"name":        "shell_command",
		"description": "Runs a shell command and returns its output.",
		"strict":      false,
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
			},
			"required":             []string{"command"},
			"additionalProperties": false,
		},
	},
}

func (t *CodexTester) Build(cfg *model.Config, apiKey string, req *TestChannelRequest) (string, http.Header, []byte, error) {
	testContent := req.Content
	if strings.TrimSpace(testContent) == "" {
		testContent = "test"
	}

	// 构建符合官方格式的请求体
	msg := map[string]any{
		"model":        req.Model,
		"instructions": codexDefaultInstructions,
		"input": []map[string]any{
			{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{
						"type": "input_text",
						"text": testContent,
					},
				},
			},
		},
		"tools":               codexDefaultTools,
		"tool_choice":         "auto",
		"parallel_tool_calls": false,
		"reasoning": map[string]any{
			"effort":  "medium",
			"summary": "auto",
		},
		"store":            false,
		"stream":           true, // Codex API 要求必须流式
		"include":          []string{"reasoning.encrypted_content"},
		"prompt_cache_key": generateUUID(),
	}

	body, err := sonic.Marshal(msg)
	if err != nil {
		return "", nil, nil, err
	}

	// 统一使用 /responses 路径（不加 v1，因为 Codex API 原生路径就是 /responses）
	baseURL := strings.TrimRight(cfg.URL, "/")
	fullURL := baseURL + "/responses"

	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer "+apiKey)

	// 官方预设需要额外的请求头
	if cfg.Preset == "official" {
		h.Set("Openai-Conversation-Id", "conv-test-"+generateUUID())
		h.Set("Openai-Request-Id", "req-test-"+generateUUID())
		h.Set("Openai-Sentinel-Chat-Requirements-Token", generateChatToken())
		// 关键：从 JWT 提取 account_id 并设置请求头
		if accountID := extractAccountIDFromJWT(apiKey); accountID != "" {
			h.Set("chatgpt-account-id", accountID)
		}
	}

	h.Set("User-Agent", "codex_cli_rs/0.41.0 (Mac OS 26.0.0; arm64) iTerm.app/3.6.1")
	h.Set("Openai-Beta", "responses=experimental")
	h.Set("Originator", "codex_cli_rs")
	h.Set("Accept", "text/event-stream") // Codex 必须流式

	return fullURL, h, body, nil
}

// generateUUID 生成简单的 UUID
func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// generateChatToken 生成 Codex 需要的 chat requirements token
func generateChatToken() string {
	// 这是一个简化的 token，实际可能需要更复杂的生成逻辑
	return "gAAAAAB" + generateUUID()
}

// extractAccountIDFromJWT 从 JWT access_token 中提取 chatgpt_account_id
func extractAccountIDFromJWT(accessToken string) string {
	if accessToken == "" {
		return ""
	}

	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return ""
	}

	// 解码 payload（URL-safe base64）
	payload := parts[1]
	payload = strings.ReplaceAll(payload, "-", "+")
	payload = strings.ReplaceAll(payload, "_", "/")
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}

	var claims map[string]any
	if err := sonic.Unmarshal(decoded, &claims); err != nil {
		return ""
	}

	// 路径: claims["https://api.openai.com/auth"]["chatgpt_account_id"]
	auth, ok := claims["https://api.openai.com/auth"].(map[string]any)
	if !ok {
		return ""
	}

	accountID, _ := auth["chatgpt_account_id"].(string)
	return accountID
}

// extractCodexResponseText 从Codex响应中提取文本（消除6层嵌套）
func extractCodexResponseText(apiResp map[string]any) (string, bool) {
	output, ok := getTypedValue[[]any](apiResp, "output")
	if !ok {
		return "", false
	}

	for _, item := range output {
		outputItem, ok := item.(map[string]any)
		if !ok {
			continue
		}

		outputType, ok := getTypedValue[string](outputItem, "type")
		if !ok || outputType != "message" {
			continue
		}

		content, ok := getTypedValue[[]any](outputItem, "content")
		if !ok || len(content) == 0 {
			continue
		}

		textBlock, ok := getSliceItem[map[string]any](content, 0)
		if !ok {
			continue
		}

		text, ok := getTypedValue[string](textBlock, "text")
		if ok {
			return text, true
		}
	}
	return "", false
}

func (t *CodexTester) Parse(statusCode int, respBody []byte) map[string]any {
	out := map[string]any{}
	var apiResp map[string]any
	if err := sonic.Unmarshal(respBody, &apiResp); err == nil {
		// 提取文本（使用辅助函数）
		if text, ok := extractCodexResponseText(apiResp); ok {
			out["response_text"] = text
		}

		// 提取usage（使用泛型工具）
		if usage, ok := getTypedValue[map[string]any](apiResp, "usage"); ok {
			out["usage"] = usage
		}

		out["api_response"] = apiResp
		return out
	}
	out["raw_response"] = string(respBody)
	return out
}

// OpenAITester 标准OpenAI API格式（用于OpenAI兼容模式测试）
type OpenAITester struct{}

func (t *OpenAITester) Build(cfg *model.Config, apiKey string, req *TestChannelRequest) (string, http.Header, []byte, error) {
	testContent := req.Content
	if strings.TrimSpace(testContent) == "" {
		testContent = "test"
	}

	// 标准OpenAI Chat Completions格式（测试默认开启流式）
	msg := map[string]any{
		"model": req.Model,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": testContent,
			},
		},
		"max_tokens": req.MaxTokens,
		"stream":     true,
	}

	body, err := sonic.Marshal(msg)
	if err != nil {
		return "", nil, nil, err
	}

	// 使用标准OpenAI API路径
	baseURL := strings.TrimRight(cfg.URL, "/")
	fullURL := baseURL + "/v1/chat/completions"

	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer "+apiKey)
	h.Set("Accept", "text/event-stream")

	return fullURL, h, body, nil
}

func (t *OpenAITester) Parse(statusCode int, respBody []byte) map[string]any {
	out := map[string]any{}
	var apiResp map[string]any
	if err := sonic.Unmarshal(respBody, &apiResp); err == nil {
		// 提取choices[0].message.content
		if choices, ok := getTypedValue[[]any](apiResp, "choices"); ok && len(choices) > 0 {
			if choice, ok := getSliceItem[map[string]any](choices, 0); ok {
				if message, ok := getTypedValue[map[string]any](choice, "message"); ok {
					if content, ok := getTypedValue[string](message, "content"); ok {
						out["response_text"] = content
					}
				}
			}
		}

		// 提取usage
		if usage, ok := getTypedValue[map[string]any](apiResp, "usage"); ok {
			out["usage"] = usage
		}

		out["api_response"] = apiResp
		return out
	}
	out["raw_response"] = string(respBody)
	return out
}

// GeminiTester 实现 Google Gemini 测试协议
type GeminiTester struct{}

// Gemini CLI 配置常量（与 gemini_auth.go 保持一致）
const (
	geminiCLIEndpoint  = "https://cloudcode-pa.googleapis.com"
	geminiCLIProjectID = "causal-voltage-327sp"
	geminiCLIUserAgent = "GeminiCLI/v22.21.0 (ccload; proxy)"
	geminiCLIAPIClient = "gl-node/22.21.0 grpc/1.24.0"
)

func (t *GeminiTester) Build(cfg *model.Config, apiKey string, req *TestChannelRequest) (string, http.Header, []byte, error) {
	testContent := req.Content
	if strings.TrimSpace(testContent) == "" {
		testContent = "test"
	}

	var body []byte
	var err error
	var fullURL string
	h := make(http.Header)
	h.Set("Content-Type", "application/json")

	// OpenAI 兼容模式：使用 /v1/chat/completions 格式
	if cfg.OpenAICompat {
		openaiMsg := map[string]any{
			"model": req.Model,
			"messages": []map[string]any{
				{
					"role":    "user",
					"content": testContent,
				},
			},
			"stream": true,
		}
		body, err = sonic.Marshal(openaiMsg)
		if err != nil {
			return "", nil, nil, err
		}

		baseURL := strings.TrimRight(cfg.URL, "/")
		fullURL = baseURL + "/v1/chat/completions"

		// OpenAI 格式使用 Bearer 认证
		h.Set("Authorization", "Bearer "+apiKey)
		h.Set("Accept", "text/event-stream")
		return fullURL, h, body, nil
	}

	// 标准 Gemini API 请求体格式（包含 model 和 role 字段）
	msg := map[string]any{
		"model": req.Model,
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]any{
					{
						"text": testContent,
					},
				},
			},
		},
	}

	// 检测端点类型
	isCLIEndpoint := strings.Contains(cfg.URL, "cloudcode-pa.googleapis.com")

	// 根据预设类型和端点选择不同的 API 格式
	if cfg.Preset == "official" && isCLIEndpoint {
		// Gemini CLI 官方预设（cloudcode-pa 端点）：使用 v1internal 端点和 CLI 格式
		// CLI 格式的 request 内部不需要 model 字段（model 在外层）
		cliRequest := map[string]any{
			"contents": []map[string]any{
				{
					"role": "user",
					"parts": []map[string]any{
						{"text": testContent},
					},
				},
			},
			"generationConfig": map[string]any{
				"temperature": 1,
				"topP":        0.95,
				"topK":        64,
			},
		}
		cliBody := map[string]any{
			"model":          req.Model,
			"project":        geminiCLIProjectID,
			"user_prompt_id": generateUUID() + "########0",
			"request":        cliRequest,
		}
		body, err = sonic.Marshal(cliBody)
		if err != nil {
			return "", nil, nil, err
		}

		// 使用 Gemini CLI 端点
		fullURL = geminiCLIEndpoint + "/v1internal:streamGenerateContent?alt=sse"

		// CLI 特殊请求头 + OAuth 认证
		h.Set("Authorization", "Bearer "+apiKey)
		h.Set("User-Agent", geminiCLIUserAgent)
		h.Set("x-goog-api-client", geminiCLIAPIClient)
		h.Set("Accept", "*/*")
	} else if cfg.Preset == "official" {
		// Gemini 标准 API 端点（generativelanguage 等）+ OAuth 认证
		// 不需要请求体转换，只需替换认证方式
		body, err = sonic.Marshal(msg)
		if err != nil {
			return "", nil, nil, err
		}

		baseURL := strings.TrimRight(cfg.URL, "/")
		// Gemini API 路径格式: /v1beta/models/{model}:streamGenerateContent（流式）
		fullURL = baseURL + "/v1beta/models/" + req.Model + ":streamGenerateContent?alt=sse"

		// OAuth Bearer 认证（而非 API Key）
		h.Set("Authorization", "Bearer "+apiKey)
		h.Set("Accept", "text/event-stream")
	} else {
		// 自定义预设或默认：使用标准 Gemini API + API Key
		body, err = sonic.Marshal(msg)
		if err != nil {
			return "", nil, nil, err
		}

		baseURL := strings.TrimRight(cfg.URL, "/")
		// Gemini API 路径格式: /v1beta/models/{model}:streamGenerateContent（流式）
		fullURL = baseURL + "/v1beta/models/" + req.Model + ":streamGenerateContent?alt=sse"

		// API Key 认证
		h.Set("x-goog-api-key", apiKey)
		h.Set("Accept", "text/event-stream")
	}

	return fullURL, h, body, nil
}

// extractGeminiResponseText 从Gemini响应中提取文本（消除5层嵌套）
func extractGeminiResponseText(apiResp map[string]any) (string, bool) {
	candidates, ok := getTypedValue[[]any](apiResp, "candidates")
	if !ok || len(candidates) == 0 {
		return "", false
	}

	candidate, ok := getSliceItem[map[string]any](candidates, 0)
	if !ok {
		return "", false
	}

	content, ok := getTypedValue[map[string]any](candidate, "content")
	if !ok {
		return "", false
	}

	parts, ok := getTypedValue[[]any](content, "parts")
	if !ok || len(parts) == 0 {
		return "", false
	}

	part, ok := getSliceItem[map[string]any](parts, 0)
	if !ok {
		return "", false
	}

	text, ok := getTypedValue[string](part, "text")
	return text, ok
}

func (t *GeminiTester) Parse(statusCode int, respBody []byte) map[string]any {
	out := map[string]any{}
	var apiResp map[string]any
	if err := sonic.Unmarshal(respBody, &apiResp); err == nil {
		// 自动检测响应格式：OpenAI 有 "choices"，Gemini 有 "candidates"
		if _, hasChoices := apiResp["choices"]; hasChoices {
			// OpenAI 格式响应
			if text, ok := extractOpenAIResponseText(apiResp); ok {
				out["response_text"] = text
			}
			if usage, ok := getTypedValue[map[string]any](apiResp, "usage"); ok {
				out["usage"] = usage
			}
		} else {
			// Gemini 格式响应
			if text, ok := extractGeminiResponseText(apiResp); ok {
				out["response_text"] = text
			}
			if usageMetadata, ok := getTypedValue[map[string]any](apiResp, "usageMetadata"); ok {
				out["usage"] = usageMetadata
			}
		}

		out["api_response"] = apiResp
		return out
	}
	out["raw_response"] = string(respBody)
	return out
}

// extractOpenAIResponseText 从OpenAI响应中提取文本
func extractOpenAIResponseText(apiResp map[string]any) (string, bool) {
	choices, ok := getTypedValue[[]any](apiResp, "choices")
	if !ok || len(choices) == 0 {
		return "", false
	}
	choice, ok := getSliceItem[map[string]any](choices, 0)
	if !ok {
		return "", false
	}
	message, ok := getTypedValue[map[string]any](choice, "message")
	if !ok {
		return "", false
	}
	content, ok := getTypedValue[string](message, "content")
	return content, ok
}

// AnthropicTester 实现 Anthropic 测试协议
type AnthropicTester struct{}

// anthropicMinimalTools Claude Code 测试用的精简工具列表
var anthropicMinimalTools = []map[string]any{
	{
		"name":        "Bash",
		"description": "Executes a bash command",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "The command to execute"},
			},
			"required": []string{"command"},
		},
	},
	{
		"name":        "Read",
		"description": "Reads a file from the filesystem",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string", "description": "The absolute path to the file"},
			},
			"required": []string{"file_path"},
		},
	},
	{
		"name":        "Edit",
		"description": "Performs string replacements in files",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path":  map[string]any{"type": "string", "description": "The absolute path to the file"},
				"old_string": map[string]any{"type": "string", "description": "The text to replace"},
				"new_string": map[string]any{"type": "string", "description": "The replacement text"},
			},
			"required": []string{"file_path", "old_string", "new_string"},
		},
	},
}

// generateAnthropicUserID 生成符合 Claude Code 格式的 user_id
// 格式: user_{hash}_account__session_{uuid}
func generateAnthropicUserID() string {
	// 生成 64 字符的 hash（模拟 SHA256）
	hashBytes := make([]byte, 32)
	rand.Read(hashBytes)
	hash := fmt.Sprintf("%x", hashBytes)
	// 生成 session UUID
	sessionID := generateUUID()
	return fmt.Sprintf("user_%s_account__session_%s", hash, sessionID)
}

func (t *AnthropicTester) Build(cfg *model.Config, apiKey string, req *TestChannelRequest) (string, http.Header, []byte, error) {
	testContent := req.Content

	msg := map[string]any{
		"system": []map[string]any{
			{
				"type":          "text",
				"text":          "You are Claude Code, Anthropic's official CLI for Claude. You are an interactive CLI tool that helps users with software engineering tasks.",
				"cache_control": map[string]any{"type": "ephemeral"},
			},
		},
		"stream": true,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": testContent,
					},
				},
			},
		},
		"model":      req.Model,
		"max_tokens": 32000,
		"tools":      anthropicMinimalTools,
		"metadata":   map[string]any{"user_id": generateAnthropicUserID()},
	}

	body, err := sonic.Marshal(msg)
	if err != nil {
		return "", nil, nil, err
	}

	baseURL := strings.TrimRight(cfg.URL, "/")
	fullURL := baseURL + "/v1/messages?beta=true"

	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	h.Set("Authorization", "Bearer "+apiKey)
	// Claude Code CLI headers（模拟真实 CLI 请求）
	h.Set("User-Agent", "claude-cli/2.0.73 (external, cli)")
	h.Set("x-app", "cli")
	h.Set("anthropic-version", "2023-06-01")
	h.Set("anthropic-beta", "interleaved-thinking-2025-05-14,context-management-2025-06-27")
	h.Set("anthropic-dangerous-direct-browser-access", "true")
	// x-stainless-* headers
	h.Set("x-stainless-arch", "x64")
	h.Set("x-stainless-lang", "js")
	h.Set("x-stainless-os", "Windows")
	h.Set("x-stainless-package-version", "0.70.0")
	h.Set("x-stainless-retry-count", "0")
	h.Set("x-stainless-runtime", "node")
	h.Set("x-stainless-runtime-version", "v22.21.0")
	h.Set("x-stainless-timeout", "600")
	h.Set("x-stainless-helper-method", "stream")
	// 额外请求头
	h.Set("Accept", "application/json")
	h.Set("accept-language", "*")
	h.Set("sec-fetch-mode", "cors")
	h.Set("accept-encoding", "br, gzip, deflate")

	return fullURL, h, body, nil
}

// extractAnthropicResponseText 从Anthropic响应中提取文本（消除3层嵌套）
func extractAnthropicResponseText(apiResp map[string]any) (string, bool) {
	content, ok := getTypedValue[[]any](apiResp, "content")
	if !ok || len(content) == 0 {
		return "", false
	}

	textBlock, ok := getSliceItem[map[string]any](content, 0)
	if !ok {
		return "", false
	}

	text, ok := getTypedValue[string](textBlock, "text")
	return text, ok
}

func (t *AnthropicTester) Parse(statusCode int, respBody []byte) map[string]any {
	out := map[string]any{}
	var apiResp map[string]any
	if err := sonic.Unmarshal(respBody, &apiResp); err == nil {
		// 提取文本响应（使用辅助函数）
		if text, ok := extractAnthropicResponseText(apiResp); ok {
			out["response_text"] = text
		}

		out["api_response"] = apiResp
		return out
	}
	out["raw_response"] = string(respBody)
	return out
}
