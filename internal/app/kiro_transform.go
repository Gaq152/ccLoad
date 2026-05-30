package app

import (
	"fmt"
	"log"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
)

// ============================================================================
// Kiro 请求转换
// 将 Anthropic Messages API 请求转换为 CodeWhisperer 格式
// 参考: https://github.com/nineyuanz/kiro2api/blob/main/converter/codewhisperer.go
// ============================================================================

// 工具描述最大长度（kiro.rs 使用 10000）
const KiroMaxToolDescriptionLength = 10000

// 工具名称最大长度（Kiro API 限制）
const KiroMaxToolNameLength = 63

// TransformToKiroRequest 将 Anthropic 请求体转换为 Kiro (CodeWhisperer) 格式
// 输入: Anthropic Messages API 格式的请求体 (JSON bytes)
// 输出: CodeWhisperer 格式的请求体 (JSON bytes)
func TransformToKiroRequest(anthropicBody []byte) ([]byte, error) {
	// 解析 Anthropic 请求
	var anthropicReq map[string]any
	if err := sonic.Unmarshal(anthropicBody, &anthropicReq); err != nil {
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}

	// 提取模型并映射
	model, _ := anthropicReq["model"].(string)
	modelId := GetKiroModelId(model)
	if modelId == "" {
		return nil, fmt.Errorf("unsupported model for Kiro: %s", model)
	}

	// 构建 Kiro 请求
	kiroReq := &KiroRequest{}

	// 设置会话状态
	kiroReq.ConversationState.AgentContinuationId = uuid.New().String()
	kiroReq.ConversationState.AgentTaskType = "vibe"
	kiroReq.ConversationState.ConversationId = uuid.New().String()

	// ChatTriggerType 始终使用 MANUAL
	// kiro.rs 注释: "AUTO" 模式可能会导致 400 Bad Request 错误
	tools, _ := anthropicReq["tools"].([]any)
	kiroReq.ConversationState.ChatTriggerType = "MANUAL"

	// 提取消息
	messages, _ := anthropicReq["messages"].([]any)
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages is empty")
	}

	// 预处理 prefill：如果末尾是 assistant 消息，截断到最后一条 user 消息
	// Claude 4.x 已弃用 assistant prefill，Kiro API 也不支持
	if lastMsg, ok := messages[len(messages)-1].(map[string]any); ok {
		if role, _ := lastMsg["role"].(string); role != "user" {
			// 找到最后一条 user 消息
			lastUserIdx := -1
			for i := len(messages) - 1; i >= 0; i-- {
				if m, ok := messages[i].(map[string]any); ok {
					if r, _ := m["role"].(string); r == "user" {
						lastUserIdx = i
						break
					}
				}
			}
			if lastUserIdx < 0 {
				return nil, fmt.Errorf("no user message found")
			}
			log.Printf("[DEBUG] [Kiro] 截断末尾 assistant 消息（prefill），从 %d 条截断到 %d 条", len(messages), lastUserIdx+1)
			messages = messages[:lastUserIdx+1]
		}
	}

	// 处理最后一条消息作为当前消息
	lastMessage, ok := messages[len(messages)-1].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid last message format")
	}

	// 提取当前消息内容和图片
	textContent, images, toolResults := processKiroMessageContent(lastMessage["content"])

	// 设置当前消息
	kiroReq.ConversationState.CurrentMessage.UserInputMessage.Content = textContent
	kiroReq.ConversationState.CurrentMessage.UserInputMessage.ModelId = modelId
	kiroReq.ConversationState.CurrentMessage.UserInputMessage.Origin = "AI_EDITOR"

	if len(images) > 0 {
		kiroReq.ConversationState.CurrentMessage.UserInputMessage.Images = images
	}

	// 如果有工具结果，设置到上下文中（保留文本内容，kiro.rs 也不清空）
	if len(toolResults) > 0 {
		kiroReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults = toolResults
	}

	// 处理工具定义（含 JSON Schema 规范化）
	if len(tools) > 0 {
		if kiroTools := convertKiroTools(tools); len(kiroTools) > 0 {
			kiroReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = kiroTools
		}
	}

	// 构建历史消息（thinking 通过 XML 标签注入系统消息）
	history := buildKiroHistory(anthropicReq, messages, modelId)

	// 验证 tool_use/tool_result 配对
	// Kiro API 要求每个 tool_use 必须有对应的 tool_result，否则返回 400 Bad Request
	if len(history) > 0 {
		toolResults = validateAndFilterToolPairing(history, toolResults)

		// 清理历史：移除因过滤产生的空消息（如 web_search 过滤后的残留）
		history = cleanupKiroHistory(history)
		kiroReq.ConversationState.History = history

		// 更新当前消息中的 tool_results（过滤后可能数量变化）
		if len(toolResults) > 0 {
			kiroReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults = toolResults
		} else {
			kiroReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults = nil
		}
	}

	// 为历史中引用但不在当前 tools 列表的工具创建占位符定义
	// Kiro API 要求：历史消息中引用的工具必须在 tools 列表中有定义
	ensureHistoryToolDefinitions(history, &kiroReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext)

	// 打印转换摘要（帮助调试 400 错误）
	toolCount := len(kiroReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools)
	trCount := len(kiroReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults)
	histCount := len(kiroReq.ConversationState.History)
	contentLen := len(kiroReq.ConversationState.CurrentMessage.UserInputMessage.Content)
	log.Printf("[DEBUG] [Kiro] 转换摘要: model=%s, historyMsgs=%d, tools=%d, currentContent=%d chars, currentToolResults=%d, thinking=%v",
		modelId, histCount, toolCount, contentLen, trCount, generateThinkingPrefix(anthropicReq) != "")

	// 序列化
	return sonic.Marshal(kiroReq)
}

// generateThinkingPrefix 生成 thinking XML 标签前缀（注入到系统消息中）
// kiro.rs 不使用 inferenceConfiguration，而是通过 XML 标签在系统消息中传递 thinking 配置
func generateThinkingPrefix(req map[string]any) string {
	thinking, ok := req["thinking"].(map[string]any)
	if !ok {
		return ""
	}

	thinkingType, _ := thinking["type"].(string)
	switch thinkingType {
	case "enabled":
		budgetTokens := 0
		if bt, ok := thinking["budget_tokens"].(float64); ok {
			budgetTokens = int(bt)
		}
		return fmt.Sprintf("<thinking_mode>enabled</thinking_mode><max_thinking_length>%d</max_thinking_length>", budgetTokens)
	case "adaptive":
		effort := "high"
		if oc, ok := req["output_config"].(map[string]any); ok {
			if e, ok := oc["effort"].(string); ok {
				effort = e
			}
		}
		return fmt.Sprintf("<thinking_mode>adaptive</thinking_mode><thinking_effort>%s</thinking_effort>", effort)
	}
	return ""
}

// processKiroMessageContent 处理消息内容，提取文本、图片和工具结果
func processKiroMessageContent(content any) (string, []KiroImage, []KiroToolResult) {
	var textParts []string
	var images []KiroImage
	var toolResults []KiroToolResult

	switch c := content.(type) {
	case string:
		return c, nil, nil

	case []any:
		for _, item := range c {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}

			blockType, _ := block["type"].(string)
			switch blockType {
			case "text":
				if text, ok := block["text"].(string); ok {
					textParts = append(textParts, text)
				}

			case "image":
				// 处理 Anthropic 格式的图片
				if source, ok := block["source"].(map[string]any); ok {
					if data, ok := source["data"].(string); ok {
						mediaType := getStringOrDefault(source, "media_type", "")
						format := convertMediaTypeToFormat(mediaType)
						if format != "" {
							img := KiroImage{Format: format}
							img.Source.Bytes = data
							images = append(images, img)
						}
					}
				}

			case "image_url":
				// 处理 OpenAI 格式的 image_url (data URL)
				if imageURL, ok := block["image_url"].(map[string]any); ok {
					if url, ok := imageURL["url"].(string); ok {
						if img := parseDataURLToKiroImage(url); img != nil {
							images = append(images, *img)
						}
					}
				}

			case "tool_result":
				// 处理工具结果
				toolResult := KiroToolResult{
					Status: "success",
				}
				if toolUseId, ok := block["tool_use_id"].(string); ok {
					toolResult.ToolUseId = toolUseId
				}
				if isError, ok := block["is_error"].(bool); ok && isError {
					toolResult.Status = "error"
					toolResult.IsError = true
				}

				// 处理 content
				toolResult.Content = extractToolResultContent(block["content"])
				toolResults = append(toolResults, toolResult)
			}
		}
	}

	return strings.TrimSpace(strings.Join(textParts, "\n")), images, toolResults
}

// extractToolResultContent 提取工具结果内容
func extractToolResultContent(content any) []map[string]any {
	var result []map[string]any

	switch c := content.(type) {
	case string:
		result = []map[string]any{{"text": c}}
	case []any:
		for _, item := range c {
			if m, ok := item.(map[string]any); ok {
				result = append(result, m)
			}
		}
	case map[string]any:
		result = []map[string]any{c}
	default:
		if c != nil {
			result = []map[string]any{{"text": fmt.Sprintf("%v", c)}}
		}
	}

	return result
}

// convertKiroTools 转换工具定义（含 JSON Schema 规范化）
func convertKiroTools(tools []any) []KiroTool {
	var kiroTools []KiroTool

	for _, tool := range tools {
		toolMap, ok := tool.(map[string]any)
		if !ok {
			continue
		}

		name, _ := toolMap["name"].(string)
		if name == "" {
			continue
		}

		// 过滤不支持的工具
		if name == "web_search" || name == "websearch" {
			continue
		}

		// 工具名称截断（Kiro API 限制 63 字符）
		if len(name) > KiroMaxToolNameLength {
			name = name[:KiroMaxToolNameLength]
		}

		description, _ := toolMap["description"].(string)
		inputSchema := toolMap["input_schema"]

		kiroTool := KiroTool{}
		kiroTool.ToolSpecification.Name = name
		kiroTool.ToolSpecification.Description = truncateKiroDescription(description)
		kiroTool.ToolSpecification.InputSchema = KiroInputSchema{
			Json: normalizeJsonSchema(inputSchema),
		}

		kiroTools = append(kiroTools, kiroTool)
	}

	return kiroTools
}

// normalizeJsonSchema 规范化 JSON Schema，修复 MCP 工具定义中常见的类型问题
// Claude Code / MCP 工具定义偶尔会出现 required: null、properties: null 等，
// 导致 Kiro API 返回 400 "Improperly formed request"
func normalizeJsonSchema(schema any) any {
	obj, ok := schema.(map[string]any)
	if !ok {
		// 非 object 类型，返回空的 object schema
		return map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"required":             []any{},
			"additionalProperties": true,
		}
	}

	// type 必须是字符串
	if t, ok := obj["type"].(string); !ok || t == "" {
		obj["type"] = "object"
	}

	// properties 必须是 object
	if _, ok := obj["properties"].(map[string]any); !ok {
		obj["properties"] = map[string]any{}
	}

	// required 必须是字符串数组（null / 缺失 → 空数组）
	switch r := obj["required"].(type) {
	case []any:
		// 过滤非字符串元素
		var cleaned []any
		for _, item := range r {
			if s, ok := item.(string); ok {
				cleaned = append(cleaned, s)
			}
		}
		if cleaned == nil {
			cleaned = []any{}
		}
		obj["required"] = cleaned
	default:
		obj["required"] = []any{}
	}

	// additionalProperties 允许 bool 或 object，其他值设为 true
	switch obj["additionalProperties"].(type) {
	case bool, map[string]any:
		// OK
	default:
		obj["additionalProperties"] = true
	}

	return obj
}

// ensureHistoryToolDefinitions 为历史中引用但不在当前 tools 列表的工具创建占位符
// Kiro API 要求：历史消息中引用的工具必须在 currentMessage.tools 中有定义
func ensureHistoryToolDefinitions(history []any, ctx *KiroUserInputMessageContext) {
	// 收集历史中使用的所有工具名称
	historyToolNames := make(map[string]bool)
	for _, msg := range history {
		if aMsg, ok := msg.(KiroHistoryAssistantMessage); ok {
			for _, tu := range aMsg.AssistantResponseMessage.ToolUses {
				if tu.Name != "" {
					historyToolNames[strings.ToLower(tu.Name)] = true
				}
			}
		}
	}

	if len(historyToolNames) == 0 {
		return
	}

	// 收集当前 tools 中已有的名称
	existingNames := make(map[string]bool)
	for _, t := range ctx.Tools {
		existingNames[strings.ToLower(t.ToolSpecification.Name)] = true
	}

	// 为缺失的工具创建占位符
	for name := range historyToolNames {
		if !existingNames[name] {
			placeholder := KiroTool{}
			placeholder.ToolSpecification.Name = name
			placeholder.ToolSpecification.Description = "Tool used in conversation history"
			placeholder.ToolSpecification.InputSchema = KiroInputSchema{
				Json: map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"required":             []any{},
					"additionalProperties": true,
				},
			}
			ctx.Tools = append(ctx.Tools, placeholder)
			log.Printf("[INFO] [Kiro] 为历史中引用的工具创建占位符定义: %s", name)
		}
	}
}

// truncateKiroDescription 截断工具描述
func truncateKiroDescription(description string) string {
	if len(description) <= KiroMaxToolDescriptionLength {
		return description
	}
	if KiroMaxToolDescriptionLength > 3 {
		return description[:KiroMaxToolDescriptionLength-3] + "..."
	}
	return description[:KiroMaxToolDescriptionLength]
}

// buildKiroHistory 构建历史消息
func buildKiroHistory(req map[string]any, messages []any, modelId string) []any {
	var history []any

	// 生成 thinking XML 标签前缀（如果需要）
	thinkingPrefix := generateThinkingPrefix(req)

	// 处理 system 消息
	systemContent := extractSystemContent(req)

	// 将 thinking 标签注入系统消息前面（kiro.rs 的方式）
	if thinkingPrefix != "" {
		if systemContent != "" && !strings.Contains(systemContent, "<thinking_mode>") {
			systemContent = thinkingPrefix + "\n" + systemContent
		} else if systemContent == "" {
			systemContent = thinkingPrefix
		}
	}

	// 如果有系统消息（含 thinking 标签），添加到历史
	if systemContent != "" {
		userMsg := KiroHistoryUserMessage{}
		userMsg.UserInputMessage.Content = systemContent
		userMsg.UserInputMessage.ModelId = modelId
		userMsg.UserInputMessage.Origin = "AI_EDITOR"
		history = append(history, userMsg)

		assistantMsg := KiroHistoryAssistantMessage{}
		assistantMsg.AssistantResponseMessage.Content = "I will follow these instructions."
		history = append(history, assistantMsg)
	}

	// 处理历史消息（除了最后一条）
	if len(messages) <= 1 {
		return history
	}

	var userBuffer []map[string]any // 累积连续的 user 消息

	for i := 0; i < len(messages)-1; i++ {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}

		role, _ := msg["role"].(string)

		if role == "user" {
			userBuffer = append(userBuffer, msg)
			continue
		}

		if role == "assistant" {
			// 处理累积的 user 消息
			if len(userBuffer) > 0 {
				mergedUserMsg := mergeKiroUserMessages(userBuffer, modelId)
				history = append(history, mergedUserMsg)
				userBuffer = nil
			}

			// 添加 assistant 消息
			assistantMsg := buildKiroAssistantMessage(msg)
			history = append(history, assistantMsg)
		}
	}

	// 处理结尾的孤立 user 消息
	if len(userBuffer) > 0 {
		mergedUserMsg := mergeKiroUserMessages(userBuffer, modelId)
		history = append(history, mergedUserMsg)

		// 添加占位 assistant 回复
		assistantMsg := KiroHistoryAssistantMessage{}
		assistantMsg.AssistantResponseMessage.Content = "OK"
		history = append(history, assistantMsg)
	}

	return history
}

// extractSystemContent 提取系统消息内容
func extractSystemContent(req map[string]any) string {
	system := req["system"]
	if system == nil {
		return ""
	}

	switch s := system.(type) {
	case string:
		return s
	case []any:
		var parts []string
		for _, item := range s {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}

	return ""
}

// mergeKiroUserMessages 合并多个 user 消息
func mergeKiroUserMessages(messages []map[string]any, modelId string) KiroHistoryUserMessage {
	var contentParts []string
	var allImages []KiroImage
	var allToolResults []KiroToolResult

	for _, msg := range messages {
		text, images, toolResults := processKiroMessageContent(msg["content"])
		if text != "" {
			contentParts = append(contentParts, text)
		}
		allImages = append(allImages, images...)
		allToolResults = append(allToolResults, toolResults...)
	}

	userMsg := KiroHistoryUserMessage{}
	userMsg.UserInputMessage.Content = strings.Join(contentParts, "\n")
	userMsg.UserInputMessage.ModelId = modelId
	userMsg.UserInputMessage.Origin = "AI_EDITOR"

	if len(allImages) > 0 {
		userMsg.UserInputMessage.Images = allImages
	}

	if len(allToolResults) > 0 {
		userMsg.UserInputMessage.UserInputMessageContext.ToolResults = allToolResults
	}

	return userMsg
}

// buildKiroAssistantMessage 构建 assistant 历史消息
func buildKiroAssistantMessage(msg map[string]any) KiroHistoryAssistantMessage {
	assistantMsg := KiroHistoryAssistantMessage{}

	content := msg["content"]
	textContent, toolUses := extractAssistantContent(content)

	assistantMsg.AssistantResponseMessage.Content = textContent
	if len(toolUses) > 0 {
		assistantMsg.AssistantResponseMessage.ToolUses = toolUses
	}

	return assistantMsg
}

// extractAssistantContent 提取 assistant 消息内容和工具调用
func extractAssistantContent(content any) (string, []KiroToolUseEntry) {
	var textParts []string
	var toolUses []KiroToolUseEntry

	switch c := content.(type) {
	case string:
		return c, nil

	case []any:
		for _, item := range c {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}

			blockType, _ := block["type"].(string)
			switch blockType {
			case "text":
				if text, ok := block["text"].(string); ok {
					textParts = append(textParts, text)
				}

			case "tool_use":
				toolUse := KiroToolUseEntry{}
				if id, ok := block["id"].(string); ok {
					toolUse.ToolUseId = id
				}
				if name, ok := block["name"].(string); ok {
					toolUse.Name = name
				}

				// 不过滤 web_search：保留历史中的搜索上下文
				// 第三方客户端（非 Claude Code）可能使用 Brave 等搜索工具，
				// 搜索结果是有价值的上下文，应传递给 Kiro
				// ensureHistoryToolDefinitions 会自动创建占位符工具定义

				if input, ok := block["input"].(map[string]any); ok {
					toolUse.Input = input
				} else {
					toolUse.Input = map[string]any{}
				}

				toolUses = append(toolUses, toolUse)
			}
		}
	}

	return strings.Join(textParts, "\n"), toolUses
}

// getStringOrDefault 获取字符串或默认值
func getStringOrDefault(m map[string]any, key, defaultVal string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return defaultVal
}

// ============================================================================
// Tool Use / Tool Result 配对验证
// Kiro API 要求 history 中每个 tool_use 都必须有匹配的 tool_result，
// 否则返回 400 "Improperly formed request"
// ============================================================================

// validateAndFilterToolPairing 验证并过滤 tool_use/tool_result 配对
// 处理范围包括历史消息和当前消息中的 tool_use/tool_result：
// 1. 收集历史中所有 assistant 消息的 tool_use_id
// 2. 过滤历史 user 消息中孤立的 tool_result（如 web_search 过滤后的残留）
// 3. 过滤当前消息中孤立的 tool_result
// 4. 从历史中移除孤立的 tool_use
// 返回过滤后的 currentToolResults，同时原地修改 history
func validateAndFilterToolPairing(history []any, currentToolResults []KiroToolResult) []KiroToolResult {
	// === Phase 1: 收集历史中所有 tool_use_id ===
	allToolUseIDs := make(map[string]bool)
	for _, msg := range history {
		if aMsg, ok := msg.(KiroHistoryAssistantMessage); ok {
			for _, tu := range aMsg.AssistantResponseMessage.ToolUses {
				if tu.ToolUseId != "" {
					allToolUseIDs[tu.ToolUseId] = true
				}
			}
		}
	}

	// === Phase 2: 过滤历史 user 消息中孤立的 tool_result ===
	// 典型场景：web_search 的 tool_use 被过滤，但对应的 tool_result 仍在历史中
	for i, msg := range history {
		uMsg, ok := msg.(KiroHistoryUserMessage)
		if !ok {
			continue
		}
		results := uMsg.UserInputMessage.UserInputMessageContext.ToolResults
		if len(results) == 0 {
			continue
		}
		var kept []KiroToolResult
		for _, tr := range results {
			if allToolUseIDs[tr.ToolUseId] {
				kept = append(kept, tr)
			} else {
				log.Printf("[WARN] [Kiro] 从历史中移除孤立的 tool_result: tool_use_id=%s", tr.ToolUseId)
			}
		}
		if len(kept) != len(results) {
			uMsg.UserInputMessage.UserInputMessageContext.ToolResults = kept
			history[i] = uMsg // 值类型，需要写回
		}
	}

	// === Phase 3: 重新收集历史中已配对的 tool_result_id ===
	historyToolResultIDs := make(map[string]bool)
	for _, msg := range history {
		if uMsg, ok := msg.(KiroHistoryUserMessage); ok {
			for _, tr := range uMsg.UserInputMessage.UserInputMessageContext.ToolResults {
				if tr.ToolUseId != "" {
					historyToolResultIDs[tr.ToolUseId] = true
				}
			}
		}
	}

	// === Phase 4: 过滤当前消息中的 tool_results ===
	unpairedToolUseIDs := make(map[string]bool)
	for id := range allToolUseIDs {
		if !historyToolResultIDs[id] {
			unpairedToolUseIDs[id] = true
		}
	}

	var filteredResults []KiroToolResult
	for _, tr := range currentToolResults {
		if unpairedToolUseIDs[tr.ToolUseId] {
			filteredResults = append(filteredResults, tr)
			delete(unpairedToolUseIDs, tr.ToolUseId)
		} else if allToolUseIDs[tr.ToolUseId] {
			log.Printf("[WARN] [Kiro] 跳过重复的 tool_result：已在历史中配对，tool_use_id=%s", tr.ToolUseId)
		} else {
			log.Printf("[WARN] [Kiro] 跳过孤立的 tool_result：找不到对应的 tool_use，tool_use_id=%s", tr.ToolUseId)
		}
	}

	// === Phase 5: 从历史中移除孤立的 tool_use ===
	if len(unpairedToolUseIDs) > 0 {
		removeOrphanedToolUses(history, unpairedToolUseIDs)
	}

	return filteredResults
}

// cleanupKiroHistory 清理历史消息，移除空消息
// 场景：web_search 等工具被过滤后，可能产生空的 user/assistant 消息对
func cleanupKiroHistory(history []any) []any {
	// 从尾部开始移除空消息（空 content + 无 toolUses/toolResults）
	for len(history) > 0 {
		last := history[len(history)-1]
		if isEmptyHistoryMessage(last) {
			log.Printf("[INFO] [Kiro] 移除尾部空历史消息")
			history = history[:len(history)-1]
		} else {
			break
		}
	}
	return history
}

// isEmptyHistoryMessage 判断历史消息是否为空（无实质内容）
func isEmptyHistoryMessage(msg any) bool {
	switch m := msg.(type) {
	case KiroHistoryAssistantMessage:
		return m.AssistantResponseMessage.Content == "" && len(m.AssistantResponseMessage.ToolUses) == 0
	case KiroHistoryUserMessage:
		return m.UserInputMessage.Content == "" &&
			len(m.UserInputMessage.Images) == 0 &&
			len(m.UserInputMessage.UserInputMessageContext.ToolResults) == 0
	}
	return false
}

// removeOrphanedToolUses 从历史中移除没有对应 tool_result 的 tool_use
func removeOrphanedToolUses(history []any, orphanedIDs map[string]bool) {
	for i, msg := range history {
		aMsg, ok := msg.(KiroHistoryAssistantMessage)
		if !ok || len(aMsg.AssistantResponseMessage.ToolUses) == 0 {
			continue
		}

		originalLen := len(aMsg.AssistantResponseMessage.ToolUses)
		var kept []KiroToolUseEntry
		for _, tu := range aMsg.AssistantResponseMessage.ToolUses {
			if !orphanedIDs[tu.ToolUseId] {
				kept = append(kept, tu)
			} else {
				log.Printf("[WARN] [Kiro] 从历史中移除孤立的 tool_use：tool_use_id=%s, name=%s", tu.ToolUseId, tu.Name)
			}
		}

		if len(kept) != originalLen {
			aMsg.AssistantResponseMessage.ToolUses = kept
			history[i] = aMsg // 值类型，需要写回
		}
	}
}

// ============================================================================
// 图片处理辅助函数
// 参考: https://github.com/nineyuanz/kiro2api/blob/main/utils/image.go
// ============================================================================

// convertMediaTypeToFormat 将 MIME 类型转换为 CodeWhisperer 图片格式
func convertMediaTypeToFormat(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return "jpeg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "image/bmp":
		return "bmp"
	default:
		return ""
	}
}

// parseDataURLToKiroImage 解析 data URL 并转换为 KiroImage
// data URL 格式: data:[<mediatype>][;base64],<data>
func parseDataURLToKiroImage(dataURL string) *KiroImage {
	if !strings.HasPrefix(dataURL, "data:") {
		return nil
	}

	// 查找 base64 标记和数据分隔符
	commaIdx := strings.Index(dataURL, ",")
	if commaIdx == -1 {
		return nil
	}

	header := dataURL[5:commaIdx] // 跳过 "data:"
	data := dataURL[commaIdx+1:]

	// 检查是否是 base64 编码
	if !strings.Contains(header, ";base64") {
		return nil
	}

	// 提取 media type
	mediaType := strings.Split(header, ";")[0]
	format := convertMediaTypeToFormat(mediaType)
	if format == "" {
		return nil
	}

	img := &KiroImage{Format: format}
	img.Source.Bytes = data
	return img
}
