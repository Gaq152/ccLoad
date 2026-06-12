package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"ccLoad/internal/config"
	"ccLoad/internal/model"
	"ccLoad/internal/storage"
	"ccLoad/internal/util"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// TokenInfo 令牌内存缓存信息（2025-12新增）
// 存储令牌的过期时间和启用状态，支持禁用检查和懒惰过期
type TokenInfo struct {
	ExpiresAt int64 // 过期时间（Unix毫秒，0=永不过期）
	IsActive  bool  // 是否启用
}

// TokenChannelConfig 令牌渠道访问配置（2025-12新增）
type TokenChannelConfig struct {
	AllChannels bool    // true=允许所有渠道, false=仅允许指定渠道
	ChannelIDs  []int64 // 允许的渠道ID列表（仅当AllChannels=false时有效）
}

// AuthService 认证和授权服务
// 职责：处理所有认证和授权相关的业务逻辑
// - Token 认证（管理界面动态令牌）
// - API 认证（数据库驱动的访问令牌）
// - 登录/登出处理
// - 速率限制（防暴力破解）
//
// 遵循 SRP 原则：仅负责认证授权，不涉及代理、日志、管理 API
type AuthService struct {
	// Token 认证（管理界面使用的动态 Token）
	// [INFO] 安全修复：存储SHA256哈希而非明文(2025-12)
	// [INFO] 2026-06：密码哈希落库，支持热更新（Web改密码/初始化向导），passwordMux 保护
	passwordHash []byte               // 管理员密码bcrypt哈希（nil = Setup模式，等待初始化）
	setupToken   string               // 初始化令牌（仅Setup模式非空，完成后清空）
	passwordMux  sync.RWMutex         // passwordHash/setupToken 并发保护
	validTokens  map[string]time.Time // TokenHash → 过期时间
	tokensMux    sync.RWMutex         // 并发保护

	// API 认证（代理 API 使用的数据库令牌）
	// [FIX] 2025-12: 存储TokenInfo包含过期时间和启用状态，支持禁用检查和懒惰过期
	authTokens        map[string]*TokenInfo         // Token哈希 → 令牌信息（过期时间+启用状态）
	authTokenIDs      map[string]int64              // Token哈希 → Token ID 映射（用于日志记录，2025-12新增）
	authTokenNames    map[string]string             // Token哈希 → 令牌名称（用于SSE日志显示，2025-12新增）
	authTokenChannels map[int64]*TokenChannelConfig // Token ID → 渠道访问配置（2025-12新增）
	authTokensMux     sync.RWMutex                  // 并发保护（支持热更新）

	// 数据库依赖（用于热更新令牌）
	store storage.Store

	// 系统配置（用于读取 Turnstile 人机验证配置，可为 nil＝禁用）
	configService *ConfigService

	// Token 加密密钥（用于解密 2FA TOTP secret，与 CCLOAD_TOKEN_KEY 一致，可为空＝明文存储）
	tokenEncryptionKey []byte

	// 2FA 待验证登录令牌（密码已通过、等待验证码的中间态，5分钟过期）
	// TokenHash → 过期时间；内存存储即可，重启丢失只需重新输一次密码
	pending2FA    map[string]time.Time
	pending2FAMux sync.Mutex

	// 速率限制（防暴力破解）
	loginRateLimiter *util.LoginRateLimiter

	// 异步更新 last_used_at（受控 worker，避免 goroutine 泄漏）
	lastUsedCh chan string    // tokenHash 更新队列
	done       chan struct{}  // 关闭信号
	wg         sync.WaitGroup // 优雅关闭
}

// NewAuthService 创建认证服务实例
// 初始化时自动从数据库加载API访问令牌和管理员会话
// passwordHash 为 nil 时进入 Setup 模式（等待引导页凭 setupToken 设置密码）
func NewAuthService(
	passwordHash []byte,
	setupToken string,
	loginRateLimiter *util.LoginRateLimiter,
	store storage.Store,
	configService *ConfigService,
	tokenEncryptionKey []byte,
) *AuthService {
	s := &AuthService{
		passwordHash:      passwordHash,
		setupToken:        setupToken,
		validTokens:       make(map[string]time.Time),
		authTokens:        make(map[string]*TokenInfo),
		authTokenIDs:      make(map[string]int64),
		authTokenNames:    make(map[string]string),
		authTokenChannels: make(map[int64]*TokenChannelConfig),
		loginRateLimiter:   loginRateLimiter,
		store:              store,
		configService:      configService,
		tokenEncryptionKey: tokenEncryptionKey,
		pending2FA:         make(map[string]time.Time),
		lastUsedCh:        make(chan string, 256), // 带缓冲，避免阻塞请求
		done:              make(chan struct{}),
	}

	// 启动 last_used_at 更新 worker
	s.wg.Add(1)
	go s.lastUsedWorker()

	// 从数据库加载API访问令牌
	if err := s.ReloadAuthTokens(); err != nil {
		log.Printf("[WARN]  初始化时加载API令牌失败: %v", err)
	}

	// 从数据库加载管理员会话（支持重启后保持登录）
	if err := s.loadSessionsFromDB(); err != nil {
		log.Printf("[WARN]  初始化时加载管理员会话失败: %v", err)
	}

	return s
}

// loadSessionsFromDB 从数据库加载管理员会话
// [INFO] 安全修复：加载tokenHash→expiry映射(2025-12)
func (s *AuthService) loadSessionsFromDB() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sessions, err := s.store.LoadAllSessions(ctx)
	if err != nil {
		return err
	}

	s.tokensMux.Lock()
	for tokenHash, expiry := range sessions {
		s.validTokens[tokenHash] = expiry
	}
	s.tokensMux.Unlock()

	if len(sessions) > 0 {
		log.Printf("[INFO] 已恢复 %d 个管理员会话（重启后保持登录）", len(sessions))
	}
	return nil
}

// lastUsedWorker 处理 last_used_at 更新的后台 worker
func (s *AuthService) lastUsedWorker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.done:
			return
		case tokenHash := <-s.lastUsedCh:
			// [FIX] P0-4: WithTimeout 的 cancel 必须在每次循环内执行，不能在循环里 defer 到 goroutine 退出。
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()

				_ = s.store.UpdateTokenLastUsed(ctx, tokenHash, time.Now())
			}()
		}
	}
}

// Close 优雅关闭 AuthService
func (s *AuthService) Close() {
	close(s.done)
	s.wg.Wait()
}

// ============================================================================
// Token 生成和验证（内部方法）
// ============================================================================

// generateToken 生成安全Token（64字符十六进制）
func (s *AuthService) generateToken() (string, error) {
	b := make([]byte, config.TokenRandomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// IsValidAdminToken 验证管理员会话 Token 有效性（供 HTML 访问鉴权中间件调用）
// 与内部 isValidToken 行为一致，作为公开 API 暴露
func (s *AuthService) IsValidAdminToken(token string) bool {
	return s.isValidToken(token)
}

// HasPassword 是否已设置管理密码（false = Setup模式，需要初始化引导）
func (s *AuthService) HasPassword() bool {
	s.passwordMux.RLock()
	defer s.passwordMux.RUnlock()
	return len(s.passwordHash) > 0
}

// isValidToken 验证Token有效性（检查过期时间）
// [INFO] 安全修复：通过tokenHash查询(2025-12)
func (s *AuthService) isValidToken(token string) bool {
	tokenHash := model.HashToken(token)

	s.tokensMux.RLock()
	expiry, exists := s.validTokens[tokenHash]
	s.tokensMux.RUnlock()

	if !exists {
		return false
	}

	// 检查是否过期
	if time.Now().After(expiry) {
		// 同步删除过期Token（避免goroutine泄漏）
		// 原因：map删除操作非常快（O(1)），无需异步，异步反而导致goroutine泄漏
		s.tokensMux.Lock()
		delete(s.validTokens, tokenHash)
		s.tokensMux.Unlock()
		return false
	}

	return true
}

// CleanExpiredTokens 清理过期Token（定期任务）
// 公开方法，供 Server 的后台协程调用
func (s *AuthService) CleanExpiredTokens() {
	now := time.Now()

	// 使用快照模式避免长时间持锁
	s.tokensMux.RLock()
	toDelete := make([]string, 0, len(s.validTokens)/10)
	for tokenHash, expiry := range s.validTokens {
		if now.After(expiry) {
			toDelete = append(toDelete, tokenHash)
		}
	}
	s.tokensMux.RUnlock()

	// 批量删除内存中的过期Token
	if len(toDelete) > 0 {
		s.tokensMux.Lock()
		for _, tokenHash := range toDelete {
			if expiry, exists := s.validTokens[tokenHash]; exists && now.After(expiry) {
				delete(s.validTokens, tokenHash)
			}
		}
		s.tokensMux.Unlock()
	}

	// 同时清理数据库中的过期会话
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.CleanExpiredSessions(ctx); err != nil {
		log.Printf("[WARN]  清理数据库过期会话失败: %v", err)
	}
}

// ============================================================================
// 认证中间件
// ============================================================================

// RequireTokenAuth Token 认证中间件（管理界面使用）
// 支持两种认证方式：
// 1. Authorization 头：Bearer <token>
// 2. URL 参数：?token=<token>（用于 SSE 等不支持自定义头的场景）
func (s *AuthService) RequireTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		var token string

		// 优先从 Authorization 头获取 Token
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			const prefix = "Bearer "
			if strings.HasPrefix(authHeader, prefix) {
				token = strings.TrimPrefix(authHeader, prefix)
			}
		}

		// 如果头部没有，尝试从 URL 参数获取（用于 SSE）
		if token == "" {
			token = c.Query("token")
		}

		// 验证 Token
		if token != "" && s.isValidToken(token) {
			c.Next()
			return
		}

		// 未授权
		RespondErrorMsg(c, http.StatusUnauthorized, "未授权访问，请先登录")
		c.Abort()
	}
}

// RequireAPIAuth API 认证中间件（代理 API 使用）
// [FIX] 2025-12: 添加过期时间校验，支持懒惰剔除过期令牌
func (s *AuthService) RequireAPIAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 未配置认证令牌时，默认全部返回 401（不允许公开访问）
		s.authTokensMux.RLock()
		tokenCount := len(s.authTokens)
		s.authTokensMux.RUnlock()

		if tokenCount == 0 {
			RespondErrorMsg(c, http.StatusUnauthorized, "invalid or missing authorization")
			c.Abort()
			return
		}

		var token string
		var tokenFound bool

		// 检查 Authorization 头（Bearer token）
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			const prefix = "Bearer "
			if strings.HasPrefix(authHeader, prefix) {
				token = strings.TrimPrefix(authHeader, prefix)
				tokenFound = true
			}
		}

		// 检查 X-API-Key 头
		if !tokenFound {
			apiKey := c.GetHeader("X-API-Key")
			if apiKey != "" {
				token = apiKey
				tokenFound = true
			}
		}

		// 检查 x-goog-api-key 头（Google API格式）
		if !tokenFound {
			googApiKey := c.GetHeader("x-goog-api-key")
			if googApiKey != "" {
				token = googApiKey
				tokenFound = true
			}
		}

		if !tokenFound {
			RespondErrorMsg(c, http.StatusUnauthorized, "invalid or missing authorization")
			c.Abort()
			return
		}

		// 计算令牌哈希并验证
		tokenHash := model.HashToken(token)

		s.authTokensMux.RLock()
		tokenInfo, exists := s.authTokens[tokenHash]
		tokenID, hasTokenID := s.authTokenIDs[tokenHash]
		tokenName := s.authTokenNames[tokenHash]
		s.authTokensMux.RUnlock()

		if !exists || tokenInfo == nil {
			RespondErrorMsg(c, http.StatusUnauthorized, "invalid or missing authorization")
			c.Abort()
			return
		}

		// [FIX] 2025-12: 禁用检查（优先于过期检查）
		// 返回 403 Forbidden 而非 401，明确区分"令牌被禁用"和"令牌无效"
		if !tokenInfo.IsActive {
			RespondErrorMsg(c, http.StatusForbidden, "token disabled")
			c.Abort()
			return
		}

		// [FIX] 过期校验：expiresAt > 0 表示有过期时间，检查是否已过期
		if tokenInfo.ExpiresAt > 0 && time.Now().UnixMilli() > tokenInfo.ExpiresAt {
			// 懒惰剔除：过期时从内存中移除（避免下次还要检查）
			s.authTokensMux.Lock()
			delete(s.authTokens, tokenHash)
			delete(s.authTokenIDs, tokenHash)
			delete(s.authTokenNames, tokenHash)
			s.authTokensMux.Unlock()

			RespondErrorMsg(c, http.StatusUnauthorized, "token expired")
			c.Abort()
			return
		}

		// 将tokenHash、tokenID、tokenName存储到context，供后续统计使用
		// 2025-11新增tokenHash, 2025-12新增tokenID和tokenName（用于SSE日志实时显示）
		c.Set("token_hash", tokenHash)
		if hasTokenID {
			c.Set("token_id", tokenID)
		}
		if tokenName != "" {
			c.Set("token_name", tokenName)
		}

		// 异步更新last_used_at（发送到受控worker，不阻塞请求）
		select {
		case s.lastUsedCh <- tokenHash:
		default:
			// channel满时丢弃，避免阻塞（last_used_at非关键数据）
		}

		c.Next()
	}
}

// ============================================================================
// 登录/登出处理
// ============================================================================

// HandleLogin 处理登录请求
// 集成登录速率限制，防暴力破解
func (s *AuthService) HandleLogin(c *gin.Context) {
	clientIP := c.ClientIP()

	// 检查速率限制
	if !s.loginRateLimiter.AllowAttempt(clientIP) {
		lockoutTime := s.loginRateLimiter.GetLockoutTime(clientIP)
		RespondErrorWithData(c, http.StatusTooManyRequests, "Too many failed login attempts", gin.H{
			"message":         fmt.Sprintf("Account locked for %d seconds. Please try again later.", lockoutTime),
			"lockout_seconds": lockoutTime,
		})
		return
	}

	// Setup 模式下不接受登录（须先完成初始化）
	if !s.HasPassword() {
		RespondErrorWithData(c, http.StatusServiceUnavailable, "系统尚未初始化，请先完成初始化设置", gin.H{
			"setup_required": true,
		})
		return
	}

	var req struct {
		Password       string `json:"password" binding:"required"`
		TurnstileToken string `json:"turnstile_token"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "Invalid request format")
		return
	}

	// Turnstile 人机验证（先于密码校验，机器人请求不消耗 bcrypt 计算）
	if enabled, _, secretKey := s.turnstileConfig(); enabled {
		if req.TurnstileToken == "" {
			RespondErrorMsg(c, http.StatusBadRequest, "请完成人机验证")
			return
		}
		if err := verifyTurnstileToken(c.Request.Context(), secretKey, req.TurnstileToken, clientIP); err != nil {
			log.Printf("[WARN]  人机验证失败: IP=%s, err=%v", clientIP, err)
			RespondErrorMsg(c, http.StatusForbidden, "人机验证失败，请刷新页面重试")
			return
		}
	}

	// 验证密码（bcrypt安全比较，读锁取哈希支持热更新）
	s.passwordMux.RLock()
	currentHash := s.passwordHash
	s.passwordMux.RUnlock()
	if err := bcrypt.CompareHashAndPassword(currentHash, []byte(req.Password)); err != nil {
		// 记录失败尝试（速率限制器已在AllowAttempt中增加计数）
		attemptCount := s.loginRateLimiter.GetAttemptCount(clientIP)
		log.Printf("[WARN]  登录失败: IP=%s, 尝试次数=%d/5", clientIP, attemptCount)

		// [SECURITY] 不返回剩余尝试次数，避免攻击者推断速率限制状态
		RespondErrorMsg(c, http.StatusUnauthorized, "Invalid password")
		return
	}

	// 密码正确，重置速率限制
	s.loginRateLimiter.RecordSuccess(clientIP)

	// 两步验证检查：已激活时不直接发会话，返回待验证令牌让前端进入第二步
	rec, err := s.load2FA()
	if err != nil {
		log.Printf("[WARN]  加载2FA配置失败: %v", err)
		// 查询失败按未开启处理（fail-open）：2FA是增强防线，不应让数据库故障锁死登录
	}
	if rec.IsActive() {
		pendingToken, err := s.createPending2FAToken()
		if err != nil {
			log.Printf("ERROR: pending token generation failed: %v", err)
			RespondErrorMsg(c, http.StatusInternalServerError, "internal error")
			return
		}
		RespondJSON(c, http.StatusOK, gin.H{
			"requires_2fa":  true,
			"pending_token": pendingToken,
		})
		return
	}

	s.issueSession(c, clientIP)
}

// issueSession 签发管理员会话（密码及2FA均通过后调用）
// 生成Token → 内存+数据库存储哈希 → 写HttpOnly Cookie → 返回明文Token
func (s *AuthService) issueSession(c *gin.Context, clientIP string) {
	token, err := s.generateToken()
	if err != nil {
		log.Printf("ERROR: token generation failed: %v", err)
		RespondErrorMsg(c, http.StatusInternalServerError, "internal error")
		return
	}
	expiry := time.Now().Add(config.TokenExpiry)

	// [INFO] 安全修复：存储tokenHash而非明文(2025-12)
	tokenHash := model.HashToken(token)

	// 存储TokenHash到内存
	s.tokensMux.Lock()
	s.validTokens[tokenHash] = expiry
	s.tokensMux.Unlock()

	// [INFO] 修复：同步写入数据库（SQLite本地写入极快，微秒级，无需异步）
	// 原因：异步goroutine未受控，关机时可能写入已关闭的连接
	// [FIX] P0-4: 使用 defer cancel() 防止 context 泄漏
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := s.store.CreateAdminSession(ctx, token, expiry); err != nil {
		log.Printf("[WARN]  保存管理员会话到数据库失败: %v", err)
		// 注意：内存中的token仍然有效，下次重启会丢失此会话
	}

	log.Printf("[INFO] 登录成功: IP=%s", clientIP)

	// 同步写入 HttpOnly Cookie，用于 HTML 访问的服务端鉴权
	// 仅 HTTPS 请求设置 Secure，避免本地 http 开发环境无法登录
	setAdminSessionCookie(c, token, int(config.TokenExpiry.Seconds()))

	// 返回明文Token给客户端（前端存储到localStorage）
	RespondJSON(c, http.StatusOK, gin.H{
		"token":     token,                             // 明文token返回给客户端
		"expiresIn": int(config.TokenExpiry.Seconds()), // 秒数
	})
}

// ============================================================================
// 两步验证（2FA）登录第二阶段
// ============================================================================

// pending2FAExpiry 待验证登录令牌有效期（密码通过到输完验证码的时间窗口）
const pending2FAExpiry = 5 * time.Minute

// load2FA 加载2FA配置（独立超时，不依赖请求context）
func (s *AuthService) load2FA() (*model.Admin2FA, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return s.store.GetAdmin2FA(ctx)
}

// createPending2FAToken 生成待验证登录令牌（存哈希，5分钟过期）
func (s *AuthService) createPending2FAToken() (string, error) {
	token, err := s.generateToken()
	if err != nil {
		return "", err
	}

	now := time.Now()
	s.pending2FAMux.Lock()
	// 顺手清理过期条目（单管理员场景map极小，遍历无成本）
	for hash, expiry := range s.pending2FA {
		if now.After(expiry) {
			delete(s.pending2FA, hash)
		}
	}
	s.pending2FA[model.HashToken(token)] = now.Add(pending2FAExpiry)
	s.pending2FAMux.Unlock()

	return token, nil
}

// validPending2FAToken 检查待验证令牌是否有效（不删除：验证码输错时无需重新输密码）
func (s *AuthService) validPending2FAToken(token string) bool {
	hash := model.HashToken(token)
	s.pending2FAMux.Lock()
	defer s.pending2FAMux.Unlock()

	expiry, exists := s.pending2FA[hash]
	if !exists {
		return false
	}
	if time.Now().After(expiry) {
		delete(s.pending2FA, hash)
		return false
	}
	return true
}

// consumePending2FAToken 删除待验证令牌（验证码通过后调用）
func (s *AuthService) consumePending2FAToken(token string) {
	s.pending2FAMux.Lock()
	delete(s.pending2FA, model.HashToken(token))
	s.pending2FAMux.Unlock()
}

// HandleLogin2FA 处理两步验证（登录第二阶段）
// POST /login/2fa  {pending_token, code}
// code 为 6 位 TOTP 验证码或恢复码；同样受登录速率限制保护（防6位码爆破）
func (s *AuthService) HandleLogin2FA(c *gin.Context) {
	clientIP := c.ClientIP()

	if !s.loginRateLimiter.AllowAttempt(clientIP) {
		lockoutTime := s.loginRateLimiter.GetLockoutTime(clientIP)
		RespondErrorWithData(c, http.StatusTooManyRequests, "Too many failed login attempts", gin.H{
			"message":         fmt.Sprintf("Account locked for %d seconds. Please try again later.", lockoutTime),
			"lockout_seconds": lockoutTime,
		})
		return
	}

	var req struct {
		PendingToken string `json:"pending_token" binding:"required"`
		Code         string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "Invalid request format")
		return
	}

	// 待验证令牌过期/无效 → 前端应返回第一步重新输密码
	if !s.validPending2FAToken(req.PendingToken) {
		RespondErrorWithData(c, http.StatusUnauthorized, "登录已过期，请重新输入密码", gin.H{
			"pending_expired": true,
		})
		return
	}

	rec, err := s.load2FA()
	if err != nil {
		log.Printf("[ERROR] 加载2FA配置失败: %v", err)
		RespondErrorMsg(c, http.StatusInternalServerError, "internal error")
		return
	}
	if !rec.IsActive() {
		// 极端情况：第二步进行中被解绑 → 让用户重新走第一步（无2FA直接登录）
		RespondErrorWithData(c, http.StatusUnauthorized, "两步验证状态已变更，请重新登录", gin.H{
			"pending_expired": true,
		})
		return
	}

	if !check2FACode(rec, req.Code, s.tokenEncryptionKey) {
		log.Printf("[WARN]  两步验证失败: IP=%s", clientIP)
		RespondErrorMsg(c, http.StatusUnauthorized, "验证码错误")
		return
	}

	// 落库：持久化防重放时间片 / 已消费的恢复码
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.store.SaveAdmin2FA(ctx, rec); err != nil {
		log.Printf("[WARN]  保存2FA状态失败: %v", err)
	}

	s.consumePending2FAToken(req.PendingToken)
	s.loginRateLimiter.RecordSuccess(clientIP)
	s.issueSession(c, clientIP)
}

// setAdminSessionCookie 写入管理员会话 Cookie
// 仅用于服务端在静态 HTML 路由上识别"是否已登录"，避免登录前的 HTML 内容外泄
// HttpOnly：JS 无法读取，规避 XSS 窃取；SameSite=Lax：避免跨站自动携带；Secure：仅 HTTPS 携带
func setAdminSessionCookie(c *gin.Context, token string, maxAgeSec int) {
	secure := c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("ccload_token", token, maxAgeSec, "/", "", secure, true)
}

// clearAdminSessionCookie 清除管理员会话 Cookie（登出/失效时调用）
func clearAdminSessionCookie(c *gin.Context) {
	secure := c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("ccload_token", "", -1, "/", "", secure, true)
}

// HandleLogout 处理登出请求
func (s *AuthService) HandleLogout(c *gin.Context) {
	// 始终清除 HttpOnly Cookie，避免登出后服务端仍因 Cookie 放行 HTML
	clearAdminSessionCookie(c)

	// 从Authorization头提取Token；若头部缺失则回退到 Cookie，保证登出能命中 token
	authHeader := c.GetHeader("Authorization")
	const prefix = "Bearer "
	token := ""
	if after, ok := strings.CutPrefix(authHeader, prefix); ok {
		token = after
	} else if v, err := c.Cookie("ccload_token"); err == nil {
		token = v
	}
	if token != "" {
		// [INFO] 安全修复：计算tokenHash删除(2025-12)
		tokenHash := model.HashToken(token)

		// 删除内存中的TokenHash
		s.tokensMux.Lock()
		delete(s.validTokens, tokenHash)
		s.tokensMux.Unlock()

		// [INFO] 修复：同步删除数据库中的会话（SQLite本地删除极快，微秒级，无需异步）
		// 原因：异步goroutine未受控，关机时可能写入已关闭的连接
		// [FIX] P0-4: 使用 defer cancel() 防止 context 泄漏
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if err := s.store.DeleteAdminSession(ctx, token); err != nil {
			log.Printf("[WARN]  删除数据库会话失败: %v", err)
		}
	}

	RespondJSON(c, http.StatusOK, gin.H{"message": "已登出"})
}

// ============================================================================
// API令牌热更新
// ============================================================================

// ReloadAuthTokens 从数据库重新加载API访问令牌
// 用于CRUD操作后立即生效，无需重启服务
// [FIX] 2025-12: 同时加载过期时间和启用状态，支持禁用检查和懒惰过期
// [FIX] 2025-12: 同时加载渠道访问配置，支持令牌级渠道控制
func (s *AuthService) ReloadAuthTokens() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// [FIX] 2025-12: 加载所有令牌（包括禁用的），以便在认证时返回明确的 403 错误
	tokens, err := s.store.ListAuthTokens(ctx)
	if err != nil {
		return fmt.Errorf("reload auth tokens: %w", err)
	}

	// 构建新的令牌映射（存储TokenInfo包含过期时间和启用状态）
	newTokens := make(map[string]*TokenInfo, len(tokens))
	newTokenIDs := make(map[string]int64, len(tokens))
	newTokenNames := make(map[string]string, len(tokens))
	newTokenChannels := make(map[int64]*TokenChannelConfig, len(tokens))

	// 统计启用/禁用令牌数量
	activeCount := 0

	// 收集需要加载渠道配置的令牌ID（仅启用的令牌）
	tokenIDsToLoad := make([]int64, 0, len(tokens))
	for _, t := range tokens {
		// ExpiresAt: nil → 0 (永不过期), *int64 → Unix毫秒
		var expiresAt int64
		if t.ExpiresAt != nil {
			expiresAt = *t.ExpiresAt
		}

		// 存储完整的令牌信息
		newTokens[t.Token] = &TokenInfo{
			ExpiresAt: expiresAt,
			IsActive:  t.IsActive,
		}
		newTokenIDs[t.Token] = t.ID
		newTokenNames[t.Token] = t.Description // 令牌名称（用于SSE日志显示）

		if t.IsActive {
			activeCount++
		}

		// 初始化渠道配置
		newTokenChannels[t.ID] = &TokenChannelConfig{
			AllChannels: t.AllChannels,
			ChannelIDs:  nil, // 稍后批量加载
		}

		// 如果不是AllChannels且令牌启用，需要加载具体的渠道列表
		if !t.AllChannels && t.IsActive {
			tokenIDsToLoad = append(tokenIDsToLoad, t.ID)
		}
	}

	// 批量加载非AllChannels令牌的渠道配置
	if len(tokenIDsToLoad) > 0 {
		channelsMap, err := s.store.LoadTokenChannelsMap(ctx, tokenIDsToLoad)
		if err != nil {
			log.Printf("[WARN] 加载令牌渠道配置失败: %v", err)
			// 降级处理：不影响基本认证功能
		} else {
			for tokenID, channelIDs := range channelsMap {
				if cfg, exists := newTokenChannels[tokenID]; exists {
					cfg.ChannelIDs = channelIDs
				}
			}
		}
	}

	// 原子替换（避免读写竞争）
	s.authTokensMux.Lock()
	s.authTokens = newTokens
	s.authTokenIDs = newTokenIDs
	s.authTokenNames = newTokenNames
	s.authTokenChannels = newTokenChannels
	s.authTokensMux.Unlock()

	log.Printf("[RELOAD] API令牌已热更新（%d个令牌，其中%d个启用）", len(newTokens), activeCount)
	return nil
}

// GetTokenChannelConfig 获取令牌的渠道访问配置
// 返回值:
//   - config: 渠道配置（nil表示令牌不存在或未配置）
//   - exists: 令牌是否存在
func (s *AuthService) GetTokenChannelConfig(tokenID int64) (*TokenChannelConfig, bool) {
	s.authTokensMux.RLock()
	defer s.authTokensMux.RUnlock()

	cfg, exists := s.authTokenChannels[tokenID]
	if !exists || cfg == nil {
		return nil, false
	}

	// 返回副本，避免并发修改
	return &TokenChannelConfig{
		AllChannels: cfg.AllChannels,
		ChannelIDs:  append([]int64(nil), cfg.ChannelIDs...),
	}, true
}
