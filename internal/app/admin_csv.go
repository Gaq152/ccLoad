package app

import (
	"bytes"
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

	header := []string{"id", "name", "api_key", "url", "priority", "models", "model_redirects", "channel_type", "key_strategy", "enabled"}
	if err := writer.Write(header); err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	for _, cfg := range cfgs {
		// 从预加载的map中获取API Keys,O(1)查找
		apiKeys := allAPIKeys[cfg.ID]

		// 格式化API Keys为逗号分隔字符串
		apiKeyStrs := make([]string, 0, len(apiKeys))
		for _, key := range apiKeys {
			apiKeyStrs = append(apiKeyStrs, key.APIKey)
		}
		apiKeyStr := strings.Join(apiKeyStrs, ",")

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

		record := []string{
			strconv.FormatInt(cfg.ID, 10),
			cfg.Name,
			apiKeyStr,
			cfg.URL,
			strconv.Itoa(cfg.Priority),
			strings.Join(cfg.Models, ","),
			modelRedirectsJSON,
			cfg.GetChannelType(), // 使用GetChannelType确保默认值
			keyStrategy,
			strconv.FormatBool(cfg.Enabled),
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
	required := []string{"name", "api_key", "url", "models"}
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

		if name == "" || apiKey == "" || modelsRaw == "" {
			summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行缺少必填字段", lineNo))
			summary.Skipped++
			continue
		}

		// OAuth 预设（通过 preset 字段判断）
		isOAuthPreset := preset == "kiro" || preset == "codex" || preset == "gemini"

		// 验证 URL（OAuth 预设可以为空）
		if url == "" && !isOAuthPreset {
			summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行缺少URL", lineNo))
			summary.Skipped++
			continue
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
		cfg := &model.Config{
			Name:           name,
			URL:            url,
			Priority:       priority,
			Models:         models,
			ModelRedirects: modelRedirects,
			ChannelType:    channelType,
			Preset:         preset,  // 设置预设类型
			Enabled:        enabled,
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

			// 提取 OAuth 字段
			refreshToken, _ := authConfig["refreshToken"].(string)
			clientId, _ := authConfig["clientId"].(string)
			clientSecret, _ := authConfig["clientSecret"].(string)
			accessToken, _ := authConfig["accessToken"].(string)

			// deviceFingerprint 应该是 JSON 格式的指纹配置，不是简单的 UUID
			// 如果 CSV 中提供了 deviceFingerprint，应该是完整的 JSON 配置
			// 否则留空，让 Kiro 预设自动生成
			deviceFingerprintJSON, _ := authConfig["deviceFingerprint"].(string)

			// 提取 tokenExpiresAt（可能是 float64 或 int）
			var tokenExpiresAt int64
			if expiresAt, ok := authConfig["tokenExpiresAt"].(float64); ok {
				tokenExpiresAt = int64(expiresAt)
			} else if expiresAt, ok := authConfig["tokenExpiresAt"].(int64); ok {
				tokenExpiresAt = expiresAt
			}

			// 验证必需字段
			if refreshToken == "" {
				summary.Errors = append(summary.Errors, fmt.Sprintf("第%d行OAuth配置缺少refreshToken", lineNo))
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
					AccessToken:       accessToken,             // 如果 CSV 中提供了 access_token
					TokenExpiresAt:    tokenExpiresAt,          // 如果 CSV 中提供了过期时间
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

			// 如果是 IdC 方式，将 clientId 和 clientSecret 存储到 id_token 字段（JSON 格式）
			// [FIX] 与 admin_testing.go:96 保持一致，IdC 配置存储在 id_token 字段
			if clientId != "" && clientSecret != "" {
				idcConfig := map[string]string{
					"clientId":     clientId,
					"clientSecret": clientSecret,
				}
				if idcJSON, err := sonic.Marshal(idcConfig); err == nil {
					apiKeys[0].IDToken = string(idcJSON)
				}
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
