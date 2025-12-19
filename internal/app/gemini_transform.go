package app

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
)

// ============================================================================
// Gemini CLI 请求格式转换
// ============================================================================

// GeminiCLIExtraHeaders Gemini CLI 请求需要的额外信息
type GeminiCLIExtraHeaders struct {
	UserPromptID string // 请求唯一标识
	ProjectID    string // GCP 项目 ID（Gemini CLI 使用固定值）
}

// Gemini CLI 固定配置
const (
	// Gemini CLI 使用的固定 Project ID
	GeminiCLIProjectID = "causal-voltage-327sp"

	// Gemini CLI User-Agent
	GeminiCLIUserAgent = "GeminiCLI/v22.21.0 (ccload; proxy)"

	// Gemini CLI API Client
	GeminiCLIAPIClient = "gl-node/22.21.0 grpc/1.24.0"
)

// TransformGeminiCLIRequestBody 转换请求体为 Gemini CLI 格式
// 输入: 标准 Gemini API 格式 {"contents": [...], "generationConfig": {...}}
// 输出: Gemini CLI 嵌套格式 {"model": "...", "project": "...", "request": {...原始请求...}}
func TransformGeminiCLIRequestBody(body []byte, model string) ([]byte, error) {
	var req map[string]any
	if err := sonic.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse request body: %w", err)
	}

	// 检查是否已是 Gemini CLI 格式（有 request 字段且有 project 字段）
	if _, hasRequest := req["request"]; hasRequest {
		if _, hasProject := req["project"]; hasProject {
			// 已是 CLI 格式，直接返回
			return body, nil
		}
	}

	// 构建 Gemini CLI 格式
	cliReq := map[string]any{
		"model":          model,
		"project":        GeminiCLIProjectID,
		"user_prompt_id": generateGeminiUUID() + "########0",
		"request":        req, // 原始请求作为嵌套对象
	}

	return sonic.Marshal(cliReq)
}

// ConvertGeminiPath 转换 Gemini 标准路径到 CLI 路径
// /v1beta/models/gemini-2.5-flash:streamGenerateContent → /v1internal:streamGenerateContent?alt=sse
// /v1beta/models/gemini-2.5-flash:generateContent → /v1internal:generateContent
func ConvertGeminiPath(path string) string {
	// 提取操作类型
	if strings.Contains(path, ":streamGenerateContent") {
		return "/v1internal:streamGenerateContent?alt=sse"
	}
	if strings.Contains(path, ":generateContent") {
		return "/v1internal:generateContent"
	}
	// 其他路径不转换
	return path
}

// ExtractModelFromGeminiPath 从 Gemini 路径中提取模型名称
// /v1beta/models/gemini-2.5-flash:streamGenerateContent → gemini-2.5-flash
func ExtractModelFromGeminiPath(path string) string {
	// 查找 /models/ 后的内容
	idx := strings.Index(path, "/models/")
	if idx == -1 {
		return ""
	}

	// 跳过 "/models/"
	modelPart := path[idx+8:]

	// 查找 : 结束
	colonIdx := strings.Index(modelPart, ":")
	if colonIdx == -1 {
		return modelPart
	}

	return modelPart[:colonIdx]
}

// InjectGeminiCLIHeaders 注入 Gemini CLI 特有的请求头
func InjectGeminiCLIHeaders(req *http.Request, accessToken string) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", GeminiCLIUserAgent)
	req.Header.Set("x-goog-api-client", GeminiCLIAPIClient)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
}

// NewGeminiCLIExtraHeaders 创建 Gemini CLI 额外信息
func NewGeminiCLIExtraHeaders() *GeminiCLIExtraHeaders {
	return &GeminiCLIExtraHeaders{
		UserPromptID: generateGeminiUUID() + "########0",
		ProjectID:    GeminiCLIProjectID,
	}
}

// IsGeminiCLIPath 检查是否为 Gemini CLI 路径（/v1internal:）
func IsGeminiCLIPath(path string) bool {
	return strings.Contains(path, "/v1internal:")
}

// IsGeminiStandardPath 检查是否为标准 Gemini API 路径（/v1beta/）
func IsGeminiStandardPath(path string) bool {
	return strings.Contains(path, "/v1beta/")
}

// IsGeminiCLIEndpoint 检查是否为 Gemini CLI 内部端点（需要路径和请求体转换）
// cloudcode-pa.googleapis.com → 需要转换
// generativelanguage.googleapis.com → 标准 API，只需 Bearer 认证
func IsGeminiCLIEndpoint(url string) bool {
	return strings.Contains(url, "cloudcode-pa.googleapis.com")
}

// InjectGeminiOAuthHeaders 注入 Gemini OAuth 认证头（标准 API 模式）
// 用于 generativelanguage.googleapis.com + OAuth Token
func InjectGeminiOAuthHeaders(req *http.Request, accessToken string) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
}

// generateGeminiUUID 生成 UUID
func generateGeminiUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
