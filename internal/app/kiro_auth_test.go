package app

import (
	"testing"
)

// TestParseKiroAuthConfig_AutoDetectIdC 测试自动推断 IdC 模式
func TestParseKiroAuthConfig_AutoDetectIdC(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedType   string
		shouldBeNil    bool
		description    string
	}{
		{
			name: "IdC模式-有clientId和clientSecret-无authType",
			input: `{
				"refreshToken": "test-refresh-token",
				"clientId": "test-client-id",
				"clientSecret": "test-client-secret"
			}`,
			expectedType: KiroAuthMethodIdC,
			shouldBeNil:  false,
			description:  "应该自动推断为 IdC 模式",
		},
		{
			name: "IdC模式-显式指定authMethod",
			input: `{
				"refreshToken": "test-refresh-token",
				"authMethod": "IdC",
				"clientId": "test-client-id",
				"clientSecret": "test-client-secret"
			}`,
			expectedType: KiroAuthMethodIdC,
			shouldBeNil:  false,
			description:  "应该识别 authMethod 字段",
		},
		{
			name: "IdC模式-显式指定auth",
			input: `{
				"refreshToken": "test-refresh-token",
				"auth": "IdC",
				"clientId": "test-client-id",
				"clientSecret": "test-client-secret"
			}`,
			expectedType: KiroAuthMethodIdC,
			shouldBeNil:  false,
			description:  "应该识别 auth 字段",
		},
		{
			name: "Social模式-只有refreshToken",
			input: `{
				"refreshToken": "test-refresh-token"
			}`,
			expectedType: KiroAuthMethodSocial,
			shouldBeNil:  false,
			description:  "应该默认为 Social 模式",
		},
		{
			name: "Social模式-显式指定",
			input: `{
				"refreshToken": "test-refresh-token",
				"authMethod": "Social"
			}`,
			expectedType: KiroAuthMethodSocial,
			shouldBeNil:  false,
			description:  "应该识别为 Social 模式",
		},
		{
			name: "只有clientId-推断为Social",
			input: `{
				"refreshToken": "test-refresh-token",
				"clientId": "test-client-id"
			}`,
			expectedType: KiroAuthMethodSocial,
			shouldBeNil:  false,
			description:  "只有 clientId 不足以推断为 IdC，应该默认为 Social",
		},
		{
			name: "IdC模式-显式指定但缺少clientSecret",
			input: `{
				"refreshToken": "test-refresh-token",
				"authMethod": "IdC",
				"clientId": "test-client-id"
			}`,
			expectedType: "",
			shouldBeNil:  true,
			description:  "显式指定 IdC 但缺少必要字段应该返回 nil",
		},
		{
			name: "缺少refreshToken",
			input: `{
				"clientId": "test-client-id",
				"clientSecret": "test-client-secret"
			}`,
			expectedType: "",
			shouldBeNil:  true,
			description:  "缺少 refreshToken 应该返回 nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := ParseKiroAuthConfig(tt.input)

			if tt.shouldBeNil {
				if config != nil {
					t.Errorf("%s: 期望返回 nil，实际返回了配置", tt.description)
				}
				return
			}

			if config == nil {
				t.Fatalf("%s: 期望返回配置，实际返回 nil", tt.description)
			}

			if config.AuthType != tt.expectedType {
				t.Errorf("%s: 期望 AuthType=%s，实际=%s", tt.description, tt.expectedType, config.AuthType)
			}

			t.Logf("[PASS] %s: AuthType=%s", tt.description, config.AuthType)
		})
	}
}

// TestParseKiroAuthConfig_AuthMethodPriority 测试 auth 和 authMethod 字段优先级
func TestParseKiroAuthConfig_AuthMethodPriority(t *testing.T) {
	// auth 字段优先于 authMethod
	input := `{
		"refreshToken": "test-token",
		"auth": "IdC",
		"authMethod": "Social",
		"clientId": "test-id",
		"clientSecret": "test-secret"
	}`

	config := ParseKiroAuthConfig(input)
	if config == nil {
		t.Fatal("期望返回配置，实际返回 nil")
	}

	if config.AuthType != KiroAuthMethodIdC {
		t.Errorf("auth 字段应该优先，期望 IdC，实际 %s", config.AuthType)
	}

	t.Logf("[PASS] auth 字段优先级正确: %s", config.AuthType)
}
