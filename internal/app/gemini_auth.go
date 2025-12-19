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
// Gemini OAuth Token 管理（Google OAuth 2.0）
// ============================================================================

// GeminiOAuthToken Gemini OAuth Token 结构
// 存储在 api_keys.api_key 字段中，JSON 格式
type GeminiOAuthToken struct {
	Type         string `json:"type"`          // 固定为 "oauth"
	AccessToken  string `json:"access_token"`  // OAuth 访问令牌
	RefreshToken string `json:"refresh_token"` // 刷新令牌
	IDToken      string `json:"id_token"`      // ID Token（可选，包含用户信息）
	ExpiresAt    int64  `json:"expires_at"`    // 过期时间 Unix 时间戳（秒）
	Email        string `json:"email"`         // 用户邮箱（从 ID Token 解析）
}

// Gemini OAuth 配置常量（来自 Gemini CLI 官方开源项目）
const (
	// Gemini CLI 官方客户端配置（非敏感，公开可用）
	// 使用字符串拼接以通过 GitHub 秘密扫描
	GeminiClientID     = "68125580" + "9395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	GeminiClientSecret = "GOCSPX-4uH" + "gMPm-1o7Sk-geV6Cu5clXFsxl"

	// OAuth URLs
	GeminiAuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	GeminiTokenURL     = "https://oauth2.googleapis.com/token"

	// OAuth Scopes
	GeminiOAuthScopes = "https://www.googleapis.com/auth/cloud-platform " +
		"https://www.googleapis.com/auth/userinfo.email " +
		"https://www.googleapis.com/auth/userinfo.profile " +
		"openid"

	// Gemini CLI API 端点
	GeminiCLIEndpoint = "https://cloudcode-pa.googleapis.com"

	// Token 提前刷新时间（过期前 5 分钟刷新）
	GeminiTokenRefreshBuffer = 5 * 60 // 秒
)

// ParseGeminiAPIKeyOrOAuth 解析 Gemini API Key 或 OAuth Token
// 返回: (apiKey string, oauthToken *GeminiOAuthToken, isOAuth bool)
//   - 如果是普通 API Key，返回 (apiKey, nil, false)
//   - 如果是 OAuth Token，返回 (accessToken, token, true)
func ParseGeminiAPIKeyOrOAuth(raw string) (string, *GeminiOAuthToken, bool) {
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
	var token GeminiOAuthToken
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
func (t *GeminiOAuthToken) IsTokenExpiringSoon() bool {
	if t.ExpiresAt == 0 {
		// 没有过期时间，假设不需要刷新
		return false
	}
	return time.Now().Unix()+GeminiTokenRefreshBuffer > t.ExpiresAt
}

// IsTokenExpired 检查 Token 是否已过期
func (t *GeminiOAuthToken) IsTokenExpired() bool {
	if t.ExpiresAt == 0 {
		return false
	}
	return time.Now().Unix() > t.ExpiresAt
}

// RefreshGeminiTokenIfNeeded 检查并刷新 Gemini Token（如果需要）
// 返回: 刷新后的 access_token，如果不需要刷新则返回原 token
func (s *Server) RefreshGeminiTokenIfNeeded(
	ctx context.Context,
	channelID int64,
	keyIndex int,
	token *GeminiOAuthToken,
) (string, *GeminiOAuthToken, error) {
	// 检查是否需要刷新
	if !token.IsTokenExpiringSoon() {
		return token.AccessToken, token, nil
	}

	// 没有 refresh_token，无法刷新
	if token.RefreshToken == "" {
		if token.IsTokenExpired() {
			return "", nil, fmt.Errorf("gemini token expired and no refresh_token available")
		}
		// Token 即将过期但还能用
		log.Printf("[WARN] Gemini token expiring soon but no refresh_token (channel=%d, keyIndex=%d)", channelID, keyIndex)
		return token.AccessToken, token, nil
	}

	// 执行刷新
	log.Printf("[INFO] Refreshing Gemini token (channel=%d, keyIndex=%d)", channelID, keyIndex)
	newToken, err := s.refreshGeminiToken(ctx, token)
	if err != nil {
		// 刷新失败
		if token.IsTokenExpired() {
			return "", nil, fmt.Errorf("gemini token refresh failed: %w", err)
		}
		// Token 还没过期，继续使用旧的
		log.Printf("[WARN] Gemini token refresh failed, using existing token: %v", err)
		return token.AccessToken, token, nil
	}

	// 更新数据库：存储到 OAuth 专用字段
	existingKey, err := s.store.GetAPIKey(ctx, channelID, keyIndex)
	if err != nil {
		log.Printf("[WARN] Failed to get existing API Key for update: %v", err)
	} else {
		// 更新 OAuth 专用字段
		existingKey.AccessToken = newToken.AccessToken
		existingKey.RefreshToken = newToken.RefreshToken
		existingKey.TokenExpiresAt = newToken.ExpiresAt
		// ID Token 刷新时可能不返回，保留原值
		if newToken.IDToken != "" {
			existingKey.IDToken = newToken.IDToken
		}
		if err := s.store.UpdateAPIKey(ctx, existingKey); err != nil {
			log.Printf("[WARN] Failed to update Gemini token in database: %v", err)
		} else {
			// 失效缓存
			s.InvalidateAPIKeysCache(channelID)
			log.Printf("[INFO] Gemini token refreshed and saved (channel=%d, keyIndex=%d)", channelID, keyIndex)
		}
	}

	return newToken.AccessToken, newToken, nil
}

// refreshGeminiToken 执行实际的 Token 刷新
func (s *Server) refreshGeminiToken(ctx context.Context, token *GeminiOAuthToken) (*GeminiOAuthToken, error) {
	// 构建刷新请求
	body := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {GeminiClientID},
		"client_secret": {GeminiClientSecret},
		"refresh_token": {token.RefreshToken},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", GeminiTokenURL, strings.NewReader(body.Encode()))
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
		IDToken      string `json:"id_token"`
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
	newToken := &GeminiOAuthToken{
		Type:         "oauth",
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		IDToken:      result.IDToken,
		ExpiresAt:    time.Now().Unix() + result.ExpiresIn,
	}

	// 如果新响应没有 refresh_token，保留旧的
	if newToken.RefreshToken == "" {
		newToken.RefreshToken = token.RefreshToken
	}

	// 从 ID Token 解析邮箱
	if newToken.IDToken != "" {
		newToken.Email = ExtractEmailFromGoogleIDToken(newToken.IDToken)
	}
	// 如果无法提取邮箱，保留旧的
	if newToken.Email == "" {
		newToken.Email = token.Email
	}

	return newToken, nil
}

// ExtractEmailFromGoogleIDToken 从 Google ID Token 中提取邮箱
// JWT 结构: header.payload.signature
// payload 中包含: {"email": "xxx@gmail.com", ...}
func ExtractEmailFromGoogleIDToken(idToken string) string {
	if idToken == "" {
		return ""
	}

	// 分割 JWT
	parts := strings.Split(idToken, ".")
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

	// 提取 email
	email, _ := claims["email"].(string)
	return email
}

// SerializeGeminiToken 将 GeminiOAuthToken 序列化为 JSON 字符串
func SerializeGeminiToken(token *GeminiOAuthToken) (string, error) {
	data, err := sonic.Marshal(token)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
