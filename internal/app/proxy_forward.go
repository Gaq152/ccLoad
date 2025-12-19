package app

import (
	"bufio"
	"bytes"
	"ccLoad/internal/config"
	"ccLoad/internal/model"
	"ccLoad/internal/util"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// SSEProbeSize 用于探测 text/plain 内容是否包含 SSE 事件的前缀长度（2KB 足够覆盖小事件）
	SSEProbeSize = 2 * 1024
)

// ============================================================================
// 请求构建和转发
// ============================================================================

// buildProxyRequest 构建上游代理请求（统一处理URL、Header、认证）
// 从proxy.go提取，遵循SRP原则
// codexHeaders: Codex 渠道的额外请求头（非 Codex 渠道传 nil）
// isGeminiCLI: 是否为 Gemini CLI 官方预设（需要特殊认证）
func (s *Server) buildProxyRequest(
	reqCtx *requestContext,
	cfg *model.Config,
	apiKey string,
	method string,
	body []byte,
	hdr http.Header,
	rawQuery, requestPath string,
	codexHeaders *CodexExtraHeaders,
	isGeminiCLI bool,
) (*http.Request, error) {
	// 1. 构建完整 URL
	var upstreamURL string
	if cfg.ChannelType == "codex" {
		// Codex 渠道（官方和非官方）：统一去掉 /v1 前缀
		// Codex CLI 发送 /v1/responses，但上游 API 期望 /responses
		codexPath := strings.TrimPrefix(requestPath, "/v1")
		upstreamURL = strings.TrimRight(cfg.URL, "/") + codexPath
		if rawQuery != "" {
			upstreamURL += "?" + rawQuery
		}
	} else if cfg.ChannelType == "gemini" && cfg.Preset == "official" {
		// Gemini 官方预设：根据端点类型决定是否转换
		if IsGeminiCLIEndpoint(cfg.URL) {
			// cloudcode-pa 端点：转换路径格式
			// /v1beta/models/gemini-2.5-flash:streamGenerateContent → /v1internal:streamGenerateContent?alt=sse
			geminiPath := ConvertGeminiPath(requestPath)
			upstreamURL = strings.TrimRight(cfg.URL, "/") + geminiPath
			// Gemini CLI 路径已包含查询参数（?alt=sse），不再追加 rawQuery
		} else {
			// generativelanguage 等标准端点：保持原路径，只用 Bearer 认证
			upstreamURL = buildUpstreamURL(cfg, requestPath, rawQuery)
		}
	} else {
		// 其他渠道：URL + 请求路径
		upstreamURL = buildUpstreamURL(cfg, requestPath, rawQuery)
	}

	// 2. 创建带上下文的请求
	req, err := buildUpstreamRequest(reqCtx.ctx, method, upstreamURL, body)
	if err != nil {
		return nil, err
	}

	// 3. 复制请求头
	copyRequestHeaders(req, hdr)

	// 4. 注入认证头
	if codexHeaders != nil {
		// Codex 渠道使用专用头注入
		injectCodexHeaders(req, apiKey, codexHeaders)
	} else if isGeminiCLI {
		// Gemini CLI 官方预设（cloudcode-pa 端点）使用专用头注入
		InjectGeminiCLIHeaders(req, apiKey)
	} else if cfg.ChannelType == "gemini" && cfg.Preset == "official" {
		// Gemini 标准 API 端点（generativelanguage 等）使用 OAuth Bearer 认证
		// 与 CLI 端点不同，标准 API 不需要路径/请求体转换，只需替换认证方式
		InjectGeminiOAuthHeaders(req, apiKey)
	} else {
		// 其他渠道使用通用头注入
		injectAPIKeyHeaders(req, apiKey, requestPath)
	}

	return req, nil
}

// ============================================================================
// 响应处理
// ============================================================================

// handleRequestError 处理网络请求错误
// 从proxy.go提取，遵循SRP原则
func (s *Server) handleRequestError(
	reqCtx *requestContext,
	cfg *model.Config,
	err error,
) (*fwResult, float64, error) {
	reqCtx.stopFirstByteTimer()
	duration := reqCtx.Duration()

	// 检测超时错误：使用统一的内部状态码+冷却策略
	var statusCode int
	if reqCtx.firstByteTimeoutTriggered() {
		// 流式请求首字节超时（定时器触发）
		statusCode = util.StatusFirstByteTimeout
		timeoutMsg := fmt.Sprintf("upstream first byte timeout after %.2fs", duration)
		timeout := s.firstByteTimeout
		if timeout > 0 {
			timeoutMsg = fmt.Sprintf("%s (threshold=%v)", timeoutMsg, timeout)
		}
		err = fmt.Errorf("%s: %w", timeoutMsg, util.ErrUpstreamFirstByteTimeout)
		log.Printf("[TIMEOUT] [上游首字节超时] 渠道ID=%d, 阈值=%v, 实际耗时=%.2fs", cfg.ID, timeout, duration)
	} else if errors.Is(err, context.DeadlineExceeded) {
		if reqCtx.isStreaming {
			// 流式请求超时
			err = fmt.Errorf("upstream timeout after %.2fs (streaming): %w", duration, err)
			statusCode = util.StatusFirstByteTimeout
			log.Printf("[TIMEOUT] [流式请求超时] 渠道ID=%d, 耗时=%.2fs", cfg.ID, duration)
		} else {
			// 非流式请求超时（context.WithTimeout触发）
			err = fmt.Errorf("upstream timeout after %.2fs (non-stream, threshold=%v): %w",
				duration, s.nonStreamTimeout, err)
			statusCode = 504 // Gateway Timeout
			log.Printf("[TIMEOUT] [非流式请求超时] 渠道ID=%d, 阈值=%v, 耗时=%.2fs", cfg.ID, s.nonStreamTimeout, duration)
		}
	} else {
		// 其他错误：使用统一分类器
		statusCode, _, _ = util.ClassifyError(err)
	}

	return &fwResult{
		Status:        statusCode,
		Body:          []byte(err.Error()),
		FirstByteTime: duration,
	}, duration, err
}

// handleErrorResponse 处理错误响应（读取完整响应体）
// 从proxy.go提取，遵循SRP原则
// 限制错误体大小防止 OOM（与入站 DefaultMaxBodyBytes 限制对称）
func (s *Server) handleErrorResponse(
	reqCtx *requestContext,
	resp *http.Response,
	firstByteTime float64,
	hdrClone http.Header,
) (*fwResult, float64, error) {
	rb, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(config.DefaultMaxBodyBytes)))
	if readErr != nil {
		s.AddLogAsync(&model.LogEntry{
			Time:    model.JSONTime{Time: time.Now()},
			Message: fmt.Sprintf("error reading upstream body: %v", readErr),
		})
	}

	duration := reqCtx.Duration()

	// 524 错误响应体日志
	if resp.StatusCode == 524 {
		bodyPreview := string(rb)
		if len(bodyPreview) > 500 {
			bodyPreview = bodyPreview[:500] + "...(truncated)"
		}
		log.Printf("[DEBUG-524] 总耗时: %.2fs 响应体: %s", duration, bodyPreview)
	}

	return &fwResult{
		Status:        resp.StatusCode,
		Header:        hdrClone,
		Body:          rb,
		FirstByteTime: firstByteTime,
	}, duration, nil
}

// streamAndParseResponse 根据Content-Type选择合适的流式传输策略并解析usage
// requestPath: 请求路径，用于判断是否需要格式转换
// requestURL: 完整请求URL，用于调试日志
// 返回: (usageParser, streamErr)
func streamAndParseResponse(ctx context.Context, body io.ReadCloser, w http.ResponseWriter, contentType string, channelType string, isStreaming bool, requestPath string, requestURL string) (usageParser, error) {
	// [INFO] Codex 渠道特殊处理
	if channelType == util.ChannelTypeCodex && strings.Contains(contentType, "text/event-stream") {
		// 判断是否为原生 Responses API 请求（/v1/responses 或 /responses）
		// 如果是，直接透传 Responses API 格式，不进行转换
		// 只有 /v1/chat/completions 等非原生路径才需要转换
		isResponsesAPI := strings.HasSuffix(requestPath, "/responses")
		if isResponsesAPI {
			// 原生 Codex 客户端请求，直接透传，不转换格式
			parser := newSSEUsageParser(channelType)
			err := streamCopySSE(ctx, body, w, parser.Feed)
			return parser, err
		}
		// 非原生路径（如 /v1/chat/completions），需要将 Responses API 转换为 Chat Completions 格式
		transformer, err := StreamCopyCodexSSE(ctx, body, w)
		// 使用 codexUsageAdapter 包装 transformer 以实现 usageParser 接口
		return &codexUsageAdapter{transformer: transformer}, err
	}

	// SSE流式响应
	if strings.Contains(contentType, "text/event-stream") {
		parser := newSSEUsageParser(channelType)
		err := streamCopySSE(ctx, body, w, parser.Feed)
		return parser, err
	}

	// 非标准SSE场景：上游以text/plain发送SSE事件
	if strings.Contains(contentType, "text/plain") && isStreaming {
		reader := bufio.NewReader(body)
		probe, _ := reader.Peek(SSEProbeSize)

		if looksLikeSSE(probe) {
			parser := newSSEUsageParser(channelType)
			err := streamCopySSE(ctx, io.NopCloser(reader), w, parser.Feed)
			return parser, err
		}
		parser := newJSONUsageParser(channelType, requestURL)
		err := streamCopy(ctx, io.NopCloser(reader), w, parser.Feed)
		return parser, err
	}

	// 非SSE响应：边转发边缓存
	parser := newJSONUsageParser(channelType, requestURL)
	err := streamCopy(ctx, body, w, parser.Feed)
	return parser, err
}

// isClientDisconnectError 判断是否为客户端主动断开导致的错误
// 只识别明确的客户端取消信号，不包括上游服务器错误
// 注意：http2: response body closed 和 stream error 是上游服务器问题，不是客户端断开！
func isClientDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	// context.Canceled 是明确的客户端取消信号（用户点"停止"）
	if errors.Is(err, context.Canceled) {
		return true
	}
	// "client disconnected" 是 gin/net/http 报告的客户端断开
	// 注意：http2: response body closed 和 stream error 是上游服务器问题，
	// 不应在此判断，否则会导致上游异常被忽略而不触发冷却逻辑
	errStr := err.Error()
	return strings.Contains(errStr, "client disconnected")
}

// buildStreamDiagnostics 生成流诊断消息
// 触发条件：流传输错误且未检测到流结束标志（[DONE]/message_stop）
// streamComplete: 是否检测到流结束标志（比 hasUsage 更可靠，因为不是所有请求都有 usage）
func buildStreamDiagnostics(streamErr error, readStats *streamReadStats, streamComplete bool, channelType string, contentType string) string {
	if readStats == nil {
		return ""
	}

	bytesRead := readStats.totalBytes
	readCount := readStats.readCount

	// 流传输异常中断(排除客户端主动断开)
	// 关键：如果检测到流结束标志（[DONE]/message_stop），说明流已完整传输
	if streamErr != nil && !isClientDisconnectError(streamErr) {
		// 已检测到流结束标志 = 流完整，http2关闭只是正常结束信号
		if streamComplete {
			return "" // 不触发冷却，数据已完整
		}
		return fmt.Sprintf("[WARN] 流传输中断: 错误=%v | 已读取=%d字节(分%d次) | 流结束标志=%v | 渠道=%s | Content-Type=%s",
			streamErr, bytesRead, readCount, streamComplete, channelType, contentType)
	}

	return ""
}

// handleSuccessResponse 处理成功响应（流式传输）
// requestPath: 请求路径，用于判断 Codex 渠道是否需要格式转换
// requestURL: 完整请求URL，用于调试日志
func (s *Server) handleSuccessResponse(
	reqCtx *requestContext,
	resp *http.Response,
	firstByteTime float64,
	hdrClone http.Header,
	w http.ResponseWriter,
	channelType string,
	_ *int64,
	_ string,
	requestPath string,
	requestURL string,
) (*fwResult, float64, error) {
	// 写入响应头
	filterAndWriteResponseHeaders(w, resp.Header)
	w.WriteHeader(resp.StatusCode)

	// 设置读取统计（流式和非流式都需要，用于判断数据是否传输）
	actualFirstByteTime := firstByteTime
	readStats := &streamReadStats{}
	resp.Body = &firstByteDetector{
		ReadCloser: resp.Body,
		stats:      readStats,
		onFirstRead: func() {
			if reqCtx.isStreaming {
				actualFirstByteTime = reqCtx.Duration()
			}
		},
	}

	// 流式传输并解析usage
	contentType := resp.Header.Get("Content-Type")
	usageParser, streamErr := streamAndParseResponse(
		reqCtx.ctx, resp.Body, w, contentType, channelType, reqCtx.isStreaming, requestPath, requestURL,
	)

	// 构建结果
	result := &fwResult{
		Status:        resp.StatusCode,
		Header:        hdrClone,
		FirstByteTime: actualFirstByteTime,
	}

	// 提取usage数据和错误事件
	var streamComplete bool
	if usageParser != nil {
		result.InputTokens, result.OutputTokens, result.CacheReadInputTokens, result.CacheCreationInputTokens = usageParser.GetUsage()
		if errorEvent := usageParser.GetLastError(); errorEvent != nil {
			result.SSEErrorEvent = errorEvent
		}
		streamComplete = usageParser.IsStreamComplete()
	}

	// 生成流诊断消息（仅流请求）
	if reqCtx.isStreaming {
		// [VALIDATE] 诊断增强: 传递contentType帮助定位问题(区分SSE/JSON/其他)
		// 使用 streamComplete 而非 hasUsage，因为不是所有请求都有 usage 信息
		if diagMsg := buildStreamDiagnostics(streamErr, readStats, streamComplete, channelType, contentType); diagMsg != "" {
			result.StreamDiagMsg = diagMsg
			log.Print(diagMsg)
		} else if streamComplete && streamErr != nil && !isClientDisconnectError(streamErr) {
			// [FIX] 流式请求：检测到流结束标志（[DONE]/message_stop）说明数据完整
			// http2流关闭只是正常结束信号，清除streamErr避免被误判为网络错误
			streamErr = nil
		}
	} else {
		// [FIX] 非流式请求：如果有数据被传输，且错误是 HTTP/2 流关闭相关的，视为成功
		// 原因：streamCopy 已将数据写入 ResponseWriter，客户端已收到完整响应
		// http2 流关闭只是 "确认结束" 阶段的错误，不影响已传输的数据
		if readStats.totalBytes > 0 && streamErr != nil && isHTTP2StreamCloseError(streamErr) {
			streamErr = nil
		}
	}

	return result, reqCtx.Duration(), streamErr
}

// isHTTP2StreamCloseError 判断是否是 HTTP/2 流关闭相关的错误
// 这类错误发生在数据传输完成后，不影响已传输的数据完整性
func isHTTP2StreamCloseError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "http2: response body closed") ||
		strings.Contains(errStr, "stream error:")
}

// looksLikeSSE 粗略判断文本内容是否包含 SSE 事件结构
func looksLikeSSE(data []byte) bool {
	// 同时包含 event: 与 data: 行的简单特征，可匹配大多数 SSE 片段
	return bytes.Contains(data, []byte("event:")) && bytes.Contains(data, []byte("data:"))
}

// handleResponse 处理 HTTP 响应（错误或成功）
// 从proxy.go提取，遵循SRP原则
// channelType: 渠道类型,用于精确识别usage格式
// cfg: 渠道配置,用于提取渠道ID
// apiKey: 使用的API Key,用于日志记录
// requestPath: 请求路径，用于判断 Codex 渠道是否需要格式转换
func (s *Server) handleResponse(
	reqCtx *requestContext,
	resp *http.Response,
	firstByteTime float64,
	w http.ResponseWriter,
	channelType string,
	cfg *model.Config,
	apiKey string,
	requestPath string,
) (*fwResult, float64, error) {
	hdrClone := resp.Header.Clone()

	// 错误状态：读取完整响应体
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 524 错误详细调试日志（Cloudflare 源站超时）
		if resp.StatusCode == 524 {
			log.Printf("[DEBUG-524] Cloudflare超时错误详情:")
			log.Printf("[DEBUG-524] 渠道: ID=%d 名称=%s URL=%s", cfg.ID, cfg.Name, cfg.URL)
			log.Printf("[DEBUG-524] 首字节耗时: %.2fs", firstByteTime)
			log.Printf("[DEBUG-524] 响应头: %v", resp.Header)
		}
		return s.handleErrorResponse(reqCtx, resp, firstByteTime, hdrClone)
	}

	// [INFO] 空响应检测：200状态码但Content-Length=0视为上游故障
	// 常见于CDN/代理错误、认证失败等异常场景，应触发渠道级重试
	if contentLen := resp.Header.Get("Content-Length"); contentLen == "0" {
		duration := reqCtx.Duration()
		err := fmt.Errorf("upstream returned empty response (200 OK with Content-Length: 0)")

		return &fwResult{
			Status:        resp.StatusCode, // 保留原始200状态码
			Header:        hdrClone,
			Body:          []byte(err.Error()),
			FirstByteTime: firstByteTime,
		}, duration, err
	}

	// 成功状态：流式转发（传递渠道信息用于日志记录）
	channelID := &cfg.ID
	requestURL := cfg.URL + requestPath // 构建完整请求URL用于调试日志
	return s.handleSuccessResponse(reqCtx, resp, firstByteTime, hdrClone, w, channelType, channelID, apiKey, requestPath, requestURL)
}

// ============================================================================
// 核心转发函数
// ============================================================================

// forwardOnceAsync 异步流式转发，透明转发客户端原始请求
// 从proxy.go提取，遵循SRP原则
// 参数新增 apiKey 用于直接传递已选中的API Key（从KeySelector获取）
// 参数新增 method 用于支持任意HTTP方法（GET、POST、PUT、DELETE等）
// 参数新增 codexHeaders 用于 Codex 渠道的额外请求头（非 Codex 渠道传 nil）
// 参数新增 isGeminiCLI 用于 Gemini CLI 官方预设的特殊认证
func (s *Server) forwardOnceAsync(ctx context.Context, cfg *model.Config, apiKey string, method string, body []byte, hdr http.Header, rawQuery, requestPath string, w http.ResponseWriter, codexHeaders *CodexExtraHeaders, isGeminiCLI bool) (*fwResult, float64, error) {
	// 1. 创建请求上下文（处理超时）
	reqCtx := s.newRequestContext(ctx, requestPath, body)
	defer reqCtx.cleanup() // [INFO] 统一清理：定时器 + context（总是安全）

	// 2. 构建上游请求
	req, err := s.buildProxyRequest(reqCtx, cfg, apiKey, method, body, hdr, rawQuery, requestPath, codexHeaders, isGeminiCLI)
	if err != nil {
		return nil, 0, err
	}

	// 3. 发送请求
	resp, err := s.client.Do(req)

	// [INFO] 修复（2025-12）：客户端取消时主动关闭 response body，立即中断上游传输
	// 问题：streamCopy 中的 Read 阻塞时，无法立即响应 context 取消，上游继续生成完整响应
	// 解决：使用 Go 1.21+ context.AfterFunc 替代手动 goroutine（零泄漏风险）
	//   - HTTP/1.1: 关闭 TCP 连接 → 上游收到 RST，立即停止发送
	//   - HTTP/2: 发送 RST_STREAM 帧 → 取消当前 stream（不影响同连接的其他请求）
	// 效果：避免 AI 流式生成场景下，用户点"停止"后上游仍生成数千 tokens 的浪费
	if resp != nil {
		// 使用 sync.Once 确保 body 只关闭一次（协调 defer 和 AfterFunc）
		var bodyCloseOnce sync.Once
		closeBodySafely := func() {
			bodyCloseOnce.Do(func() {
				resp.Body.Close()
			})
		}

		// [INFO] 使用 context.AfterFunc 监听客户端取消（Go 1.21+，标准库保证无泄漏）
		stop := context.AfterFunc(ctx, closeBodySafely)
		defer stop() // 取消注册（请求正常结束时避免内存泄漏）

		// 正常返回时关闭（与 AfterFunc 互斥，Once 保证只执行一次）
		defer closeBodySafely()
	}

	if err != nil {
		return s.handleRequestError(reqCtx, cfg, err)
	}

	// 4. 首字节到达，停止计时器
	reqCtx.stopFirstByteTimer()
	firstByteTime := reqCtx.Duration()

	// 5. 处理响应(传递channelType用于精确识别usage格式,传递渠道信息用于日志记录)
	return s.handleResponse(reqCtx, resp, firstByteTime, w, cfg.ChannelType, cfg, apiKey, requestPath)
}

// ============================================================================
// 单次转发尝试
// ============================================================================

// forwardAttempt 单次转发尝试（包含错误处理和日志记录）
// 从proxy.go提取，遵循SRP原则
// 返回：(proxyResult, shouldContinueRetry, shouldBreakToNextChannel)
func (s *Server) forwardAttempt(
	ctx context.Context,
	cfg *model.Config,
	keyIndex int,
	selectedKey string,
	reqCtx *proxyRequestContext,
	actualModel string, // [INFO] 重定向后的实际模型名称
	bodyToSend []byte,
	w http.ResponseWriter,
) (*proxyResult, bool, bool) {
	// [VALIDATE] Key级验证器检查(88code套餐验证等)
	// 每个Key单独验证，避免误杀免费key或误放付费key
	if s.validatorManager != nil {
		available, reason := s.validatorManager.ValidateChannel(ctx, cfg, selectedKey)
		if !available {
			// Key验证失败: 跳过此key，尝试下一个
			log.Printf("[VALIDATE] 渠道 %s (ID=%d) Key#%d 验证失败: %s, 跳过", cfg.Name, cfg.ID, keyIndex, reason)
			return nil, true, false // shouldContinue=true, shouldBreak=false
		}
	}

	// 转发请求（传递实际的API Key字符串）
	// [INFO] Codex 渠道额外传递 codexHeaders，Gemini CLI 传递 isGeminiCLI 标志
	res, duration, err := s.forwardOnceAsync(ctx, cfg, selectedKey, reqCtx.requestMethod,
		bodyToSend, reqCtx.header, reqCtx.rawQuery, reqCtx.requestPath, w, reqCtx.codexHeaders, reqCtx.isGeminiCLI)

	// 处理网络错误或异常响应（如空响应）
	// [INFO] 修复：handleResponse可能返回err即使StatusCode=200（例如Content-Length=0）
	if err != nil {
		return s.handleNetworkError(ctx, cfg, keyIndex, actualModel, selectedKey, reqCtx.tokenID, reqCtx.clientIP, duration, err)
	}

	// 处理成功响应（仅当err==nil且状态码2xx时）
	if res.Status >= 200 && res.Status < 300 {
		// [INFO] 检查SSE流中是否有error事件（如1308错误）
		// 虽然HTTP状态码是200，但error事件表示实际上发生了错误，需要触发冷却逻辑
		if res.SSEErrorEvent != nil {
			// [FIX] 流式响应已写出数据，不能重试（会导致混流和token倍增）
			// 只触发冷却，不返回重试动作
			log.Printf("[WARN]  [SSE错误处理] HTTP状态码200但检测到SSE error事件，触发冷却但不重试（避免混流）")
			res.Body = res.SSEErrorEvent
			// 触发冷却但不重试
			_, _ = s.cooldownManager.HandleError(ctx, cfg.ID, keyIndex, 200, res.SSEErrorEvent, false, nil)
			s.invalidateChannelRelatedCache(cfg.ID)
			// 记录失败日志
			s.AddLogAsync(buildLogEntry(actualModel, cfg.ID, cfg.Name, 200,
				duration, reqCtx.isStreaming, selectedKey, cfg.URL, reqCtx.tokenID, reqCtx.clientIP, res, "SSE error event"))
			// 返回成功（因为已经写出了200状态码和部分数据）
			return &proxyResult{
				status:    200,
				channelID: &cfg.ID,
				message:   "partial success with SSE error",
				duration:  duration,
				succeeded: true, // 标记为成功，避免上层重试
			}, false, false
		}

		// [INFO] 检查流响应是否不完整（2025-12新增）
		// 虽然HTTP状态码是200且流传输结束，但检测到流响应不完整或流传输中断，需要触发冷却逻辑
		// 触发条件：(1) 流传输错误  (2) 流式请求但没有usage数据（疑似不完整响应）
		if res.StreamDiagMsg != "" {
			// [FIX] 流式响应已写出数据，不能重试（会导致混流和token倍增）
			// 只触发冷却，不返回重试动作
			log.Printf("[WARN]  [流响应不完整] HTTP状态码200但检测到流响应不完整，触发冷却但不重试（避免混流）: %s", res.StreamDiagMsg)
			// 触发冷却但不重试
			_, _ = s.cooldownManager.HandleError(ctx, cfg.ID, keyIndex, util.StatusStreamIncomplete, []byte(res.StreamDiagMsg), false, nil)
			s.invalidateChannelRelatedCache(cfg.ID)
			// 记录失败日志
			s.AddLogAsync(buildLogEntry(actualModel, cfg.ID, cfg.Name, util.StatusStreamIncomplete,
				duration, reqCtx.isStreaming, selectedKey, cfg.URL, reqCtx.tokenID, reqCtx.clientIP, res, res.StreamDiagMsg))
			// 返回成功（因为已经写出了200状态码和部分数据）
			return &proxyResult{
				status:    200,
				channelID: &cfg.ID,
				message:   "partial success with incomplete stream",
				duration:  duration,
				succeeded: true, // 标记为成功，避免上层重试
			}, false, false
		}

		return s.handleProxySuccess(ctx, cfg, keyIndex, actualModel, selectedKey, res, duration, reqCtx)
	}

	// 处理错误响应
	return s.handleProxyErrorResponse(ctx, cfg, keyIndex, actualModel, selectedKey, res, duration, reqCtx)
}

// ============================================================================
// 渠道内Key重试
// ============================================================================

// tryChannelWithKeys 在单个渠道内尝试多个Key（Key级重试）
// 从proxy.go提取，遵循SRP原则
func (s *Server) tryChannelWithKeys(ctx context.Context, cfg *model.Config, reqCtx *proxyRequestContext, w http.ResponseWriter) (*proxyResult, error) {
	// 查询渠道的API Keys（使用缓存层，<1ms vs 数据库查询10-20ms）
	// 性能优化：缓存优先，避免高并发场景下的数据库瓶颈
	apiKeys, err := s.getAPIKeys(ctx, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get API keys: %w", err)
	}

	// 计算实际重试次数
	actualKeyCount := len(apiKeys)
	if actualKeyCount == 0 {
		return nil, fmt.Errorf("no API keys configured for channel %d", cfg.ID)
	}

	maxKeyRetries := min(s.maxKeyRetries, actualKeyCount)

	triedKeys := make(map[int]bool) // 本次请求内已尝试过的Key

	// 准备请求体（处理模型重定向）
	// [INFO] 修复：保存重定向后的模型名称，用于日志记录和调试
	actualModel, bodyToSend := prepareRequestBody(cfg, reqCtx)

	// [INFO] Codex 渠道预处理：转换请求体格式（在 Key 循环外执行，避免重复转换）
	isCodexChannel := cfg.ChannelType == util.ChannelTypeCodex
	if isCodexChannel {
		reqCtx.isCodex = true
		// 转换请求体到 Codex 格式
		codexBody, err := TransformCodexRequestBody(bodyToSend)
		if err != nil {
			log.Printf("[ERROR] [Codex] 请求体转换失败: %v", err)
			return nil, fmt.Errorf("codex request transform failed: %w", err)
		}
		bodyToSend = codexBody
	}

	// [INFO] Gemini 官方预设预处理：根据端点类型决定是否转换
	// - cloudcode-pa.googleapis.com (Gemini CLI 内部端点)：需要路径+请求体转换
	// - generativelanguage.googleapis.com (标准 API)：不需要转换，只需 Bearer 认证
	isGeminiOfficial := cfg.ChannelType == util.ChannelTypeGemini && cfg.Preset == "official"
	isGeminiCLIEndpoint := isGeminiOfficial && IsGeminiCLIEndpoint(cfg.URL)
	if isGeminiOfficial {
		reqCtx.isGeminiCLI = isGeminiCLIEndpoint // 仅 CLI 端点需要特殊认证头
	}
	if isGeminiCLIEndpoint {
		// 仅 cloudcode-pa 端点需要转换请求体
		// 提取模型名称
		model := ExtractModelFromGeminiPath(reqCtx.requestPath)
		if model == "" {
			model = actualModel // 回退使用请求体中的模型名
		}
		// 转换请求体到 Gemini CLI 格式
		geminiBody, err := TransformGeminiCLIRequestBody(bodyToSend, model)
		if err != nil {
			log.Printf("[ERROR] [Gemini CLI] 请求体转换失败: %v", err)
			return nil, fmt.Errorf("gemini cli request transform failed: %w", err)
		}
		bodyToSend = geminiBody
	}

	// Key重试循环
	for range maxKeyRetries {
		// 选择可用的API Key（直接传入apiKeys，避免重复查询）
		keyIndex, selectedKey, err := s.keySelector.SelectAvailableKey(cfg.ID, apiKeys, triedKeys)
		if err != nil {
			// 所有Key都在冷却中，返回特殊错误标识（使用sentinel error而非魔法字符串）
			return nil, fmt.Errorf("%w: %v", ErrAllKeysUnavailable, err)
		}

		// 标记Key为已尝试
		triedKeys[keyIndex] = true

		// [INFO] Codex 渠道：根据预设类型处理认证
		actualKey := selectedKey
		if isCodexChannel && cfg.Preset == "official" {
			// 官方预设：从 apiKeys 中查找对应的 OAuth Token 字段
			// 注意：keyIndex 是数据库中的真实 KeyIndex，需要遍历查找
			var foundKey *model.APIKey
			for _, ak := range apiKeys {
				if ak.KeyIndex == keyIndex {
					foundKey = ak
					break
				}
			}

			if foundKey == nil || foundKey.AccessToken == "" {
				log.Printf("[WARN] [Codex] 官方预设 Key#%d 没有 AccessToken，跳过", keyIndex)
				continue
			}

			// 从数据库字段构建 OAuth Token 对象
			oauthToken := &CodexOAuthToken{
				Type:         "oauth",
				AccessToken:  foundKey.AccessToken,
				RefreshToken: foundKey.RefreshToken,
				ExpiresAt:    foundKey.TokenExpiresAt,
				AccountID:    ExtractAccountIDFromJWT(foundKey.AccessToken),
			}

			// 检查并刷新 Token
			refreshedKey, refreshedToken, err := s.RefreshCodexTokenIfNeeded(ctx, cfg.ID, keyIndex, oauthToken)
			if err != nil {
				log.Printf("[ERROR] [Codex] Token 刷新失败: %v", err)
				continue
			}

			actualKey = refreshedKey
			reqCtx.codexToken = refreshedToken
			// 生成新的请求头（每次请求使用新的 UUID）
			reqCtx.codexHeaders = NewCodexExtraHeaders(refreshedToken.AccountID)
		}

		// [INFO] Gemini 官方预设：处理 OAuth 认证（适用于所有端点）
		// - cloudcode-pa 端点：使用 CLI 格式请求头 + Bearer 认证
		// - generativelanguage 等标准端点：直接使用 Bearer 认证
		if isGeminiOfficial {
			// 官方预设：从 apiKeys 中查找对应的 OAuth Token 字段
			var foundKey *model.APIKey
			for _, ak := range apiKeys {
				if ak.KeyIndex == keyIndex {
					foundKey = ak
					break
				}
			}

			if foundKey == nil || foundKey.AccessToken == "" {
				log.Printf("[WARN] [Gemini] 官方预设 Key#%d 没有 AccessToken，跳过", keyIndex)
				continue
			}

			// 从数据库字段构建 OAuth Token 对象
			oauthToken := &GeminiOAuthToken{
				Type:         "oauth",
				AccessToken:  foundKey.AccessToken,
				RefreshToken: foundKey.RefreshToken,
				IDToken:      foundKey.IDToken,
				ExpiresAt:    foundKey.TokenExpiresAt,
				Email:        ExtractEmailFromGoogleIDToken(foundKey.IDToken),
			}

			// 检查并刷新 Token
			refreshedKey, _, err := s.RefreshGeminiTokenIfNeeded(ctx, cfg.ID, keyIndex, oauthToken)
			if err != nil {
				log.Printf("[ERROR] [Gemini] Token 刷新失败: %v", err)
				continue
			}

			actualKey = refreshedKey
		}
		// 自定义预设或其他：直接使用普通 API Key，无需特殊处理

		// 单次转发尝试（传递实际的API Key字符串）
		// [INFO] 修复：传递 actualModel 用于日志记录
		result, shouldContinue, shouldBreak := s.forwardAttempt(
			ctx, cfg, keyIndex, actualKey, reqCtx, actualModel, bodyToSend, w)

		// 如果返回了结果，直接返回
		if result != nil {
			return result, nil
		}

		// 需要切换到下一个渠道
		if shouldBreak {
			break
		}

		// 继续重试下一个Key
		if !shouldContinue {
			break
		}
	}

	// Key重试循环结束，所有Key都失败
	return nil, fmt.Errorf("all keys exhausted")
}
