package app

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ccLoad/internal/util"

	"github.com/gin-gonic/gin"
)

// ============================================================
// Admin API: 获取渠道可用模型列表
// ============================================================

// FetchModelsRequest 获取模型列表请求参数
type FetchModelsRequest struct {
	ChannelType string `json:"channel_type" binding:"required"`
	URL         string `json:"url" binding:"required"`
	APIKey      string `json:"api_key"`                       // 普通渠道使用
	AccessToken string `json:"access_token,omitempty"`        // Codex OAuth 官方预设使用
}

// FetchModelsResponse 获取模型列表响应
type FetchModelsResponse struct {
	Models      []string          `json:"models"`          // 模型列表
	ChannelType string            `json:"channel_type"`    // 渠道类型
	Source      string            `json:"source"`          // 数据来源: "api"(从API获取) 或 "predefined"(预定义)
	Debug       *FetchModelsDebug `json:"debug,omitempty"` // 调试信息（仅开发环境）
}

// FetchModelsDebug 调试信息结构
type FetchModelsDebug struct {
	NormalizedType string `json:"normalized_type"` // 规范化后的渠道类型
	FetcherType    string `json:"fetcher_type"`    // 使用的Fetcher类型
	ChannelURL     string `json:"channel_url"`     // 渠道URL（脱敏）
}

// HandleFetchModels 获取指定渠道的可用模型列表
// 路由: GET /admin/channels/:id/models/fetch
// 功能:
//   - 根据渠道类型调用对应的Models API
//   - Anthropic/Codex: 返回预定义列表(官方无API)
//   - OpenAI/Gemini: 调用官方/v1/models接口
//
// 设计模式: 适配器模式(Adapter Pattern) + 策略模式(Strategy Pattern)
func (s *Server) HandleFetchModels(c *gin.Context) {
	// 1. 解析路径参数
	idStr := c.Param("id")
	channelID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "无效的渠道ID")
		return
	}

	// 2. 查询渠道配置
	channel, err := s.channelCache.GetConfig(c.Request.Context(), channelID)
	if err != nil {
		RespondErrorMsg(c, http.StatusNotFound, "渠道不存在")
		return
	}

	// 3. 获取第一个API Key（用于调用Models API）
	keys, err := s.store.GetAPIKeys(c.Request.Context(), channelID)
	if err != nil || len(keys) == 0 {
		RespondErrorMsg(c, http.StatusBadRequest, "该渠道没有可用的API Key")
		return
	}
	apiKey := keys[0].APIKey

	// 4. 根据渠道配置执行模型抓取（支持query参数覆盖渠道类型）
	channelType := c.Query("channel_type")
	if channelType == "" {
		channelType = channel.ChannelType
	}
	response, err := fetchModelsForConfig(c.Request.Context(), channelType, channel.URL, apiKey)
	if err != nil {
		// [INFO] 修复：统一返回200（与HandleFetchModelsPreview保持一致）
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	RespondJSON(c, http.StatusOK, response)
}

// HandleFetchModelsPreview 支持未保存的渠道配置直接测试模型列表
// 路由: POST /admin/channels/models/fetch
func (s *Server) HandleFetchModelsPreview(c *gin.Context) {
	var req FetchModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "参数无效: "+err.Error())
		return
	}

	req.ChannelType = strings.TrimSpace(req.ChannelType)
	req.URL = strings.TrimSpace(req.URL)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.AccessToken = strings.TrimSpace(req.AccessToken)

	if req.ChannelType == "" || req.URL == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "channel_type、url为必填字段")
		return
	}

	// 确定使用哪个认证凭据
	// Codex 官方预设使用 access_token，其他渠道使用 api_key
	authKey := req.APIKey
	if authKey == "" && req.AccessToken != "" {
		authKey = req.AccessToken
	}

	// 检查是否需要认证凭据（预定义列表类型不需要）
	source := determineSource(req.ChannelType, req.URL)
	if source == "api" && authKey == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "该渠道类型需要提供api_key或access_token")
		return
	}

	response, err := fetchModelsForConfig(c.Request.Context(), req.ChannelType, req.URL, authKey)
	if err != nil {
		// [INFO] 修复：统一返回200，通过success字段区分成功/失败（上游错误是预期内的）
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}
	RespondJSON(c, http.StatusOK, response)
}

func fetchModelsForConfig(ctx context.Context, channelType, channelURL, apiKey string) (*FetchModelsResponse, error) {
	normalizedType := util.NormalizeChannelType(channelType)
	source := determineSource(channelType, channelURL)

	var (
		models     []string
		fetcherStr string
		err        error
	)

	// Anthropic/Codex等官方无开放接口的渠道，直接返回预设模型列表
	if source == "predefined" {
		models = util.PredefinedModels(normalizedType)
		if len(models) == 0 {
			return nil, fmt.Errorf("渠道类型:%s 暂无预设模型列表", normalizedType)
		}
		fetcherStr = "predefined"
	} else {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		fetcher := util.NewModelsFetcher(channelType)
		fetcherStr = fmt.Sprintf("%T", fetcher)

		models, err = fetcher.FetchModels(ctx, channelURL, apiKey)
		if err != nil {
			return nil, fmt.Errorf(
				"获取模型列表失败(渠道类型:%s, 规范化类型:%s, 数据来源:%s): %w",
				channelType, normalizedType, source, err,
			)
		}
	}

	return &FetchModelsResponse{
		Models:      models,
		ChannelType: channelType,
		Source:      source,
		Debug: &FetchModelsDebug{
			NormalizedType: normalizedType,
			FetcherType:    fetcherStr,
			ChannelURL:     channelURL,
		},
	}, nil
}

// determineSource 判断模型列表来源（辅助函数）
// channelURL 用于区分 Gemini CLI 官方端点（无 models API）和标准 Gemini API
func determineSource(channelType, channelURL string) string {
	switch util.NormalizeChannelType(channelType) {
	case util.ChannelTypeGemini:
		// Gemini CLI 官方端点没有 models API，使用预定义列表
		if strings.Contains(channelURL, "cloudcode-pa.googleapis.com") {
			return "predefined"
		}
		return "api" // 标准 Gemini API 从接口获取
	default:
		return "predefined" // 预定义列表（Anthropic/Codex等官方无开放接口）
	}
}

// ============================================================
// Admin API: 选择最低计费模型（用于测试功能默认选择）
// ============================================================

// SelectCheapestModelRequest 选择最低计费模型请求
type SelectCheapestModelRequest struct {
	Models []string `json:"models" binding:"required"`
}

// SelectCheapestModelResponse 选择最低计费模型响应
type SelectCheapestModelResponse struct {
	Model      string  `json:"model"`       // 推荐的模型名称
	HasPricing bool    `json:"has_pricing"` // 是否有计费信息
	Price      float64 `json:"price"`       // 输入价格（$/1M tokens），无计费时为 0
}

// HandleSelectCheapestModel 从模型列表中选择计费最低的模型
// 路由: POST /admin/models/cheapest
// 功能:
//   - 输入: 模型名称列表
//   - 输出: 计费最低的模型（用于测试时的默认选择）
//   - 规则: 有计费信息的模型优先；全无计费则返回第一个
func (s *Server) HandleSelectCheapestModel(c *gin.Context) {
	var req SelectCheapestModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "参数无效: "+err.Error())
		return
	}

	if len(req.Models) == 0 {
		RespondErrorMsg(c, http.StatusBadRequest, "模型列表不能为空")
		return
	}

	model, hasPricing := util.SelectCheapestModel(req.Models)
	var price float64
	if hasPricing {
		price, _ = util.GetModelInputPrice(model)
	}

	RespondJSON(c, http.StatusOK, SelectCheapestModelResponse{
		Model:      model,
		HasPricing: hasPricing,
		Price:      price,
	})
}
