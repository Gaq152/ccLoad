package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
)

// ============================================================================
// Kiro OAuth Token 管理
// 参考: https://github.com/nineyuanz/kiro2api/blob/main/auth/refresh.go
// ============================================================================

// ParseKiroAuthConfig 解析 Kiro 认证配置
// 输入: JSON 格式的配置字符串
// 返回: 解析后的配置，如果解析失败返回 nil
func ParseKiroAuthConfig(raw string) *KiroAuthConfig {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	// 必须是 JSON 格式
	if !strings.HasPrefix(raw, "{") {
		return nil
	}

	var config KiroAuthConfig
	if err := sonic.Unmarshal([]byte(raw), &config); err != nil {
		return nil
	}

	// 验证必要字段
	if config.RefreshToken == "" {
		return nil
	}

	// 设置默认认证类型
	if config.AuthType == "" {
		config.AuthType = KiroAuthMethodSocial
	}

	// IdC 方式需要额外字段
	if config.AuthType == KiroAuthMethodIdC {
		if config.ClientID == "" || config.ClientSecret == "" {
			return nil
		}
	}

	return &config
}

// IsKiroTokenExpiringSoon 检查 Kiro Token 是否即将过期
func IsKiroTokenExpiringSoon(expiresAt int64) bool {
	if expiresAt == 0 {
		return true // 没有过期时间，需要刷新
	}
	return time.Now().UnixMilli()+KiroTokenRefreshBuffer*1000 > expiresAt
}

// IsKiroTokenExpired 检查 Kiro Token 是否已过期
func IsKiroTokenExpired(expiresAt int64) bool {
	if expiresAt == 0 {
		return true
	}
	return time.Now().UnixMilli() > expiresAt
}

// RefreshKiroTokenIfNeeded 检查并刷新 Kiro Token（如果需要）
// 返回: 刷新后的 access_token 和过期时间
func (s *Server) RefreshKiroTokenIfNeeded(
	ctx context.Context,
	channelID int64,
	keyIndex int,
	config *KiroAuthConfig,
	currentAccessToken string,
	currentExpiresAt int64,
) (string, int64, error) {
	// 检查是否需要刷新
	if !IsKiroTokenExpiringSoon(currentExpiresAt) && currentAccessToken != "" {
		return currentAccessToken, currentExpiresAt, nil
	}

	// 执行刷新
	log.Printf("[INFO] [Kiro] 刷新 Token (channel=%d, keyIndex=%d, authType=%s)", channelID, keyIndex, config.AuthType)

	var tokenInfo *KiroTokenInfo
	var err error

	switch config.AuthType {
	case KiroAuthMethodIdC:
		tokenInfo, err = s.refreshKiroIdCToken(ctx, config)
	default:
		tokenInfo, err = s.refreshKiroSocialToken(ctx, config)
	}

	if err != nil {
		// 刷新失败
		if IsKiroTokenExpired(currentExpiresAt) {
			return "", 0, fmt.Errorf("kiro token refresh failed: %w", err)
		}
		// Token 还没过期，继续使用旧的
		log.Printf("[WARN] [Kiro] Token 刷新失败，使用现有 Token: %v", err)
		return currentAccessToken, currentExpiresAt, nil
	}

	// 更新数据库
	existingKey, err := s.store.GetAPIKey(ctx, channelID, keyIndex)
	if err != nil {
		log.Printf("[WARN] [Kiro] 获取 API Key 失败: %v", err)
	} else {
		existingKey.AccessToken = tokenInfo.AccessToken
		existingKey.TokenExpiresAt = tokenInfo.ExpiresAt
		if err := s.store.UpdateAPIKey(ctx, existingKey); err != nil {
			log.Printf("[WARN] [Kiro] 更新 Token 到数据库失败: %v", err)
		} else {
			s.InvalidateAPIKeysCache(channelID)
			log.Printf("[INFO] [Kiro] Token 刷新成功并保存 (channel=%d, keyIndex=%d)", channelID, keyIndex)
		}
	}

	return tokenInfo.AccessToken, tokenInfo.ExpiresAt, nil
}

// refreshKiroSocialToken 刷新 Social 方式的 Token
func (s *Server) refreshKiroSocialToken(ctx context.Context, config *KiroAuthConfig) (*KiroTokenInfo, error) {
	// 构建请求体
	reqBody := KiroSocialRefreshRequest{
		RefreshToken: config.RefreshToken,
	}
	bodyBytes, err := sonic.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("serialize request: %w", err)
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, "POST", KiroRefreshTokenURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// 检查状态码
	if resp.StatusCode != http.StatusOK {
		log.Printf("[ERROR] [Kiro Social] Refresh failed (status=%d): %s", resp.StatusCode, string(respBody))
		return nil, fmt.Errorf("refresh failed (status=%d): %s", resp.StatusCode, string(respBody))
	}

	// 解析响应
	var result KiroSocialRefreshResponse
	if err := sonic.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if result.AccessToken == "" {
		return nil, fmt.Errorf("response missing accessToken")
	}

	return &KiroTokenInfo{
		AccessToken: result.AccessToken,
		ExpiresAt:   time.Now().UnixMilli() + result.ExpiresIn*1000, // 转为毫秒
	}, nil
}

// refreshKiroIdCToken 刷新 IdC 方式的 Token
func (s *Server) refreshKiroIdCToken(ctx context.Context, config *KiroAuthConfig) (*KiroTokenInfo, error) {
	// 构建请求体（JSON 格式，使用 camelCase 字段名）
	reqBody := KiroIdCRefreshRequest{
		ClientId:     config.ClientID,
		ClientSecret: config.ClientSecret,
		GrantType:    "refresh_token",
		RefreshToken: config.RefreshToken,
	}

	jsonData, err := sonic.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, "POST", KiroIdCRefreshTokenURL, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Host", "oidc.us-east-1.amazonaws.com")

	// 添加 AWS SDK 风格的请求头（参考 kiro2api）
	req.Header.Set("x-amz-user-agent", "aws-sdk-js/3.738.0 ua/2.1 os/linux lang/js md/browser api/sso-oidc#3.738.0 m/E KiroIDE")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "node")

	// 发送请求
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// 检查状态码
	if resp.StatusCode != http.StatusOK {
		log.Printf("[ERROR] [Kiro IdC] Refresh failed (status=%d): %s", resp.StatusCode, string(respBody))
		return nil, fmt.Errorf("refresh failed (status=%d): %s", resp.StatusCode, string(respBody))
	}

	// 解析响应
	var result KiroIdCRefreshResponse
	if err := sonic.Unmarshal(respBody, &result); err != nil {
		log.Printf("[ERROR] [Kiro IdC] Failed to decode response: %v", err)
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if result.AccessToken == "" {
		return nil, fmt.Errorf("response missing access_token")
	}

	return &KiroTokenInfo{
		AccessToken: result.AccessToken,
		ExpiresAt:   time.Now().UnixMilli() + result.ExpiresIn*1000, // 转为毫秒
	}, nil
}

// ============================================================================
// Kiro 请求头构建
// ============================================================================

// 默认设备指纹（降级时使用，来自真实 Kiro IDE）
const KiroDefaultDeviceFingerprint = "0823f56eae74294cd89009c31e1b6828034938a892a7f4ca1409ef8e3dd1c846"

// BuildKiroRequestHeaders 构建 Kiro API 请求头
// deviceFingerprint: 设备指纹，为空时使用默认值
func BuildKiroRequestHeaders(accessToken string, isStreaming bool, deviceFingerprintJSON string) http.Header {
	headers := make(http.Header)

	headers.Set("Authorization", "Bearer "+accessToken)
	headers.Set("Content-Type", "application/json")

	if isStreaming {
		headers.Set("Accept", "text/event-stream")
	} else {
		headers.Set("Accept", "*/*")
	}

	// AWS CodeWhisperer 必需的请求头（参考 kiro2api）
	headers.Set("x-amzn-kiro-agent-mode", "vibe")
	headers.Set("x-amzn-codewhisperer-optout", "true")
	headers.Set("amz-sdk-invocation-id", uuid.New().String())
	headers.Set("amz-sdk-request", "attempt=1; max=3")

	// 解析设备指纹
	fm := GetFingerprintManager()
	fp, err := fm.ParseFingerprint(deviceFingerprintJSON)
	if err != nil || fp == nil {
		// 解析失败或为空，生成新的指纹
		fp, err = fm.GenerateFingerprint()
		if err != nil {
			// 生成失败，使用默认值（降级）
			log.Printf("[WARN] [Kiro] Failed to generate fingerprint: %v, using defaults", err)
			headers.Set("x-amz-user-agent", "aws-sdk-js/1.0.27 KiroIDE-0.8.0-"+KiroDefaultDeviceFingerprint)
			headers.Set("User-Agent", "aws-sdk-js/1.0.27 ua/2.1 os/darwin#25.0.0 lang/js md/nodejs#20.16.0 api/codewhispererstreaming#1.0.27 m/E KiroIDE-0.8.0-"+KiroDefaultDeviceFingerprint)
			headers.Set("Accept-Language", "en-US,en;q=0.9")
			headers.Set("Accept-Encoding", "gzip, deflate, br")
			headers.Set("Connection", "close")
			return headers
		}
	}

	// 应用完整的设备指纹
	headers.Set("x-amz-user-agent", fp.BuildAmzUserAgent())
	headers.Set("User-Agent", fp.BuildUserAgent())
	headers.Set("Accept-Language", fp.AcceptLanguage)
	headers.Set("Accept-Encoding", fp.AcceptEncoding)
	headers.Set("Connection", "close")

	return headers
}

// GetKiroModelId 获取 Kiro 模型 ID
// 如果模型不在映射表中，返回空字符串
func GetKiroModelId(anthropicModel string) string {
	if modelId, ok := KiroModelMap[anthropicModel]; ok {
		return modelId
	}
	return ""
}

// IsKiroSupportedModel 检查模型是否被 Kiro 支持
func IsKiroSupportedModel(anthropicModel string) bool {
	return GetKiroModelId(anthropicModel) != ""
}
