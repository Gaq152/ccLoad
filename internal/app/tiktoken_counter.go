package app

import (
	"strings"
	"sync"

	"github.com/bytedance/sonic"
	tiktoken "github.com/pkoukk/tiktoken-go"
)

var (
	tiktokenMu    sync.Mutex
	tiktokenCache = map[string]*tiktoken.Tiktoken{}
)

// countTokensWithTiktoken 使用 tiktoken 库计算 token 数量
// 如果模型未知，回退到 cl100k_base 编码
// Claude 模型使用 cl100k_base 作为近似值（比纯算法更准确）
func countTokensWithTiktoken(text string, model string) int {
	if text == "" {
		return 0
	}
	enc := getEncodingForModel(model)
	if enc == nil {
		// 回退到纯算法估算（约4字符/token）
		return (len([]rune(text)) + 3) / 4
	}
	// 防止 tiktoken 库 panic
	fallback := (len([]rune(text)) + 3) / 4
	defer func() {
		if r := recover(); r != nil {
			_ = fallback // 使用 fallback
		}
	}()
	ids := enc.Encode(text, nil, nil)
	return len(ids)
}

// getEncodingForModel 获取模型对应的编码器（带缓存）
func getEncodingForModel(model string) *tiktoken.Tiktoken {
	// Claude 模型使用 cl100k_base
	if model == "" || strings.HasPrefix(strings.ToLower(model), "claude") {
		model = "cl100k_base"
	}

	tiktokenMu.Lock()
	defer tiktokenMu.Unlock()

	if enc, ok := tiktokenCache[model]; ok {
		return enc
	}

	enc, err := tiktoken.EncodingForModel(model)
	if err != nil {
		enc, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			return nil
		}
		tiktokenCache[model] = enc
		return enc
	}

	tiktokenCache[model] = enc
	return enc
}

// countTokensWithTiktokenFromRequest 使用 tiktoken 计算整个请求的 token 数量
func countTokensWithTiktokenFromRequest(req *CountTokensRequest) int {
	if req == nil {
		return 0
	}

	tokens := 0
	encodingName := "cl100k_base"

	// 1. 系统提示词
	if req.System != nil {
		switch sys := req.System.(type) {
		case string:
			if sys != "" {
				tokens += countTokensWithTiktoken(sys, encodingName)
			}
		case []any:
			for _, block := range sys {
				tokens += countContentBlockWithTiktoken(block, encodingName)
			}
		default:
			if jsonBytes, err := sonic.Marshal(sys); err == nil {
				tokens += countTokensWithTiktoken(string(jsonBytes), encodingName)
			}
		}
	}

	// 2. 消息内容
	for _, msg := range req.Messages {
		// 角色标记开销
		tokens += 4

		switch content := msg.Content.(type) {
		case string:
			tokens += countTokensWithTiktoken(content, encodingName)
		case []any:
			for _, block := range content {
				tokens += countContentBlockWithTiktoken(block, encodingName)
			}
		default:
			if jsonBytes, err := sonic.Marshal(content); err == nil {
				tokens += countTokensWithTiktoken(string(jsonBytes), encodingName)
			}
		}
	}

	// 3. 工具定义
	if len(req.Tools) > 0 {
		if jsonBytes, err := sonic.Marshal(req.Tools); err == nil {
			tokens += countTokensWithTiktoken(string(jsonBytes), encodingName)
		}
	}

	if tokens < 1 {
		return 1
	}
	return tokens
}

// countContentBlockWithTiktoken 计算单个内容块的 token 数量
func countContentBlockWithTiktoken(block any, encodingName string) int {
	blockMap, ok := block.(map[string]any)
	if !ok {
		return 10
	}

	blockType, _ := blockMap["type"].(string)

	switch blockType {
	case "text":
		if text, ok := blockMap["text"].(string); ok {
			return countTokensWithTiktoken(text, encodingName)
		}
		return 10

	case "image":
		// 图片固定估算
		return 1500

	case "document":
		return 500

	case "tool_use":
		name, _ := blockMap["name"].(string)
		input := blockMap["input"]
		inputJSON, _ := sonic.Marshal(input)
		return countTokensWithTiktoken("<invoke name=\""+name+"\">"+string(inputJSON)+"</invoke>", encodingName)

	case "tool_result":
		var contentStr string
		switch c := blockMap["content"].(type) {
		case string:
			contentStr = c
		default:
			if jb, err := sonic.Marshal(c); err == nil {
				contentStr = string(jb)
			}
		}
		return countTokensWithTiktoken("<tool_result>"+contentStr+"</tool_result>", encodingName)

	default:
		if jsonBytes, err := sonic.Marshal(block); err == nil {
			return countTokensWithTiktoken(string(jsonBytes), encodingName)
		}
		return 10
	}
}
