package app

import (
	"bufio"
	"bytes"
	"context"
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
	cfg *model.Config,
	reqCtx *proxyRequestContext,
	kiroBody []byte,
	w http.ResponseWriter,
) (*fwResult, float64, error) {
	startTime := time.Now()

	// 检查 Access Token
	if reqCtx.kiroAccessToken == "" {
		return nil, 0, fmt.Errorf("kiro access token is empty")
	}

	// 发送请求到 Kiro API
	resp, err := s.ForwardKiroRequest(ctx, kiroBody, reqCtx.kiroAccessToken, reqCtx.isStreaming)
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
) (*http.Response, error) {
	// 创建请求
	req, err := http.NewRequestWithContext(ctx, "POST", KiroAPIEndpoint, bytes.NewReader(kiroBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// 设置请求头
	headers := BuildKiroRequestHeaders(accessToken, isStreaming)
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

// StreamCopyKiroSSE 流式复制 Kiro SSE 响应并转换格式
// Kiro (CodeWhisperer) 的 SSE 格式与 Anthropic 基本兼容，可以直接透传
// 只需处理少数特殊情况：
// 1. exception 事件 -> 映射为 max_tokens 或错误
// 2. 工具调用跟踪 -> 正确设置 stop_reason
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

	reader := bufio.NewReader(body)
	var eventType string
	var dataBuffer bytes.Buffer

	for {
		select {
		case <-ctx.Done():
			return parser, ctx.Err()
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				// 处理最后一个事件
				if dataBuffer.Len() > 0 {
					processKiroSSEEvent(w, flusher, eventType, dataBuffer.Bytes(), parser)
				}
				return parser, nil
			}
			return parser, err
		}

		line = bytes.TrimRight(line, "\r\n")

		// 空行表示事件结束
		if len(line) == 0 {
			if dataBuffer.Len() > 0 {
				processKiroSSEEvent(w, flusher, eventType, dataBuffer.Bytes(), parser)
				eventType = ""
				dataBuffer.Reset()
			}
			continue
		}

		// 解析 SSE 字段
		if bytes.HasPrefix(line, []byte("event:")) {
			eventType = strings.TrimSpace(string(line[6:]))
		} else if bytes.HasPrefix(line, []byte("data:")) {
			data := bytes.TrimPrefix(line, []byte("data:"))
			if dataBuffer.Len() > 0 {
				dataBuffer.WriteByte('\n')
			}
			dataBuffer.Write(bytes.TrimSpace(data))
		}
	}
}

// processKiroSSEEvent 处理单个 Kiro SSE 事件
func processKiroSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, data []byte, parser *kiroSSEParser) {
	// 解析 JSON 数据
	var dataMap map[string]any
	if err := sonic.Unmarshal(data, &dataMap); err != nil {
		// JSON 解析失败，直接透传
		writeSSEEvent(w, flusher, eventType, data)
		return
	}

	// 获取事件类型（可能在 data 中）
	if dataType, ok := dataMap["type"].(string); ok && eventType == "" {
		eventType = dataType
	}

	// 处理特殊事件
	switch eventType {
	case "exception":
		// 处理异常事件
		if handleKiroException(w, flusher, dataMap, parser) {
			return // 已处理，不透传
		}

	case "content_block_start":
		// 跟踪工具调用
		parser.trackToolUseStart(dataMap)

	case "content_block_stop":
		// 跟踪工具调用结束
		parser.trackToolUseStop(dataMap)

	case "message_delta":
		// 提取 usage 信息
		parser.extractUsage(dataMap)
	}

	// 透传事件
	writeSSEEvent(w, flusher, eventType, data)
}

// handleKiroException 处理 Kiro 异常事件
// 返回 true 表示已处理，不需要透传
func handleKiroException(w http.ResponseWriter, flusher http.Flusher, dataMap map[string]any, parser *kiroSSEParser) bool {
	exceptionType, _ := dataMap["exception_type"].(string)

	// 检查是否为内容长度超限异常
	if exceptionType == "ContentLengthExceededException" ||
		strings.Contains(exceptionType, "CONTENT_LENGTH_EXCEEDS") {

		log.Printf("[INFO] [Kiro] 检测到内容长度超限异常，映射为 max_tokens")

		// 发送 message_delta 事件（max_tokens）
		maxTokensEvent := map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   "max_tokens",
				"stop_sequence": nil,
			},
			"usage": map[string]any{
				"output_tokens": parser.outputTokens,
			},
		}
		eventData, _ := sonic.Marshal(maxTokensEvent)
		writeSSEEvent(w, flusher, "message_delta", eventData)

		// 发送 message_stop 事件
		stopEvent := map[string]any{"type": "message_stop"}
		stopData, _ := sonic.Marshal(stopEvent)
		writeSSEEvent(w, flusher, "message_stop", stopData)

		return true
	}

	return false
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
	// 工具调用跟踪
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
