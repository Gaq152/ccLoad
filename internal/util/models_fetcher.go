package util

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ModelsFetcher 模型列表获取器接口
// 不同渠道类型有不同的API实现
type ModelsFetcher interface {
	FetchModels(ctx context.Context, baseURL string, apiKey string) ([]string, error)
}

// NewModelsFetcher 根据渠道类型创建对应的Fetcher
// [FIX] P2-9: 删除口号式注释，代码已经够清晰
func NewModelsFetcher(channelType string) ModelsFetcher {
	switch NormalizeChannelType(channelType) {
	case ChannelTypeAnthropic:
		return &AnthropicModelsFetcher{}
	case ChannelTypeGemini:
		return &GeminiModelsFetcher{}
	case ChannelTypeCodex:
		return &CodexModelsFetcher{}
	default:
		return &AnthropicModelsFetcher{} // 默认使用Anthropic格式
	}
}

// ============================================================
// 公共辅助函数 - 避免重复HTTP请求逻辑
// ============================================================

// 全局复用的 HTTP Client（连接池化，避免每次请求创建新客户端）
// [FIX] P2-8: 使用全局 HTTP Client，复用连接池
var defaultModelsFetcherClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	},
}

// doHTTPRequest 执行HTTP GET请求并返回响应体
// 封装公共的HTTP请求、错误处理、超时控制逻辑
func doHTTPRequest(req *http.Request) ([]byte, error) {
	resp, err := defaultModelsFetcherClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// [INFO] 修复：区分4xx和5xx错误，便于上层返回正确的HTTP状态码
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, fmt.Errorf("上游配置错误 (HTTP %d): %s", resp.StatusCode, string(body))
		}
		return nil, fmt.Errorf("上游服务器错误 (HTTP %d): %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// ============================================================
// Anthropic/Claude Code 渠道适配器
// ============================================================
type AnthropicModelsFetcher struct{}

type anthropicModelsResponse struct {
	Data []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Type        string `json:"type"`
		CreatedAt   string `json:"created_at"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	FirstID string `json:"first_id"`
	LastID  string `json:"last_id"`
}

func (f *AnthropicModelsFetcher) FetchModels(ctx context.Context, baseURL string, apiKey string) ([]string, error) {
	// Anthropic Models API: https://docs.claude.com/en/api/models-list
	endpoint := baseURL + "/v1/models"

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// Anthropic要求的请求头
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	// 使用公共HTTP请求函数 (ctx已包含在req中)
	body, err := doHTTPRequest(req)
	if err != nil {
		return nil, err
	}

	var result anthropicModelsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}

	return models, nil
}

// ============================================================
// OpenAI 渠道适配器
// ============================================================
type OpenAIModelsFetcher struct{}

type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func (f *OpenAIModelsFetcher) FetchModels(ctx context.Context, baseURL string, apiKey string) ([]string, error) {
	// OpenAI Models API: https://platform.openai.com/docs/api-reference/models/list
	endpoint := baseURL + "/v1/models"

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	// 使用公共HTTP请求函数 (ctx已包含在req中)
	body, err := doHTTPRequest(req)
	if err != nil {
		return nil, err
	}

	var result openAIModelsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}

	return models, nil
}

// ============================================================
// Google Gemini 渠道适配器
// ============================================================
type GeminiModelsFetcher struct{}

type geminiModelsResponse struct {
	Models []struct {
		Name string `json:"name"` // 格式: "models/gemini-1.5-flash"
	} `json:"models"`
}

func (f *GeminiModelsFetcher) FetchModels(ctx context.Context, baseURL string, apiKey string) ([]string, error) {
	// Gemini Models API: https://ai.google.dev/api/rest/v1beta/models/list
	endpoint := baseURL + "/v1beta/models?key=" + apiKey

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 使用公共HTTP请求函数 (ctx已包含在req中)
	body, err := doHTTPRequest(req)
	if err != nil {
		return nil, err
	}

	var result geminiModelsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	models := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		// 提取模型名称（去掉"models/"前缀）
		if len(m.Name) > 7 && m.Name[:7] == "models/" {
			models = append(models, m.Name[7:])
		} else {
			models = append(models, m.Name)
		}
	}

	return models, nil
}

// ============================================================
// Codex 渠道适配器
// ============================================================
type CodexModelsFetcher struct{}

func (f *CodexModelsFetcher) FetchModels(ctx context.Context, baseURL string, apiKey string) ([]string, error) {
	// Codex使用与OpenAI相同的标准接口 /v1/models
	openAIFetcher := &OpenAIModelsFetcher{}
	return openAIFetcher.FetchModels(ctx, baseURL, apiKey)
}

// ============================================================
// 预设模型列表（用于官方无Models API的渠道）
// ============================================================

var predefinedModelSets = map[string][]string{
	ChannelTypeAnthropic: {
		// Claude 4.5 系列（2025年最新）
		"claude-opus-4-5-20251101",
		"claude-sonnet-4-5-20250929",
		"claude-haiku-4-5-20251001",
		// Claude 4.1 系列
		"claude-opus-4-1-20250805",
		// Claude 4.0 系列
		"claude-sonnet-4-20250514",
		"claude-opus-4-20250514",
	},
	ChannelTypeCodex: {
		"gpt-5",
		"gpt-5.1",
		"gpt-5.1-codex",
		"gpt-5.1-codex-max",
		"gpt-5.2",
	},
	ChannelTypeGemini: {
		// Gemini 3 系列（2025年11月最新）
		"gemini-3-pro",
		"gemini-3-deep-think",
		// Gemini 2.5 系列
		"gemini-2.5-pro",
		"gemini-2.5-flash",
	},
}

// PredefinedModels 返回给定渠道类型的预设模型列表
func PredefinedModels(channelType string) []string {
	ct := NormalizeChannelType(channelType)
	models, ok := predefinedModelSets[ct]
	if !ok {
		return nil
	}
	result := make([]string, len(models))
	copy(result, models)
	return result
}
