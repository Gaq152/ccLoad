package util

import (
	"encoding/json"
	"strings"
	"unicode"
)

// ParseAPIKeys 解析 API Key 字符串（支持逗号分隔的多个 Key）
// 设计原则（DRY）：统一的Key解析逻辑，供多个模块复用
// 特殊处理：如果整个字符串是一个 JSON 对象/数组，则视为单个 Key（如 OAuth Token）
func ParseAPIKeys(apiKey string) []string {
	apiKey = strings.TrimSpace(apiKey)
	apiKey = strings.TrimPrefix(apiKey, "\ufeff") // 移除可能存在的 BOM
	if apiKey == "" {
		return []string{}
	}

	// 特殊处理：JSON 格式的 OAuth Token
	// - 允许前缀空白
	// - 仅当整个字符串是有效 JSON 对象/数组时才视为单个 Key，避免误判
	normalized := strings.TrimLeftFunc(apiKey, unicode.IsSpace)
	if len(normalized) > 0 {
		first := normalized[0]
		if (first == '{' || first == '[') && json.Valid([]byte(apiKey)) {
			return []string{apiKey}
		}
	}

	// 普通 API Key：按逗号分割
	parts := strings.Split(apiKey, ",")
	keys := make([]string, 0, len(parts))
	for _, k := range parts {
		k = strings.TrimSpace(k)
		if k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}
