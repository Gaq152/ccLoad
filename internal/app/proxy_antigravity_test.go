package app

import (
	"testing"

	"github.com/bytedance/sonic"
)

func TestFilterAntigravityRequestBody(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantKeys []string // 期望被删除的键
		keepKeys []string // 期望保留的键
	}{
		{
			name: "过滤 tools[].input_schema 中的 exclusiveMinimum",
			input: `{
				"model": "claude-3",
				"tools": [{
					"name": "test_tool",
					"input_schema": {
						"type": "object",
						"properties": {
							"line": {
								"type": "integer",
								"exclusiveMinimum": 0,
								"description": "line number"
							}
						}
					}
				}]
			}`,
			wantKeys: []string{"exclusiveMinimum"},
			keepKeys: []string{"type", "description"},
		},
		{
			name: "过滤多个不支持的字段",
			input: `{
				"tools": [{
					"input_schema": {
						"type": "object",
						"properties": {
							"count": {
								"type": "integer",
								"minimum": 1,
								"maximum": 100,
								"exclusiveMinimum": 0,
								"exclusiveMaximum": 101
							}
						}
					}
				}]
			}`,
			wantKeys: []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"},
			keepKeys: []string{"type"},
		},
		{
			name: "保留不包含敏感字段的请求体",
			input: `{
				"model": "claude-3",
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantKeys: []string{},
			keepKeys: []string{"model", "messages"},
		},
		{
			name: "递归过滤嵌套 properties",
			input: `{
				"tools": [{
					"input_schema": {
						"type": "object",
						"properties": {
							"config": {
								"type": "object",
								"properties": {
									"timeout": {
										"type": "integer",
										"minimum": 0,
										"default": 30
									}
								}
							}
						}
					}
				}]
			}`,
			wantKeys: []string{"minimum", "default"},
			keepKeys: []string{"type"},
		},
		{
			name: "过滤 Gemini 原生格式 function_declarations",
			input: `{
				"tools": [{
					"function_declarations": [{
						"name": "read_file",
						"parameters": {
							"type": "object",
							"properties": {
								"line": {
									"type": "integer",
									"exclusiveMinimum": 0,
									"description": "line number"
								},
								"limit": {
									"type": "integer",
									"minimum": 1,
									"maximum": 1000
								}
							}
						}
					}]
				}]
			}`,
			wantKeys: []string{"exclusiveMinimum", "minimum", "maximum"},
			keepKeys: []string{"type", "description", "name", "function_declarations"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := FilterAntigravityRequestBody([]byte(tt.input))
			if err != nil {
				t.Fatalf("FilterAntigravityRequestBody() error = %v", err)
			}

			// 验证结果是有效 JSON
			var data map[string]any
			if err := sonic.Unmarshal(result, &data); err != nil {
				t.Fatalf("结果不是有效 JSON: %v", err)
			}

			// 将结果转为字符串检查
			resultStr := string(result)

			// 验证不需要的键被删除
			for _, key := range tt.wantKeys {
				if containsKey(resultStr, key) {
					t.Errorf("期望删除的键 %q 仍然存在", key)
				}
			}

			// 验证需要保留的键存在
			for _, key := range tt.keepKeys {
				if !containsKey(resultStr, key) {
					t.Errorf("期望保留的键 %q 被删除", key)
				}
			}
		})
	}
}

// containsKey 简单检查 JSON 字符串是否包含某个键
func containsKey(jsonStr, key string) bool {
	return len(jsonStr) > 0 && (
		// 检查 "key": 模式
		len(key) > 0 && (
			jsonContains(jsonStr, `"`+key+`":`) ||
			jsonContains(jsonStr, `"`+key+`" :`)))
}

func jsonContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestFilterAntigravityRequestBody_Empty(t *testing.T) {
	// 空请求体应该原样返回
	result, err := FilterAntigravityRequestBody([]byte{})
	if err != nil {
		t.Fatalf("FilterAntigravityRequestBody() error = %v", err)
	}
	if len(result) != 0 {
		t.Errorf("空请求体应该返回空，实际返回 %d bytes", len(result))
	}
}

func TestFilterAntigravityRequestBody_NonJSON(t *testing.T) {
	// 非 JSON 请求体应该原样返回
	input := []byte("not json")
	result, err := FilterAntigravityRequestBody(input)
	if err != nil {
		t.Fatalf("FilterAntigravityRequestBody() error = %v", err)
	}
	if string(result) != string(input) {
		t.Errorf("非 JSON 应该原样返回")
	}
}
