package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"

	"ccLoad/internal/model"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// API访问令牌管理 (Admin API)
// ============================================================================

// HandleListAuthTokens 列出所有API访问令牌（支持时间范围统计，2025-12扩展）
// GET /admin/auth-tokens?range=today
func (s *Server) HandleListAuthTokens(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tokens, err := s.store.ListAuthTokens(ctx)
	if err != nil {
		log.Print("❌ 列出令牌失败: " + err.Error())
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 脱敏处理 + 计算过期状态
	for _, t := range tokens {
		t.Token = model.MaskToken(t.Token)
		t.IsExpiredFlag = t.IsExpired() // 计算是否过期，供前端使用
	}
	if tokens == nil {
		tokens = make([]*model.AuthToken, 0)
	}

	// 如果请求中包含range参数，则叠加时间范围统计（用于tokens.html页面）
	timeRange := strings.TrimSpace(c.Query("range"))
	if timeRange != "" && timeRange != "all" {
		params := ParsePaginationParams(c)
		startTime, endTime := params.GetTimeRange()

		// 从logs表聚合时间范围内的统计
		rangeStats, err := s.store.GetAuthTokenStatsInRange(ctx, startTime, endTime)
		if err != nil {
			log.Printf("[WARN]  查询时间范围统计失败: %v", err)
			// 降级处理：统计查询失败不影响token列表返回，仅记录警告
		} else {
			// 将时间范围统计叠加到每个token的响应中
			for _, t := range tokens {
				if stat, ok := rangeStats[t.ID]; ok {
					// 用时间范围统计覆盖累计统计字段（前端透明）
					t.SuccessCount = stat.SuccessCount
					t.FailureCount = stat.FailureCount
					t.PromptTokensTotal = stat.PromptTokens
					t.CompletionTokensTotal = stat.CompletionTokens
					t.CacheReadTokensTotal = stat.CacheReadTokens
					t.CacheCreationTokensTotal = stat.CacheCreationTokens
					t.TotalCostUSD = stat.TotalCost
					t.StreamAvgTTFB = stat.StreamAvgTTFB
					t.NonStreamAvgRT = stat.NonStreamAvgRT
					t.StreamCount = stat.StreamCount
					t.NonStreamCount = stat.NonStreamCount
				} else {
					// 该token在此时间范围内无数据，清零统计字段
					t.SuccessCount = 0
					t.FailureCount = 0
					t.PromptTokensTotal = 0
					t.CompletionTokensTotal = 0
					t.CacheReadTokensTotal = 0
					t.CacheCreationTokensTotal = 0
					t.TotalCostUSD = 0
					t.StreamAvgTTFB = 0
					t.NonStreamAvgRT = 0
					t.StreamCount = 0
					t.NonStreamCount = 0
				}
			}
		}
	}

	RespondJSON(c, http.StatusOK, gin.H{"tokens": tokens})
}

// HandleCreateAuthToken 创建新的API访问令牌
// POST /admin/auth-tokens
func (s *Server) HandleCreateAuthToken(c *gin.Context) {
	var req struct {
		Description string `json:"description" binding:"required"`
		ExpiresAt   *int64 `json:"expires_at"` // Unix毫秒时间戳，nil表示永不过期
		IsActive    *bool  `json:"is_active"`  // nil表示默认启用
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}

	// 生成安全令牌(64字符十六进制)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		log.Print("❌ 生成令牌失败: " + err.Error())
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	tokenPlain := hex.EncodeToString(tokenBytes)

	// 计算SHA256哈希用于存储
	tokenHash := model.HashToken(tokenPlain)

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	authToken := &model.AuthToken{
		Token:       tokenHash,
		Description: req.Description,
		ExpiresAt:   req.ExpiresAt,
		IsActive:    isActive,
		AllChannels: true, // 默认允许所有渠道
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.store.CreateAuthToken(ctx, authToken); err != nil {
		log.Print("❌ 创建令牌失败: " + err.Error())
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 触发热更新（立即生效）
	if err := s.authService.ReloadAuthTokens(); err != nil {
		log.Print("[WARN]  热更新失败: " + err.Error())
	}

	log.Printf("[INFO] 创建API令牌: ID=%d, 描述=%s", authToken.ID, authToken.Description)

	// 返回明文令牌（仅此一次机会）
	RespondJSON(c, http.StatusOK, gin.H{
		"id":          authToken.ID,
		"token":       tokenPlain, // 明文令牌，仅创建时返回
		"description": authToken.Description,
		"created_at":  authToken.CreatedAt,
		"expires_at":  authToken.ExpiresAt,
		"is_active":   authToken.IsActive,
	})
}

// HandleUpdateAuthToken 更新令牌信息
// PUT /admin/auth-tokens/:id
func (s *Server) HandleUpdateAuthToken(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid token id")
		return
	}

	var req struct {
		Description *string `json:"description"`
		IsActive    *bool   `json:"is_active"`
		ExpiresAt   *int64  `json:"expires_at"`
		AllChannels *bool   `json:"all_channels"` // 是否允许使用所有渠道（2025-12新增）
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 获取现有令牌
	token, err := s.store.GetAuthToken(ctx, id)
	if err != nil {
		RespondErrorMsg(c, http.StatusNotFound, "token not found")
		return
	}

	// 更新字段
	if req.Description != nil {
		token.Description = *req.Description
	}
	// ExpiresAt: 0 表示永不过期，需要显式更新
	if req.ExpiresAt != nil {
		if *req.ExpiresAt == 0 {
			// 永不过期：设置为 nil（数据库存储为 0）
			token.ExpiresAt = nil
		} else {
			token.ExpiresAt = req.ExpiresAt
		}
	}
	if req.AllChannels != nil {
		token.AllChannels = *req.AllChannels
	}

	// 处理启用/禁用请求
	// 启用时检查过期时间（过期令牌不能启用）
	if req.IsActive != nil {
		if *req.IsActive {
			// 尝试启用令牌：检查过期时间
			if token.ExpiresAt != nil && *token.ExpiresAt > 0 {
				if time.Now().UnixMilli() > *token.ExpiresAt {
					RespondErrorMsg(c, http.StatusBadRequest, "令牌已过期，请先修改过期时间后再启用")
					return
				}
			}
			token.IsActive = true
		} else {
			token.IsActive = false
		}
	}

	if err := s.store.UpdateAuthToken(ctx, token); err != nil {
		log.Print("❌ 更新令牌失败: " + err.Error())
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 触发热更新
	if err := s.authService.ReloadAuthTokens(); err != nil {
		log.Print("[WARN]  热更新失败: " + err.Error())
	}

	log.Printf("[INFO] 更新API令牌: ID=%d", id)

	// 返回脱敏后的令牌信息 + 计算过期状态
	token.Token = model.MaskToken(token.Token)
	token.IsExpiredFlag = token.IsExpired()
	RespondJSON(c, http.StatusOK, token)
}

// HandleDeleteAuthToken 删除令牌
// DELETE /admin/auth-tokens/:id
func (s *Server) HandleDeleteAuthToken(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid token id")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.store.DeleteAuthToken(ctx, id); err != nil {
		log.Print("❌ 删除令牌失败: " + err.Error())
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 触发热更新
	if err := s.authService.ReloadAuthTokens(); err != nil {
		log.Print("[WARN]  热更新失败: " + err.Error())
	}

	log.Printf("[INFO] 删除API令牌: ID=%d", id)

	RespondJSON(c, http.StatusOK, gin.H{"id": id})
}

// ============================================================================
// 令牌渠道访问控制 API（2025-12新增）
// ============================================================================

// HandleGetTokenChannels 获取令牌的渠道访问配置
// GET /admin/auth-tokens/:id/channels
func (s *Server) HandleGetTokenChannels(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid token id")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 获取令牌信息
	token, err := s.store.GetAuthToken(ctx, id)
	if err != nil {
		RespondErrorMsg(c, http.StatusNotFound, "token not found")
		return
	}

	// 获取允许的渠道列表
	channelIDs, err := s.store.GetTokenChannels(ctx, id)
	if err != nil {
		log.Print("❌ 获取令牌渠道配置失败: " + err.Error())
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	RespondJSON(c, http.StatusOK, gin.H{
		"token_id":     id,
		"all_channels": token.AllChannels,
		"channel_ids":  channelIDs,
	})
}

// HandleSetTokenChannels 设置令牌的渠道访问配置
// PUT /admin/auth-tokens/:id/channels
func (s *Server) HandleSetTokenChannels(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid token id")
		return
	}

	var req struct {
		AllChannels bool    `json:"all_channels"` // 是否允许使用所有渠道
		ChannelIDs  []int64 `json:"channel_ids"`  // 允许的渠道ID列表（仅当 all_channels=false 时有效）
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 获取现有令牌
	token, err := s.store.GetAuthToken(ctx, id)
	if err != nil {
		RespondErrorMsg(c, http.StatusNotFound, "token not found")
		return
	}

	// 更新 AllChannels 字段
	token.AllChannels = req.AllChannels
	if err := s.store.UpdateAuthToken(ctx, token); err != nil {
		log.Print("❌ 更新令牌失败: " + err.Error())
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 更新渠道关联（无论 AllChannels 的值如何，都保存 ChannelIDs 以便切换时保留配置）
	if err := s.store.SetTokenChannels(ctx, id, req.ChannelIDs); err != nil {
		log.Print("❌ 设置令牌渠道关联失败: " + err.Error())
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 触发热更新
	if err := s.authService.ReloadAuthTokens(); err != nil {
		log.Print("[WARN]  热更新失败: " + err.Error())
	}

	log.Printf("[INFO] 设置API令牌渠道配置: ID=%d, AllChannels=%v, ChannelCount=%d", id, req.AllChannels, len(req.ChannelIDs))

	RespondJSON(c, http.StatusOK, gin.H{
		"token_id":     id,
		"all_channels": req.AllChannels,
		"channel_ids":  req.ChannelIDs,
	})
}
