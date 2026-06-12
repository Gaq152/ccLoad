package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Cloudflare Turnstile 服务端校验接口
const turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// turnstileHTTPClient 校验请求专用客户端（独立超时，避免阻塞登录流程）
var turnstileHTTPClient = &http.Client{Timeout: 10 * time.Second}

// turnstileConfig 返回 Turnstile 生效状态及密钥对
// 业务规则：开关打开但密钥未配齐时视为未启用（fail-open），
// 避免管理员误开启后因无法渲染/校验组件而把自己锁在门外
func (s *AuthService) turnstileConfig() (enabled bool, siteKey, secretKey string) {
	if s.configService == nil {
		return false, "", ""
	}
	if !s.configService.GetBool("turnstile_enabled", false) {
		return false, "", ""
	}
	siteKey = strings.TrimSpace(s.configService.GetString("turnstile_site_key", ""))
	secretKey = strings.TrimSpace(s.configService.GetString("turnstile_secret_key", ""))
	if siteKey == "" || secretKey == "" {
		return false, "", ""
	}
	return true, siteKey, secretKey
}

// verifyTurnstileToken 调用 Cloudflare siteverify 接口校验前端提交的验证令牌
func verifyTurnstileToken(ctx context.Context, secretKey, token, remoteIP string) error {
	form := url.Values{
		"secret":   {secretKey},
		"response": {token},
	}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, turnstileVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build turnstile request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := turnstileHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile verify request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode turnstile response: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("turnstile verify failed: %s", strings.Join(result.ErrorCodes, ","))
	}
	return nil
}

// HandleLoginConfig 登录页公开配置
// GET /public/login-config
// 仅暴露 Site Key（公开密钥），Secret Key 不出服务端
func (s *Server) HandleLoginConfig(c *gin.Context) {
	enabled, siteKey, _ := s.authService.turnstileConfig()
	RespondJSON(c, http.StatusOK, gin.H{
		"turnstile_enabled":  enabled,
		"turnstile_site_key": siteKey,
		"setup_required":     !s.authService.HasPassword(), // 未初始化时前端跳转引导页（服务端302的兜底）
	})
}
