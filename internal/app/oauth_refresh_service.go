package app

import (
	"context"
	"log"
	"time"

	"ccLoad/internal/util"
)

// OAuth Token 定时刷新配置
const (
	// 刷新检查间隔（默认每周一次）
	OAuthRefreshInterval = 7 * 24 * time.Hour

	// 启动后首次检查延迟（避免启动时立即刷新）
	OAuthRefreshInitialDelay = 1 * time.Minute
)

// oauthRefreshLoop 定时刷新 OAuth Token（Codex/Gemini 官方预设）
// 目的：防止长期不使用导致 refresh_token 过期
func (s *Server) oauthRefreshLoop() {
	defer s.wg.Done()

	// 启动后等待一段时间再开始（避免启动时立即刷新）
	select {
	case <-s.shutdownCh:
		return
	case <-time.After(OAuthRefreshInitialDelay):
	}

	ticker := time.NewTicker(OAuthRefreshInterval)
	defer ticker.Stop()

	// 启动后立即执行一次检查
	s.refreshAllOAuthTokens()

	for {
		select {
		case <-s.shutdownCh:
			return
		case <-ticker.C:
			s.refreshAllOAuthTokens()
		}
	}
}

// refreshAllOAuthTokens 刷新所有 OAuth 预设渠道的 Token
func (s *Server) refreshAllOAuthTokens() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 获取所有渠道
	channels, err := s.store.ListConfigs(ctx)
	if err != nil {
		log.Printf("[OAUTH-REFRESH] 获取渠道列表失败: %v", err)
		return
	}

	var refreshedCount, failedCount int

	for _, cfg := range channels {
		// 只处理官方预设的 Codex/Gemini 渠道
		if cfg.Preset != "official" {
			continue
		}

		channelType := cfg.GetChannelType()
		if channelType != util.ChannelTypeCodex && channelType != util.ChannelTypeGemini {
			continue
		}

		// 获取该渠道的所有 API Keys
		apiKeys, err := s.store.GetAPIKeys(ctx, cfg.ID)
		if err != nil {
			log.Printf("[OAUTH-REFRESH] 获取渠道 %d 的 API Keys 失败: %v", cfg.ID, err)
			continue
		}

		for keyIndex, apiKey := range apiKeys {
			// 根据渠道类型刷新 Token
			if channelType == util.ChannelTypeCodex {
				_, oauthToken, isOAuth := ParseAPIKeyOrOAuth(apiKey.APIKey)
				if !isOAuth || oauthToken == nil {
					continue
				}

				// 调用刷新逻辑
				_, _, err := s.RefreshCodexTokenIfNeeded(ctx, cfg.ID, keyIndex, oauthToken)
				if err != nil {
					log.Printf("[OAUTH-REFRESH] Codex Token 刷新失败 (channel=%d, key=%d): %v", cfg.ID, keyIndex, err)
					failedCount++
				} else {
					refreshedCount++
				}

			} else { // Gemini
				_, oauthToken, isOAuth := ParseGeminiAPIKeyOrOAuth(apiKey.APIKey)
				if !isOAuth || oauthToken == nil {
					continue
				}

				// 调用刷新逻辑
				_, _, err := s.RefreshGeminiTokenIfNeeded(ctx, cfg.ID, keyIndex, oauthToken)
				if err != nil {
					log.Printf("[OAUTH-REFRESH] Gemini Token 刷新失败 (channel=%d, key=%d): %v", cfg.ID, keyIndex, err)
					failedCount++
				} else {
					refreshedCount++
				}
			}
		}
	}

	if refreshedCount > 0 || failedCount > 0 {
		log.Printf("[OAUTH-REFRESH] Token 刷新完成: 成功=%d, 失败=%d", refreshedCount, failedCount)
	}
}
