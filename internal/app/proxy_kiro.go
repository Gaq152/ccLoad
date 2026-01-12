package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"ccLoad/internal/model"
	"ccLoad/internal/util"

	"github.com/bytedance/sonic"
)

// ============================================================================
// Kiro 代理处理
// 处理 Kiro (CodeWhisperer) 的请求转发和响应处理
// 参考: https://github.com/nineyuanz/kiro2api/blob/main/server/stream_processor.go
// ============================================================================

// forwardKiroRequest 转发 Kiro 请求并处理响应
// 这是 Kiro 预设的专用转发函数，替代通用的 forwardOnceAsync
func (s *Server) forwardKiroRequest(
	ctx context.Context,
	_ *model.Config, // cfg 保留用于接口一致性，当前未使用
	reqCtx *proxyRequestContext,
	kiroBody []byte,
	w http.ResponseWriter,
) (*fwResult, float64, error) {
	startTime := time.Now()

	// 检查 Access Token
	if reqCtx.kiroAccessToken == "" {
		return nil, 0, fmt.Errorf("kiro access token is empty")
	}

	// 估算输入 token（使用原始 Anthropic 请求体）
	// 注意：这是快速估算，用于监控统计，误差约 10-20%
	estimatedInputTokens := estimateKiroInputTokens(reqCtx.body)

	// 发送请求到 Kiro API（使用配置的设备指纹）
	resp, err := s.ForwardKiroRequest(ctx, kiroBody, reqCtx.kiroAccessToken, reqCtx.isStreaming, reqCtx.kiroDeviceFingerprint)
	if err != nil {
		duration := time.Since(startTime).Seconds()
		return nil, duration, fmt.Errorf("forward kiro request: %w", err)
	}
	defer resp.Body.Close()

	duration := time.Since(startTime).Seconds()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		// 读取错误响应体
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

		// 检测内容长度超限错误（CONTENT_LENGTH_EXCEEDS_THRESHOLD）
		// 参考 kiro2api: 将此错误映射为 Claude API 的 max_tokens stop_reason
		if resp.StatusCode == http.StatusBadRequest && IsKiroContentLengthExceeds(errorBody) {
			log.Printf("[INFO] [Kiro] 检测到内容长度超限错误，转换为 max_tokens stop_reason")
			// 构建 max_tokens 响应并直接写入
			BuildKiroContentLengthExceedsResponse(w, reqCtx.originalModel, estimatedInputTokens)
			return &fwResult{
				Status:       http.StatusOK, // 返回 200，因为已经写入了有效的 SSE 响应
				InputTokens:  estimatedInputTokens,
				OutputTokens: 0,
			}, duration, nil
		}

		// 检测 AWS 账户暂停错误（TEMPORARILY_SUSPENDED）
		// 需要应用 24 小时冷却（参考 kiro2api 实现）
		actualStatus := resp.StatusCode
		if IsKiroTemporarilySuspended(errorBody) {
			actualStatus = util.StatusTemporarilySuspended
			log.Printf("[WARN] [Kiro] 检测到 AWS 账户暂停错误，将应用 24 小时冷却: %s", string(errorBody))
		} else {
			log.Printf("[ERROR] [Kiro] 上游返回错误: status=%d, body=%s", resp.StatusCode, string(errorBody))
		}

		return &fwResult{
			Status: actualStatus,
			Header: resp.Header.Clone(),
			Body:   errorBody,
		}, duration, nil
	}

	// 处理响应
	contentType := resp.Header.Get("Content-Type")

	// 读取完整响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, duration, fmt.Errorf("read response body: %w", err)
	}

	// 检测是否是 AWS Event Stream 二进制格式
	// 方法1: Content-Type 包含 event-stream 或 amazon
	// 方法2: 响应体包含 AWS Event Stream 二进制特征（:event-type 头部）
	isAWSEventStream := strings.Contains(contentType, "event-stream") ||
		strings.Contains(contentType, "amazon") ||
		isAWSEventStreamBinary(body)

	if isAWSEventStream {
		// 解析 AWS Event Stream 并转换为 Anthropic SSE 格式
		// 使用请求中的模型名称和估算的输入 token
		parser, err := ProcessKiroAWSEventStream(ctx, body, w, reqCtx.originalModel, estimatedInputTokens)
		if err != nil && err != io.EOF {
			log.Printf("[WARN] [Kiro] AWS Event Stream 处理错误: %v", err)
		}

		// 获取输出 token（从响应中提取）
		_, outputTokens := parser.GetUsage()

		return &fwResult{
			Status:       http.StatusOK,
			Header:       resp.Header.Clone(),
			InputTokens:  estimatedInputTokens, // 使用估算的输入 token
			OutputTokens: outputTokens,          // 使用实际的输出 token
		}, duration, nil
	}

	// 非 AWS Event Stream 响应（纯 JSON）
	// 处理 JSON 响应
	processedBody, err := ProcessKiroJSONResponse(body)
	if err != nil {
		log.Printf("[WARN] [Kiro] JSON 响应处理失败: %v", err)
		processedBody = body
	}

	// 写入响应
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(processedBody)

	return &fwResult{
		Status: resp.StatusCode,
		Header: resp.Header.Clone(),
		Body:   processedBody,
	}, duration, nil
}

// isAWSEventStreamBinary 检测响应体是否是 AWS Event Stream 二进制格式
// AWS Event Stream 帧包含 :event-type, :content-type, :message-type 等头部
func isAWSEventStreamBinary(data []byte) bool {
	// 检查是否包含 AWS Event Stream 的特征字符串
	// 这些是二进制头部中的字符串标记
	return bytes.Contains(data, []byte(":event-type")) ||
		bytes.Contains(data, []byte(":message-type"))
}

// ForwardKiroRequest 转发请求到 Kiro API
// 返回: 响应对象，需要调用方关闭 Body
func (s *Server) ForwardKiroRequest(
	ctx context.Context,
	kiroBody []byte,
	accessToken string,
	isStreaming bool,
	deviceFingerprint string,
) (*http.Response, error) {
	// 创建请求
	req, err := http.NewRequestWithContext(ctx, "POST", KiroAPIEndpoint, bytes.NewReader(kiroBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// 设置请求头（使用配置的设备指纹）
	headers := BuildKiroRequestHeaders(accessToken, isStreaming, deviceFingerprint)
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// 发送请求
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	return resp, nil
}

// ProcessKiroAWSEventStream 处理已读取的 AWS Event Stream 响应并转换为 Anthropic SSE 格式
// 与 StreamCopyKiroSSE 不同，此函数接收已读取的字节数组而非流
// requestedModel: 请求的模型名称，用于响应中的 model 字段
func ProcessKiroAWSEventStream(ctx context.Context, data []byte, w http.ResponseWriter, requestedModel string, estimatedInputTokens int) (*kiroSSEParser, error) {
	parser := newKiroSSEParser()
	if requestedModel != "" {
		parser.requestedModel = requestedModel
	}
	// 设置估算的输入 token（Kiro 响应不包含 input_tokens）
	parser.inputTokens = estimatedInputTokens

	// 设置 SSE 响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return parser, fmt.Errorf("response writer does not support flushing")
	}

	// 解析所有帧
	buf := data
	for len(buf) >= 16 {
		select {
		case <-ctx.Done():
			return parser, ctx.Err()
		default:
		}

		frameLen, frame, remaining := parseAWSEventStreamFrame(buf)
		if frameLen == 0 {
			break
		}

		// 处理帧
		processAWSEventStreamFrame(w, flusher, frame, parser)
		buf = remaining
	}

	// 如果有内容输出但没有收到 completionEvent，手动发送结束事件
	// Kiro 可能不发送 completionEvent，需要在流结束时补充
	if parser.messageStarted {
		sendKiroStreamEndEvents(w, flusher, parser)
	}

	return parser, nil
}

// sendKiroStreamEndEvents 发送流结束事件
func sendKiroStreamEndEvents(w http.ResponseWriter, flusher http.Flusher, parser *kiroSSEParser) {
	// 只有当有未关闭的内容块时才发送 content_block_stop
	// 思考块、文本块或工具调用块如果已发送 start 但未发送 stop，需要关闭
	// 工具调用块在 handleKiroToolUseEvent 中已经处理，不需要在这里关闭
	if (parser.thinkingBlockSent && !parser.thinkingBlockStopped) ||
		(parser.textBlockSent && !parser.textBlockStopped) {
		blockStop := map[string]any{
			"type":  "content_block_stop",
			"index": parser.currentBlockIndex,
		}
		blockStopData, _ := sonic.Marshal(blockStop)
		writeSSEEvent(w, flusher, "content_block_stop", blockStopData)
	}

	// 发送 message_delta
	stopReason := "end_turn"
	if parser.hasToolUse {
		stopReason = "tool_use"
	}
	msgDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": parser.outputTokens,
		},
	}
	msgDeltaData, _ := sonic.Marshal(msgDelta)
	writeSSEEvent(w, flusher, "message_delta", msgDeltaData)

	// 发送 message_stop
	msgStop := map[string]any{"type": "message_stop"}
	msgStopData, _ := sonic.Marshal(msgStop)
	writeSSEEvent(w, flusher, "message_stop", msgStopData)
}

// StreamCopyKiroSSE 流式复制 Kiro 响应并转换为 Anthropic SSE 格式
// Kiro (CodeWhisperer) 返回 AWS Event Stream 二进制格式，需要解析后转换为标准 SSE
// AWS Event Stream 帧结构:
//   - Prelude (12字节): 总长度(4) + 头部长度(4) + Prelude CRC(4)
//   - Headers: 二进制编码的键值对
//   - Payload: JSON 数据
//   - Message CRC (4字节)
func StreamCopyKiroSSE(ctx context.Context, body io.ReadCloser, w http.ResponseWriter) (*kiroSSEParser, error) {
	parser := newKiroSSEParser()

	// 设置 SSE 响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return parser, fmt.Errorf("response writer does not support flushing")
	}

	// 读取整个响应到缓冲区（AWS Event Stream 需要完整帧解析）
	buf := make([]byte, 0, 64*1024)
	readBuf := make([]byte, 4096)

	for {
		select {
		case <-ctx.Done():
			return parser, ctx.Err()
		default:
		}

		n, err := body.Read(readBuf)
		if n > 0 {
			buf = append(buf, readBuf[:n]...)

			// 尝试解析完整的帧
			for {
				frameLen, frame, remaining := parseAWSEventStreamFrame(buf)
				if frameLen == 0 {
					break // 数据不足，等待更多数据
				}

				// 处理帧
				processAWSEventStreamFrame(w, flusher, frame, parser)
				buf = remaining
			}
		}

		if err != nil {
			if err == io.EOF {
				// 处理剩余数据
				for len(buf) >= 16 {
					frameLen, frame, remaining := parseAWSEventStreamFrame(buf)
					if frameLen == 0 {
						break
					}
					processAWSEventStreamFrame(w, flusher, frame, parser)
					buf = remaining
				}
				return parser, nil
			}
			return parser, err
		}
	}
}

// parseAWSEventStreamFrame 解析 AWS Event Stream 帧
// 返回: 帧长度, 帧数据, 剩余数据
// 如果数据不足返回 0, nil, 原数据
func parseAWSEventStreamFrame(data []byte) (int, *awsEventFrame, []byte) {
	// AWS Event Stream 最小帧长度: 4+4+4+4=16字节
	if len(data) < 16 {
		return 0, nil, data
	}

	// 解析 Prelude
	totalLength := binary.BigEndian.Uint32(data[0:4])
	headerLength := binary.BigEndian.Uint32(data[4:8])
	// preludeCRC := binary.BigEndian.Uint32(data[8:12]) // 可选校验

	// 验证长度
	if totalLength < 16 || totalLength > 1024*1024 {
		// 无效帧，跳过一个字节继续查找
		return 1, nil, data[1:]
	}

	if int(totalLength) > len(data) {
		return 0, nil, data // 数据不足
	}

	// 提取帧数据
	frameData := data[:totalLength]
	remaining := data[totalLength:]

	// 解析 Headers
	headerStart := 12
	headerEnd := 12 + int(headerLength)
	if headerEnd > int(totalLength)-4 {
		return int(totalLength), nil, remaining // 无效帧
	}

	headers := parseAWSEventStreamHeaders(frameData[headerStart:headerEnd])

	// 提取 Payload
	payloadStart := headerEnd
	payloadEnd := int(totalLength) - 4 // 减去 Message CRC
	var payload []byte
	if payloadEnd > payloadStart {
		payload = frameData[payloadStart:payloadEnd]
	}

	return int(totalLength), &awsEventFrame{
		Headers: headers,
		Payload: payload,
	}, remaining
}

// awsEventFrame AWS Event Stream 帧
type awsEventFrame struct {
	Headers map[string]string
	Payload []byte
}

// parseAWSEventStreamHeaders 解析 AWS Event Stream 头部
// 头部格式: [name_len(1)][name][type(1)][value_len(2)][value]...
func parseAWSEventStreamHeaders(data []byte) map[string]string {
	headers := make(map[string]string)
	pos := 0

	for pos < len(data) {
		// 读取 name 长度 (1字节)
		if pos >= len(data) {
			break
		}
		nameLen := int(data[pos])
		pos++

		// 读取 name
		if pos+nameLen > len(data) {
			break
		}
		name := string(data[pos : pos+nameLen])
		pos += nameLen

		// 读取 type (1字节)
		if pos >= len(data) {
			break
		}
		valueType := data[pos]
		pos++

		// 根据类型读取值
		switch valueType {
		case 7: // String 类型
			// 读取 value 长度 (2字节, big-endian)
			if pos+2 > len(data) {
				break
			}
			valueLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
			pos += 2

			// 读取 value
			if pos+valueLen > len(data) {
				break
			}
			headers[name] = string(data[pos : pos+valueLen])
			pos += valueLen

		default:
			// 其他类型暂不处理，跳过
			// 大多数情况下 Kiro 只使用 String 类型
		}
	}

	return headers
}

// processAWSEventStreamFrame 处理 AWS Event Stream 帧并转换为 SSE
func processAWSEventStreamFrame(w http.ResponseWriter, flusher http.Flusher, frame *awsEventFrame, parser *kiroSSEParser) {
	if frame == nil || len(frame.Payload) == 0 {
		return
	}

	// 获取事件类型
	eventType := frame.Headers[":event-type"]
	messageType := frame.Headers[":message-type"]

	// 忽略非事件消息
	if messageType != "event" && messageType != "" {
		return
	}

	// 解析 Payload JSON
	var payloadMap map[string]any
	if err := sonic.Unmarshal(frame.Payload, &payloadMap); err != nil {
		log.Printf("[WARN] [Kiro] 解析 payload 失败: %v", err)
		return
	}

	// 根据事件类型处理
	switch eventType {
	case "assistantResponseEvent":
		// 旧格式: {"content": "文本内容"}
		// 转换为 Anthropic SSE 格式
		handleKiroAssistantResponseEvent(w, flusher, payloadMap, parser)

	case "meteringEvent":
		// 计量事件，提取 usage 信息
		handleKiroMeteringEvent(payloadMap, parser)

	case "contextUsageEvent":
		// 上下文使用事件，可忽略

	case "toolUseEvent":
		// 工具调用事件
		handleKiroToolUseEvent(w, flusher, payloadMap, parser)

	case "completionEvent":
		// 完成事件
		handleKiroCompletionEvent(w, flusher, payloadMap, parser)

	default:
		// 其他事件类型，尝试透传
		if content, ok := payloadMap["content"].(string); ok && content != "" {
			// 有内容的事件，转换为 content_block_delta
			handleKiroAssistantResponseEvent(w, flusher, payloadMap, parser)
		}
	}
}

// handleKiroAssistantResponseEvent 处理 Kiro assistantResponseEvent
// 转换为 Anthropic 的 content_block_delta 格式
// 支持 <thinking>...</thinking> 标签转换为独立的 thinking content block
func handleKiroAssistantResponseEvent(w http.ResponseWriter, flusher http.Flusher, payloadMap map[string]any, parser *kiroSSEParser) {
	content, ok := payloadMap["content"].(string)
	if !ok || content == "" {
		return
	}

	// 如果是第一个内容块，先发送 message_start
	if !parser.messageStarted {
		parser.messageStarted = true

		// 发送 message_start（使用估算的 input_tokens）
		msgStart := map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            "msg_kiro_" + fmt.Sprintf("%d", time.Now().UnixNano()),
				"type":          "message",
				"role":          "assistant",
				"content":       []any{},
				"model":         parser.requestedModel,
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  parser.inputTokens,
					"output_tokens": 0,
				},
			},
		}
		msgStartData, _ := sonic.Marshal(msgStart)
		writeSSEEvent(w, flusher, "message_start", msgStartData)
	}

	// 处理 thinking 标签
	// 检测 <thinking> 开始标签
	if strings.Contains(content, "<thinking>") {
		parser.inThinkingBlock = true
		// 移除 <thinking> 标签
		content = strings.Replace(content, "<thinking>", "", 1)
		// 如果移除后为空或只有换行，跳过
		if strings.TrimSpace(content) == "" {
			return
		}
	}

	// 检测 </thinking> 结束标签
	if strings.Contains(content, "</thinking>") {
		// 分割内容：</thinking> 之前是思考，之后是正常文本
		parts := strings.SplitN(content, "</thinking>", 2)

		// 发送 thinking 部分（如果有）
		thinkingContent := strings.TrimSpace(parts[0])
		if thinkingContent != "" && parser.inThinkingBlock {
			sendKiroThinkingDelta(w, flusher, thinkingContent, parser)
		}

		// 结束 thinking block
		if parser.thinkingBlockSent && !parser.thinkingBlockStopped {
			// 发送 thinking block stop
			blockStop := map[string]any{
				"type":  "content_block_stop",
				"index": parser.currentBlockIndex,
			}
			blockStopData, _ := sonic.Marshal(blockStop)
			writeSSEEvent(w, flusher, "content_block_stop", blockStopData)
			parser.thinkingBlockStopped = true
			parser.currentBlockIndex++
		}

		parser.inThinkingBlock = false

		// 发送 </thinking> 之后的文本部分（如果有）
		if len(parts) > 1 {
			textContent := strings.TrimLeft(parts[1], "\n\r")
			if textContent != "" {
				sendKiroTextDelta(w, flusher, textContent, parser)
			}
		}
		return
	}

	// 根据当前状态发送对应类型的 delta
	if parser.inThinkingBlock {
		sendKiroThinkingDelta(w, flusher, content, parser)
	} else {
		sendKiroTextDelta(w, flusher, content, parser)
	}

	// 累计输出 token（粗略估计）
	parser.outputTokens += len(content) / 4
}

// sendKiroThinkingDelta 发送 thinking 类型的 content_block_delta
func sendKiroThinkingDelta(w http.ResponseWriter, flusher http.Flusher, content string, parser *kiroSSEParser) {
	// 如果还没发送 thinking block start，先发送
	if !parser.thinkingBlockSent {
		parser.thinkingBlockSent = true
		blockStart := map[string]any{
			"type":  "content_block_start",
			"index": parser.currentBlockIndex,
			"content_block": map[string]any{
				"type":     "thinking",
				"thinking": "",
			},
		}
		blockStartData, _ := sonic.Marshal(blockStart)
		writeSSEEvent(w, flusher, "content_block_start", blockStartData)
	}

	// 发送 thinking_delta
	delta := map[string]any{
		"type":  "content_block_delta",
		"index": parser.currentBlockIndex,
		"delta": map[string]any{
			"type":     "thinking_delta",
			"thinking": content,
		},
	}
	deltaData, _ := sonic.Marshal(delta)
	writeSSEEvent(w, flusher, "content_block_delta", deltaData)
}

// sendKiroTextDelta 发送 text 类型的 content_block_delta
func sendKiroTextDelta(w http.ResponseWriter, flusher http.Flusher, content string, parser *kiroSSEParser) {
	// 如果还没发送 text block start，先发送
	if !parser.textBlockSent {
		parser.textBlockSent = true
		blockStart := map[string]any{
			"type":  "content_block_start",
			"index": parser.currentBlockIndex,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		}
		blockStartData, _ := sonic.Marshal(blockStart)
		writeSSEEvent(w, flusher, "content_block_start", blockStartData)
	}

	// 发送 text_delta
	delta := map[string]any{
		"type":  "content_block_delta",
		"index": parser.currentBlockIndex,
		"delta": map[string]any{
			"type": "text_delta",
			"text": content,
		},
	}
	deltaData, _ := sonic.Marshal(delta)
	writeSSEEvent(w, flusher, "content_block_delta", deltaData)
}

// handleKiroMeteringEvent 处理 Kiro meteringEvent
func handleKiroMeteringEvent(payloadMap map[string]any, parser *kiroSSEParser) {
	// 提取 usage 信息
	if usage, ok := payloadMap["usage"].(float64); ok {
		// usage 是 credit 消耗，可以用于估算 token
		parser.outputTokens = int(usage * 1000) // 粗略转换
	}
}

// handleKiroToolUseEvent 处理 Kiro toolUseEvent
// Kiro 的工具调用是流式发送的：
// 1. 第一个事件：只有 name 和 toolUseId
// 2. 后续事件：input 字段分片发送
// 3. 最后一个事件：带有 stop:true 标记
func handleKiroToolUseEvent(w http.ResponseWriter, flusher http.Flusher, payloadMap map[string]any, parser *kiroSSEParser) {
	parser.hasToolUse = true

	// 提取工具调用信息
	toolUseId, _ := payloadMap["toolUseId"].(string)
	toolName, _ := payloadMap["name"].(string)
	inputDelta, hasInput := payloadMap["input"].(string)
	isStop, _ := payloadMap["stop"].(bool)

	// 如果没有工具 ID，忽略
	if toolUseId == "" {
		return
	}

	// 将 Kiro 的 tooluse_ 前缀转换为 Anthropic 的 toolu_ 前缀
	// Kiro 使用 "tooluse_xxx" 格式，Anthropic 使用 "toolu_xxx" 格式
	// Claude Code 客户端需要 toolu_ 前缀才能正确识别 tool_use
	if strings.HasPrefix(toolUseId, "tooluse_") {
		toolUseId = "toolu_" + strings.TrimPrefix(toolUseId, "tooluse_")
	}

	// 检查是否是新的工具调用
	if parser.activeToolUseId != toolUseId {
		// 如果有之前未完成的工具调用，先关闭它
		if parser.toolUseBlockSent {
			blockStop := map[string]any{
				"type":  "content_block_stop",
				"index": parser.activeToolUseIndex,
			}
			blockStopData, _ := sonic.Marshal(blockStop)
			writeSSEEvent(w, flusher, "content_block_stop", blockStopData)
			parser.currentBlockIndex++
		}

		// 开始新的工具调用
		parser.activeToolUseId = toolUseId
		parser.activeToolUseName = toolName
		parser.toolUseBlockSent = false
	}

	// 如果还没有发送 message_start，先发送
	if !parser.messageStarted {
		parser.messageStarted = true
		msgStart := map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            "msg_kiro_" + toolUseId,
				"type":          "message",
				"role":          "assistant",
				"content":       []any{},
				"model":         parser.requestedModel,
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  parser.inputTokens,
					"output_tokens": 1,
				},
			},
		}
		msgStartData, _ := sonic.Marshal(msgStart)
		writeSSEEvent(w, flusher, "message_start", msgStartData)
	}

	// 如果是停止事件
	if isStop {
		if parser.toolUseBlockSent {
			// 发送 content_block_stop
			blockStop := map[string]any{
				"type":  "content_block_stop",
				"index": parser.activeToolUseIndex,
			}
			blockStopData, _ := sonic.Marshal(blockStop)
			writeSSEEvent(w, flusher, "content_block_stop", blockStopData)
			parser.currentBlockIndex++
		}

		// 标记已完成
		if parser.completedToolUseIds == nil {
			parser.completedToolUseIds = make(map[string]bool)
		}
		parser.completedToolUseIds[toolUseId] = true

		// 重置工具调用状态
		parser.activeToolUseId = ""
		parser.activeToolUseName = ""
		parser.toolUseBlockSent = false
		return
	}

	// 如果还没发送 content_block_start，现在发送
	if !parser.toolUseBlockSent && toolName != "" {
		// 关闭之前的思考块（如果有）
		if parser.inThinkingBlock && parser.thinkingBlockSent && !parser.thinkingBlockStopped {
			blockStop := map[string]any{
				"type":  "content_block_stop",
				"index": parser.currentBlockIndex,
			}
			blockStopData, _ := sonic.Marshal(blockStop)
			writeSSEEvent(w, flusher, "content_block_stop", blockStopData)
			parser.thinkingBlockStopped = true
			parser.currentBlockIndex++
			parser.inThinkingBlock = false
		}

		// 关闭之前的文本块（如果有）
		if parser.textBlockSent && !parser.textBlockStopped {
			blockStop := map[string]any{
				"type":  "content_block_stop",
				"index": parser.currentBlockIndex,
			}
			blockStopData, _ := sonic.Marshal(blockStop)
			writeSSEEvent(w, flusher, "content_block_stop", blockStopData)
			parser.textBlockStopped = true
			parser.currentBlockIndex++
		}

		// 记录工具调用索引
		parser.activeToolUseIndex = parser.currentBlockIndex
		if parser.toolUseIdByBlockIndex == nil {
			parser.toolUseIdByBlockIndex = make(map[int]string)
		}
		parser.toolUseIdByBlockIndex[parser.activeToolUseIndex] = toolUseId

		// 发送 content_block_start（tool_use 类型）
		blockStart := map[string]any{
			"type":  "content_block_start",
			"index": parser.activeToolUseIndex,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    toolUseId,
				"name":  toolName,
				"input": map[string]any{},
			},
		}
		blockStartData, _ := sonic.Marshal(blockStart)
		writeSSEEvent(w, flusher, "content_block_start", blockStartData)
		parser.toolUseBlockSent = true
	}

	// 如果有 input 增量，发送 content_block_delta
	if hasInput && inputDelta != "" {
		blockDelta := map[string]any{
			"type":  "content_block_delta",
			"index": parser.activeToolUseIndex,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": inputDelta,
			},
		}
		blockDeltaData, _ := sonic.Marshal(blockDelta)
		writeSSEEvent(w, flusher, "content_block_delta", blockDeltaData)
	}
}

// handleKiroCompletionEvent 处理 Kiro completionEvent
// 注意：所有 content_block 应该在各自的处理函数中关闭（thinking、text、tool_use）
// 这里只负责发送 message_delta 和 message_stop
func handleKiroCompletionEvent(w http.ResponseWriter, flusher http.Flusher, _ map[string]any, parser *kiroSSEParser) {
	if parser.messageStarted {
		// 发送 message_delta
		stopReason := "end_turn"
		if parser.hasToolUse {
			stopReason = "tool_use"
		}
		msgDelta := map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": map[string]any{
				"output_tokens": parser.outputTokens,
			},
		}
		msgDeltaData, _ := sonic.Marshal(msgDelta)
		writeSSEEvent(w, flusher, "message_delta", msgDeltaData)

		// 发送 message_stop
		msgStop := map[string]any{"type": "message_stop"}
		msgStopData, _ := sonic.Marshal(msgStop)
		writeSSEEvent(w, flusher, "message_stop", msgStopData)
	}
}

// writeSSEEvent 写入 SSE 事件
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, data []byte) {
	if eventType != "" {
		fmt.Fprintf(w, "event: %s\n", eventType)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// ============================================================================
// Kiro SSE 解析器
// ============================================================================

type kiroSSEParser struct {
	// 消息状态
	messageStarted bool

	// 思考块状态
	inThinkingBlock     bool // 当前是否在 thinking 块内
	thinkingBlockSent   bool // 是否已发送 thinking block start
	thinkingBlockStopped bool // 是否已发送 thinking block stop
	textBlockSent       bool // 是否已发送 text block start
	textBlockStopped    bool // 是否已发送 text block stop
	currentBlockIndex   int  // 当前 block 索引

	// 请求的模型名称
	requestedModel string

	// 工具调用跟踪
	hasToolUse            bool
	toolUseIdByBlockIndex map[int]string
	completedToolUseIds   map[string]bool

	// 当前活跃的工具调用（流式累积）
	activeToolUseId    string // 当前正在处理的工具调用 ID
	activeToolUseName  string // 当前工具名称
	activeToolUseIndex int    // 当前工具调用的 block 索引
	toolUseBlockSent   bool   // 是否已发送 tool_use block start

	// 统计信息
	outputTokens int
	inputTokens  int
}

func newKiroSSEParser() *kiroSSEParser {
	return &kiroSSEParser{
		toolUseIdByBlockIndex: make(map[int]string),
		completedToolUseIds:   make(map[string]bool),
		requestedModel:        "claude-sonnet-4-20250514", // 默认模型
	}
}

// trackToolUseStart 跟踪工具调用开始
func (p *kiroSSEParser) trackToolUseStart(dataMap map[string]any) {
	cb, ok := dataMap["content_block"].(map[string]any)
	if !ok {
		return
	}

	cbType, _ := cb["type"].(string)
	if cbType != "tool_use" {
		return
	}

	idx := extractKiroIndex(dataMap)
	if idx < 0 {
		return
	}

	id, _ := cb["id"].(string)
	if id != "" {
		p.toolUseIdByBlockIndex[idx] = id
	}
}

// trackToolUseStop 跟踪工具调用结束
func (p *kiroSSEParser) trackToolUseStop(dataMap map[string]any) {
	idx := extractKiroIndex(dataMap)
	if idx < 0 {
		return
	}

	if toolId, exists := p.toolUseIdByBlockIndex[idx]; exists && toolId != "" {
		p.completedToolUseIds[toolId] = true
		delete(p.toolUseIdByBlockIndex, idx)
	}
}

// extractUsage 提取 usage 信息
func (p *kiroSSEParser) extractUsage(dataMap map[string]any) {
	usage, ok := dataMap["usage"].(map[string]any)
	if !ok {
		return
	}

	if v, ok := usage["output_tokens"].(float64); ok {
		p.outputTokens = int(v)
	}
	if v, ok := usage["input_tokens"].(float64); ok {
		p.inputTokens = int(v)
	}
}

// HasToolUse 是否有工具调用
func (p *kiroSSEParser) HasToolUse() bool {
	return len(p.toolUseIdByBlockIndex) > 0 || len(p.completedToolUseIds) > 0
}

// GetUsage 获取 usage 统计
func (p *kiroSSEParser) GetUsage() (inputTokens, outputTokens int) {
	return p.inputTokens, p.outputTokens
}

// extractKiroIndex 从数据中提取索引
func extractKiroIndex(dataMap map[string]any) int {
	if v, ok := dataMap["index"].(float64); ok {
		return int(v)
	}
	if v, ok := dataMap["index"].(int); ok {
		return v
	}
	return -1
}

// ============================================================================
// Kiro 非流式响应处理
// ============================================================================

// ProcessKiroJSONResponse 处理 Kiro 非流式 JSON 响应
// CodeWhisperer 的 JSON 响应格式与 Anthropic 基本兼容，可以直接透传
func ProcessKiroJSONResponse(body []byte) ([]byte, error) {
	// 直接透传，CodeWhisperer 响应格式与 Anthropic 兼容
	return body, nil
}

// ============================================================================
// Kiro 错误检测
// ============================================================================

// IsKiroTemporarilySuspended 检测是否是 AWS 账户暂停错误
// AWS CodeWhisperer 在账户暂停时会返回特定的错误消息
// 参考 kiro2api: 需要对这类错误实施 24 小时冷却
func IsKiroTemporarilySuspended(errorBody []byte) bool {
	if len(errorBody) == 0 {
		return false
	}

	errorStr := strings.ToLower(string(errorBody))

	// 检测 AWS 账户暂停的特征字符串
	// 1. "TEMPORARILY_SUSPENDED" - AWS 官方错误码
	// 2. "temporarily is suspended" - 错误消息文本
	// 3. "account suspended" - 通用暂停提示
	return strings.Contains(errorStr, "temporarily_suspended") ||
		strings.Contains(errorStr, "temporarily is suspended") ||
		strings.Contains(errorStr, "account suspended")
}

// IsKiroContentLengthExceeds 检测是否是内容长度超限错误
// CodeWhisperer 在上下文过长时会返回 CONTENT_LENGTH_EXCEEDS_THRESHOLD 错误
// 参考 kiro2api: 需要将此错误映射为 Claude API 的 max_tokens stop_reason
func IsKiroContentLengthExceeds(errorBody []byte) bool {
	if len(errorBody) == 0 {
		return false
	}

	errorStr := string(errorBody)

	// 检测内容长度超限的特征字符串
	// 1. "CONTENT_LENGTH_EXCEEDS_THRESHOLD" - AWS 官方错误码
	// 2. "content length exceeds" - 错误消息文本
	return strings.Contains(errorStr, "CONTENT_LENGTH_EXCEEDS_THRESHOLD") ||
		strings.Contains(strings.ToLower(errorStr), "content length exceeds")
}

// BuildKiroContentLengthExceedsResponse 构建内容长度超限的 SSE 响应
// 将 CodeWhisperer 的 CONTENT_LENGTH_EXCEEDS_THRESHOLD 错误转换为
// Claude API 兼容的 max_tokens stop_reason 响应
// 参考 kiro2api: error_mapper.go ContentLengthExceedsStrategy
func BuildKiroContentLengthExceedsResponse(w http.ResponseWriter, model string, inputTokens int) {
	// 设置 SSE 响应头
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)

	// 生成消息 ID
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())

	// 1. 发送 message_start 事件
	messageStart := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":           msgID,
			"type":         "message",
			"role":         "assistant",
			"content":      []any{},
			"model":        model,
			"stop_reason":  nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  inputTokens,
				"output_tokens": 0,
			},
		},
	}
	if data, err := sonic.Marshal(messageStart); err == nil {
		fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// 2. 发送 message_delta 事件（包含 max_tokens stop_reason）
	messageDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   "max_tokens",
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": 0,
		},
	}
	if data, err := sonic.Marshal(messageDelta); err == nil {
		fmt.Fprintf(w, "event: message_delta\ndata: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// 3. 发送 message_stop 事件
	messageStop := map[string]any{
		"type": "message_stop",
	}
	if data, err := sonic.Marshal(messageStop); err == nil {
		fmt.Fprintf(w, "event: message_stop\ndata: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	log.Printf("[INFO] [Kiro] 内容长度超限，已转换为 max_tokens stop_reason")
}

// ============================================================================
// Kiro 输入 Token 估算
// ============================================================================

// estimateKiroInputTokens 估算 Kiro 请求的输入 token 数量
// 使用原始 Anthropic 请求体进行快速估算（纯算法，无依赖）
// 注意：这是快速估算，用于监控统计，误差约 10-20%
func estimateKiroInputTokens(anthropicBody []byte) int {
	if len(anthropicBody) == 0 {
		return 0
	}

	// 解析 Anthropic 请求体
	var anthropicReq map[string]any
	if err := sonic.Unmarshal(anthropicBody, &anthropicReq); err != nil {
		// 解析失败，使用字符数估算
		return len(anthropicBody) / 4
	}

	// 构建 CountTokensRequest 用于估算
	req := &CountTokensRequest{
		Model: getStringField(anthropicReq, "model"),
	}

	// 提取 system
	if system, ok := anthropicReq["system"]; ok {
		req.System = system
	}

	// 提取 messages
	if messages, ok := anthropicReq["messages"].([]any); ok {
		for _, msg := range messages {
			if msgMap, ok := msg.(map[string]any); ok {
				req.Messages = append(req.Messages, MessageParam{
					Role:    getStringField(msgMap, "role"),
					Content: msgMap["content"],
				})
			}
		}
	}

	// 提取 tools
	if tools, ok := anthropicReq["tools"].([]any); ok {
		for _, tool := range tools {
			if toolMap, ok := tool.(map[string]any); ok {
				req.Tools = append(req.Tools, Tool{
					Name:        getStringField(toolMap, "name"),
					Description: getStringField(toolMap, "description"),
					InputSchema: toolMap["input_schema"],
				})
			}
		}
	}

	// 使用现有的估算函数
	return EstimateInputTokens(req)
}

// getStringField 从 map 中安全获取字符串字段
func getStringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
