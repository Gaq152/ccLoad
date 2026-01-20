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

	"ccLoad/internal/cooldown"
	"ccLoad/internal/model"
	"ccLoad/internal/storage"
	"ccLoad/internal/testutil"
	"ccLoad/internal/util"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"
)

// ==================== 渠道测试功能 ====================
// 从admin.go拆分渠道测试,遵循SRP原则

func (s *Server) HandleChannelTest(c *gin.Context) {
	// 解析渠道ID
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid channel id")
		return
	}

	// 解析请求体
	var testReq testutil.TestChannelRequest
	if err := BindAndValidate(c, &testReq); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}

	// 获取渠道配置
	cfg, err := s.store.GetConfig(c.Request.Context(), id)
	if err != nil {
		RespondError(c, http.StatusNotFound, fmt.Errorf("channel not found"))
		return
	}

	// 查询渠道的API Keys
	apiKeys, err := s.store.GetAPIKeys(c.Request.Context(), id)
	if err != nil || len(apiKeys) == 0 {
		RespondJSON(c, http.StatusOK, gin.H{
			"success": false,
			"error":   "渠道未配置有效的 API Key",
		})
		return
	}

	// 验证并选择 Key 索引
	keyIndex := testReq.KeyIndex
	if keyIndex < 0 || keyIndex >= len(apiKeys) {
		keyIndex = 0 // 默认使用第一个 Key
	}

	// [FIX] Codex/Gemini/Kiro 预设使用 AccessToken 字段，并检查是否需要刷新
	var selectedKey string
	var kiroDeviceFingerprint string // Kiro 设备指纹
	channelType := cfg.GetChannelType()
	isOAuthPreset := cfg.Preset == "official" && (channelType == util.ChannelTypeCodex || channelType == util.ChannelTypeGemini)
	isKiroPreset := cfg.Preset == "kiro" && channelType == util.ChannelTypeAnthropic

	if isKiroPreset {
		// Kiro 预设：检查 AccessToken，需要时刷新
		// [FIX] 与 Codex/Gemini 逻辑一致：优先检查 AccessToken 是否存在
		apiKeyData := apiKeys[keyIndex]

		// 检查 AccessToken 是否存在（与 Codex/Gemini 一致）
		if apiKeyData.AccessToken == "" && apiKeyData.RefreshToken == "" {
			RespondJSON(c, http.StatusOK, gin.H{
				"success": false,
				"error":   "Kiro 预设未配置有效的 Token，请先配置",
			})
			return
		}

		// 保存设备指纹
		kiroDeviceFingerprint = apiKeyData.DeviceFingerprint

		// [FIX] 如果设备指纹为空，自动生成（与 proxy_forward.go:980 逻辑一致）
		if kiroDeviceFingerprint == "" {
			fm := GetFingerprintManager()
			fp, err := fm.GenerateFingerprint()
			if err != nil {
				log.Printf("[WARN] [测试] 生成 Kiro 设备指纹失败: %v", err)
			} else {
				fpJSON, err := fp.ToJSON()
				if err != nil {
					log.Printf("[WARN] [测试] 序列化设备指纹失败: %v", err)
				} else {
					kiroDeviceFingerprint = fpJSON
					// 异步保存到数据库
					go func(channelID int64, keyIndex int, fingerprint string) {
						updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						if err := s.store.SetDeviceFingerprint(updateCtx, channelID, keyIndex, fingerprint); err != nil {
							log.Printf("[ERROR] [测试] 保存设备指纹失败 (channel=%d, key=%d): %v", channelID, keyIndex, err)
						} else {
							log.Printf("[INFO] [测试] 已为渠道 #%d Key #%d 生成并保存设备指纹: %s", channelID, keyIndex, fp.GetSummary())
						}
					}(id, keyIndex, fpJSON)
				}
			}
		}

		// 构建 Kiro 认证配置（从独立字段读取）
		kiroConfig := &KiroAuthConfig{
			AuthType:     KiroAuthMethodSocial, // 默认 Social 方式
			RefreshToken: apiKeyData.RefreshToken,
		}

		// 检查是否是 IdC 方式（id_token 字段存储了 IdC 配置 JSON）
		if apiKeyData.IDToken != "" && strings.HasPrefix(apiKeyData.IDToken, "{") {
			var idcInfo struct {
				StartUrl     string `json:"startUrl"`
				Region       string `json:"region"`
				ClientID     string `json:"clientId"`
				ClientSecret string `json:"clientSecret"`
			}
			if err := sonic.Unmarshal([]byte(apiKeyData.IDToken), &idcInfo); err == nil && idcInfo.StartUrl != "" {
				kiroConfig.AuthType = KiroAuthMethodIdC
				kiroConfig.ClientID = idcInfo.ClientID
				kiroConfig.ClientSecret = idcInfo.ClientSecret
			}
		}

		// 检查并刷新 Token（内部判断是否过期，未过期直接返回现有 AccessToken）
		accessToken, _, err := s.RefreshKiroTokenIfNeeded(
			c.Request.Context(),
			id,
			keyIndex,
			kiroConfig,
			apiKeyData.AccessToken,
			apiKeyData.TokenExpiresAt,
		)
		if err != nil {
			log.Printf("[ERROR] Kiro Token 刷新失败 (channel=%d, key=%d): %v", id, keyIndex, err)
			RespondJSON(c, http.StatusOK, gin.H{
				"success": false,
				"error":   "Kiro Token 刷新失败: " + err.Error(),
			})
			return
		}
		selectedKey = accessToken

	} else if isOAuthPreset {
		apiKeyData := apiKeys[keyIndex]

		// 根据渠道类型调用对应的刷新逻辑
		if channelType == util.ChannelTypeCodex {
			// 优先使用新架构的独立字段，兼容旧架构从 api_key 解析
			var oauthToken *CodexOAuthToken
			if apiKeyData.AccessToken != "" {
				// 新架构：直接使用独立字段
				oauthToken = &CodexOAuthToken{
					AccessToken:  apiKeyData.AccessToken,
					RefreshToken: apiKeyData.RefreshToken,
					ExpiresAt:    apiKeyData.TokenExpiresAt,
				}
			} else {
				// 旧架构兼容：从 api_key 字段解析
				_, oauthToken, _ = ParseAPIKeyOrOAuth(apiKeyData.APIKey)
			}

			if oauthToken == nil || oauthToken.AccessToken == "" {
				log.Printf("[DEBUG] Codex OAuth Token 检查失败: AccessToken长度=%d, APIKey长度=%d",
					len(apiKeyData.AccessToken), len(apiKeyData.APIKey))
				RespondJSON(c, http.StatusOK, gin.H{
					"success": false,
					"error":   "Codex 官方预设未配置有效的 OAuth Token，请先完成授权",
				})
				return
			}

			// 检查并刷新 Token（提前 5 分钟刷新）
			refreshedKey, _, err := s.RefreshCodexTokenIfNeeded(c.Request.Context(), id, keyIndex, oauthToken)
			if err != nil {
				log.Printf("[ERROR] Codex Token 刷新失败 (channel=%d, key=%d): %v", id, keyIndex, err)
				RespondJSON(c, http.StatusOK, gin.H{
					"success": false,
					"error":   "Codex Token 刷新失败: " + err.Error(),
				})
				return
			}
			selectedKey = refreshedKey

		} else { // Gemini
			// 优先使用新架构的独立字段，兼容旧架构从 api_key 解析
			var oauthToken *GeminiOAuthToken
			if apiKeyData.AccessToken != "" {
				// 新架构：直接使用独立字段
				oauthToken = &GeminiOAuthToken{
					AccessToken:  apiKeyData.AccessToken,
					IDToken:      apiKeyData.IDToken,
					RefreshToken: apiKeyData.RefreshToken,
					ExpiresAt:    apiKeyData.TokenExpiresAt,
				}
			} else {
				// 旧架构兼容：从 api_key 字段解析
				_, oauthToken, _ = ParseGeminiAPIKeyOrOAuth(apiKeyData.APIKey)
			}

			if oauthToken == nil || oauthToken.AccessToken == "" {
				log.Printf("[DEBUG] Gemini OAuth Token 检查失败: AccessToken长度=%d, APIKey长度=%d",
					len(apiKeyData.AccessToken), len(apiKeyData.APIKey))
				RespondJSON(c, http.StatusOK, gin.H{
					"success": false,
					"error":   "Gemini 官方预设未配置有效的 OAuth Token，请先完成授权",
				})
				return
			}

			// 检查并刷新 Token（提前 5 分钟刷新）
			refreshedKey, _, err := s.RefreshGeminiTokenIfNeeded(c.Request.Context(), id, keyIndex, oauthToken)
			if err != nil {
				log.Printf("[ERROR] Gemini Token 刷新失败 (channel=%d, key=%d): %v", id, keyIndex, err)
				RespondJSON(c, http.StatusOK, gin.H{
					"success": false,
					"error":   "Gemini Token 刷新失败: " + err.Error(),
				})
				return
			}
			selectedKey = refreshedKey
		}
	} else {
		selectedKey = apiKeys[keyIndex].APIKey
	}

	// 检查模型是否支持
	modelSupported := false
	for _, model := range cfg.Models {
		if model == testReq.Model {
			modelSupported = true
			break
		}
	}
	if !modelSupported {
		RespondJSON(c, http.StatusOK, gin.H{
			"success":          false,
			"error":            "模型 " + testReq.Model + " 不在此渠道的支持列表中",
			"model":            testReq.Model,
			"supported_models": cfg.Models,
		})
		return
	}

	// 执行测试（传递实际的API Key字符串）
	testResult := s.testChannelAPI(cfg, selectedKey, &testReq, kiroDeviceFingerprint)
	// 添加测试的 Key 索引信息到结果中
	testResult["tested_key_index"] = keyIndex
	testResult["total_keys"] = len(apiKeys)

	// [INFO] 修复：根据测试结果应用冷却逻辑
	if success, ok := testResult["success"].(bool); ok && success {
		// 测试成功：清除该Key的冷却状态
		if err := s.store.ResetKeyCooldown(c.Request.Context(), id, keyIndex); err != nil {
			log.Printf("[WARN] 清除Key #%d冷却状态失败: %v", keyIndex, err)
		}

		// ✨ 优化：同时清除渠道级冷却（因为至少有一个Key可用）
		// 设计理念：测试成功证明渠道恢复正常，应立即解除渠道级冷却，避免选择器过滤该渠道
		_ = s.store.ResetChannelCooldown(c.Request.Context(), id)

		// [INFO] 修复：使API Keys缓存和冷却状态缓存失效，确保前端能立即看到状态更新
		s.InvalidateAPIKeysCache(id)
		s.invalidateCooldownCache()
	} else {
		// 🔥 修复：测试失败时应用冷却策略
		// 提取状态码和错误体
		statusCode, _ := testResult["status_code"].(int)
		var errorBody []byte
		if apiError, ok := testResult["api_error"].(map[string]any); ok {
			errorBody, _ = sonic.Marshal(apiError)
		} else if rawResp, ok := testResult["raw_response"].(string); ok {
			errorBody = []byte(rawResp)
		}

		// 提取响应头（用于429错误的精确分类）
		var headers map[string][]string
		if respHeaders, ok := testResult["response_headers"].(map[string]string); ok && statusCode == 429 {
			headers = make(map[string][]string, len(respHeaders))
			for k, v := range respHeaders {
				headers[k] = []string{v}
			}
		}

		// 调用统一冷却管理器处理错误
		action, err := s.cooldownManager.HandleError(
			c.Request.Context(),
			id,
			keyIndex,
			statusCode,
			errorBody,
			false,   // 测试API不是网络错误（已经收到HTTP响应）
			headers, // 传递响应头以支持429错误的精确分类
		)
		if err != nil {
			log.Printf("[WARN] 应用冷却策略失败 (channel=%d, key=%d, status=%d): %v", id, keyIndex, statusCode, err)
			// 失败时降级尝试渠道级冷却，避免误报"已冷却"但实际未生效
			if action == cooldown.ActionRetryKey {
				if _, chErr := s.store.BumpChannelCooldown(c.Request.Context(), id, time.Now(), statusCode); chErr != nil {
					log.Printf("[WARN] 渠道级降级冷却失败 (channel=%d): %v", id, chErr)
				} else {
					action = cooldown.ActionRetryChannel
				}
			}
			testResult["cooldown_error"] = err.Error()
		}

		// [INFO] 修复：使API Keys缓存和冷却状态缓存失效，确保前端能立即看到冷却状态更新
		// 无论是Key级冷却还是渠道级冷却，都需要使缓存失效
		s.InvalidateAPIKeysCache(id)
		s.invalidateCooldownCache()

		// 记录冷却决策结果到测试响应中
		var actionStr string
		switch action {
		case cooldown.ActionRetrySameChannel:
			actionStr = "retry_same_channel_no_cooldown"
		case cooldown.ActionRetryKey:
			actionStr = "key_cooldown_applied"
		case cooldown.ActionRetryChannel:
			actionStr = "channel_cooldown_applied"
		case cooldown.ActionReturnClient:
			actionStr = "client_error_no_cooldown"
		default:
			actionStr = "unknown_action"
		}
		testResult["cooldown_action"] = actionStr
	}

	RespondJSON(c, http.StatusOK, testResult)
}

// 测试渠道API连通性
func (s *Server) testChannelAPI(cfg *model.Config, apiKey string, testReq *testutil.TestChannelRequest, deviceFingerprint string) map[string]any {
	// 设置默认测试内容（从配置读取）
	if strings.TrimSpace(testReq.Content) == "" {
		testReq.Content = s.configService.GetString("channel_test_content", "sonnet 4.0的发布日期是什么")
	}

	// [INFO] 修复：应用模型重定向逻辑（与正常代理流程保持一致）
	originalModel := testReq.Model
	actualModel := originalModel

	// 检查模型重定向
	if len(cfg.ModelRedirects) > 0 {
		if redirectModel, ok := cfg.ModelRedirects[originalModel]; ok && redirectModel != "" {
			actualModel = redirectModel
			log.Printf("[RELOAD] [测试-模型重定向] 渠道ID=%d, 原始模型=%s, 重定向模型=%s", cfg.ID, originalModel, actualModel)
		}
	}

	// 如果模型发生重定向，更新测试请求中的模型名称
	if actualModel != originalModel {
		testReq.Model = actualModel
		log.Printf("[INFO] [测试-请求体修改] 渠道ID=%d, 修改后模型=%s", cfg.ID, actualModel)
	}

	// 选择并规范化渠道类型
	channelType := util.NormalizeChannelType(testReq.ChannelType)

	// Kiro 预设特殊处理：使用 CodeWhisperer API 格式
	if cfg.Preset == "kiro" && channelType == "anthropic" {
		return s.testKiroChannel(cfg, apiKey, testReq, deviceFingerprint)
	}

	var tester testutil.ChannelTester
	switch channelType {
	case "codex":
		tester = &testutil.CodexTester{}
	case "gemini":
		tester = &testutil.GeminiTester{}
	case "anthropic":
		tester = &testutil.AnthropicTester{}
	default:
		tester = &testutil.AnthropicTester{}
	}

	// 构建请求（传递实际的API Key和重定向后的模型）
	fullURL, baseHeaders, body, err := tester.Build(cfg, apiKey, testReq)
	if err != nil {
		return map[string]any{"success": false, "error": "构造测试请求失败: " + err.Error()}
	}

	// 创建HTTP请求
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewReader(body))
	if err != nil {
		return map[string]any{"success": false, "error": "创建HTTP请求失败: " + err.Error()}
	}

	// 设置基础请求头
	for k, vs := range baseHeaders {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// 添加/覆盖自定义请求头
	for key, value := range testReq.Headers {
		req.Header.Set(key, value)
	}

	// 发送请求
	start := time.Now()
	resp, err := s.client.Do(req)
	duration := time.Since(start)
	if err != nil {
		return map[string]any{"success": false, "error": "网络请求失败: " + err.Error(), "duration_ms": duration.Milliseconds()}
	}
	defer resp.Body.Close()

	// 判断是否为SSE响应
	contentType := resp.Header.Get("Content-Type")
	// Codex API 流式响应不返回 Content-Type，需要根据请求参数判断
	isEventStream := strings.Contains(strings.ToLower(contentType), "text/event-stream") ||
		(channelType == "codex" && testReq.Stream)

	// 通用结果初始化
	result := map[string]any{
		"success":     resp.StatusCode >= 200 && resp.StatusCode < 300,
		"status_code": resp.StatusCode,
		"duration_ms": duration.Milliseconds(),
	}

	// 附带响应头与类型，便于排查（不含请求头以避免泄露）
	if len(resp.Header) > 0 {
		hdr := make(map[string]string, len(resp.Header))
		for k, vs := range resp.Header {
			if len(vs) == 1 {
				hdr[k] = vs[0]
			} else if len(vs) > 1 {
				hdr[k] = strings.Join(vs, "; ")
			}
		}
		result["response_headers"] = hdr
	}
	if contentType != "" {
		result["content_type"] = contentType
	}

	if isEventStream {
		// 流式解析（SSE）。无论状态码是否2xx，都尽量读取并回显上游返回内容。
		var rawBuilder strings.Builder
		var textBuilder strings.Builder
		var lastErrMsg string

		scanner := bufio.NewScanner(resp.Body)
		// 提高扫描缓冲，避免长行截断
		buf := make([]byte, 0, 1024*1024)
		scanner.Buffer(buf, 16*1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			rawBuilder.WriteString(line)
			rawBuilder.WriteString("\n")

			// SSE 行通常以 "data:" 开头
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}

			var obj map[string]any
			if err := sonic.Unmarshal([]byte(data), &obj); err != nil {
				// 非JSON数据，忽略
				continue
			}

			// OpenAI: choices[0].delta.content
			if choices, ok := obj["choices"].([]any); ok && len(choices) > 0 {
				if choice, ok := choices[0].(map[string]any); ok {
					if delta, ok := choice["delta"].(map[string]any); ok {
						if content, ok := delta["content"].(string); ok && content != "" {
							textBuilder.WriteString(content)
							continue
						}
					}
				}
			}

			// Gemini: 支持两种格式
			// 1. 标准 API: candidates[0].content.parts[0].text
			// 2. CLI 格式: response.candidates[0].content.parts[0].text
			var candidates []any
			if resp, ok := obj["response"].(map[string]any); ok {
				// CLI 格式：从 response 内部获取 candidates
				candidates, _ = resp["candidates"].([]any)
			} else {
				// 标准格式：直接获取 candidates
				candidates, _ = obj["candidates"].([]any)
			}
			if len(candidates) > 0 {
				if candidate, ok := candidates[0].(map[string]any); ok {
					if content, ok := candidate["content"].(map[string]any); ok {
						if parts, ok := content["parts"].([]any); ok && len(parts) > 0 {
							if part, ok := parts[0].(map[string]any); ok {
								if text, ok := part["text"].(string); ok && text != "" {
									textBuilder.WriteString(text)
									continue
								}
							}
						}
					}
				}
			}

			// Anthropic: type == content_block_delta 且 delta.text 为增量
			if typ, ok := obj["type"].(string); ok {
				if typ == "content_block_delta" {
					if delta, ok := obj["delta"].(map[string]any); ok {
						if tx, ok := delta["text"].(string); ok && tx != "" {
							textBuilder.WriteString(tx)
							continue
						}
					}
				}
				// Codex: type == response.output_text.done 包含完整文本
				if typ == "response.output_text.done" {
					if text, ok := obj["text"].(string); ok && text != "" {
						textBuilder.WriteString(text)
						continue
					}
				}
			}

			// 错误事件通用: data 中包含 error 字段或 message
			if errObj, ok := obj["error"].(map[string]any); ok {
				if msg, ok := errObj["message"].(string); ok && msg != "" {
					lastErrMsg = msg
				} else if typeStr, ok := errObj["type"].(string); ok && typeStr != "" {
					lastErrMsg = typeStr
				}
				// 记录完整错误对象
				result["api_error"] = obj
				continue
			}
			if msg, ok := obj["message"].(string); ok && msg != "" {
				lastErrMsg = msg
				result["api_error"] = obj
				continue
			}
		}

		if err := scanner.Err(); err != nil {
			result["error"] = "读取流式响应失败: " + err.Error()
			result["raw_response"] = rawBuilder.String()
			return result
		}

		if textBuilder.Len() > 0 {
			result["response_text"] = textBuilder.String()
		}
		result["raw_response"] = rawBuilder.String()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			result["message"] = "API测试成功（流式）"
		} else {
			if lastErrMsg == "" {
				lastErrMsg = "API返回错误状态: " + resp.Status
			}
			result["error"] = lastErrMsg
		}

		// 监控捕获测试请求（流式）
		s.captureTestForMonitor(cfg, testReq.Model, body, []byte(rawBuilder.String()), resp.StatusCode, duration.Seconds(), true, "admin-test")

		return result
	}

	// 非流式或非SSE响应：按原逻辑读取完整响应（即便前端请求了流式，但上游未返回SSE，也按普通响应处理，确保能展示完整错误体）
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return map[string]any{"success": false, "error": "读取响应失败: " + err.Error(), "duration_ms": duration.Milliseconds(), "status_code": resp.StatusCode}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// 成功：委托给 tester 解析
		parsed := tester.Parse(resp.StatusCode, respBody)
		for k, v := range parsed {
			result[k] = v
		}
		result["message"] = "API测试成功"
	} else {
		// 错误：统一解析
		var errorMsg string
		var apiError map[string]any
		if err := sonic.Unmarshal(respBody, &apiError); err == nil {
			if errInfo, ok := apiError["error"].(map[string]any); ok {
				if msg, ok := errInfo["message"].(string); ok {
					errorMsg = msg
				} else if typeStr, ok := errInfo["type"].(string); ok {
					errorMsg = typeStr
				}
			}
			result["api_error"] = apiError
		} else {
			result["raw_response"] = string(respBody)
		}
		if errorMsg == "" {
			errorMsg = "API返回错误状态: " + resp.Status
		}
		result["error"] = errorMsg
	}

	// 监控捕获测试请求
	s.captureTestForMonitor(cfg, testReq.Model, body, respBody, resp.StatusCode, duration.Seconds(), testReq.Stream, "admin-test")

	return result
}

// captureTestForMonitor 捕获测试请求用于监控
func (s *Server) captureTestForMonitor(
	cfg *model.Config,
	testModel string,
	requestBody []byte,
	responseBody []byte,
	statusCode int,
	duration float64,
	isStreaming bool,
	clientIP string,
) {
	// 检查监控服务是否可用且开启
	if s.monitorService == nil || !s.monitorService.IsEnabled() {
		return
	}

	// 优先从响应体解析 token 数量，解析失败则估算
	inputTokens, outputTokens := parseTokensFromResponse(responseBody, isStreaming)
	if inputTokens <= 0 {
		// 响应中没有 input_tokens，降级到估算
		inputTokens = estimateInputTokensFromBody(requestBody)
	}

	// 构建追踪记录（标记为测试请求）
	trace := &storage.Trace{
		Time:         time.Now().UnixMilli(),
		ChannelID:    int(cfg.ID),
		ChannelName:  cfg.Name,
		ChannelType:  cfg.GetChannelType(),
		Model:        testModel,
		RequestPath:  fmt.Sprintf("/admin/channels/%d/test", cfg.ID),
		StatusCode:   statusCode,
		Duration:     duration,
		IsStreaming:  isStreaming,
		IsTest:       true, // 标记为测试请求
		ClientIP:     clientIP,
		APIKeyUsed:   "[测试]",
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}

	// 捕获请求体（限制大小）
	const maxCaptureSize = 1024 * 1024 // 1MB
	if len(requestBody) > 0 {
		if len(requestBody) <= maxCaptureSize {
			trace.RequestBody = string(requestBody)
		} else {
			trace.RequestBody = string(requestBody[:maxCaptureSize]) + "\n...(truncated)"
		}
	}

	// 捕获响应体（限制大小）
	if len(responseBody) > 0 {
		if len(responseBody) <= maxCaptureSize {
			trace.ResponseBody = string(responseBody)
		} else {
			trace.ResponseBody = string(responseBody[:maxCaptureSize]) + "\n...(truncated)"
		}
	}

	// 异步捕获（不阻塞主流程）
	s.monitorService.Capture(trace)
}

// parseTokensFromResponse 从响应体解析 input_tokens 和 output_tokens
// 返回 (inputTokens, outputTokens)，解析失败返回 0
func parseTokensFromResponse(body []byte, isStreaming bool) (int, int) {
	if len(body) == 0 {
		return 0, 0
	}

	if isStreaming {
		return parseStreamingTokens(body)
	}

	// 非流式响应：直接解析 usage 字段
	var resp struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := sonic.Unmarshal(body, &resp); err == nil {
		return resp.Usage.InputTokens, resp.Usage.OutputTokens
	}

	return 0, 0
}

// parseStreamingTokens 解析流式响应中的 input_tokens 和 output_tokens
func parseStreamingTokens(body []byte) (int, int) {
	bodyStr := string(body)

	var inputTokens, outputTokens int

	// 解析 input_tokens（通常在 message_start 或 message_delta 中）
	inputIdx := strings.LastIndex(bodyStr, `"input_tokens"`)
	if inputIdx != -1 {
		substr := bodyStr[inputIdx:]
		if _, err := fmt.Sscanf(substr, `"input_tokens":%d`, &inputTokens); err != nil {
			fmt.Sscanf(substr, `"input_tokens": %d`, &inputTokens)
		}
	}

	// 解析 output_tokens（通常在 message_delta 中）
	outputIdx := strings.LastIndex(bodyStr, `"output_tokens"`)
	if outputIdx != -1 {
		substr := bodyStr[outputIdx:]
		if _, err := fmt.Sscanf(substr, `"output_tokens":%d`, &outputTokens); err != nil {
			fmt.Sscanf(substr, `"output_tokens": %d`, &outputTokens)
		}
	}

	// 如果标准格式解析失败，尝试 AWS EventStream 格式（Kiro）
	if outputTokens == 0 && strings.Contains(bodyStr, "meteringEvent") {
		usageIdx := strings.LastIndex(bodyStr, `"usage":`)
		if usageIdx != -1 {
			substr := bodyStr[usageIdx:]
			var usage float64
			if _, err := fmt.Sscanf(substr, `"usage":%f`, &usage); err == nil && usage > 0 {
				// 从 credit 消耗估算 token
				outputTokens = int(usage / 0.003)
			}
		}
	}

	return inputTokens, outputTokens
}

// estimateInputTokensFromBody 从请求体估算输入 token 数量
func estimateInputTokensFromBody(body []byte) int {
	if len(body) == 0 {
		return 0
	}

	// 解析请求体
	var req CountTokensRequest
	if err := sonic.Unmarshal(body, &req); err != nil {
		// 解析失败，使用简单估算（约4字符/token）
		return len(body) / 4
	}

	// 使用 tiktoken 计算
	tokens := countTokensWithTiktokenFromRequest(&req)
	if tokens <= 0 {
		// 降级到纯算法
		tokens = estimateTokens(&req)
	}
	return tokens
}

// testKiroChannel Kiro 预设专用测试函数
// 使用 CodeWhisperer API 格式发送请求，响应转换为 Anthropic SSE 格式
func (s *Server) testKiroChannel(cfg *model.Config, accessToken string, testReq *testutil.TestChannelRequest, deviceFingerprint string) map[string]any {
	// 构建 Anthropic 格式的请求体（用于转换和 token 估算）
	anthropicReq := map[string]any{
		"model": testReq.Model,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": testReq.Content,
			},
		},
		"max_tokens": testReq.MaxTokens,
		"stream":     true,
	}

	anthropicBody, err := sonic.Marshal(anthropicReq)
	if err != nil {
		return map[string]any{"success": false, "error": "构造请求体失败: " + err.Error()}
	}

	// 转换为 Kiro (CodeWhisperer) 格式
	kiroBody, err := TransformToKiroRequest(anthropicBody)
	if err != nil {
		return map[string]any{"success": false, "error": "转换 Kiro 请求失败: " + err.Error()}
	}

	// 构建请求头（使用配置的设备指纹）
	headers := BuildKiroRequestHeaders(accessToken, true, deviceFingerprint)

	// 创建 HTTP 请求
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", KiroAPIEndpoint, bytes.NewReader(kiroBody))
	if err != nil {
		return map[string]any{"success": false, "error": "创建 HTTP 请求失败: " + err.Error()}
	}

	// 设置请求头
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	// 发送请求
	start := time.Now()
	resp, err := s.client.Do(req)
	duration := time.Since(start)
	if err != nil {
		return map[string]any{"success": false, "error": "网络请求失败: " + err.Error(), "duration_ms": duration.Milliseconds()}
	}
	defer resp.Body.Close()

	// 通用结果初始化
	result := map[string]any{
		"success":     resp.StatusCode >= 200 && resp.StatusCode < 300,
		"status_code": resp.StatusCode,
		"duration_ms": duration.Milliseconds(),
	}

	// 附带响应头
	if len(resp.Header) > 0 {
		hdr := make(map[string]string, len(resp.Header))
		for k, vs := range resp.Header {
			if len(vs) == 1 {
				hdr[k] = vs[0]
			} else if len(vs) > 1 {
				hdr[k] = strings.Join(vs, "; ")
			}
		}
		result["response_headers"] = hdr
	}

	// 读取完整响应体
	bodyData, err := io.ReadAll(resp.Body)
	if err != nil {
		result["error"] = "读取响应失败: " + err.Error()
		return result
	}

	// 检测是否是 AWS Event Stream 二进制格式
	contentType := resp.Header.Get("Content-Type")
	isAWSEventStream := strings.Contains(contentType, "event-stream") ||
		strings.Contains(contentType, "amazon") ||
		isAWSEventStreamBinary(bodyData)

	var sseResponse string
	var inputTokens, outputTokens int

	if isAWSEventStream && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// 将 AWS EventStream 转换为 Anthropic SSE 格式
		sseResponse, inputTokens, outputTokens = convertKiroToAnthropicSSE(bodyData, testReq.Model, anthropicBody)
		result["content_type"] = "text/event-stream"
		result["response_text"] = sseResponse
		result["message"] = "API测试成功（Kiro → Anthropic SSE）"
	} else {
		// 非 AWS EventStream 或错误响应，直接返回原始内容
		result["content_type"] = contentType
		result["raw_response"] = string(bodyData)

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			result["message"] = "API测试成功（Kiro）"
		} else {
			// 尝试解析错误信息
			var errResp map[string]any
			if err := sonic.Unmarshal(bodyData, &errResp); err == nil {
				if msg, ok := errResp["message"].(string); ok {
					result["error"] = msg
				} else {
					result["error"] = "API返回错误状态: " + resp.Status
				}
			} else {
				result["error"] = "API返回错误状态: " + resp.Status
			}
		}

		// 估算 token（错误情况下）
		inputTokens = estimateInputTokensFromBody(anthropicBody)
	}

	// 监控捕获：使用转换后的 SSE 响应
	var captureBody []byte
	if sseResponse != "" {
		captureBody = []byte(sseResponse)
	} else {
		captureBody = bodyData
	}

	// 创建带 token 信息的监控记录
	s.captureTestForMonitorWithTokens(cfg, testReq.Model, anthropicBody, captureBody, resp.StatusCode, duration.Seconds(), true, "admin-test", inputTokens, outputTokens)

	return result
}

// convertKiroToAnthropicSSE 将 Kiro AWS EventStream 转换为 Anthropic SSE 格式字符串
// 返回：SSE 字符串、输入 token 数、输出 token 数
func convertKiroToAnthropicSSE(data []byte, model string, anthropicBody []byte) (string, int, int) {
	parser := newKiroSSEParser()
	parser.requestedModel = model

	// 估算输入 token 并设置到 parser
	inputTokens := estimateInputTokensFromBody(anthropicBody)
	parser.inputTokens = inputTokens

	var sseBuilder strings.Builder

	// 解析所有帧并转换
	buf := data
	for len(buf) >= 16 {
		frameLen, frame, remaining := parseAWSEventStreamFrame(buf)
		if frameLen == 0 {
			break
		}

		if frame != nil && len(frame.Payload) > 0 {
			// 处理帧并收集 SSE 输出
			sseEvents := processAWSEventStreamFrameToSSE(frame, parser)
			for _, event := range sseEvents {
				sseBuilder.WriteString(event)
			}
		}

		buf = remaining
	}

	// 如果有内容输出但没有收到 completionEvent，手动发送结束事件
	if parser.messageStarted {
		endEvents := generateKiroStreamEndEvents(parser)
		for _, event := range endEvents {
			sseBuilder.WriteString(event)
		}
	}

	return sseBuilder.String(), inputTokens, parser.outputTokens
}

// processAWSEventStreamFrameToSSE 处理单个 AWS EventStream 帧并返回 SSE 事件字符串列表
func processAWSEventStreamFrameToSSE(frame *awsEventFrame, parser *kiroSSEParser) []string {
	if frame == nil || len(frame.Payload) == 0 {
		return nil
	}

	var payloadMap map[string]any
	if err := sonic.Unmarshal(frame.Payload, &payloadMap); err != nil {
		return nil
	}

	eventType := frame.Headers[":event-type"]
	var events []string

	switch eventType {
	case "assistantResponseEvent":
		events = handleKiroAssistantResponseEventToSSE(payloadMap, parser)
	case "meteringEvent":
		handleKiroMeteringEventForParser(payloadMap, parser)
	case "toolUseEvent":
		events = handleKiroToolUseEventToSSE(payloadMap, parser)
	case "completionEvent":
		events = handleKiroCompletionEventToSSE(payloadMap, parser)
	default:
		// 其他事件类型，尝试提取 content
		if content, ok := payloadMap["content"].(string); ok && content != "" {
			events = handleKiroAssistantResponseEventToSSE(map[string]any{"content": content}, parser)
		}
	}

	return events
}

// handleKiroAssistantResponseEventToSSE 处理 assistantResponseEvent 并返回 SSE 事件
func handleKiroAssistantResponseEventToSSE(payloadMap map[string]any, parser *kiroSSEParser) []string {
	content, ok := payloadMap["content"].(string)
	if !ok || content == "" {
		return nil
	}

	var events []string

	// 首次收到内容时发送 message_start（使用估算的 input_tokens）
	if !parser.messageStarted {
		parser.messageStarted = true
		msgStart := map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            fmt.Sprintf("msg_kiro_%d", time.Now().UnixNano()),
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
		events = append(events, formatSSEEvent("message_start", msgStartData))
	}

	// 检测思考内容
	isThinking := strings.HasPrefix(content, "<thinking>") || parser.inThinkingBlock
	if strings.Contains(content, "<thinking>") {
		parser.inThinkingBlock = true
		isThinking = true
	}
	if strings.Contains(content, "</thinking>") {
		parser.inThinkingBlock = false
	}

	// 发送 content_block_start（如果需要）
	if isThinking && !parser.thinkingBlockSent {
		parser.thinkingBlockSent = true
		parser.currentBlockIndex = 0
		blockStart := map[string]any{
			"type":  "content_block_start",
			"index": 0,
			"content_block": map[string]any{
				"type":     "thinking",
				"thinking": "",
			},
		}
		blockStartData, _ := sonic.Marshal(blockStart)
		events = append(events, formatSSEEvent("content_block_start", blockStartData))
	} else if !isThinking && !parser.textBlockSent {
		// 如果之前有思考块，先关闭它
		if parser.thinkingBlockSent && !parser.thinkingBlockStopped {
			parser.thinkingBlockStopped = true
			blockStop := map[string]any{
				"type":  "content_block_stop",
				"index": parser.currentBlockIndex,
			}
			blockStopData, _ := sonic.Marshal(blockStop)
			events = append(events, formatSSEEvent("content_block_stop", blockStopData))
			parser.currentBlockIndex++
		}

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
		events = append(events, formatSSEEvent("content_block_start", blockStartData))
	}

	// 发送 content_block_delta
	var delta map[string]any
	if isThinking {
		delta = map[string]any{
			"type":          "content_block_delta",
			"index":         parser.currentBlockIndex,
			"delta": map[string]any{
				"type":     "thinking_delta",
				"thinking": content,
			},
		}
	} else {
		delta = map[string]any{
			"type":  "content_block_delta",
			"index": parser.currentBlockIndex,
			"delta": map[string]any{
				"type": "text_delta",
				"text": content,
			},
		}
	}
	deltaData, _ := sonic.Marshal(delta)
	events = append(events, formatSSEEvent("content_block_delta", deltaData))

	// 累计输出 token（简单估算）
	parser.outputTokens += (len([]rune(content)) + 3) / 4

	return events
}

// handleKiroMeteringEventForParser 处理 meteringEvent 更新 parser 状态
func handleKiroMeteringEventForParser(payloadMap map[string]any, parser *kiroSSEParser) {
	if usage, ok := payloadMap["usage"].(float64); ok && usage > 0 {
		// 从 credit 消耗估算 token（约 0.003 credit/token for haiku）
		estimatedTokens := int(usage / 0.003)
		if estimatedTokens > parser.outputTokens {
			parser.outputTokens = estimatedTokens
		}
	}
}

// handleKiroToolUseEventToSSE 处理 toolUseEvent 并返回 SSE 事件
func handleKiroToolUseEventToSSE(payloadMap map[string]any, parser *kiroSSEParser) []string {
	parser.hasToolUse = true

	var events []string

	// 关闭之前的文本块
	if parser.textBlockSent && !parser.textBlockStopped {
		parser.textBlockStopped = true
		blockStop := map[string]any{
			"type":  "content_block_stop",
			"index": parser.currentBlockIndex,
		}
		blockStopData, _ := sonic.Marshal(blockStop)
		events = append(events, formatSSEEvent("content_block_stop", blockStopData))
		parser.currentBlockIndex++
	}

	// 提取工具信息
	toolName, _ := payloadMap["name"].(string)
	toolID, _ := payloadMap["id"].(string)
	if toolID == "" {
		toolID = fmt.Sprintf("toolu_%d", time.Now().UnixNano())
	}

	// 发送 tool_use content_block_start
	blockStart := map[string]any{
		"type":  "content_block_start",
		"index": parser.currentBlockIndex,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    toolID,
			"name":  toolName,
			"input": map[string]any{},
		},
	}
	blockStartData, _ := sonic.Marshal(blockStart)
	events = append(events, formatSSEEvent("content_block_start", blockStartData))

	// 发送 input_json_delta
	if input, ok := payloadMap["input"]; ok {
		inputJSON, _ := sonic.Marshal(input)
		delta := map[string]any{
			"type":  "content_block_delta",
			"index": parser.currentBlockIndex,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": string(inputJSON),
			},
		}
		deltaData, _ := sonic.Marshal(delta)
		events = append(events, formatSSEEvent("content_block_delta", deltaData))
	}

	// 发送 content_block_stop
	blockStop := map[string]any{
		"type":  "content_block_stop",
		"index": parser.currentBlockIndex,
	}
	blockStopData, _ := sonic.Marshal(blockStop)
	events = append(events, formatSSEEvent("content_block_stop", blockStopData))

	parser.currentBlockIndex++

	return events
}

// handleKiroCompletionEventToSSE 处理 completionEvent 并返回 SSE 事件
func handleKiroCompletionEventToSSE(_ map[string]any, parser *kiroSSEParser) []string {
	return generateKiroStreamEndEvents(parser)
}

// generateKiroStreamEndEvents 生成流结束事件
func generateKiroStreamEndEvents(parser *kiroSSEParser) []string {
	var events []string

	// 关闭未关闭的内容块
	if (parser.thinkingBlockSent && !parser.thinkingBlockStopped) ||
		(parser.textBlockSent && !parser.textBlockStopped) {
		blockStop := map[string]any{
			"type":  "content_block_stop",
			"index": parser.currentBlockIndex,
		}
		blockStopData, _ := sonic.Marshal(blockStop)
		events = append(events, formatSSEEvent("content_block_stop", blockStopData))
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
	events = append(events, formatSSEEvent("message_delta", msgDeltaData))

	// 发送 message_stop
	msgStop := map[string]any{"type": "message_stop"}
	msgStopData, _ := sonic.Marshal(msgStop)
	events = append(events, formatSSEEvent("message_stop", msgStopData))

	return events
}

// formatSSEEvent 格式化 SSE 事件
func formatSSEEvent(eventType string, data []byte) string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, string(data))
}

// captureTestForMonitorWithTokens 捕获测试请求用于监控（带 token 信息）
func (s *Server) captureTestForMonitorWithTokens(
	cfg *model.Config,
	testModel string,
	requestBody []byte,
	responseBody []byte,
	statusCode int,
	duration float64,
	isStreaming bool,
	clientIP string,
	inputTokens int,
	outputTokens int,
) {
	if s.monitorService == nil || !s.monitorService.IsEnabled() {
		return
	}

	trace := &storage.Trace{
		Time:         time.Now().UnixMilli(),
		ChannelID:    int(cfg.ID),
		ChannelName:  cfg.Name,
		ChannelType:  cfg.GetChannelType(),
		Model:        testModel,
		RequestPath:  fmt.Sprintf("/admin/channels/%d/test", cfg.ID),
		StatusCode:   statusCode,
		Duration:     duration,
		IsStreaming:  isStreaming,
		IsTest:       true,
		ClientIP:     clientIP,
		APIKeyUsed:   "[测试]",
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}

	const maxCaptureSize = 1024 * 1024
	if len(requestBody) > 0 {
		if len(requestBody) <= maxCaptureSize {
			trace.RequestBody = string(requestBody)
		} else {
			trace.RequestBody = string(requestBody[:maxCaptureSize]) + "\n...(truncated)"
		}
	}

	if len(responseBody) > 0 {
		if len(responseBody) <= maxCaptureSize {
			trace.ResponseBody = string(responseBody)
		} else {
			trace.ResponseBody = string(responseBody[:maxCaptureSize]) + "\n...(truncated)"
		}
	}

	s.monitorService.Capture(trace)
}
