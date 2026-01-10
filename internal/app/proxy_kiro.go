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
		log.Printf("[ERROR] [Kiro] 上游返回错误: status=%d, body=%s", resp.StatusCode, string(errorBody))

		return &fwResult{
			Status: resp.StatusCode,
			Header: resp.Header.Clone(),
			Body:   errorBody,
		}, duration, nil
	}

	// 处理响应
	contentType := resp.Header.Get("Content-Type")

	if strings.Contains(contentType, "text/event-stream") {
		// 流式响应
		parser, err := StreamCopyKiroSSE(ctx, resp.Body, w)
		if err != nil && err != io.EOF {
			log.Printf("[WARN] [Kiro] SSE 流处理错误: %v", err)
		}

		inputTokens, outputTokens := parser.GetUsage()
		return &fwResult{
			Status:       http.StatusOK,
			Header:       resp.Header.Clone(),
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		}, duration, nil
	}

	// 非流式响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, duration, fmt.Errorf("read response body: %w", err)
	}

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
			break
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
		// 上下文使用事件，可忽略或记录
		// log.Printf("[DEBUG] [Kiro] contextUsage: %v", payloadMap)

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
func handleKiroAssistantResponseEvent(w http.ResponseWriter, flusher http.Flusher, payloadMap map[string]any, parser *kiroSSEParser) {
	content, ok := payloadMap["content"].(string)
	if !ok || content == "" {
		return
	}

	// 如果是第一个内容块，先发送 message_start 和 content_block_start
	if !parser.messageStarted {
		parser.messageStarted = true

		// 发送 message_start
		msgStart := map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":           "msg_kiro_" + fmt.Sprintf("%d", time.Now().UnixNano()),
				"type":         "message",
				"role":         "assistant",
				"content":      []any{},
				"model":        "claude-sonnet-4-20250514", // Kiro 使用的模型
				"stop_reason":  nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  0,
					"output_tokens": 0,
				},
			},
		}
		msgStartData, _ := sonic.Marshal(msgStart)
		writeSSEEvent(w, flusher, "message_start", msgStartData)

		// 发送 content_block_start
		blockStart := map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		}
		blockStartData, _ := sonic.Marshal(blockStart)
		writeSSEEvent(w, flusher, "content_block_start", blockStartData)
	}

	// 发送 content_block_delta
	delta := map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type": "text_delta",
			"text": content,
		},
	}
	deltaData, _ := sonic.Marshal(delta)
	writeSSEEvent(w, flusher, "content_block_delta", deltaData)

	// 累计输出 token（粗略估计）
	parser.outputTokens += len(content) / 4
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
func handleKiroToolUseEvent(w http.ResponseWriter, flusher http.Flusher, payloadMap map[string]any, parser *kiroSSEParser) {
	// 工具调用事件，转换为 Anthropic 格式
	// 暂时简单处理，后续可以完善
	parser.hasToolUse = true
}

// handleKiroCompletionEvent 处理 Kiro completionEvent
func handleKiroCompletionEvent(w http.ResponseWriter, flusher http.Flusher, payloadMap map[string]any, parser *kiroSSEParser) {
	// 发送 content_block_stop
	if parser.messageStarted {
		blockStop := map[string]any{
			"type":  "content_block_stop",
			"index": 0,
		}
		blockStopData, _ := sonic.Marshal(blockStop)
		writeSSEEvent(w, flusher, "content_block_stop", blockStopData)

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

	// 工具调用跟踪
	hasToolUse            bool
	toolUseIdByBlockIndex map[int]string
	completedToolUseIds   map[string]bool

	// 统计信息
	outputTokens int
	inputTokens  int
}

func newKiroSSEParser() *kiroSSEParser {
	return &kiroSSEParser{
		toolUseIdByBlockIndex: make(map[int]string),
		completedToolUseIds:   make(map[string]bool),
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
