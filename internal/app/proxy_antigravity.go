package app

import (
	"github.com/bytedance/sonic"
)

// ============================================================================
// Antigravity 预设：Anthropic → Gemini 反代兼容处理
// ============================================================================
// Antigravity 是一种特殊的反代服务，将 Anthropic 格式请求转发到 Google 后端。
// 由于 Gemini API 只支持 JSON Schema 的子集，需要过滤不支持的字段。

// unsupportedSchemaFields Gemini API 不支持的 JSON Schema 字段
// 参考：https://ai.google.dev/gemini-api/docs/function-calling
var unsupportedSchemaFields = map[string]bool{
	// 数值约束（JSON Schema draft-04/06/07）
	"exclusiveMinimum": true,
	"exclusiveMaximum": true,
	"minimum":          true,
	"maximum":          true,
	"multipleOf":       true,

	// 字符串约束
	"minLength": true,
	"maxLength": true,
	"pattern":   true,

	// 数组约束
	"minItems":    true,
	"maxItems":    true,
	"uniqueItems": true,

	// 对象约束
	"additionalProperties": true,
	"patternProperties":    true,
	"minProperties":        true,
	"maxProperties":        true,
	"dependencies":         true,

	// 引用和元数据
	"$schema":     true,
	"$ref":        true,
	"$id":         true,
	"definitions": true,
	"$defs":       true,

	// 组合关键字（Gemini 部分支持，但为安全起见也过滤）
	"allOf": true,
	"anyOf": true,
	"oneOf": true,
	"not":   true,

	// 条件关键字
	"if":   true,
	"then": true,
	"else": true,

	// 其他
	"const":            true,
	"default":          true,
	"examples":         true,
	"contentMediaType": true,
	"contentEncoding":  true,
	"title":            true, // Gemini 可能支持，但 Google 错误显示不支持
}

// FilterAntigravityRequestBody 过滤 Antigravity 预设的请求体
// 移除 Gemini API 不支持的 JSON Schema 字段
func FilterAntigravityRequestBody(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}

	var data map[string]any
	if err := sonic.Unmarshal(body, &data); err != nil {
		// 解析失败，原样返回（可能不是 JSON）
		return body, nil
	}

	// 过滤 tools 中的 input_schema（Anthropic 格式）
	if tools, ok := data["tools"].([]any); ok {
		for _, tool := range tools {
			if toolMap, ok := tool.(map[string]any); ok {
				// Anthropic 格式：tools[].input_schema
				if inputSchema, ok := toolMap["input_schema"].(map[string]any); ok {
					filterSchemaRecursive(inputSchema)
				}
				// OpenAI 格式：tools[].function.parameters
				if function, ok := toolMap["function"].(map[string]any); ok {
					if parameters, ok := function["parameters"].(map[string]any); ok {
						filterSchemaRecursive(parameters)
					}
				}
			}
		}
	}

	return sonic.Marshal(data)
}

// filterSchemaRecursive 递归过滤 JSON Schema 中不支持的字段
func filterSchemaRecursive(schema map[string]any) {
	// 删除不支持的顶层字段
	for field := range unsupportedSchemaFields {
		delete(schema, field)
	}

	// 递归处理 properties
	if properties, ok := schema["properties"].(map[string]any); ok {
		for _, prop := range properties {
			if propMap, ok := prop.(map[string]any); ok {
				filterSchemaRecursive(propMap)
			}
		}
	}

	// 递归处理 items（数组类型）
	if items, ok := schema["items"].(map[string]any); ok {
		filterSchemaRecursive(items)
	}
	// items 也可能是数组（tuple validation）
	if itemsArray, ok := schema["items"].([]any); ok {
		for _, item := range itemsArray {
			if itemMap, ok := item.(map[string]any); ok {
				filterSchemaRecursive(itemMap)
			}
		}
	}

	// 递归处理嵌套的 allOf/anyOf/oneOf（虽然我们删除了这些，但以防万一）
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		if arr, ok := schema[keyword].([]any); ok {
			for _, item := range arr {
				if itemMap, ok := item.(map[string]any); ok {
					filterSchemaRecursive(itemMap)
				}
			}
		}
	}
}
