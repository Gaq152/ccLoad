package app

import (
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
)

// ============================================================================
// Kiro 请求转换
// 将 Anthropic Messages API 请求转换为 CodeWhisperer 格式
// 参考: https://github.com/nineyuanz/kiro2api/blob/main/converter/codewhisperer.go
// ============================================================================

// 工具描述最大长度
const KiroMaxToolDescriptionLength = 1024

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

	// 确定 ChatTriggerType
	tools, _ := anthropicReq["tools"].([]any)
	kiroReq.ConversationState.ChatTriggerType = determineChatTriggerType(anthropicReq, tools)

	// 提取消息
	messages, _ := anthropicReq["messages"].([]any)
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages is empty")
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

	// 如果有工具结果，设置到上下文中
	if len(toolResults) > 0 {
		kiroReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults = toolResults
		// 包含工具结果时，content 应为空
		kiroReq.ConversationState.CurrentMessage.UserInputMessage.Content = ""
	}

	// 处理工具定义
	if len(tools) > 0 {
		kiroTools := convertKiroTools(tools)
		if len(kiroTools) > 0 {
			kiroReq.ConversationState.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools = kiroTools
		}
	}

	// 构建历史消息
	history := buildKiroHistory(anthropicReq, messages, modelId)
	if len(history) > 0 {
		kiroReq.ConversationState.History = history
	}

	// 处理 thinking 配置
	if thinking, ok := anthropicReq["thinking"].(map[string]any); ok {
		if thinkingType, _ := thinking["type"].(string); thinkingType == "enabled" {
			budgetTokens := 0
			if bt, ok := thinking["budget_tokens"].(float64); ok {
				budgetTokens = int(bt)
			}

			// 获取 max_tokens
			maxTokens := 4096
			if mt, ok := anthropicReq["max_tokens"].(float64); ok {
				maxTokens = int(mt)
			}

			// 确保 max_tokens > budget_tokens
			if maxTokens <= budgetTokens {
				maxTokens = budgetTokens + 4096
			}

			kiroReq.InferenceConfiguration = &KiroInferenceConfiguration{
				MaxTokens: maxTokens,
				Thinking: &KiroThinking{
					Type:         "enabled",
					BudgetTokens: budgetTokens,
				},
			}

			// 如果有 temperature
			if temp, ok := anthropicReq["temperature"].(float64); ok {
				kiroReq.InferenceConfiguration.Temperature = &temp
			}
		}
	}

	// 序列化
	return sonic.Marshal(kiroReq)
}

// determineChatTriggerType 确定聊天触发类型
func determineChatTriggerType(req map[string]any, tools []any) string {
	if len(tools) > 0 {
		if toolChoice, ok := req["tool_choice"].(map[string]any); ok {
			if tcType, _ := toolChoice["type"].(string); tcType == "any" || tcType == "tool" {
				return "AUTO"
			}
		}
	}
	return "MANUAL"
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

// convertKiroTools 转换工具定义
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

		description, _ := toolMap["description"].(string)
		inputSchema := toolMap["input_schema"]

		kiroTool := KiroTool{}
		kiroTool.ToolSpecification.Name = name
		kiroTool.ToolSpecification.Description = truncateKiroDescription(description)
		kiroTool.ToolSpecification.InputSchema = KiroInputSchema{
			Json: inputSchema,
		}

		kiroTools = append(kiroTools, kiroTool)
	}

	return kiroTools
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

	// 处理 system 消息
	systemContent := extractSystemContent(req)

	// 处理 thinking 配置，生成前缀
	thinkingPrefix := ""
	if thinking, ok := req["thinking"].(map[string]any); ok {
		if thinkingType, _ := thinking["type"].(string); thinkingType == "enabled" {
			budgetTokens := 0
			if bt, ok := thinking["budget_tokens"].(float64); ok {
				budgetTokens = int(bt)
			}
			thinkingPrefix = fmt.Sprintf("<thinking_mode>enabled</thinking_mode><max_thinking_length>%d</max_thinking_length>", budgetTokens)
		}
	}

	// 如果有系统消息或 thinking 前缀，添加到历史
	if systemContent != "" || thinkingPrefix != "" {
		fullSystemContent := systemContent
		if thinkingPrefix != "" && !strings.Contains(systemContent, "<thinking_mode>") {
			if fullSystemContent != "" {
				fullSystemContent = thinkingPrefix + "\n" + fullSystemContent
			} else {
				fullSystemContent = thinkingPrefix
			}
		}

		if fullSystemContent != "" {
			userMsg := KiroHistoryUserMessage{}
			userMsg.UserInputMessage.Content = fullSystemContent
			userMsg.UserInputMessage.ModelId = modelId
			userMsg.UserInputMessage.Origin = "AI_EDITOR"
			history = append(history, userMsg)

			assistantMsg := KiroHistoryAssistantMessage{}
			assistantMsg.AssistantResponseMessage.Content = "OK"
			history = append(history, assistantMsg)
		}
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
		userMsg.UserInputMessage.Content = "" // 包含工具结果时 content 为空
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

				// 过滤不支持的工具
				if toolUse.Name == "web_search" || toolUse.Name == "websearch" {
					continue
				}

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
