package app

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
)

// ============================================================================
// Codex 请求/响应格式转换
// ============================================================================

// CodexExtraHeaders Codex 请求需要的额外 Headers
type CodexExtraHeaders struct {
	AccountID      string
	ConversationID string
	SessionID      string
}

// TransformCodexRequestBody 仅转换请求体格式（不生成 headers）
// 用于在 Key 选择循环外预处理请求体，避免重复转换
func TransformCodexRequestBody(body []byte) ([]byte, error) {
	var req map[string]any
	if err := sonic.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parse request body: %w", err)
	}

	// 检查是否已有 instructions（如果有，可能是原生 Codex 请求）
	_, hasInstructions := req["instructions"]
	_, hasInput := req["input"]

	if hasInstructions && hasInput {
		// 已经是 Codex 格式，只做必要的调整
		req["stream"] = true
		req["store"] = false

		// 处理 temperature 和 top_p 冲突
		if _, hasTemp := req["temperature"]; hasTemp {
			delete(req, "top_p")
		}

		// 确保有 prompt_cache_key
		if _, ok := req["prompt_cache_key"]; !ok {
			req["prompt_cache_key"] = uuid.New().String()
		}

		// 确保有 reasoning
		if _, ok := req["reasoning"]; !ok {
			req["reasoning"] = map[string]any{
				"effort":  "xhigh",
				"summary": "auto",
			}
		}

		// 确保有 include
		if _, ok := req["include"]; !ok {
			req["include"] = []string{"reasoning.encrypted_content"}
		}

		return sonic.Marshal(req)
	}

	// 需要从 OpenAI 格式转换
	messages, ok := req["messages"].([]any)
	if !ok {
		return nil, fmt.Errorf("missing or invalid 'messages' field")
	}

	// 转换 messages 为 Codex input 格式
	input := convertMessagesToInput(messages)

	// 构建 Codex 请求
	codexReq := map[string]any{
		"stream":              true,
		"store":               false,
		"instructions":        defaultCodexInstructions,
		"input":               input,
		"tools":               defaultCodexTools,
		"tool_choice":         "auto",
		"parallel_tool_calls": false,
		"reasoning": map[string]any{
			"effort":  "xhigh",
			"summary": "auto",
		},
		"include":          []string{"reasoning.encrypted_content"},
		"prompt_cache_key": uuid.New().String(),
	}

	// 保留 model（如果有）
	if model, ok := req["model"].(string); ok {
		codexReq["model"] = model
	} else {
		codexReq["model"] = "gpt-5.1-codex-max"
	}

	// 保留 temperature（如果有）
	if temp, ok := req["temperature"]; ok {
		codexReq["temperature"] = temp
	}

	// 保留 max_tokens（如果有）
	if maxTokens, ok := req["max_tokens"]; ok {
		codexReq["max_tokens"] = maxTokens
	}

	return sonic.Marshal(codexReq)
}

// NewCodexExtraHeaders 创建 Codex 额外请求头（每次请求生成新的 UUID）
func NewCodexExtraHeaders(accountID string) *CodexExtraHeaders {
	return &CodexExtraHeaders{
		AccountID:      accountID,
		ConversationID: uuid.New().String(),
		SessionID:      uuid.New().String(),
	}
}

// TransformToCodexRequest 将 OpenAI 格式请求转换为 Codex 格式
// 如果请求已包含 instructions，则保留；否则补充默认值
// 返回: (transformedBody, extraHeaders, error)
func TransformToCodexRequest(body []byte, token *CodexOAuthToken) ([]byte, *CodexExtraHeaders, error) {
	var req map[string]any
	if err := sonic.Unmarshal(body, &req); err != nil {
		return nil, nil, fmt.Errorf("parse request body: %w", err)
	}

	// 1. 检查是否已有 instructions（如果有，可能是原生 Codex 请求）
	_, hasInstructions := req["instructions"]
	_, hasInput := req["input"]

	if hasInstructions && hasInput {
		// 已经是 Codex 格式，只做必要的调整
		return adjustCodexRequest(req, token)
	}

	// 2. 需要从 OpenAI 格式转换
	return convertOpenAIToCodex(req, token)
}

// adjustCodexRequest 调整已有的 Codex 格式请求
func adjustCodexRequest(req map[string]any, token *CodexOAuthToken) ([]byte, *CodexExtraHeaders, error) {
	// 强制设置必要字段
	req["stream"] = true
	req["store"] = false

	// 处理 temperature 和 top_p 冲突
	if _, hasTemp := req["temperature"]; hasTemp {
		delete(req, "top_p")
	}

	// 确保有 prompt_cache_key
	if _, ok := req["prompt_cache_key"]; !ok {
		req["prompt_cache_key"] = uuid.New().String()
	}

	// 确保有 reasoning
	if _, ok := req["reasoning"]; !ok {
		req["reasoning"] = map[string]any{
			"effort":  "xhigh",
			"summary": "auto",
		}
	}

	// 确保有 include
	if _, ok := req["include"]; !ok {
		req["include"] = []string{"reasoning.encrypted_content"}
	}

	// 构建额外 Headers
	headers := &CodexExtraHeaders{
		AccountID:      token.AccountID,
		ConversationID: uuid.New().String(),
		SessionID:      uuid.New().String(),
	}

	newBody, err := sonic.Marshal(req)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal adjusted request: %w", err)
	}

	return newBody, headers, nil
}

// convertOpenAIToCodex 将 OpenAI Chat Completions 格式转换为 Codex 格式
func convertOpenAIToCodex(req map[string]any, token *CodexOAuthToken) ([]byte, *CodexExtraHeaders, error) {
	// 提取 messages
	messages, ok := req["messages"].([]any)
	if !ok {
		return nil, nil, fmt.Errorf("missing or invalid 'messages' field")
	}

	// 转换 messages 为 Codex input 格式
	input := convertMessagesToInput(messages)

	// 构建 Codex 请求
	codexReq := map[string]any{
		"stream":           true,
		"store":            false,
		"instructions":     defaultCodexInstructions,
		"input":            input,
		"tools":            defaultCodexTools,
		"tool_choice":      "auto",
		"parallel_tool_calls": false,
		"reasoning": map[string]any{
			"effort":  "xhigh",
			"summary": "auto",
		},
		"include":          []string{"reasoning.encrypted_content"},
		"prompt_cache_key": uuid.New().String(),
	}

	// 保留 model（如果有）
	if model, ok := req["model"].(string); ok {
		codexReq["model"] = model
	} else {
		codexReq["model"] = "gpt-5.1-codex-max"
	}

	// 保留 temperature（如果有）
	if temp, ok := req["temperature"]; ok {
		codexReq["temperature"] = temp
	}

	// 保留 max_tokens（如果有）
	if maxTokens, ok := req["max_tokens"]; ok {
		codexReq["max_tokens"] = maxTokens
	}

	// 构建额外 Headers
	headers := &CodexExtraHeaders{
		AccountID:      token.AccountID,
		ConversationID: uuid.New().String(),
		SessionID:      uuid.New().String(),
	}

	newBody, err := sonic.Marshal(codexReq)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal codex request: %w", err)
	}

	return newBody, headers, nil
}

// convertMessagesToInput 将 OpenAI messages 格式转换为 Codex input 格式
func convertMessagesToInput(messages []any) []map[string]any {
	var input []map[string]any

	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}

		role, _ := msgMap["role"].(string)
		content, _ := msgMap["content"].(string)

		if role == "" || content == "" {
			continue
		}

		// 转换为 Codex input 格式
		inputMsg := map[string]any{
			"type": "message",
			"role": role,
			"content": []map[string]any{
				{
					"type": "input_text",
					"text": content,
				},
			},
		}

		input = append(input, inputMsg)
	}

	return input
}

// ============================================================================
// Codex SSE 响应转换
// ============================================================================

// CodexSSETransformer 将 Codex SSE 转换为 OpenAI SSE 格式
type CodexSSETransformer struct {
	buffer     bytes.Buffer
	eventType  string
	dataLines  []string
	totalUsage *CodexUsage
}

// CodexUsage Codex 响应中的 usage 统计
type CodexUsage struct {
	InputTokens  int
	OutputTokens int
}

// NewCodexSSETransformer 创建新的转换器
func NewCodexSSETransformer() *CodexSSETransformer {
	return &CodexSSETransformer{
		totalUsage: &CodexUsage{},
	}
}

// TransformChunk 转换一个 SSE 数据块
// 返回: 转换后的数据，如果不需要输出则返回 nil
func (t *CodexSSETransformer) TransformChunk(chunk []byte) []byte {
	t.buffer.Write(chunk)

	var output bytes.Buffer

	for {
		line, err := t.buffer.ReadString('\n')
		if err != nil {
			// 没有完整行，放回缓冲区
			t.buffer.WriteString(line)
			break
		}

		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")

		if line == "" {
			// 空行表示事件结束，处理当前事件
			if len(t.dataLines) > 0 {
				transformed := t.processEvent()
				if transformed != nil {
					output.Write(transformed)
				}
			}
			t.eventType = ""
			t.dataLines = nil
			continue
		}

		if strings.HasPrefix(line, "event:") {
			t.eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimPrefix(data, " ")
			t.dataLines = append(t.dataLines, data)
		}
	}

	if output.Len() > 0 {
		return output.Bytes()
	}
	return nil
}

// processEvent 处理单个 SSE 事件
func (t *CodexSSETransformer) processEvent() []byte {
	if len(t.dataLines) == 0 {
		return nil
	}

	dataStr := strings.Join(t.dataLines, "")
	if dataStr == "" {
		return nil
	}

	switch t.eventType {
	case "response.output_text.delta":
		return t.transformTextDelta(dataStr)
	case "response.output_text.done":
		// 文本完成，发送 [DONE]
		return []byte("data: [DONE]\n\n")
	case "response.completed":
		// 提取 usage 统计
		t.extractUsage(dataStr)
		return nil
	default:
		// 其他事件忽略
		return nil
	}
}

// transformTextDelta 转换文本增量事件
func (t *CodexSSETransformer) transformTextDelta(data string) []byte {
	var delta struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
	}

	if err := sonic.Unmarshal([]byte(data), &delta); err != nil {
		return nil
	}

	if delta.Delta == "" {
		return nil
	}

	// 转换为 OpenAI 格式
	openaiEvent := map[string]any{
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]string{
					"content": delta.Delta,
				},
			},
		},
	}

	jsonData, err := sonic.Marshal(openaiEvent)
	if err != nil {
		return nil
	}

	return []byte(fmt.Sprintf("data: %s\n\n", jsonData))
}

// extractUsage 从 response.completed 事件提取 usage
func (t *CodexSSETransformer) extractUsage(data string) {
	var completed struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := sonic.Unmarshal([]byte(data), &completed); err != nil {
		return
	}

	t.totalUsage.InputTokens = completed.Usage.InputTokens
	t.totalUsage.OutputTokens = completed.Usage.OutputTokens
}

// GetUsage 获取累计的 usage 统计
func (t *CodexSSETransformer) GetUsage() (inputTokens, outputTokens int) {
	return t.totalUsage.InputTokens, t.totalUsage.OutputTokens
}

// StreamCopyCodexSSE 流式复制并转换 Codex SSE 响应
func StreamCopyCodexSSE(ctx any, src io.Reader, dst io.Writer) (*CodexSSETransformer, error) {
	transformer := NewCodexSSETransformer()
	buf := make([]byte, SSEBufferSize)

	for {
		n, err := src.Read(buf)
		if n > 0 {
			transformed := transformer.TransformChunk(buf[:n])
			if transformed != nil {
				if _, wErr := dst.Write(transformed); wErr != nil {
					return transformer, wErr
				}
				// Flush if possible
				if flusher, ok := dst.(interface{ Flush() }); ok {
					flusher.Flush()
				}
			}
		}

		if err != nil {
			if err == io.EOF {
				return transformer, nil
			}
			return transformer, err
		}
	}
}

// ============================================================================
// 默认配置常量
// ============================================================================

// defaultCodexInstructions Codex 默认系统指令
// 当请求中没有 instructions 时使用
const defaultCodexInstructions = `You are Codex, a coding assistant. You help users with programming tasks.

When responding:
- Be concise and direct
- Provide working code examples
- Explain complex concepts clearly
- Follow best practices for the programming language being used`

// defaultCodexTools Codex 默认工具定义
var defaultCodexTools = []map[string]any{
	{
		"type": "function",
		"name": "shell_command",
		"description": "Runs a shell command and returns its output.",
		"strict": false,
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
