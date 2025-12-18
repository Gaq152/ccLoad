package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"
)

// ============================================================================
// Codex OAuth Token 管理
// ============================================================================

// CodexOAuthToken Codex OAuth Token 结构
// 存储在 api_keys.api_key 字段中，JSON 格式
type CodexOAuthToken struct {
	Type         string `json:"type"`          // 固定为 "oauth"
	AccessToken  string `json:"access_token"`  // JWT 格式的访问令牌
	RefreshToken string `json:"refresh_token"` // 刷新令牌
	ExpiresAt    int64  `json:"expires_at"`    // 过期时间 Unix 时间戳（秒）
	AccountID    string `json:"account_id"`    // 从 JWT 提取的 chatgpt_account_id
}

// Codex OAuth 配置常量
const (
	// Codex CLI 官方客户端 ID
	CodexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

	// done-hub 使用的客户端 ID（用于 refresh token）
	CodexRefreshClientID = "pdlLIX2Y72MIl2rhLhTE9VV9bN905kBh"

	// Token 刷新 URL（注意：与首次授权 URL 不同）
	CodexRefreshTokenURL = "https://auth0.openai.com/oauth/token"

	// Codex API 端点
	CodexAPIEndpoint = "https://chatgpt.com/backend-api/codex/responses"

	// Token 提前刷新时间（过期前 5 分钟刷新）
	TokenRefreshBuffer = 5 * 60 // 秒
)

// ParseAPIKeyOrOAuth 解析 API Key 或 OAuth Token
// 返回: (apiKey string, oauthToken *CodexOAuthToken, isOAuth bool)
//   - 如果是普通 API Key，返回 (apiKey, nil, false)
//   - 如果是 OAuth Token，返回 (accessToken, token, true)
func ParseAPIKeyOrOAuth(raw string) (string, *CodexOAuthToken, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil, false
	}

	// 检查是否以 { 开头（JSON 格式）
	if !strings.HasPrefix(raw, "{") {
		// 普通 API Key
		return raw, nil, false
	}

	// 尝试解析为 OAuth Token
	var token CodexOAuthToken
	if err := sonic.Unmarshal([]byte(raw), &token); err != nil {
		// JSON 解析失败，当作普通 Key 返回
		return raw, nil, false
	}

	// 验证是否为有效的 OAuth Token
	if token.Type != "oauth" || token.AccessToken == "" {
		// 不是有效的 OAuth Token 格式
		return raw, nil, false
	}

	return token.AccessToken, &token, true
}

// IsTokenExpiringSoon 检查 Token 是否即将过期
func (t *CodexOAuthToken) IsTokenExpiringSoon() bool {
	if t.ExpiresAt == 0 {
		// 没有过期时间，假设不需要刷新
		return false
	}
	return time.Now().Unix()+TokenRefreshBuffer > t.ExpiresAt
}

// IsTokenExpired 检查 Token 是否已过期
func (t *CodexOAuthToken) IsTokenExpired() bool {
	if t.ExpiresAt == 0 {
		return false
	}
	return time.Now().Unix() > t.ExpiresAt
}

// RefreshCodexTokenIfNeeded 检查并刷新 Codex Token（如果需要）
// 返回: 刷新后的 access_token，如果不需要刷新则返回原 token
func (s *Server) RefreshCodexTokenIfNeeded(
	ctx context.Context,
	channelID int64,
	keyIndex int,
	token *CodexOAuthToken,
) (string, *CodexOAuthToken, error) {
	// 检查是否需要刷新
	if !token.IsTokenExpiringSoon() {
		return token.AccessToken, token, nil
	}

	// 没有 refresh_token，无法刷新
	if token.RefreshToken == "" {
		if token.IsTokenExpired() {
			return "", nil, fmt.Errorf("codex token expired and no refresh_token available")
		}
		// Token 即将过期但还能用
		log.Printf("[WARN] Codex token expiring soon but no refresh_token (channel=%d, keyIndex=%d)", channelID, keyIndex)
		return token.AccessToken, token, nil
	}

	// 执行刷新
	log.Printf("[INFO] Refreshing Codex token (channel=%d, keyIndex=%d)", channelID, keyIndex)
	newToken, err := s.refreshCodexToken(ctx, token)
	if err != nil {
		// 刷新失败
		if token.IsTokenExpired() {
			return "", nil, fmt.Errorf("codex token refresh failed: %w", err)
		}
		// Token 还没过期，继续使用旧的
		log.Printf("[WARN] Codex token refresh failed, using existing token: %v", err)
		return token.AccessToken, token, nil
	}

	// 更新数据库
	newJSON, err := sonic.Marshal(newToken)
	if err != nil {
		log.Printf("[WARN] Failed to marshal new Codex token: %v", err)
		return newToken.AccessToken, newToken, nil
	}

	// 获取现有 API Key 记录
	existingKey, err := s.store.GetAPIKey(ctx, channelID, keyIndex)
	if err != nil {
		log.Printf("[WARN] Failed to get existing API Key for update: %v", err)
	} else {
		// 更新 API Key 值
		existingKey.APIKey = string(newJSON)
		if err := s.store.UpdateAPIKey(ctx, existingKey); err != nil {
			log.Printf("[WARN] Failed to update Codex token in database: %v", err)
		} else {
			// 失效缓存
			s.InvalidateAPIKeysCache(channelID)
			log.Printf("[INFO] Codex token refreshed and saved (channel=%d, keyIndex=%d)", channelID, keyIndex)
		}
	}

	return newToken.AccessToken, newToken, nil
}

// refreshCodexToken 执行实际的 Token 刷新
func (s *Server) refreshCodexToken(ctx context.Context, token *CodexOAuthToken) (*CodexOAuthToken, error) {
	// 构建刷新请求
	body := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {CodexRefreshClientID},
		"refresh_token": {token.RefreshToken},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", CodexRefreshTokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// 发送请求
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send refresh request: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read refresh response: %w", err)
	}

	// 检查状态码
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("refresh token failed (status=%d): %s", resp.StatusCode, string(respBody))
	}

	// 解析响应
	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		TokenType    string `json:"token_type"`
	}
	if err := sonic.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode refresh response: %w", err)
	}

	if result.AccessToken == "" {
		return nil, fmt.Errorf("refresh response missing access_token")
	}

	// 构建新的 Token
	newToken := &CodexOAuthToken{
		Type:         "oauth",
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresAt:    time.Now().Unix() + result.ExpiresIn,
		AccountID:    ExtractAccountIDFromJWT(result.AccessToken),
	}

	// 如果新响应没有 refresh_token，保留旧的
	if newToken.RefreshToken == "" {
		newToken.RefreshToken = token.RefreshToken
	}

	// 如果无法提取 account_id，保留旧的
	if newToken.AccountID == "" {
		newToken.AccountID = token.AccountID
	}

	return newToken, nil
}

// ExtractAccountIDFromJWT 从 JWT access_token 中提取 chatgpt_account_id
// JWT 结构: header.payload.signature
// payload 中包含: {"https://api.openai.com/auth": {"chatgpt_account_id": "xxx"}}
func ExtractAccountIDFromJWT(accessToken string) string {
	if accessToken == "" {
		return ""
	}

	// 分割 JWT
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return ""
	}

	// 解码 payload（第二部分）
	payload := parts[1]
	// JWT 使用 URL-safe base64，需要处理填充
	payload = strings.ReplaceAll(payload, "-", "+")
	payload = strings.ReplaceAll(payload, "_", "/")
	// 添加填充
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}

	// 解析 JSON
	var claims map[string]any
	if err := sonic.Unmarshal(decoded, &claims); err != nil {
		return ""
	}

	// 提取 account_id
	// 路径: claims["https://api.openai.com/auth"]["chatgpt_account_id"]
	auth, ok := claims["https://api.openai.com/auth"].(map[string]any)
	if !ok {
		return ""
	}

	accountID, _ := auth["chatgpt_account_id"].(string)
	return accountID
}

// SerializeCodexToken 将 CodexOAuthToken 序列化为 JSON 字符串
func SerializeCodexToken(token *CodexOAuthToken) (string, error) {
	data, err := sonic.Marshal(token)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
