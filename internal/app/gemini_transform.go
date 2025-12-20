package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
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

// ============================================================================
// Gemini CLI 响应格式转换
// ============================================================================

// TransformGeminiCLISSEEvent 转换单个 Gemini CLI SSE 事件到标准格式
// 输入: {"response": {"candidates": [...], "usageMetadata": {...}}, "traceId": "..."}
// 输出: {"candidates": [...], "usageMetadata": {...}}
func TransformGeminiCLISSEEvent(data []byte) ([]byte, error) {
	var event map[string]any
	if err := sonic.Unmarshal(data, &event); err != nil {
		return nil, err
	}

	// 检查是否有 response 包装
	resp, hasResponse := event["response"].(map[string]any)
	if !hasResponse {
		// 没有 response 包装，直接返回原数据
		return data, nil
	}

	// 提取 response 内容作为新的顶层对象
	return sonic.Marshal(resp)
}

// GeminiCLISSETransformer Gemini CLI SSE 流转换器
// 将 cloudcode-pa 端点的响应格式转换为标准 Gemini API 格式
type GeminiCLISSETransformer struct {
	buffer      strings.Builder // SSE 事件缓冲区
	usageParser *sseUsageParser // 复用 SSE usage 解析器
}

// NewGeminiCLISSETransformer 创建 Gemini CLI SSE 转换器
func NewGeminiCLISSETransformer() *GeminiCLISSETransformer {
	return &GeminiCLISSETransformer{
		usageParser: newSSEUsageParser("gemini"),
	}
}

// GetUsageParser 获取内部的 usage 解析器
func (t *GeminiCLISSETransformer) GetUsageParser() *sseUsageParser {
	return t.usageParser
}

// StreamCopyGeminiCLISSE 流式复制并转换 Gemini CLI SSE 响应
// 将 cloudcode-pa 格式转换为标准 Gemini API 格式
// 输入格式: data: {"response": {"candidates": [...], "usageMetadata": {...}}, "traceId": "..."}
// 输出格式: data: {"candidates": [...], "usageMetadata": {...}}
func StreamCopyGeminiCLISSE(ctx context.Context, src io.Reader, dst http.ResponseWriter) (*GeminiCLISSETransformer, error) {
	transformer := NewGeminiCLISSETransformer()
	buf := make([]byte, SSEBufferSize) // 4KB SSE 缓冲区
	var lineBuf strings.Builder

	for {
		select {
		case <-ctx.Done():
			return transformer, ctx.Err()
		default:
		}

		n, err := src.Read(buf)
		if n > 0 {
			// 将数据追加到行缓冲区
			lineBuf.Write(buf[:n])

			// 处理完整的行
			for {
				content := lineBuf.String()
				idx := strings.Index(content, "\n")
				if idx == -1 {
					break
				}

				line := content[:idx]
				lineBuf.Reset()
				lineBuf.WriteString(content[idx+1:])

				// 处理 SSE 行
				transformed := transformer.processLine(line)

				// 写入转换后的数据
				if _, writeErr := dst.Write([]byte(transformed + "\n")); writeErr != nil {
					return transformer, writeErr
				}
			}

			// 刷新输出
			if flusher, ok := dst.(http.Flusher); ok {
				flusher.Flush()
			}
		}

		if err != nil {
			if err == io.EOF {
				// 处理剩余数据
				if lineBuf.Len() > 0 {
					transformed := transformer.processLine(lineBuf.String())
					if _, writeErr := dst.Write([]byte(transformed)); writeErr != nil {
						return transformer, writeErr
					}
				}
				return transformer, nil
			}
			if ctx.Err() != nil {
				return transformer, ctx.Err()
			}
			return transformer, err
		}
	}
}

// processLine 处理单行 SSE 数据
func (t *GeminiCLISSETransformer) processLine(line string) string {
	// 去除 \r
	line = strings.TrimSuffix(line, "\r")

	// 检查是否为 data: 行
	if !strings.HasPrefix(line, "data: ") {
		return line
	}

	// 提取 JSON 数据
	jsonData := strings.TrimPrefix(line, "data: ")
	if jsonData == "" {
		return line
	}

	// 喂入 usage 解析器（使用原始数据解析 usage）
	_ = t.usageParser.Feed([]byte(line + "\n\n"))

	// 转换 JSON
	transformed, err := TransformGeminiCLISSEEvent([]byte(jsonData))
	if err != nil {
		// 转换失败，返回原始数据
		return line
	}

	return "data: " + string(transformed)
}

// StreamCopyGeminiCLIJSON 复制并转换 Gemini CLI JSON 响应（非 SSE）
// 将 cloudcode-pa 格式转换为标准 Gemini API 格式
// 输入格式: {"response": {"candidates": [...], "usageMetadata": {...}}, "traceId": "..."}
// 输出格式: {"candidates": [...], "usageMetadata": {...}}
func StreamCopyGeminiCLIJSON(ctx context.Context, src io.Reader, dst http.ResponseWriter, requestURL string) (usageParser, error) {
	// 读取完整响应体
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}

	// 检查 context 是否被取消
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// 转换 JSON
	transformed, transformErr := TransformGeminiCLISSEEvent(data)
	if transformErr != nil {
		// 转换失败，返回原始数据
		transformed = data
	}

	// 写入转换后的数据
	if _, writeErr := dst.Write(transformed); writeErr != nil {
		return nil, writeErr
	}

	// 刷新输出
	if flusher, ok := dst.(http.Flusher); ok {
		flusher.Flush()
	}

	// 创建 JSON usage 解析器并解析转换后的数据
	parser := newJSONUsageParser("gemini", requestURL)
	_ = parser.Feed(transformed)

	return parser, nil
}
