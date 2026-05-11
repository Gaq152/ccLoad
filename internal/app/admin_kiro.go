package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// HandleKiroRefresh 手动刷新 Kiro Token
// POST /admin/kiro/refresh
func (s *Server) HandleKiroRefresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
		AuthType     string `json:"auth_type"` // "Social" 或 "IdC"，默认 "Social"
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ERROR] [Kiro Refresh] Failed to parse request: %v", err)
		RespondError(c, http.StatusBadRequest, err)
		return
	}

	// 构建认证配置
	config := &KiroAuthConfig{
		RefreshToken: req.RefreshToken,
		AuthType:     req.AuthType,
		ClientID:     req.ClientID,
		ClientSecret: req.ClientSecret,
	}

	// 自动推断认证类型（与其他地方保持一致）
	if config.AuthType == "" {
		// 如果同时存在 clientId 和 clientSecret，自动推断为 IdC
		if config.ClientID != "" && config.ClientSecret != "" {
			config.AuthType = KiroAuthMethodIdC
			log.Printf("[INFO] [Kiro Refresh] 自动推断为 IdC 认证模式 (clientId=%s)", config.ClientID)
		} else {
			config.AuthType = KiroAuthMethodSocial
		}
	}

	// IdC 方式需要 client_id 和 client_secret
	if config.AuthType == KiroAuthMethodIdC {
		if config.ClientID == "" || config.ClientSecret == "" {
			log.Printf("[ERROR] [Kiro Refresh] IdC auth missing credentials")
			RespondError(c, http.StatusBadRequest, nil)
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 刷新 Token
	var tokenInfo *KiroTokenInfo
	var err error

	if config.AuthType == KiroAuthMethodIdC {
		tokenInfo, err = s.refreshKiroIdCToken(ctx, config)
	} else {
		tokenInfo, err = s.refreshKiroSocialToken(ctx, config)
	}

	if err != nil {
		log.Printf("[ERROR] [Kiro Refresh] Refresh failed: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	log.Printf("[INFO] [Kiro Refresh] Token refreshed successfully, expiresAt=%d", tokenInfo.ExpiresAt)

	RespondJSON(c, http.StatusOK, gin.H{
		"access_token": tokenInfo.AccessToken,
		"expires_at":   tokenInfo.ExpiresAt,
	})
}

// HandleKiroGetEmail 获取 Kiro 用户邮箱
// POST /admin/kiro/email
func (s *Server) HandleKiroGetEmail(c *gin.Context) {
	var req struct {
		AccessToken string `json:"access_token" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ERROR] [Kiro Email] Failed to parse request: %v", err)
		RespondError(c, http.StatusBadRequest, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 调用 AWS CodeWhisperer getUsageLimits API
	usageLimitsURL := "https://q.us-east-1.amazonaws.com/getUsageLimits?isEmailRequired=true&origin=AI_EDITOR&resourceType=AGENTIC_REQUEST"

	httpReq, err := http.NewRequestWithContext(ctx, "GET", usageLimitsURL, nil)
	if err != nil {
		log.Printf("[ERROR] [Kiro Email] Failed to create request: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 设置请求头（参考 kiro.rs: codewhispererruntime 用于 getUsageLimits）
	invocationID := fmt.Sprintf("%d-%s", time.Now().UnixNano(), uuid.New().String()[:8])
	httpReq.Header.Set("Authorization", "Bearer "+req.AccessToken)
	httpReq.Header.Set("Host", "q.us-east-1.amazonaws.com")
	httpReq.Header.Set("x-amz-user-agent", "aws-sdk-js/1.0.34 KiroIDE")
	httpReq.Header.Set("amz-sdk-invocation-id", invocationID)
	httpReq.Header.Set("amz-sdk-request", "attempt=1; max=1")
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")

	// 发送请求
	resp, err := s.client.Do(httpReq)
	if err != nil {
		log.Printf("[ERROR] [Kiro Email] Request failed: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[ERROR] [Kiro Email] Failed to read response: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[ERROR] [Kiro Email] API failed (status=%d): %s", resp.StatusCode, string(respBody))
		RespondError(c, http.StatusInternalServerError, fmt.Errorf("usage limits API failed: %s", string(respBody)))
		return
	}

	// 解析响应
	var usageLimits struct {
		UserInfo struct {
			Email  string `json:"email"`
			UserID string `json:"userId"`
		} `json:"userInfo"`
		SubscriptionInfo struct {
			SubscriptionTitle string `json:"subscriptionTitle"`
		} `json:"subscriptionInfo"`
	}

	if err := sonic.Unmarshal(respBody, &usageLimits); err != nil {
		log.Printf("[ERROR] [Kiro Email] Failed to parse response: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	if usageLimits.UserInfo.Email == "" {
		log.Printf("[WARN] [Kiro Email] No email in response")
		RespondError(c, http.StatusNotFound, fmt.Errorf("email not found"))
		return
	}

	log.Printf("[INFO] [Kiro Email] Retrieved email: %s, subscription: %s",
		usageLimits.UserInfo.Email, usageLimits.SubscriptionInfo.SubscriptionTitle)

	RespondJSON(c, http.StatusOK, gin.H{
		"email":              usageLimits.UserInfo.Email,
		"user_id":            usageLimits.UserInfo.UserID,
		"subscription_title": usageLimits.SubscriptionInfo.SubscriptionTitle,
	})
}

// HandleKiroIdcRegisterClient 代理 AWS SSO OIDC 客户端注册
// POST /admin/kiro/idc/register
// 前端提供 start_url 和 redirect_uri，本接口向 AWS OIDC 注册公共客户端，
// 返回 clientId、clientSecret 等信息供后续授权码流程使用
func (s *Server) HandleKiroIdcRegisterClient(c *gin.Context) {
	var req struct {
		StartURL    string `json:"start_url" binding:"required"`
		Region      string `json:"region"`
		RedirectURI string `json:"redirect_uri" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ERROR] [Kiro IdC] 注册客户端参数解析失败: %v", err)
		RespondError(c, http.StatusBadRequest, err)
		return
	}

	// 默认区域
	if req.Region == "" {
		req.Region = "us-east-1"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 构建注册请求体
	registerBody := map[string]any{
		"clientName":   "Kiro IDE",
		"clientType":   "public",
		"scopes":       []string{"codewhisperer:completions", "codewhisperer:analysis", "codewhisperer:conversations", "codewhisperer:transformations", "codewhisperer:taskassist"},
		"grantTypes":   []string{"authorization_code", "refresh_token"},
		"redirectUris": []string{req.RedirectURI},
		"issuerUrl":    req.StartURL,
	}
	bodyBytes, err := sonic.Marshal(registerBody)
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "序列化请求失败: "+err.Error())
		return
	}

	// 构建注册 URL
	registerURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/client/register", req.Region)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", registerURL, bytes.NewReader(bodyBytes))
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "创建请求失败: "+err.Error())
		return
	}
	host := fmt.Sprintf("oidc.%s.amazonaws.com", req.Region)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Host", host)
	httpReq.Header.Set("x-amz-user-agent", "aws-sdk-js/3.738.0 ua/2.1 os/linux lang/js md/browser api/sso-oidc#3.738.0 m/E KiroIDE")
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")
	httpReq.Header.Set("Accept", "*/*")
	httpReq.Header.Set("User-Agent", "node")

	// 发送请求
	resp, err := s.client.Do(httpReq)
	if err != nil {
		log.Printf("[ERROR] [Kiro IdC] 注册客户端请求失败: %v", err)
		RespondErrorMsg(c, http.StatusInternalServerError, "注册客户端请求失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "读取响应失败: "+err.Error())
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[ERROR] [Kiro IdC] 客户端注册失败 (status=%d): %s", resp.StatusCode, string(respBody))
		RespondErrorWithData(c, http.StatusOK, fmt.Sprintf("客户端注册失败 (%d): %s", resp.StatusCode, string(respBody)), gin.H{
			"status_code": resp.StatusCode,
		})
		return
	}

	// 解析响应
	var registerResp struct {
		ClientId              string `json:"clientId"`
		ClientSecret          string `json:"clientSecret"`
		AuthorizationEndpoint string `json:"authorizationEndpoint"`
		TokenEndpoint         string `json:"tokenEndpoint"`
	}
	if err := sonic.Unmarshal(respBody, &registerResp); err != nil {
		log.Printf("[ERROR] [Kiro IdC] 解析注册响应失败: %v, body=%s", err, string(respBody))
		RespondErrorMsg(c, http.StatusInternalServerError, "解析注册响应失败: "+err.Error())
		return
	}

	log.Printf("[INFO] [Kiro IdC] 客户端注册成功, clientId=%s", registerResp.ClientId)

	RespondJSON(c, http.StatusOK, gin.H{
		"clientId":              registerResp.ClientId,
		"clientSecret":          registerResp.ClientSecret,
		"authorizationEndpoint": registerResp.AuthorizationEndpoint,
		"tokenEndpoint":         registerResp.TokenEndpoint,
	})
}

// HandleKiroIdcTokenExchange 使用授权码交换 Kiro IdC OAuth Token
// POST /admin/kiro/idc/exchange
// 前端完成 IdC 登录后，使用授权码通过此接口换取 access_token + refresh_token
func (s *Server) HandleKiroIdcTokenExchange(c *gin.Context) {
	var req struct {
		Code         string `json:"code" binding:"required"`
		CodeVerifier string `json:"code_verifier" binding:"required"`
		RedirectURI  string `json:"redirect_uri" binding:"required"`
		ClientID     string `json:"client_id" binding:"required"`
		ClientSecret string `json:"client_secret" binding:"required"`
		Region       string `json:"region"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[ERROR] [Kiro IdC] Token 交换参数解析失败: %v", err)
		RespondError(c, http.StatusBadRequest, err)
		return
	}

	// 默认区域
	if req.Region == "" {
		req.Region = "us-east-1"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 构建请求体（camelCase 字段名，与 IdC 刷新接口保持一致）
	exchangeBody := map[string]string{
		"clientId":     req.ClientID,
		"clientSecret": req.ClientSecret,
		"grantType":    "authorization_code",
		"code":         req.Code,
		"codeVerifier": req.CodeVerifier,
		"redirectUri":  req.RedirectURI,
	}
	bodyBytes, err := sonic.Marshal(exchangeBody)
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "序列化请求失败: "+err.Error())
		return
	}

	// 构建 Token URL
	tokenURL := fmt.Sprintf("https://oidc.%s.amazonaws.com/token", req.Region)
	host := fmt.Sprintf("oidc.%s.amazonaws.com", req.Region)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", tokenURL, bytes.NewReader(bodyBytes))
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "创建请求失败: "+err.Error())
		return
	}

	// 设置请求头（与 refreshKiroIdCToken 保持一致）
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Host", host)
	httpReq.Header.Set("x-amz-user-agent", "aws-sdk-js/3.738.0 ua/2.1 os/linux lang/js md/browser api/sso-oidc#3.738.0 m/E KiroIDE")
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")
	httpReq.Header.Set("Accept", "*/*")
	httpReq.Header.Set("User-Agent", "node")

	// 发送请求
	resp, err := s.client.Do(httpReq)
	if err != nil {
		log.Printf("[ERROR] [Kiro IdC] Token 交换请求失败: %v", err)
		RespondErrorMsg(c, http.StatusInternalServerError, "Token 交换请求失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "读取响应失败: "+err.Error())
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[ERROR] [Kiro IdC] Token 交换失败 (status=%d): %s", resp.StatusCode, string(respBody))
		RespondErrorWithData(c, http.StatusOK, fmt.Sprintf("Token 交换失败 (%d): %s", resp.StatusCode, string(respBody)), gin.H{
			"status_code": resp.StatusCode,
		})
		return
	}

	// 解析响应
	var tokenResp struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		TokenType    string `json:"tokenType"`
		IdToken      string `json:"idToken,omitempty"`
	}
	if err := sonic.Unmarshal(respBody, &tokenResp); err != nil {
		log.Printf("[ERROR] [Kiro IdC] 解析 Token 响应失败: %v, body=%s", err, string(respBody))
		RespondErrorMsg(c, http.StatusInternalServerError, "解析 Token 响应失败: "+err.Error())
		return
	}

	if tokenResp.AccessToken == "" || tokenResp.RefreshToken == "" {
		RespondErrorMsg(c, http.StatusInternalServerError, "响应缺少 accessToken 或 refreshToken")
		return
	}

	log.Printf("[INFO] [Kiro IdC] Token 交换成功, expiresIn=%d", tokenResp.ExpiresIn)

	// 计算过期时间（毫秒时间戳，与 Social OAuth 交换保持一致）
	expiresAt := time.Now().UnixMilli() + tokenResp.ExpiresIn*1000

	RespondJSON(c, http.StatusOK, gin.H{
		"accessToken":  tokenResp.AccessToken,
		"refreshToken": tokenResp.RefreshToken,
		"expiresIn":    tokenResp.ExpiresIn,
		"expiresAt":    expiresAt,
		"tokenType":    tokenResp.TokenType,
		"idToken":      tokenResp.IdToken,
	})
}

// HandleKiroGenerateFingerprint 生成新的随机设备指纹
// GET /admin/kiro/fingerprint/generate
func (s *Server) HandleKiroGenerateFingerprint(c *gin.Context) {
	fm := GetFingerprintManager()
	fp, err := fm.GenerateFingerprint()
	if err != nil {
		log.Printf("[ERROR] [Kiro Fingerprint] 生成失败: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	fpJSON, err := fp.ToJSON()
	if err != nil {
		log.Printf("[ERROR] [Kiro Fingerprint] 序列化失败: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	log.Printf("[INFO] [Kiro Fingerprint] 生成成功: %s", fp.GetSummary())

	RespondJSON(c, http.StatusOK, gin.H{
		"fingerprint": fpJSON,
		"summary":     fp.GetSummary(),
	})
}
