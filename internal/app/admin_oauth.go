package app

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// OAuthTokenRequest Token 交换请求
type OAuthTokenRequest struct {
	TokenURL    string `json:"token_url"`
	Body        string `json:"body"`
	ContentType string `json:"content_type"`
}

// HandleOAuthToken 处理 OAuth token 交换（代理请求避免 CORS）
func (s *Server) HandleOAuthToken(c *gin.Context) {
	var req OAuthTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "参数错误: " + err.Error()})
		return
	}

	if req.TokenURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "token_url 不能为空"})
		return
	}

	// 创建请求
	httpReq, err := http.NewRequest("POST", req.TokenURL, strings.NewReader(req.Body))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "创建请求失败: " + err.Error()})
		return
	}

	// 设置 Content-Type
	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	httpReq.Header.Set("Content-Type", contentType)

	// 发送请求
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "请求失败: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "读取响应失败: " + err.Error()})
		return
	}

	// 返回结果
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		c.JSON(http.StatusOK, gin.H{
			"success":     true,
			"status_code": resp.StatusCode,
			"data":        string(body),
		})
	} else {
		c.JSON(http.StatusOK, gin.H{
			"success":     false,
			"status_code": resp.StatusCode,
			"error":       string(body),
		})
	}
}
