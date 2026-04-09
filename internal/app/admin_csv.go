package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ccLoad/internal/model"
	"ccLoad/internal/util"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"
)

// ==================== CSV导入导出 ====================
// 从admin.go拆分CSV功能,遵循SRP原则

// handleExportChannelsCSV 导出渠道为CSV
// GET /admin/channels/export
func (s *Server) HandleExportChannelsCSV(c *gin.Context) {
	cfgs, err := s.store.ListConfigs(c.Request.Context())
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 批量查询所有API Keys,消除N+1问题(100渠道从100次查询降为1次)
	allAPIKeys, err := s.store.GetAllAPIKeys(c.Request.Context())
	if err != nil {
		log.Printf("[WARN] 批量查询API Keys失败: %v", err)
		allAPIKeys = make(map[int64][]*model.APIKey) // 降级:使用空map
	}

	buf := &bytes.Buffer{}
	// 添加 UTF-8 BOM,兼容 Excel 等工具
	buf.WriteString("\ufeff")

	writer := csv.NewWriter(buf)
	defer writer.Flush()

	header := []string{"id", "name", "api_key", "url", "priority", "models", "model_redirects", "channel_type", "key_strategy", "enabled", "preset", "quota_config"}
	if err := writer.Write(header); err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	for _, cfg := range cfgs {
		// 从预加载的map中获取API Keys,O(1)查找
		apiKeys := allAPIKeys[cfg.ID]

		// 判断是否为 OAuth 预设渠道
		isKiroPreset := cfg.Preset == "kiro"
		isOfficialPreset := cfg.Preset == "official"
		channelType := cfg.GetChannelType()
		isOAuthChannel := isKiroPreset ||
			(isOfficialPreset && (channelType == util.ChannelTypeCodex || channelType == util.ChannelTypeGemini))

		// 格式化 API Key 字段
		var apiKeyStr string
		if isOAuthChannel && len(apiKeys) > 0 {
			// OAuth 预设：将认证信息序列化为 JSON
			ak := apiKeys[0]
			authConfig := map[string]any{}
			if ak.RefreshToken != "" {
				authConfig["refreshToken"] = ak.RefreshToken
			}
			if ak.AccessToken != "" {
				authConfig["accessToken"] = ak.AccessToken
			}
			if ak.IDToken != "" {
				authConfig["idToken"] = ak.IDToken
			}
			if ak.TokenExpiresAt > 0 {
				authConfig["tokenExpiresAt"] = ak.TokenExpiresAt
			}
			if ak.DeviceFingerprint != "" {
				authConfig["deviceFingerprint"] = ak.DeviceFingerprint
			}
			if jsonBytes, err := sonic.Marshal(authConfig); err == nil {
				apiKeyStr = string(jsonBytes)
			}
		} else {
			// 普通渠道：逗号分隔的 API Key
			apiKeyStrs := make([]string, 0, len(apiKeys))
			for _, key := range apiKeys {
				apiKeyStrs = append(apiKeyStrs, key.APIKey)
			}
			apiKeyStr = strings.Join(apiKeyStrs, ",")
		}

		// 获取Key策略(从第一个Key)
		keyStrategy := model.KeyStrategySequential // 默认值
		if len(apiKeys) > 0 && apiKeys[0].KeyStrategy != "" {
			keyStrategy = apiKeys[0].KeyStrategy
		}

		// 序列化模型重定向为JSON字符串
		modelRedirectsJSON := "{}"
		if len(cfg.ModelRedirects) > 0 {
			if jsonBytes, err := sonic.Marshal(cfg.ModelRedirects); err == nil {
				modelRedirectsJSON = string(jsonBytes)
			}
		}

		// 导出 preset：DB "official" → CSV "codex"/"gemini"（与导入映射对称）
		exportPreset := cfg.Preset
		if cfg.Preset == "official" {
			switch channelType {
			case util.ChannelTypeCodex:
				exportPreset = "codex"
			case util.ChannelTypeGemini:
				exportPreset = "gemini"
			}
		}

		// 序列化 quota_config
		quotaConfigJSON := ""
		if cfg.QuotaConfig != nil {
			if jsonBytes, err := sonic.Marshal(cfg.QuotaConfig); err == nil {
				quotaConfigJSON = string(jsonBytes)
			}
		}

		record := []string{
			strconv.FormatInt(cfg.ID, 10),
			cfg.Name,
			apiKeyStr,
			cfg.URL,
			strconv.Itoa(cfg.Priority),
			strings.Join(cfg.Models, ","),
			modelRedirectsJSON,
			channelType,
			keyStrategy,
			strconv.FormatBool(cfg.Enabled),
			exportPreset,
			quotaConfigJSON,
		}
		if err := writer.Write(record); err != nil {
			RespondError(c, http.StatusInternalServerError, err)
			return
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	filename := fmt.Sprintf("channels-%s.csv", time.Now().Format("20060102-150405"))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	c.Header("Cache-Control", "no-cache")
	c.String(http.StatusOK, buf.String())
}

// handleImportChannelsCSV 导入渠道CSV
// POST /admin/channels/import
func (s *Server) HandleImportChannelsCSV(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "缺少上传文件")
		return
	}

	src, err := fileHeader.Open()
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	defer src.Close()

	reader := csv.NewReader(src)
	reader.TrimLeadingSpace = true

	headerRow, err := reader.Read()
	if err == io.EOF {
		RespondErrorMsg(c, http.StatusBadRequest, "CSV内容为空")
		return
	}
	if err != nil {
		RespondError(c, http.StatusBadRequest, err)
		return
	}

	columnIndex := buildCSVColumnIndex(headerRow)
	// OAuth 预设（preset 列存在时）只需 name + api_key，url/models 可自动填充
	_, hasPresetCol := columnIndex["preset"]
	required := []string{"name", "api_key"}
	if !hasPresetCol {
		required = append(required, "url", "models")
	}
	for _, key := range required {
		if _, ok := columnIndex[key]; !ok {
			RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("缺少必需列: %s", key))
			return
		}
	}

	summary := ChannelImportSummary{}
	lineNo := 1

	// 批量收集有效记录,最后一次性导入(减少数据库往返)
	validChannels := make([]*model.ChannelWithKeys, 0, 100) // 预分配容量,减少扩容

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		lineNo++

		if err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行读取失败: %v", lineNo, err))
			summary.Skipped++
			continue
		}

		if isCSVRecordEmpty(record) {
			summary.Skipped++
			continue
		}

		fetch := func(key string) string {
			idx, ok := columnIndex[key]
			if !ok || idx >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[idx])
		}

		name := fetch("name")
		apiKey := fetch("api_key")
		url := fetch("url")
		modelsRaw := fetch("models")
		modelRedirectsRaw := fetch("model_redirects")
		channelType := fetch("channel_type")
		preset := fetch("preset")        // 预设类型（kiro/codex/gemini）
		keyStrategy := fetch("key_strategy")
		quotaConfigRaw := fetch("quota_config")  // 用量查询配置（JSON 格式）

		// OAuth 预设（通过 preset 字段判断）
		isOAuthPreset := preset == "kiro" || preset == "codex" || preset == "gemini"

		// 必填字段校验：OAuth 预设放宽 models 要求（可自动填充）
		if name == "" || apiKey == "" {
			summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行缺少必填字段(name/api_key)", lineNo))
			summary.Skipped++
			continue
		}
		if modelsRaw == "" && !isOAuthPreset {
			summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行缺少必填字段(models)", lineNo))
			summary.Skipped++
			continue
		}

		// OAuth 预设自动推导 channel_type
		if isOAuthPreset && channelType == "" {
			switch preset {
			case "kiro":
				channelType = util.ChannelTypeAnthropic
			case "codex":
				channelType = util.ChannelTypeCodex
			case "gemini":
				channelType = util.ChannelTypeGemini
			}
		}

		// 验证 URL（OAuth 预设可以为空）
		if url == "" && !isOAuthPreset {
			summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行缺少URL", lineNo))
			summary.Skipped++
			continue
		}

		// OAuth 预设自动填充默认 URL
		if url == "" && isOAuthPreset {
			switch preset {
			case "kiro":
				url = "https://q.us-east-1.amazonaws.com"
			case "codex":
				url = "https://chatgpt.com/backend-api/codex"
			case "gemini":
				url = "https://generativelanguage.googleapis.com"
			}
		}

		// OAuth 预设自动填充默认模型
		if modelsRaw == "" && isOAuthPreset {
			switch preset {
			case "kiro":
				modelsRaw = "claude-opus-4-6,claude-sonnet-4-6,claude-sonnet-4-20250514,claude-3-5-sonnet-20241022,claude-3-5-haiku-20241022"
			case "codex":
				modelsRaw = "gpt-5.3-codex,gpt-5.3-codex-spark,gpt-5.2-codex,gpt-5.2,gpt-5.1-codex,gpt-5.1-codex-max,gpt-5.1-codex-mini,gpt-5,gpt-5.1"
			case "gemini":
				modelsRaw = "gemini-2.5-pro,gemini-2.5-flash"
			}
			log.Printf("[INFO] [CSV导入] 第%d行 %s 预设自动填充模型: %s", lineNo, preset, modelsRaw)
		}

		if url != "" {
			normalizedURL, err := validateChannelBaseURL(url)
			if err != nil {
				summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行URL无效: %v", lineNo, err))
				summary.Skipped++
				continue
			}
			url = normalizedURL
		}

		// 渠道类型规范化（OAuth 预设不规范化，保持原值）
		if !isOAuthPreset {
			// 兼容旧数据：已删除的渠道类型（如 openai）会自动回退
			channelType = util.NormalizeChannelTypeWithFallback(channelType)
		}

		// 验证Key使用策略(可选字段,默认sequential)
		if keyStrategy == "" {
			keyStrategy = model.KeyStrategySequential // 默认值
		} else if !model.IsValidKeyStrategy(keyStrategy) {
			summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行Key使用策略无效: %s(仅支持sequential/round_robin)", lineNo, keyStrategy))
			summary.Skipped++
			continue
		}

		models := parseImportModels(modelsRaw)
		if len(models) == 0 {
			summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行模型格式无效", lineNo))
			summary.Skipped++
			continue
		}

		// 解析模型重定向(可选字段)
		var modelRedirects map[string]string
		if modelRedirectsRaw != "" && modelRedirectsRaw != "{}" {
			if err := sonic.Unmarshal([]byte(modelRedirectsRaw), &modelRedirects); err != nil {
				summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行模型重定向格式错误: %v", lineNo, err))
				summary.Skipped++
				continue
			}
		}

		priority := 0
		if pRaw := fetch("priority"); pRaw != "" {
			p, err := strconv.Atoi(pRaw)
			if err != nil {
				summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行优先级格式错误: %v", lineNo, err))
				summary.Skipped++
				continue
			}
			priority = p
		}

		enabled := true
		if eRaw := fetch("enabled"); eRaw != "" {
			if val, ok := parseImportEnabled(eRaw); ok {
				enabled = val
			} else {
				summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行启用状态格式错误: %s", lineNo, eRaw))
				summary.Skipped++
				continue
			}
		}

		// 构建渠道配置
		// CSV preset 值到数据库 preset 值的映射：
		// CSV "codex"  → DB channel_type="codex",  preset="official"
		// CSV "gemini" → DB channel_type="gemini", preset="official"
		// CSV "kiro"   → DB channel_type="anthropic", preset="kiro"
		// 其他值直接透传
		dbPreset := preset
		if preset == "codex" || preset == "gemini" {
			dbPreset = "official"
		}

		cfg := &model.Config{
			Name:           name,
			URL:            url,
			Priority:       priority,
			Models:         models,
			ModelRedirects: modelRedirects,
			ChannelType:    channelType,
			Preset:         dbPreset,
			Enabled:        enabled,
		}

		// 解析用量查询配置（可选，JSON 格式）
		if quotaConfigRaw != "" && quotaConfigRaw != "{}" {
			var quotaConfig model.QuotaConfig
			if err := sonic.Unmarshal([]byte(quotaConfigRaw), &quotaConfig); err != nil {
				summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行用量查询配置JSON格式错误: %v", lineNo, err))
				summary.Skipped++
				continue
			}
			cfg.QuotaConfig = &quotaConfig
		}

		// 解析并构建API Keys
		// 支持两种格式：
		// 1. 普通格式：逗号分隔的 API Key 字符串
		// 2. OAuth 预设格式（kiro/codex/gemini）：JSON 格式的认证配置
		var apiKeys []model.APIKey

		if isOAuthPreset && strings.HasPrefix(strings.TrimSpace(apiKey), "{") {
			// OAuth 预设：解析 JSON 格式的认证配置
			var authConfig map[string]any
			if err := sonic.Unmarshal([]byte(apiKey), &authConfig); err != nil {
				summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行OAuth认证配置JSON格式错误: %v", lineNo, err))
				summary.Skipped++
				continue
			}

			// 提取 OAuth 字段（同时支持 camelCase 和 snake_case）
			refreshToken := getStringFromMap(authConfig, "refreshToken", "refresh_token")
			clientId := getStringFromMap(authConfig, "clientId", "client_id")
			clientSecret := getStringFromMap(authConfig, "clientSecret", "client_secret")
			accessToken := getStringFromMap(authConfig, "accessToken", "access_token")
			idToken := getStringFromMap(authConfig, "idToken", "id_token")
			deviceFingerprintJSON := getStringFromMap(authConfig, "deviceFingerprint", "device_fingerprint")

			// 提取 tokenExpiresAt / token_expires_at（可能是 float64 或 int）
			var tokenExpiresAt int64
			for _, key := range []string{"tokenExpiresAt", "token_expires_at"} {
				if expiresAt, ok := authConfig[key].(float64); ok {
					tokenExpiresAt = int64(expiresAt)
					break
				} else if expiresAt, ok := authConfig[key].(int64); ok {
					tokenExpiresAt = expiresAt
					break
				}
			}

			// 支持 ISO 格式的 expired / expires 字段（如 "2026-03-07T19:52:09+08:00"）
			if tokenExpiresAt == 0 {
				expiredStr := getStringFromMap(authConfig, "expired", "expires")
				if expiredStr != "" {
					if t, err := time.Parse(time.RFC3339, expiredStr); err == nil {
						tokenExpiresAt = t.Unix()
					}
				}
			}

			// 验证必需字段：refresh_token 或 access_token 至少有一个
			if refreshToken == "" && accessToken == "" {
				summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行OAuth配置缺少refreshToken/accessToken", lineNo))
				summary.Skipped++
				continue
			}

			// 构建单个 API Key（OAuth 预设每个渠道只有一个认证配置）
			apiKeys = []model.APIKey{
				{
					KeyIndex:          0,
					APIKey:            "",                      // OAuth 预设不使用 api_key 字段（IdC 方式除外）
					KeyStrategy:       keyStrategy,
					RefreshToken:      refreshToken,            // 存储到 refresh_token 字段
					AccessToken:       accessToken,             // 存储到 access_token 字段
					IDToken:           idToken,                 // 存储到 id_token 字段（Codex/Gemini JWT）
					TokenExpiresAt:    tokenExpiresAt,          // 过期时间 Unix 时间戳
					DeviceFingerprint: deviceFingerprintJSON,   // Kiro 设备指纹 JSON 配置（可选）
				},
			}

			// [FIX] Kiro 预设：如果没有提供设备指纹，自动生成
			if preset == "kiro" && deviceFingerprintJSON == "" {
				fm := GetFingerprintManager()
				fp, err := fm.GenerateFingerprint()
				if err != nil {
					log.Printf("[WARN] [CSV导入] 生成 Kiro 设备指纹失败 (第%d行): %v", lineNo, err)
				} else {
					fpJSON, err := fp.ToJSON()
					if err != nil {
						log.Printf("[WARN] [CSV导入] 序列化设备指纹失败 (第%d行): %v", lineNo, err)
					} else {
						apiKeys[0].DeviceFingerprint = fpJSON
						log.Printf("[INFO] [CSV导入] 已为第%d行生成设备指纹: %s", lineNo, fp.GetSummary())
					}
				}
			}

			// Kiro IdC 方式：将 clientId 和 clientSecret 存储到 id_token 字段（JSON 格式）
			// [FIX] 与 admin_testing.go:96 保持一致，IdC 配置存储在 id_token 字段
			if preset == "kiro" && clientId != "" && clientSecret != "" {
				idcConfig := map[string]string{
					"clientId":     clientId,
					"clientSecret": clientSecret,
				}
				if idcJSON, err := sonic.Marshal(idcConfig); err == nil {
					apiKeys[0].IDToken = string(idcJSON)
				}
			}

			// Codex 预设：自动生成 quota_config（用量监控配置）
			if preset == "codex" && quotaConfigRaw == "" && accessToken != "" {
				accountID := ExtractAccountIDFromJWT(accessToken)
				cfg.QuotaConfig = buildCodexDefaultQuotaConfig(accessToken, accountID)
				log.Printf("[INFO] [CSV导入] 第%d行 Codex 预设自动生成 quota_config", lineNo)
			}
		} else {
			// 普通格式：逗号分隔的 API Key
			apiKeyList := util.ParseAPIKeys(apiKey)
			apiKeys = make([]model.APIKey, len(apiKeyList))
			for i, key := range apiKeyList {
				apiKeys[i] = model.APIKey{
					KeyIndex:    i,
					APIKey:      key,
					KeyStrategy: keyStrategy,
				}
			}
		}

		// 收集有效记录
		validChannels = append(validChannels, &model.ChannelWithKeys{
			Config:  cfg,
			APIKeys: apiKeys,
		})
	}

	// 批量导入所有有效记录(单事务 + 预编译语句)
	if len(validChannels) > 0 {
		created, updated, err := s.store.ImportChannelBatch(c.Request.Context(), validChannels)
		if err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("批量导入失败: %v", err))
			RespondErrorWithData(c, http.StatusInternalServerError, err.Error(), summary)
			return
		}
		summary.Created = created
		summary.Updated = updated
	}

	summary.Processed = summary.Created + summary.Updated + summary.Skipped

	if len(validChannels) > 0 {
		s.InvalidateChannelListCache()
		s.InvalidateAllAPIKeysCache()
		s.invalidateCooldownCache()

		// [FIX] 导入完成后，同步 OAuth 预设的 Authorization 头
		// 如果 CSV 中已经包含了 AccessToken，立即同步到 quota_config
		go func() {
			ctx := context.Background()
			// 重新查询所有渠道，获取 ID
			allConfigs, err := s.store.ListConfigs(ctx)
			if err != nil {
				return
			}
			nameToID := make(map[string]int64)
			for _, cfg := range allConfigs {
				nameToID[cfg.Name] = cfg.ID
			}

			for _, cwk := range validChannels {
				isKiro := cwk.Config.Preset == "kiro"
				isCodex := cwk.Config.Preset == "codex"
				if (isKiro || isCodex) && cwk.Config.QuotaConfig != nil && cwk.Config.QuotaConfig.Enabled {
					channelID, ok := nameToID[cwk.Config.Name]
					if !ok {
						continue
					}
					// 获取 AccessToken
					if len(cwk.APIKeys) > 0 && cwk.APIKeys[0].AccessToken != "" {
						s.syncQuotaConfigAuthorization(ctx, channelID, cwk.APIKeys[0].AccessToken)
					}
				}
			}
		}()
	}

	// 导入完成后,检查Redis同步状态(批量导入方法会自动触发同步)
	summary.RedisSyncEnabled = s.store.IsRedisEnabled()
	if summary.RedisSyncEnabled {
		summary.RedisSyncSuccess = true // 批量导入方法已自动同步
		// 获取当前渠道总数作为同步数量
		if configs, err := s.store.ListConfigs(c.Request.Context()); err == nil {
			summary.RedisSyncedChannels = len(configs)
		}
	}

	RespondJSON(c, http.StatusOK, summary)
}

// ==================== CSV辅助函数 ====================

// buildCSVColumnIndex 构建CSV列索引映射
func buildCSVColumnIndex(header []string) map[string]int {
	index := make(map[string]int, len(header))
	for i, col := range header {
		norm := normalizeCSVHeader(col)
		if norm == "" {
			continue
		}
		index[norm] = i
	}
	return index
}

// normalizeCSVHeader 规范化CSV列名
func normalizeCSVHeader(name string) string {
	trimmed := strings.TrimSpace(name)
	trimmed = strings.TrimPrefix(trimmed, "\ufeff")
	lower := strings.ToLower(trimmed)
	switch lower {
	case "apikey", "api-key", "api key":
		return "api_key"
	case "model", "model_list", "model(s)":
		return "models"
	case "model_redirect", "model-redirects", "modelredirects", "redirects":
		return "model_redirects"
	case "key_strategy", "key-strategy", "keystrategy", "策略", "使用策略":
		return "key_strategy"
	case "status":
		return "enabled"
	default:
		return lower
	}
}

// isCSVRecordEmpty 检查CSV记录是否为空
func isCSVRecordEmpty(record []string) bool {
	for _, cell := range record {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

// parseImportModels 解析CSV中的模型列表
func parseImportModels(raw string) []string {
	if raw == "" {
		return nil
	}
	splitter := func(r rune) bool {
		switch r {
		case ',', ';', '|', '\n', '\r', '\t':
			return true
		default:
			return false
		}
	}
	parts := strings.FieldsFunc(raw, splitter)
	if len(parts) == 0 {
		return nil
	}
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		clean := strings.TrimSpace(p)
		if clean == "" {
			continue
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

// parseImportEnabled 解析CSV中的启用状态
func parseImportEnabled(raw string) (bool, bool) {
	val := strings.TrimSpace(strings.ToLower(raw))
	switch val {
	case "1", "true", "yes", "y", "启用", "enabled", "on":
		return true, true
	case "0", "false", "no", "n", "禁用", "disabled", "off":
		return false, true
	default:
		return false, false
	}
}

// getStringFromMap 从 map 中按多个候选 key 提取字符串值（支持 camelCase / snake_case 兼容）
func getStringFromMap(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if val, ok := m[key].(string); ok && val != "" {
			return val
		}
	}
	return ""
}

// buildCodexDefaultQuotaConfig 构建 Codex 预设的默认用量监控配置
// 与前端 QUOTA_TEMPLATES.codex + applyCodexQuotaTemplate 逻辑保持一致
func buildCodexDefaultQuotaConfig(accessToken, accountID string) *model.QuotaConfig {
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}
	if accountID != "" {
		headers["chatgpt-account-id"] = accountID
	}

	return &model.QuotaConfig{
		Enabled:       true,
		RequestURL:    "https://chatgpt.com/backend-api/wham/usage",
		RequestMethod: "GET",
		RequestHeaders: headers,
		ExtractorScript: `function(response) {
  const data = typeof response === 'string' ? JSON.parse(response) : response;

  if (!data.rate_limit) {
    return { isValid: false, error: "响应格式错误：缺少 rate_limit" };
  }

  const rl = data.rate_limit;
  const primary = rl.primary_window;

  if (!primary) {
    return { isValid: false, error: "响应格式错误：缺少 primary_window" };
  }

  var plan = data.plan_type || '';
  var hasDualWindow = !!rl.secondary_window;

  var remaining, detail;
  if (hasDualWindow) {
    var h5 = Math.round(100 - primary.used_percent);
    var weekly = Math.round(100 - rl.secondary_window.used_percent);
    var h5Reset = new Date(primary.reset_at * 1000).toLocaleString();
    var weeklyReset = new Date(rl.secondary_window.reset_at * 1000).toLocaleString();
    remaining = h5 + '|' + weekly;
    detail = plan + ' | 5h重置: ' + h5Reset + ' | 周重置: ' + weeklyReset;
  } else {
    var weeklyPct = Math.round(100 - primary.used_percent);
    var resetTime = new Date(primary.reset_at * 1000).toLocaleString();
    remaining = '-|' + weeklyPct;
    detail = plan + ' | 重置: ' + resetTime;
  }

  return {
    isValid: true,
    remaining: remaining,
    unit: '',
    detail: detail,
    limitReached: rl.limit_reached || false
  };
}`,
		IntervalSeconds: 300,
	}
}
