package app

import (
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

	// 默认 Social 方式
	if config.AuthType == "" {
		config.AuthType = KiroAuthMethodSocial
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
	usageLimitsURL := "https://codewhisperer.us-east-1.amazonaws.com/getUsageLimits?isEmailRequired=true&origin=AI_EDITOR&resourceType=AGENTIC_REQUEST"

	httpReq, err := http.NewRequestWithContext(ctx, "GET", usageLimitsURL, nil)
	if err != nil {
		log.Printf("[ERROR] [Kiro Email] Failed to create request: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 设置请求头
	invocationID := fmt.Sprintf("%d-%s", time.Now().UnixNano(), uuid.New().String()[:8])
	httpReq.Header.Set("Authorization", "Bearer "+req.AccessToken)
	httpReq.Header.Set("Host", "codewhisperer.us-east-1.amazonaws.com")
	httpReq.Header.Set("x-amz-user-agent", "aws-sdk-js/3.738.0 ua/2.1 os/linux lang/js md/browser api/codewhisperer m/E KiroIDE")
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

	log.Printf("[INFO] [Kiro Email] Retrieved email: %s", usageLimits.UserInfo.Email)

	RespondJSON(c, http.StatusOK, gin.H{
		"email":   usageLimits.UserInfo.Email,
		"user_id": usageLimits.UserInfo.UserID,
	})
}
