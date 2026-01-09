package app

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
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
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	RespondJSON(c, http.StatusOK, gin.H{
		"access_token": tokenInfo.AccessToken,
		"expires_at":   tokenInfo.ExpiresAt,
	})
}
