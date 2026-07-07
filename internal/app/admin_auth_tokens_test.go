package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"ccLoad/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthToken_MaskToken(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected string
	}{
		{
			name:     "Long token",
			token:    "sk-ant-1234567890abcdefghijklmnop",
			expected: "sk-a****mnop",
		},
		{
			name:     "Short token",
			token:    "short",
			expected: "****",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			masked := model.MaskToken(tt.token)
			if masked != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, masked)
			}
		})
	}
}

func TestAdminAPI_CreateAuthToken_Basic(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	requestBody := map[string]any{
		"description": "Test Token",
	}

	body, _ := json.Marshal(requestBody)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/admin/auth-tokens", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.HandleCreateAuthToken(c)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if !response.Success || len(response.Data.Token) == 0 {
		t.Error("Token creation failed")
	}

	ctx := context.Background()
	stored, err := server.store.GetAuthToken(ctx, response.Data.ID)
	if err != nil {
		t.Fatalf("DB error: %v", err)
	}

	expectedHash := model.HashToken(response.Data.Token)
	if stored.Token != expectedHash {
		t.Error("Hash mismatch")
	}

	if stored.QuotaLimitUSD != nil {
		t.Fatalf("new token quota = %v, want unlimited nil", *stored.QuotaLimitUSD)
	}
}

func TestAdminAPI_CreateAuthToken_WithQuotaLimit(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	requestBody := map[string]any{
		"description":     "Limited Token",
		"quota_limit_usd": 1.25,
	}

	body, _ := json.Marshal(requestBody)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/admin/auth-tokens", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.HandleCreateAuthToken(c)

	require.Equal(t, http.StatusOK, w.Code)

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success)

	stored, err := server.store.GetAuthToken(context.Background(), response.Data.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.QuotaLimitUSD)
	assert.Equal(t, 1.25, *stored.QuotaLimitUSD)
}

func TestAdminAPI_UpdateAuthToken_RejectsNegativeQuota(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	tokenHash := model.HashToken("sk-ccl-negative-quota")
	err := server.store.CreateAuthToken(context.Background(), &model.AuthToken{
		Token:       tokenHash,
		Description: "Negative quota test",
		IsActive:    true,
		AllChannels: true,
	})
	require.NoError(t, err)

	stored, err := server.store.GetAuthTokenByValue(context.Background(), tokenHash)
	require.NoError(t, err)

	requestBody := map[string]any{
		"quota_limit_usd": -0.01,
	}
	body, _ := json.Marshal(requestBody)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(stored.ID, 10)}}
	c.Request = httptest.NewRequest(http.MethodPut, "/admin/auth-tokens/"+strconv.FormatInt(stored.ID, 10), bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.HandleUpdateAuthToken(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAuthTokenQuota_AutoDisablesTokenAndReloadsAuthCache(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	rawToken := "sk-ccl-quota-auto-disable"
	tokenHash := model.HashToken(rawToken)
	quota := 0.01

	err := server.store.CreateAuthToken(context.Background(), &model.AuthToken{
		Token:         tokenHash,
		Description:   "Quota auto disable",
		IsActive:      true,
		AllChannels:   true,
		QuotaLimitUSD: &quota,
	})
	require.NoError(t, err)
	require.NoError(t, server.authService.ReloadAuthTokens())

	t.Run("active token passes before quota is reached", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
		c.Request.Header.Set("Authorization", "Bearer "+rawToken)

		server.authService.RequireAPIAuth()(c)

		assert.False(t, c.IsAborted())
		assert.Equal(t, http.StatusOK, w.Code)
	})

	server.applyTokenStatsUpdate(tokenStatsUpdate{
		tokenHash:        tokenHash,
		isSuccess:        true,
		duration:         1.2,
		isStreaming:      false,
		promptTokens:     100,
		completionTokens: 200,
		costUSD:          0.01,
	})

	stored, err := server.store.GetAuthTokenByValue(context.Background(), tokenHash)
	require.NoError(t, err)
	assert.False(t, stored.IsActive)

	t.Run("token is rejected from refreshed auth cache", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
		c.Request.Header.Set("Authorization", "Bearer "+rawToken)

		server.authService.RequireAPIAuth()(c)

		assert.True(t, c.IsAborted())
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

func TestAuthTokenQuota_UnlimitedTokenStaysActiveAfterUsage(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	rawToken := "sk-ccl-quota-unlimited"
	tokenHash := model.HashToken(rawToken)

	err := server.store.CreateAuthToken(context.Background(), &model.AuthToken{
		Token:       tokenHash,
		Description: "Unlimited quota",
		IsActive:    true,
		AllChannels: true,
	})
	require.NoError(t, err)
	require.NoError(t, server.authService.ReloadAuthTokens())

	server.applyTokenStatsUpdate(tokenStatsUpdate{
		tokenHash:        tokenHash,
		isSuccess:        true,
		duration:         1.2,
		isStreaming:      false,
		promptTokens:     100,
		completionTokens: 200,
		costUSD:          999,
	})

	stored, err := server.store.GetAuthTokenByValue(context.Background(), tokenHash)
	require.NoError(t, err)
	assert.True(t, stored.IsActive)
	assert.Nil(t, stored.QuotaLimitUSD)
}

// TestRequireAPIAuth_DisabledToken 测试禁用的令牌返回 403
func TestRequireAPIAuth_DisabledToken(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()

	// 步骤1: 创建一个令牌
	requestBody := map[string]any{
		"description": "Test Token for Disable",
		"is_active":   true,
	}
	body, _ := json.Marshal(requestBody)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/admin/auth-tokens", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	server.HandleCreateAuthToken(c)
	require.Equal(t, http.StatusOK, w.Code, "创建令牌应该成功")

	var createResp struct {
		Success bool `json:"success"`
		Data    struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	rawToken := createResp.Data.Token
	tokenID := createResp.Data.ID

	// 步骤2: 验证启用的令牌可以通过认证
	t.Run("ActiveToken_ShouldPass", func(t *testing.T) {
		w2 := httptest.NewRecorder()
		c2, _ := gin.CreateTestContext(w2)
		c2.Request = httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
		c2.Request.Header.Set("Authorization", "Bearer "+rawToken)

		// 执行认证中间件
		middleware := server.authService.RequireAPIAuth()

		middleware(c2)
		c2.Next()

		// 验证通过（未被Abort）
		assert.False(t, c2.IsAborted(), "启用的令牌应该通过认证")
		assert.Equal(t, http.StatusOK, w2.Code, "启用的令牌不应返回错误状态码")
	})

	// 步骤3: 禁用令牌（需要先获取完整token再更新）
	token, err := server.store.GetAuthToken(ctx, tokenID)
	require.NoError(t, err, "获取令牌应该成功")
	token.IsActive = false
	err = server.store.UpdateAuthToken(ctx, token)
	require.NoError(t, err, "更新令牌状态应该成功")

	// 热更新认证令牌
	err = server.authService.ReloadAuthTokens()
	require.NoError(t, err, "热更新令牌应该成功")

	// 步骤4: 验证禁用的令牌返回 403
	t.Run("DisabledToken_ShouldReturn403", func(t *testing.T) {
		w3 := httptest.NewRecorder()
		c3, _ := gin.CreateTestContext(w3)
		c3.Request = httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
		c3.Request.Header.Set("Authorization", "Bearer "+rawToken)

		middleware := server.authService.RequireAPIAuth()
		middleware(c3)

		// 验证返回 403
		assert.True(t, c3.IsAborted(), "禁用的令牌应该被拒绝")
		assert.Equal(t, http.StatusForbidden, w3.Code, "禁用的令牌应该返回 403")

		// 验证错误消息（统一响应格式：{success: bool, error: string}）
		var errResp struct {
			Success bool   `json:"success"`
			Error   string `json:"error"`
		}
		require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &errResp))
		assert.False(t, errResp.Success, "success 应为 false")
		assert.Equal(t, "token disabled", errResp.Error, "错误消息应为 'token disabled'")
	})

	// 步骤5: 重新启用令牌，验证可以恢复访问
	t.Run("ReenabledToken_ShouldPass", func(t *testing.T) {
		// 获取完整token再更新
		token, err := server.store.GetAuthToken(ctx, tokenID)
		require.NoError(t, err, "获取令牌应该成功")
		token.IsActive = true
		err = server.store.UpdateAuthToken(ctx, token)
		require.NoError(t, err, "重新启用令牌应该成功")

		// 热更新认证令牌
		err = server.authService.ReloadAuthTokens()
		require.NoError(t, err, "热更新令牌应该成功")

		w4 := httptest.NewRecorder()
		c4, _ := gin.CreateTestContext(w4)
		c4.Request = httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
		c4.Request.Header.Set("Authorization", "Bearer "+rawToken)

		middleware := server.authService.RequireAPIAuth()
		middleware(c4)

		// 验证通过
		assert.False(t, c4.IsAborted(), "重新启用的令牌应该通过认证")
		assert.Equal(t, http.StatusOK, w4.Code, "重新启用的令牌不应返回错误状态码")
	})
}
