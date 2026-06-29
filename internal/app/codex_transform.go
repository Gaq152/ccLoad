package app

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
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

	// 判断请求格式：有 input 字段即为 Codex/Responses 格式
	_, hasInput := req["input"]

	if hasInput {
		// 已经是 Codex/Responses 格式，做规范化调整
		normalizeCodexNativeRequest(req)
		return sonic.Marshal(req)
	}

	// 需要从 OpenAI Chat Completions 格式转换
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
		"parallel_tool_calls": true,
		"reasoning": map[string]any{
			"effort":  "medium",
			"summary": "auto",
		},
		"include":          []string{"reasoning.encrypted_content"},
		"prompt_cache_key": uuid.New().String(),
	}

	// 转换请求中的 tools
	if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
		codexReq["tools"] = convertToolsForCodex(tools)
	} else {
		codexReq["tools"] = defaultCodexTools
	}

	// 转换 tool_choice
	if tc, ok := req["tool_choice"]; ok {
		codexReq["tool_choice"] = convertToolChoiceForCodex(tc)
	} else {
		codexReq["tool_choice"] = "auto"
	}

	// 保留 model（如果有）
	if model, ok := req["model"].(string); ok {
		codexReq["model"] = model
	} else {
		codexReq["model"] = "gpt-5.1-codex-max"
	}

	// 保留 reasoning_effort（如果有）
	if effort, ok := req["reasoning_effort"].(string); ok {
		codexReq["reasoning"] = map[string]any{
			"effort":  effort,
			"summary": "auto",
		}
	}

	return sonic.Marshal(codexReq)
}

// NewCodexExtraHeaders 创建 Codex 额外请求头。
// 原始请求已经携带会话标识时优先透传，避免破坏 Codex 服务端的会话级缓存路由。
func NewCodexExtraHeaders(accountID string, source ...http.Header) *CodexExtraHeaders {
	headers := &CodexExtraHeaders{AccountID: accountID}
	if len(source) > 0 && source[0] != nil {
		headers.ConversationID = firstCodexHeader(source[0], "conversation_id", "conversation-id")
		headers.SessionID = firstCodexHeader(source[0], "session_id", "session-id")
	}
	if headers.ConversationID == "" {
		headers.ConversationID = uuid.New().String()
	}
	if headers.SessionID == "" {
		headers.SessionID = uuid.New().String()
	}
	return headers
}

func firstCodexHeader(h http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(h.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

// TransformToCodexRequest 将 OpenAI 格式请求转换为 Codex 格式
// 如果请求已包含 instructions，则保留；否则补充默认值
// 返回: (transformedBody, extraHeaders, error)
func TransformToCodexRequest(body []byte, token *CodexOAuthToken) ([]byte, *CodexExtraHeaders, error) {
	var req map[string]any
	if err := sonic.Unmarshal(body, &req); err != nil {
		return nil, nil, fmt.Errorf("parse request body: %w", err)
	}

	// 判断请求格式：有 input 字段即为 Codex/Responses 格式
	_, hasInput := req["input"]

	if hasInput {
		// 已经是 Codex/Responses 格式，只做规范化调整
		return adjustCodexRequest(req, token)
	}

	// 需要从 OpenAI Chat Completions 格式转换
	return convertOpenAIToCodex(req, token)
}

// adjustCodexRequest 调整已有的 Codex 格式请求（official 预设路径）
func adjustCodexRequest(req map[string]any, token *CodexOAuthToken) ([]byte, *CodexExtraHeaders, error) {
	normalizeCodexNativeRequest(req)

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

// normalizeCodexNativeRequest 对已经是 Codex/Responses API 格式的请求做规范化
// 包括：强制字段、删除不支持字段、input 中 system→developer、工具类型规范化
func normalizeCodexNativeRequest(req map[string]any) {
	// 强制设置必要字段
	req["stream"] = true
	req["store"] = false
	req["parallel_tool_calls"] = true

	// 删除 Codex 不支持的字段
	for _, field := range []string{
		"max_output_tokens", "max_completion_tokens",
		"temperature", "top_p", "truncation",
		"context_management", "user",
	} {
		delete(req, field)
	}

	// 确保有 instructions
	if _, ok := req["instructions"]; !ok {
		req["instructions"] = ""
	}

	// 确保有 prompt_cache_key
	if _, ok := req["prompt_cache_key"]; !ok {
		req["prompt_cache_key"] = uuid.New().String()
	}

	// 确保有 reasoning
	if _, ok := req["reasoning"]; !ok {
		req["reasoning"] = map[string]any{
			"effort":  "medium",
			"summary": "auto",
		}
	}

	// 确保有 include
	if _, ok := req["include"]; !ok {
		req["include"] = []string{"reasoning.encrypted_content"}
	}

	// service_tier 只保留 "priority" 值
	if tier, ok := req["service_tier"]; ok {
		if s, ok := tier.(string); !ok || s != "priority" {
			delete(req, "service_tier")
		}
	}

	// 规范化 input 中的 system → developer
	normalizeCodexInput(req)

	// 规范化工具类型（web_search_preview → web_search）
	normalizeCodexTools(req)
}

// normalizeCodexInput 规范化 Codex input 数组：system → developer
func normalizeCodexInput(req map[string]any) {
	input, ok := req["input"].([]any)
	if !ok {
		// input 可能是字符串（单条消息简写）
		if s, ok := req["input"].(string); ok && s != "" {
			req["input"] = []map[string]any{
				{
					"type": "message",
					"role": "user",
					"content": []map[string]any{
						{"type": "input_text", "text": s},
					},
				},
			}
		}
		return
	}

	for _, item := range input {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := itemMap["role"].(string); role == "system" {
			itemMap["role"] = "developer"
		}
	}
}

// normalizeCodexTools 规范化工具类型名称（web_search_preview → web_search）
func normalizeCodexTools(req map[string]any) {
	tools, ok := req["tools"].([]any)
	if !ok {
		return
	}
	for _, t := range tools {
		tMap, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if toolType, _ := tMap["type"].(string); toolType != "" {
			if normalized := normalizeBuiltinToolType(toolType); normalized != "" {
				tMap["type"] = normalized
			}
		}
	}

	// 同时规范化 tool_choice 中的类型
	if tc, ok := req["tool_choice"].(map[string]any); ok {
		if toolType, _ := tc["type"].(string); toolType != "" {
			if normalized := normalizeBuiltinToolType(toolType); normalized != "" {
				tc["type"] = normalized
			}
		}
	}
}

// normalizeBuiltinToolType 将预览版工具类型转换为稳定名称
func normalizeBuiltinToolType(toolType string) string {
	switch toolType {
	case "web_search_preview", "web_search_preview_2025_03_11":
		return "web_search"
	default:
		return ""
	}
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
		"stream":              true,
		"store":               false,
		"instructions":        defaultCodexInstructions,
		"input":               input,
		"parallel_tool_calls": true,
		"reasoning": map[string]any{
			"effort":  "medium",
			"summary": "auto",
		},
		"include":          []string{"reasoning.encrypted_content"},
		"prompt_cache_key": uuid.New().String(),
	}

	// 转换请求中的 tools（展平 function 嵌套）
	if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
		codexReq["tools"] = convertToolsForCodex(tools)
	} else {
		codexReq["tools"] = defaultCodexTools
	}

	// 转换 tool_choice
	if tc, ok := req["tool_choice"]; ok {
		codexReq["tool_choice"] = convertToolChoiceForCodex(tc)
	} else {
		codexReq["tool_choice"] = "auto"
	}

	// 保留 model（如果有）
	if model, ok := req["model"].(string); ok {
		codexReq["model"] = model
	} else {
		codexReq["model"] = "gpt-5.1-codex-max"
	}

	// 保留 reasoning_effort（如果有）
	if effort, ok := req["reasoning_effort"].(string); ok {
		codexReq["reasoning"] = map[string]any{
			"effort":  effort,
			"summary": "auto",
		}
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

// convertToolsForCodex 将 OpenAI Chat Completions 工具声明展平为 Codex 格式
// OpenAI: {"type":"function","function":{"name":"...","parameters":{...}}}
// Codex:  {"type":"function","name":"...","parameters":{...}}
func convertToolsForCodex(tools []any) []map[string]any {
	var result []map[string]any
	for _, t := range tools {
		tMap, ok := t.(map[string]any)
		if !ok {
			continue
		}

		toolType, _ := tMap["type"].(string)

		// 非 function 类型（如 web_search）直接透传
		if toolType != "" && toolType != "function" {
			result = append(result, tMap)
			continue
		}

		if toolType == "function" {
			fnMap, _ := tMap["function"].(map[string]any)
			if fnMap == nil {
				continue
			}
			item := map[string]any{"type": "function"}
			if name, ok := fnMap["name"].(string); ok {
				item["name"] = shortenToolName(name)
			}
			if desc, ok := fnMap["description"]; ok {
				item["description"] = desc
			}
			if params, ok := fnMap["parameters"]; ok {
				item["parameters"] = params
			}
			if strict, ok := fnMap["strict"]; ok {
				item["strict"] = strict
			}
			result = append(result, item)
		}
	}
	return result
}

// convertToolChoiceForCodex 转换 tool_choice 为 Codex 格式
// 字符串直接保留，对象格式需要展平 function 嵌套
func convertToolChoiceForCodex(tc any) any {
	// 字符串格式（"auto"/"none"）直接返回
	if s, ok := tc.(string); ok {
		return s
	}
	// 对象格式
	if tcMap, ok := tc.(map[string]any); ok {
		tcType, _ := tcMap["type"].(string)
		if tcType == "function" {
			if fnMap, ok := tcMap["function"].(map[string]any); ok {
				name, _ := fnMap["name"].(string)
				result := map[string]any{"type": "function"}
				if name != "" {
					result["name"] = shortenToolName(name)
				}
				return result
			}
		}
		return tcMap
	}
	return "auto"
}

// convertMessagesToInput 将 OpenAI messages 格式转换为 Codex input 格式
// 处理所有消息类型：system, user, assistant（含 tool_calls）, tool
func convertMessagesToInput(messages []any) []map[string]any {
	var input []map[string]any

	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}

		role, _ := msgMap["role"].(string)
		if role == "" {
			continue
		}

		// tool 角色 → 顶级 function_call_output 对象
		if role == "tool" {
			callID, _ := msgMap["tool_call_id"].(string)
			content, _ := msgMap["content"].(string)
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  content,
			})
			continue
		}

		// system → developer
		codexRole := role
		if role == "system" {
			codexRole = "developer"
		}

		// 构建消息内容
		var contentParts []map[string]any
		contentParts = appendContentParts(contentParts, role, msgMap["content"])

		// 只有有内容时才添加 message 对象（避免空 assistant 消息导致 call_id 匹配失败）
		if len(contentParts) > 0 {
			input = append(input, map[string]any{
				"type":    "message",
				"role":    codexRole,
				"content": contentParts,
			})
		}

		// assistant 消息的 tool_calls → 顶级 function_call 对象
		if role == "assistant" {
			if toolCalls, ok := msgMap["tool_calls"].([]any); ok {
				for _, tc := range toolCalls {
					tcMap, ok := tc.(map[string]any)
					if !ok {
						continue
					}
					callID, _ := tcMap["id"].(string)
					fnMap, _ := tcMap["function"].(map[string]any)
					if fnMap == nil {
						continue
					}
					fnName, _ := fnMap["name"].(string)
					fnArgs, _ := fnMap["arguments"].(string)

					fnName = shortenToolName(fnName)

					input = append(input, map[string]any{
						"type":      "function_call",
						"call_id":   callID,
						"name":      fnName,
						"arguments": fnArgs,
					})
				}
			}
		}
	}

	return input
}

// appendContentParts 从 OpenAI content 字段提取内容，支持字符串和数组两种格式
func appendContentParts(parts []map[string]any, role string, content any) []map[string]any {
	if content == nil {
		return parts
	}

	// 字符串格式
	if s, ok := content.(string); ok && s != "" {
		partType := "input_text"
		if role == "assistant" {
			partType = "output_text"
		}
		return append(parts, map[string]any{
			"type": partType,
			"text": s,
		})
	}

	// 数组格式（多模态内容）
	arr, ok := content.([]any)
	if !ok {
		return parts
	}

	for _, item := range arr {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		itemType, _ := itemMap["type"].(string)

		switch itemType {
		case "text":
			text, _ := itemMap["text"].(string)
			if text == "" {
				continue
			}
			partType := "input_text"
			if role == "assistant" {
				partType = "output_text"
			}
			parts = append(parts, map[string]any{
				"type": partType,
				"text": text,
			})
		case "image_url":
			if role == "user" {
				if imgURL, ok := itemMap["image_url"].(map[string]any); ok {
					if url, _ := imgURL["url"].(string); url != "" {
						parts = append(parts, map[string]any{
							"type":      "input_image",
							"image_url": url,
						})
					}
				}
			}
		}
	}

	return parts
}

// shortenToolName 缩短工具名称，Codex 限制 64 字符
func shortenToolName(name string) string {
	const limit = 64
	if len(name) <= limit {
		return name
	}
	if strings.HasPrefix(name, "mcp__") {
		idx := strings.LastIndex(name, "__")
		if idx > 0 {
			candidate := "mcp__" + name[idx+2:]
			if len(candidate) > limit {
				return candidate[:limit]
			}
			return candidate
		}
	}
	return name[:limit]
}

// ============================================================================
// Codex SSE 响应转换
// ============================================================================

// CodexSSETransformer 将 Codex SSE 转换为 OpenAI Chat Completions SSE 格式
// 支持事件类型：文本流、reasoning、function_call（tool_calls）、usage
type CodexSSETransformer struct {
	buffer     bytes.Buffer
	eventType  string
	dataLines  []string
	totalUsage *CodexUsage

	// 响应元数据（从 response.created 事件提取）
	responseID string
	createdAt  int64
	model      string

	// function_call 状态追踪
	functionCallIndex         int
	hasReceivedArgumentsDelta bool
	hasToolCallAnnounced      bool
	hasToolCalls              bool
}

// CodexUsage Codex 响应中的 usage 统计
type CodexUsage struct {
	InputTokens  int
	OutputTokens int
}

// NewCodexSSETransformer 创建新的转换器
func NewCodexSSETransformer() *CodexSSETransformer {
	return &CodexSSETransformer{
		totalUsage:        &CodexUsage{},
		functionCallIndex: -1,
	}
}

// TransformChunk 转换一个 SSE 数据块
func (t *CodexSSETransformer) TransformChunk(chunk []byte) []byte {
	t.buffer.Write(chunk)

	var output bytes.Buffer

	for {
		line, err := t.buffer.ReadString('\n')
		if err != nil {
			t.buffer.WriteString(line)
			break
		}

		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")

		if line == "" {
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

// processEvent 处理单个 SSE 事件，根据事件类型分发
func (t *CodexSSETransformer) processEvent() []byte {
	if len(t.dataLines) == 0 {
		return nil
	}

	dataStr := strings.Join(t.dataLines, "")
	if dataStr == "" {
		return nil
	}

	// 优先从 event: 行获取事件类型，回退到 data JSON 的 type 字段
	eventType := t.eventType
	if eventType == "" {
		var typeHolder struct {
			Type string `json:"type"`
		}
		if sonic.Unmarshal([]byte(dataStr), &typeHolder) == nil {
			eventType = typeHolder.Type
		}
	}

	switch eventType {
	case "response.created":
		return t.handleCreated(dataStr)
	case "response.output_text.delta":
		return t.transformTextDelta(dataStr)
	case "response.output_text.done":
		return nil // 文本结束，不需要额外输出
	case "response.reasoning_summary_text.delta":
		return t.transformReasoningDelta(dataStr)
	case "response.reasoning_summary_text.done":
		return t.emitSSE(map[string]any{
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{
					"role":              "assistant",
					"reasoning_content": "\n\n",
				},
			}},
		})
	case "response.output_item.added":
		return t.handleOutputItemAdded(dataStr)
	case "response.function_call_arguments.delta":
		return t.handleFunctionCallArgsDelta(dataStr)
	case "response.function_call_arguments.done":
		return t.handleFunctionCallArgsDone(dataStr)
	case "response.output_item.done":
		return t.handleOutputItemDone(dataStr)
	case "response.completed":
		return t.handleCompleted(dataStr)
	default:
		return nil
	}
}

// handleCreated 处理 response.created 事件，提取响应元数据
func (t *CodexSSETransformer) handleCreated(data string) []byte {
	var created struct {
		Response struct {
			ID        string `json:"id"`
			CreatedAt int64  `json:"created_at"`
			Model     string `json:"model"`
		} `json:"response"`
	}
	if sonic.Unmarshal([]byte(data), &created) == nil {
		t.responseID = created.Response.ID
		t.createdAt = created.Response.CreatedAt
		t.model = created.Response.Model
	}
	return nil
}

// transformTextDelta 转换文本增量事件
func (t *CodexSSETransformer) transformTextDelta(data string) []byte {
	var delta struct {
		Delta string `json:"delta"`
	}
	if err := sonic.Unmarshal([]byte(data), &delta); err != nil || delta.Delta == "" {
		return nil
	}
	return t.emitSSE(map[string]any{
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"role":    "assistant",
				"content": delta.Delta,
			},
		}},
	})
}

// transformReasoningDelta 转换推理内容增量事件
func (t *CodexSSETransformer) transformReasoningDelta(data string) []byte {
	var delta struct {
		Delta string `json:"delta"`
	}
	if err := sonic.Unmarshal([]byte(data), &delta); err != nil || delta.Delta == "" {
		return nil
	}
	return t.emitSSE(map[string]any{
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"role":              "assistant",
				"reasoning_content": delta.Delta,
			},
		}},
	})
}

// handleOutputItemAdded 处理新的输出项（function_call 宣布）
func (t *CodexSSETransformer) handleOutputItemAdded(data string) []byte {
	var event struct {
		Item struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"item"`
	}
	if err := sonic.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}
	if event.Item.Type != "function_call" {
		return nil
	}

	t.functionCallIndex++
	t.hasReceivedArgumentsDelta = false
	t.hasToolCallAnnounced = true
	t.hasToolCalls = true

	toolCall := map[string]any{
		"index": t.functionCallIndex,
		"id":    event.Item.CallID,
		"type":  "function",
		"function": map[string]any{
			"name":      event.Item.Name,
			"arguments": "",
		},
	}

	return t.emitSSE(map[string]any{
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"role":       "assistant",
				"tool_calls": []map[string]any{toolCall},
			},
		}},
	})
}

// handleFunctionCallArgsDelta 处理 function_call 参数增量
func (t *CodexSSETransformer) handleFunctionCallArgsDelta(data string) []byte {
	t.hasReceivedArgumentsDelta = true

	var event struct {
		Delta string `json:"delta"`
	}
	if err := sonic.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}

	toolCall := map[string]any{
		"index": t.functionCallIndex,
		"function": map[string]any{
			"arguments": event.Delta,
		},
	}

	return t.emitSSE(map[string]any{
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []map[string]any{toolCall},
			},
		}},
	})
}

// handleFunctionCallArgsDone 处理 function_call 参数完成
func (t *CodexSSETransformer) handleFunctionCallArgsDone(data string) []byte {
	if t.hasReceivedArgumentsDelta {
		// 参数已通过 delta 事件流式发送，无需重复
		return nil
	}

	// 回退路径：没有收到 delta 事件，一次性发出完整参数
	var event struct {
		Arguments string `json:"arguments"`
	}
	if err := sonic.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}

	toolCall := map[string]any{
		"index": t.functionCallIndex,
		"function": map[string]any{
			"arguments": event.Arguments,
		},
	}

	return t.emitSSE(map[string]any{
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []map[string]any{toolCall},
			},
		}},
	})
}

// handleOutputItemDone 处理输出项完成（function_call 的回退路径）
func (t *CodexSSETransformer) handleOutputItemDone(data string) []byte {
	var event struct {
		Item struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"item"`
	}
	if err := sonic.Unmarshal([]byte(data), &event); err != nil {
		return nil
	}
	if event.Item.Type != "function_call" {
		return nil
	}

	if t.hasToolCallAnnounced {
		// 已通过 output_item.added 宣布过，跳过
		t.hasToolCallAnnounced = false
		return nil
	}

	// 回退：上游跳过了 output_item.added，一次性发出完整 tool_call
	t.functionCallIndex++
	t.hasToolCalls = true

	toolCall := map[string]any{
		"index": t.functionCallIndex,
		"id":    event.Item.CallID,
		"type":  "function",
		"function": map[string]any{
			"name":      event.Item.Name,
			"arguments": event.Item.Arguments,
		},
	}

	return t.emitSSE(map[string]any{
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"role":       "assistant",
				"tool_calls": []map[string]any{toolCall},
			},
		}},
	})
}

// handleCompleted 处理 response.completed 事件
func (t *CodexSSETransformer) handleCompleted(data string) []byte {
	t.extractUsage(data)

	finishReason := "stop"
	if t.hasToolCalls {
		finishReason = "tool_calls"
	}

	// 发送带 finish_reason 的最终 chunk
	result := t.emitSSE(map[string]any{
		"choices": []map[string]any{{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": finishReason,
		}},
	})

	// 追加 [DONE]
	done := []byte("data: [DONE]\n\n")
	if result != nil {
		return append(result, done...)
	}
	return done
}

// emitSSE 将 OpenAI 格式事件序列化为 SSE data 行
func (t *CodexSSETransformer) emitSSE(event map[string]any) []byte {
	// 填充响应元数据
	if t.responseID != "" {
		event["id"] = t.responseID
	}
	if t.model != "" {
		event["model"] = t.model
	}
	if t.createdAt > 0 {
		event["created"] = t.createdAt
	}
	event["object"] = "chat.completion.chunk"

	jsonData, err := sonic.Marshal(event)
	if err != nil {
		return nil
	}
	return []byte(fmt.Sprintf("data: %s\n\n", jsonData))
}

// extractUsage 从 response.completed 事件提取 usage
func (t *CodexSSETransformer) extractUsage(data string) {
	var completed struct {
		Response struct {
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"response"`
	}
	if err := sonic.Unmarshal([]byte(data), &completed); err != nil {
		return
	}
	t.totalUsage.InputTokens = completed.Response.Usage.InputTokens
	t.totalUsage.OutputTokens = completed.Response.Usage.OutputTokens
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
