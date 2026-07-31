package util

import (
	"log"
	"strings"
	"sync"
)

// ============================================================================
// AI API 成本计算器（Claude + OpenAI）
// ============================================================================

// ModelPricing AI模型定价（单位：美元/百万tokens）
type ModelPricing struct {
	InputPrice      float64 // 基础输入token价格（$/1M tokens）
	OutputPrice     float64 // 输出token价格（$/1M tokens）
	CacheReadPrice  float64 // 基础缓存读取价格（$/1M tokens）
	CacheWritePrice float64 // 基础缓存写入价格（$/1M tokens）

	// 长上下文分段定价
	// 如果为0，表示无分段定价，使用InputPrice/OutputPrice
	InputPriceHigh      float64 // 高上下文输入价格（$/1M tokens）
	OutputPriceHigh     float64 // 高上下文输出价格（$/1M tokens）
	CacheReadPriceHigh  float64 // 高上下文缓存读取价格（$/1M tokens）
	CacheWritePriceHigh float64 // 高上下文缓存写入价格（$/1M tokens）
	HighPriceThreshold  int64   // 切换到高价档的输入token阈值
}

// DBPricingEntry DB 定价缓存条目（轻量结构，不依赖 model 包）
type DBPricingEntry struct {
	Model               string  // 基础模型名
	DisplayName         string  // 前端显示名
	ChannelType         string  // anthropic/codex/gemini
	InputPrice          float64 // $/1M tokens
	OutputPrice         float64 // $/1M tokens
	CacheReadPrice      float64 // 缓存读取 $/1M tokens
	CacheWritePrice     float64 // 缓存写入 $/1M tokens
	InputPriceHigh      float64 // 长上下文输入价
	OutputPriceHigh     float64 // 长上下文输出价
	CacheReadPriceHigh  float64 // 长上下文缓存读取价
	CacheWritePriceHigh float64 // 长上下文缓存写入价
	HighPriceThreshold  int64   // 高价档输入Token阈值
}

// dbPricingCache DB 定价内存缓存（sync.Map，并发安全，O(1) 查询）
// key: model name (string), value: DBPricingEntry
var dbPricingCache sync.Map

// SetDBPricing 批量加载 DB 定价数据到内存缓存（启动时和 Admin API 修改后调用）
func SetDBPricing(entries []DBPricingEntry) {
	// 先清空旧缓存
	dbPricingCache.Range(func(key, value any) bool {
		dbPricingCache.Delete(key)
		return true
	})
	// 加载新数据
	for _, e := range entries {
		dbPricingCache.Store(e.Model, e)
	}
}

// ClearDBPricing 清空 DB 定价缓存
func ClearDBPricing() {
	dbPricingCache.Range(func(key, value any) bool {
		dbPricingCache.Delete(key)
		return true
	})
}

// dbDefaultModels DB 默认模型列表缓存
// key: channelType (string), value: []string
var dbDefaultModels sync.Map

// dbAliasesCache DB 别名缓存（alias → base model）
var dbAliasesCache sync.Map

// SetDBDefaultModels 设置某渠道类型的默认模型列表。
func SetDBDefaultModels(channelType string, models []string) {
	copyOfModels := append([]string(nil), models...)
	dbDefaultModels.Store(NormalizeChannelType(channelType), copyOfModels)
}

// ClearDBDefaultModels 清除数据库默认列表缓存。
func ClearDBDefaultModels() {
	dbDefaultModels.Range(func(key, value any) bool {
		dbDefaultModels.Delete(key)
		return true
	})
}

// SetDBAliases 批量加载 DB 别名到内存缓存
func SetDBAliases(aliases map[string]string) {
	dbAliasesCache.Range(func(key, value any) bool {
		dbAliasesCache.Delete(key)
		return true
	})
	for alias, base := range aliases {
		dbAliasesCache.Store(alias, base)
	}
}

// GetDefaultPricing 导出硬编码定价供 Admin API "导入默认定价" 使用
func GetDefaultPricing() []DBPricingEntry {
	entries := make([]DBPricingEntry, 0, len(basePricing))
	for model, p := range basePricing {
		channelType := classifyModelChannelType(model)
		threshold := p.HighPriceThreshold
		if p.InputPriceHigh > 0 && threshold <= 0 {
			threshold = DefaultHighPriceThresholdForChannel(channelType)
		} else if p.InputPriceHigh <= 0 {
			threshold = 0
		}
		entries = append(entries, DBPricingEntry{
			Model:               model,
			ChannelType:         channelType,
			InputPrice:          p.InputPrice,
			OutputPrice:         p.OutputPrice,
			CacheReadPrice:      p.CacheReadPrice,
			CacheWritePrice:     p.CacheWritePrice,
			InputPriceHigh:      p.InputPriceHigh,
			OutputPriceHigh:     p.OutputPriceHigh,
			CacheReadPriceHigh:  p.CacheReadPriceHigh,
			CacheWritePriceHigh: p.CacheWritePriceHigh,
			HighPriceThreshold:  threshold,
		})
	}
	return entries
}

const (
	// DefaultHighPriceThreshold 是 OpenAI/GPT 模型默认的高价档输入Token阈值。
	DefaultHighPriceThreshold int64 = 272_000
	// GeminiHighPriceThreshold 保留 Gemini 官方的 200K 长上下文分段规则。
	GeminiHighPriceThreshold int64 = 200_000
)

// DefaultHighPriceThresholdForChannel 返回新建或旧版请求未传阈值时的默认值。
func DefaultHighPriceThresholdForChannel(channelType string) int64 {
	if strings.EqualFold(channelType, "gemini") {
		return GeminiHighPriceThreshold
	}
	return DefaultHighPriceThreshold
}

// classifyModelChannelType 根据模型名推断渠道类型
func classifyModelChannelType(model string) string {
	lower := strings.ToLower(model)
	if strings.HasPrefix(lower, "claude-") {
		return "anthropic"
	}
	if strings.HasPrefix(lower, "gemini-") {
		return "gemini"
	}
	return ChannelTypeCodex
}

// GetModelAliasesReverse 反向别名映射（base model → alias 列表）
func GetModelAliasesReverse() map[string][]string {
	result := make(map[string][]string)
	for alias, base := range modelAliases {
		result[base] = append(result[base], alias)
	}
	return result
}

// basePricing 基础定价表（无重复，每个模型只定义一次）
// 数据来源：
// - models.dev: https://models.dev/api.json
// 价格单位统一为美元/百万 tokens；缓存价格也是绝对价格，不再使用倍率。
// 最近同步：2026-07-31（仅保留 Anthropic、OpenAI、Google 的常用文本模型）。
func modelPricing(input, output, cacheRead, cacheWrite float64) ModelPricing {
	return ModelPricing{
		InputPrice: input, OutputPrice: output,
		CacheReadPrice: cacheRead, CacheWritePrice: cacheWrite,
	}
}

func tieredModelPricing(input, output, cacheRead, cacheWrite, highInput, highOutput, highCacheRead, highCacheWrite float64, threshold int64) ModelPricing {
	return ModelPricing{
		InputPrice: input, OutputPrice: output,
		CacheReadPrice: cacheRead, CacheWritePrice: cacheWrite,
		InputPriceHigh: highInput, OutputPriceHigh: highOutput,
		CacheReadPriceHigh: highCacheRead, CacheWritePriceHigh: highCacheWrite,
		HighPriceThreshold: threshold,
	}
}

var basePricing = map[string]ModelPricing{
	// ========== Claude 模型 ==========
	"claude-opus-5":     modelPricing(5.00, 25.00, 0.50, 6.25),
	"claude-sonnet-5":   modelPricing(2.00, 10.00, 0.20, 2.50),
	"claude-fable-5":    modelPricing(10.00, 50.00, 1.00, 12.50),
	"claude-opus-4-8":   modelPricing(5.00, 25.00, 0.50, 6.25),
	"claude-opus-4-7":   modelPricing(5.00, 25.00, 0.50, 6.25),
	"claude-opus-4-6":   modelPricing(5.00, 25.00, 0.50, 6.25),
	"claude-sonnet-4-6": modelPricing(3.00, 15.00, 0.30, 3.75),
	"claude-opus-4-5":   modelPricing(5.00, 25.00, 0.50, 6.25),
	"claude-sonnet-4-5": modelPricing(3.00, 15.00, 0.30, 3.75),
	"claude-haiku-4-5":  modelPricing(1.00, 5.00, 0.10, 1.25),
	"claude-opus-4-1":   modelPricing(15.00, 75.00, 1.50, 18.75),
	"claude-sonnet-4-0": modelPricing(3.00, 15.00, 0.30, 3.75),
	"claude-opus-4-0":   modelPricing(15.00, 75.00, 1.50, 18.75),
	"claude-3-7-sonnet": modelPricing(3.00, 15.00, 0.30, 3.75),
	"claude-3-5-sonnet": modelPricing(3.00, 15.00, 0.30, 3.75),
	"claude-3-5-haiku":  modelPricing(0.80, 4.00, 0.08, 1.00),
	"claude-3-opus":     modelPricing(15.00, 75.00, 1.50, 18.75),
	"claude-3-sonnet":   modelPricing(3.00, 15.00, 0.30, 3.75),
	"claude-3-haiku":    modelPricing(0.25, 1.25, 0.025, 0.3125),
	// 通用兜底（未来新版本）
	"claude-opus":   modelPricing(5.00, 25.00, 0.50, 6.25),
	"claude-sonnet": modelPricing(3.00, 15.00, 0.30, 3.75),
	"claude-haiku":  modelPricing(1.00, 5.00, 0.10, 1.25),

	// ========== OpenAI GPT系列 ==========
	"gpt-5.6":             tieredModelPricing(5.00, 30.00, 0.50, 6.25, 10.00, 45.00, 1.00, 12.50, DefaultHighPriceThreshold),
	"gpt-5.6-sol":         tieredModelPricing(5.00, 30.00, 0.50, 6.25, 10.00, 45.00, 1.00, 12.50, DefaultHighPriceThreshold),
	"gpt-5.6-terra":       tieredModelPricing(2.00, 12.00, 0.20, 2.50, 4.00, 18.00, 0.40, 5.00, DefaultHighPriceThreshold),
	"gpt-5.6-luna":        tieredModelPricing(0.20, 1.20, 0.02, 0.25, 0.40, 1.80, 0.04, 0.50, DefaultHighPriceThreshold),
	"gpt-5.5":             tieredModelPricing(5.00, 30.00, 0.50, 0, 10.00, 45.00, 1.00, 0, DefaultHighPriceThreshold),
	"gpt-5.5-pro":         tieredModelPricing(30.00, 180.00, 0, 0, 60.00, 270.00, 0, 0, DefaultHighPriceThreshold),
	"gpt-5.4":             tieredModelPricing(2.50, 15.00, 0.25, 0, 5.00, 22.50, 0.50, 0, DefaultHighPriceThreshold),
	"gpt-5.4-pro":         tieredModelPricing(30.00, 180.00, 0, 0, 60.00, 270.00, 0, 0, DefaultHighPriceThreshold),
	"gpt-5.4-mini":        modelPricing(0.75, 4.50, 0.075, 0),
	"gpt-5.4-nano":        modelPricing(0.20, 1.25, 0.02, 0),
	"gpt-5.3-codex":       modelPricing(1.75, 14.00, 0.175, 0),
	"gpt-5.3-codex-spark": modelPricing(1.75, 14.00, 0.175, 0),
	"gpt-5.2":             modelPricing(1.75, 14.00, 0.175, 0),
	"gpt-5.2-pro":         modelPricing(21.00, 168.00, 0, 0),
	"gpt-5":               modelPricing(1.25, 10.00, 0.125, 0),
	"gpt-5-mini":          modelPricing(0.25, 2.00, 0.025, 0),
	"gpt-5-nano":          modelPricing(0.05, 0.40, 0.005, 0),
	"gpt-5-pro":           modelPricing(15.00, 120.00, 0, 0),
	"gpt-5.1-codex-mini":  modelPricing(0.25, 2.00, 0.025, 0),
	"gpt-4.1":             modelPricing(2.00, 8.00, 0.50, 0),
	"gpt-4.1-mini":        modelPricing(0.40, 1.60, 0.10, 0),
	"gpt-4.1-nano":        modelPricing(0.10, 0.40, 0.025, 0),
	"gpt-4o":              modelPricing(2.50, 10.00, 1.25, 0),
	"gpt-4o-legacy":       modelPricing(5.00, 15.00, 2.50, 0), // 2024-05-13等旧版
	"gpt-4o-mini":         modelPricing(0.15, 0.60, 0.075, 0),
	"gpt-4-turbo":         modelPricing(10.00, 30.00, 0, 0),
	"gpt-4":               modelPricing(30.00, 60.00, 0, 0),
	"gpt-4-32k":           modelPricing(60.00, 120.00, 0, 0),
	"gpt-3.5-turbo":       modelPricing(0.50, 1.50, 0, 0),
	"gpt-3.5-legacy":      modelPricing(1.50, 2.00, 0, 0), // 旧版本
	"gpt-3.5-16k":         modelPricing(3.00, 4.00, 0, 0),

	// ========== OpenAI o系列 ==========
	"o1":               modelPricing(15.00, 60.00, 7.50, 0),
	"o1-pro":           modelPricing(150.00, 600.00, 0, 0),
	"o1-mini":          modelPricing(1.10, 4.40, 0.55, 0),
	"o3":               modelPricing(2.00, 8.00, 0.50, 0),
	"o3-pro":           modelPricing(20.00, 80.00, 0, 0),
	"o3-mini":          modelPricing(1.10, 4.40, 0.55, 0),
	"o3-deep-research": modelPricing(10.00, 40.00, 2.50, 0),
	"o4-mini":          modelPricing(1.10, 4.40, 0.275, 0),

	// ========== OpenAI 其他 ==========
	"computer-use-preview": modelPricing(3.00, 12.00, 0, 0),
	"codex-mini-latest":    modelPricing(1.50, 6.00, 0.375, 0),
	"davinci-002":          modelPricing(2.00, 2.00, 0, 0),
	"babbage-002":          modelPricing(0.40, 0.40, 0, 0),

	// ========== Gemini 模型 ==========
	"gemini-3.6-flash":      modelPricing(1.50, 7.50, 0.15, 0),
	"gemini-3.5-flash":      modelPricing(1.50, 9.00, 0.15, 0),
	"gemini-3.5-flash-lite": modelPricing(0.30, 2.50, 0.03, 0),
	"gemini-3.1-pro":        tieredModelPricing(2.00, 12.00, 0.20, 0, 4.00, 18.00, 0.40, 0, GeminiHighPriceThreshold),
	"gemini-3.1-flash-lite": modelPricing(0.25, 1.50, 0.025, 0),
	"gemini-3-pro":          tieredModelPricing(2.00, 12.00, 0.20, 0, 4.00, 18.00, 0.40, 0, GeminiHighPriceThreshold),
	"gemini-3-flash":        modelPricing(0.50, 3.00, 0.05, 0),
	"gemini-2.5-pro":        tieredModelPricing(1.25, 10.00, 0.125, 0, 2.50, 15.00, 0.25, 0, GeminiHighPriceThreshold),
	"gemini-2.5-flash":      modelPricing(0.30, 2.50, 0.03, 0),
	"gemini-2.5-flash-lite": modelPricing(0.10, 0.40, 0.01, 0),
	"gemini-2.0-flash":      modelPricing(0.10, 0.40, 0.01, 0),
	"gemini-2.0-flash-lite": modelPricing(0.075, 0.30, 0.0075, 0),
	"gemini-1.5-pro":        modelPricing(1.25, 5.00, 0.125, 0),
	"gemini-1.5-flash":      modelPricing(0.20, 0.60, 0.02, 0),
}

// modelAliases 模型别名映射（多对一）
// key: 别名, value: basePricing中的基础模型名
var modelAliases = map[string]string{
	// Claude别名
	"claude-opus-5-latest":       "claude-opus-5",
	"claude-sonnet-5-latest":     "claude-sonnet-5",
	"claude-sonnet-4-5-20250929": "claude-sonnet-4-5",
	"claude-haiku-4-5-20251001":  "claude-haiku-4-5",
	"claude-opus-4-1-20250805":   "claude-opus-4-1",
	"claude-sonnet-4-20250514":   "claude-sonnet-4-0",
	"claude-opus-4-20250514":     "claude-opus-4-0",
	"claude-3-7-sonnet-20250219": "claude-3-7-sonnet",
	"claude-3-7-sonnet-latest":   "claude-3-7-sonnet",
	"claude-3-5-sonnet-20241022": "claude-3-5-sonnet",
	"claude-3-5-sonnet-20240620": "claude-3-5-sonnet",
	"claude-3-5-sonnet-latest":   "claude-3-5-sonnet",
	"claude-3-5-haiku-20241022":  "claude-3-5-haiku",
	"claude-3-5-haiku-latest":    "claude-3-5-haiku",
	"claude-3-opus-20240229":     "claude-3-opus",
	"claude-3-opus-latest":       "claude-3-opus",
	"claude-3-sonnet-20240229":   "claude-3-sonnet",
	"claude-3-sonnet-latest":     "claude-3-sonnet",
	"claude-3-haiku-20240307":    "claude-3-haiku",
	"claude-3-haiku-latest":      "claude-3-haiku",

	// OpenAI GPT别名
	"gpt-5.3-codex-spark":        "gpt-5.3-codex", // 定价未公布，暂按 5.3-codex 级别
	"gpt-5.2-codex":              "gpt-5.2",
	"gpt-5.2-chat-latest":        "gpt-5.2",
	"gpt-5.1":                    "gpt-5",
	"gpt-5.1-chat-latest":        "gpt-5",
	"gpt-5-chat-latest":          "gpt-5",
	"gpt-5.1-codex":              "gpt-5",
	"gpt-5.1-codex-max":          "gpt-5",
	"gpt-5-codex":                "gpt-5",
	"gpt-5.1-codex-mini":         "gpt-5.1-codex-mini",
	"gpt-5-search-api":           "gpt-5",
	"gpt-4o-2024-05-13":          "gpt-4o-legacy",
	"chatgpt-4o-latest":          "gpt-4o-legacy",
	"gpt-4o-mini-search-preview": "gpt-4o-mini",
	"gpt-4o-search-preview":      "gpt-4o",
	"gpt-4-turbo-2024-04-09":     "gpt-4-turbo",
	"gpt-4-0125-preview":         "gpt-4-turbo",
	"gpt-4-1106-preview":         "gpt-4-turbo",
	"gpt-4-1106-vision-preview":  "gpt-4-turbo",
	"gpt-4-0613":                 "gpt-4",
	"gpt-4-0314":                 "gpt-4",
	"gpt-4-32k-0613":             "gpt-4-32k",
	"gpt-3.5-turbo-0125":         "gpt-3.5-turbo",
	"gpt-3.5-turbo-1106":         "gpt-3.5-legacy",
	"gpt-3.5-turbo-0613":         "gpt-3.5-legacy",
	"gpt-3.5-0301":               "gpt-3.5-legacy",
	"gpt-3.5-turbo-instruct":     "gpt-3.5-legacy",
	"gpt-3.5-turbo-16k-0613":     "gpt-3.5-16k",

	// o系列别名
	"o4-mini-deep-research": "o3-deep-research", // 相同定价

	// Gemini别名
	"gemini-3.1-pro-preview":        "gemini-3.1-pro",
	"gemini-3.1-flash-lite-preview": "gemini-3.1-flash-lite",
	"gemini-3-flash-preview":        "gemini-3-flash",
	"gemini-3-pro-preview":          "gemini-3-pro",
}

// getPricing 获取模型定价
// 查询优先级:
//  1. DB 内存缓存精确匹配
//  2. 硬编码 modelAliases 解析 → 再查 DB 缓存
//  3. 硬编码 basePricing 精确匹配
func getPricing(model string) (ModelPricing, bool) {
	// 1. DB 缓存精确匹配
	if p, ok := getDBPricing(model); ok {
		return p, true
	}

	// 2. 别名解析 → 再查 DB 缓存（优先 DB 别名，其次硬编码别名）
	if base, ok := dbAliasesCache.Load(model); ok {
		if p, ok := getDBPricing(base.(string)); ok {
			return p, true
		}
		model = base.(string)
	} else if base, ok := modelAliases[model]; ok {
		if p, ok := getDBPricing(base); ok {
			return p, true
		}
		model = base
	}

	// 3. 硬编码 basePricing 精确匹配
	p, ok := basePricing[model]
	return p, ok
}

// getDBPricing 从 DB 缓存查询定价，转换为 ModelPricing
func getDBPricing(model string) (ModelPricing, bool) {
	v, ok := dbPricingCache.Load(model)
	if !ok {
		return ModelPricing{}, false
	}
	e := v.(DBPricingEntry)
	return ModelPricing{
		InputPrice:          e.InputPrice,
		OutputPrice:         e.OutputPrice,
		CacheReadPrice:      e.CacheReadPrice,
		CacheWritePrice:     e.CacheWritePrice,
		InputPriceHigh:      e.InputPriceHigh,
		OutputPriceHigh:     e.OutputPriceHigh,
		CacheReadPriceHigh:  e.CacheReadPriceHigh,
		CacheWritePriceHigh: e.CacheWritePriceHigh,
		HighPriceThreshold:  e.HighPriceThreshold,
	}, true
}

// CalculateCost 计算单次请求的成本（美元）
// 参数：
//   - model: 模型名称（如"claude-sonnet-4-5-20250929"或"gpt-5.1-codex"）
//   - inputTokens: 输入token数量（已归一化为可计费token）
//   - outputTokens: 输出token数量
//   - cacheReadTokens: 缓存读取token数量（Claude: cache_read_input_tokens, OpenAI: cached_tokens）
//   - cacheCreationTokens: 缓存创建token数量（Claude: cache_creation_input_tokens）
//
// 重要: inputTokens应为"可计费输入token"，由解析层（proxy_sse_parser.go）负责归一化：
//   - OpenAI: 解析层已自动扣除cached_tokens（prompt_tokens - cached_tokens）
//   - Claude/Gemini: 解析层直接返回input_tokens（本身就是非缓存部分）
//
// 设计原则: 平台语义差异在解析层处理，计费层无需关心（SRP原则）
//
// 返回：总成本（美元），如果模型未知则返回0.0
func CalculateCost(model string, inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens int) float64 {
	// 防御性检查:拒绝负数token
	if inputTokens < 0 || outputTokens < 0 || cacheReadTokens < 0 || cacheCreationTokens < 0 {
		log.Printf("ERROR: negative tokens detected (model=%s): input=%d output=%d cache_read=%d cache_create=%d",
			model, inputTokens, outputTokens, cacheReadTokens, cacheCreationTokens)
		return 0.0
	}

	pricing, ok := getPricing(model)
	if !ok {
		// 尝试模糊匹配(例如:claude-3-opus-xxx → claude-3-opus)
		pricing, ok = fuzzyMatchModel(model)
		if !ok {
			return 0.0 // 未知模型
		}
	}

	// 成本计算公式(单位:美元)
	// 注意:价格是per 1M tokens,需要除以1,000,000
	cost := 0.0

	// 长上下文分段定价按完整输入上下文判断：普通输入 + 缓存读取 + 缓存写入。
	// 输出 token 不参与是否跨档的判断；跨档后整套输入、输出和缓存绝对价格同时生效。
	inputContextTokens := int64(inputTokens) + int64(cacheReadTokens) + int64(cacheCreationTokens)
	useHighPricing := pricing.InputPriceHigh > 0 && pricing.HighPriceThreshold > 0 && inputContextTokens > pricing.HighPriceThreshold

	// 选择适用的价格
	inputPricePerM := pricing.InputPrice
	outputPricePerM := pricing.OutputPrice
	cacheReadPricePerM := pricing.CacheReadPrice
	cacheWritePricePerM := pricing.CacheWritePrice
	if useHighPricing {
		inputPricePerM = pricing.InputPriceHigh
		outputPricePerM = pricing.OutputPriceHigh // Gemini长上下文定价同时影响输入和输出
		cacheReadPricePerM = pricing.CacheReadPriceHigh
		cacheWritePricePerM = pricing.CacheWritePriceHigh
	}

	// 1. 基础输入token成本（inputTokens已由解析层归一化，无需再处理平台差异）
	if inputTokens > 0 {
		cost += float64(inputTokens) * inputPricePerM / 1_000_000
	}

	// 2. 输出token成本
	if outputTokens > 0 {
		cost += float64(outputTokens) * outputPricePerM / 1_000_000
	}

	// 3. 缓存读取成本
	if cacheReadTokens > 0 {
		cost += float64(cacheReadTokens) * cacheReadPricePerM / 1_000_000
	}

	// 4. 缓存创建成本
	if cacheCreationTokens > 0 {
		cost += float64(cacheCreationTokens) * cacheWritePricePerM / 1_000_000
	}

	return cost
}

// isOpenAIModel 判断是否为OpenAI模型
// OpenAI模型包括：gpt-*, o*, chatgpt-*, davinci-*, babbage-*, computer-use-preview, codex-*
func isOpenAIModel(model string) bool {
	lowerModel := strings.ToLower(model)
	return strings.HasPrefix(lowerModel, "gpt-") ||
		strings.HasPrefix(lowerModel, "o1") ||
		strings.HasPrefix(lowerModel, "o3") ||
		strings.HasPrefix(lowerModel, "o4") ||
		strings.HasPrefix(lowerModel, "chatgpt-") ||
		strings.HasPrefix(lowerModel, "davinci-") ||
		strings.HasPrefix(lowerModel, "babbage-") ||
		strings.HasPrefix(lowerModel, "codex-") ||
		lowerModel == "computer-use-preview"
}

// LegacyCacheReadMultiplier 仅用于把旧数据库中的倍率配置迁移为绝对价格。
// 新计费逻辑不再使用倍率。
func LegacyCacheReadMultiplier(model string) float64 {
	lowerModel := strings.ToLower(model)
	if !isOpenAIModel(lowerModel) {
		return 0.1
	}

	// GPT-5系列: 90%折扣 (0.1倍)
	if strings.HasPrefix(lowerModel, "gpt-5") {
		return 0.1
	}

	// GPT-4.1系列: 75%折扣 (0.25倍)
	if strings.HasPrefix(lowerModel, "gpt-4.1") {
		return 0.25
	}

	// o3/o4系列（除o3-mini外）: 75%折扣 (0.25倍)
	if strings.HasPrefix(lowerModel, "o3") && !strings.Contains(lowerModel, "mini") {
		return 0.25
	}
	if strings.HasPrefix(lowerModel, "o4") {
		return 0.25
	}

	// codex-mini-latest: 75%折扣 (0.25倍)
	if strings.HasPrefix(lowerModel, "codex-mini") {
		return 0.25
	}

	// GPT-4o系列/o1系列/o3-mini/o1-mini: 50%折扣 (0.5倍)
	// 这是默认值，涵盖:
	//   - gpt-4o, gpt-4o-mini
	//   - o1, o1-mini, o1-pro
	//   - o3-mini
	return 0.5
}

// GetModelInputPrice 获取模型的输入价格（用于成本比较）
// 返回: (价格, 是否找到)
// 如果找不到定价返回 (0, false)
func GetModelInputPrice(model string) (float64, bool) {
	pricing, ok := getPricing(model)
	if !ok {
		pricing, ok = fuzzyMatchModel(model)
		if !ok {
			return 0, false
		}
	}
	return pricing.InputPrice, true
}

// SelectCheapestModel 从模型列表中选择计费最低的模型
// 规则：
// 1. 优先选择有计费信息且 InputPrice 最低的模型
// 2. 没有计费信息的模型跳过
// 3. 如果全部模型都没有计费信息，返回列表第一个模型
// 返回: (推荐模型, 是否有计费信息)
func SelectCheapestModel(models []string) (string, bool) {
	if len(models) == 0 {
		return "", false
	}

	var cheapestModel string
	var cheapestPrice float64 = -1
	hasPricing := false

	for _, model := range models {
		price, ok := GetModelInputPrice(model)
		if !ok {
			continue // 跳过无计费信息的模型
		}
		hasPricing = true
		if cheapestPrice < 0 || price < cheapestPrice {
			cheapestPrice = price
			cheapestModel = model
		}
	}

	if !hasPricing {
		// 全部无计费信息，返回第一个
		return models[0], false
	}

	return cheapestModel, true
}

// fuzzyMatchModel 模糊匹配模型名称
// 例如：claude-3-opus-20240229-extended → claude-3-opus
//
//	gpt-4o-2024-12-01 → gpt-4o
func fuzzyMatchModel(model string) (ModelPricing, bool) {
	lowerModel := strings.ToLower(model)

	// 硬编码前缀列表（按优先级和长度排序，更具体的前缀优先）
	// 优点：比动态排序快，可预测，并发安全
	prefixes := []string{
		// Claude模型（按版本降序，具体版本优先，通用兜底在最后）
		"claude-sonnet-5", "claude-fable-5", "claude-opus-5",
		"claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6", "claude-sonnet-4-6",
		"claude-sonnet-4-5", "claude-haiku-4-5", "claude-opus-4-5", "claude-opus-4-1",
		"claude-sonnet-4-0", "claude-opus-4-0", "claude-3-7-sonnet",
		"claude-3-5-sonnet", "claude-3-5-haiku",
		"claude-3-opus", "claude-3-sonnet", "claude-3-haiku",
		"claude-opus", "claude-sonnet", "claude-haiku", // 通用兜底

		// Gemini模型（按版本降序，更长的前缀优先）
		"gemini-3.6-flash", "gemini-3.5-flash-lite", "gemini-3.5-flash",
		"gemini-3.1-flash-lite", "gemini-3.1-pro",
		"gemini-3-flash", "gemini-3-pro", // Gemini 3 系列
		"gemini-2.5-flash-lite", "gemini-2.5-flash", "gemini-2.5-pro",
		"gemini-2.0-flash-lite", "gemini-2.0-flash",
		"gemini-1.5-pro", "gemini-1.5-flash",

		// OpenAI GPT系列（更长的前缀优先，避免gpt-4o-legacy被gpt-4o截断）
		"gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6",
		"gpt-5.5-pro", "gpt-5.5", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.4-pro", "gpt-5.4",
		"gpt-5.3-codex-spark", "gpt-5.3-codex", "gpt-5.2-pro", "gpt-5.2",
		"gpt-5-pro", "gpt-5-nano", "gpt-5-mini", "gpt-5",
		"gpt-4.1-nano", "gpt-4.1-mini", "gpt-4.1",
		"gpt-4o-legacy", "gpt-4o-mini", "gpt-4o", // legacy必须在gpt-4o之前
		"gpt-4-turbo", "gpt-4-32k", "gpt-4",
		"gpt-3.5-legacy", "gpt-3.5-16k", "gpt-3.5-turbo",

		// OpenAI o系列
		"o3-deep-research", "o3-pro", "o3-mini", "o3",
		"o1-pro", "o1-mini", "o1", "o4-mini",

		// OpenAI其他专用模型
		"computer-use-preview", "codex-mini-latest",
		"davinci-002", "babbage-002",
	}

	for _, prefix := range prefixes {
		if strings.HasPrefix(lowerModel, prefix) {
			// 先查 DB 缓存
			if p, ok := getDBPricing(prefix); ok {
				return p, true
			}
			// 再查硬编码
			if pricing, ok := basePricing[prefix]; ok {
				return pricing, true
			}
		}
	}

	// 最后尝试 DB 缓存中的前缀匹配（覆盖用户新增的模型）
	var bestMatch DBPricingEntry
	bestLen := 0
	dbPricingCache.Range(func(key, value any) bool {
		k := key.(string)
		if strings.HasPrefix(lowerModel, strings.ToLower(k)) && len(k) > bestLen {
			bestMatch = value.(DBPricingEntry)
			bestLen = len(k)
		}
		return true
	})
	if bestLen > 0 {
		return ModelPricing{
			InputPrice:          bestMatch.InputPrice,
			OutputPrice:         bestMatch.OutputPrice,
			CacheReadPrice:      bestMatch.CacheReadPrice,
			CacheWritePrice:     bestMatch.CacheWritePrice,
			InputPriceHigh:      bestMatch.InputPriceHigh,
			OutputPriceHigh:     bestMatch.OutputPriceHigh,
			CacheReadPriceHigh:  bestMatch.CacheReadPriceHigh,
			CacheWritePriceHigh: bestMatch.CacheWritePriceHigh,
			HighPriceThreshold:  bestMatch.HighPriceThreshold,
		}, true
	}

	return ModelPricing{}, false
}
