package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"ccLoad/internal/storage"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// 管理密码规则
const minPasswordLength = 8

// setupTokenLength 初始化令牌长度（打印到容器日志，引导页回填，防首访竞态）
const setupTokenLength = 12

// HashAdminPassword 计算管理密码的bcrypt哈希（失败直接退出，fail-fast）
func HashAdminPassword(password string) []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("FATAL: failed to hash password: %v", err)
	}
	return hash
}

// generateSetupToken 生成初始化令牌（字符集去除易混淆字符，便于从日志抄录）
func generateSetupToken() string {
	const charset = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	buf := make([]byte, setupTokenLength)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("FATAL: crypto/rand failed: %v", err)
	}
	chars := make([]byte, setupTokenLength)
	for i, b := range buf {
		chars[i] = charset[int(b)%len(charset)]
	}
	// 4-4-4 分组展示，降低抄错概率
	return string(chars[:4]) + "-" + string(chars[4:8]) + "-" + string(chars[8:])
}

// resolveAdminPassword 启动时管理密码判定（替代旧的"无CCLOAD_PASS则退出"逻辑）
//
// 判定顺序：
//  1. 逃生通道 CCLOAD_PASS_RESET → 强制覆盖DB哈希并吊销全部会话
//  2. 逃生通道 CCLOAD_2FA_RESET=1 → 删除两步验证绑定
//  3. DB 有哈希 → 正常模式（CCLOAD_PASS 被忽略，提示可移除）
//  4. DB 无哈希 + CCLOAD_PASS 存在 → bcrypt 播种入库（老部署无感迁移）
//  5. 都没有 → Setup 模式：生成一次性初始化令牌打印到日志，等待引导页设置密码
//
// 返回：passwordHash（nil = Setup模式）、setupToken（Setup模式时非空）
func resolveAdminPassword(store storage.Store) (passwordHash []byte, setupToken string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. 密码重置逃生通道（每次启动只要存在就执行，忘删的后果仅是重复重置）
	if resetPass := os.Getenv("CCLOAD_PASS_RESET"); resetPass != "" {
		hash := HashAdminPassword(resetPass)
		if err := store.SaveAdminPasswordHash(ctx, string(hash)); err != nil {
			log.Fatalf("FATAL: 密码重置失败: %v", err)
		}
		if err := store.DeleteAllAdminSessions(ctx); err != nil {
			log.Printf("[WARN]  吊销旧会话失败: %v", err)
		}
		log.Print("================================================================")
		log.Print("[警告] 管理密码已通过 CCLOAD_PASS_RESET 强制重置，全部会话已吊销！")
		log.Print("[警告] 请立即从环境变量中移除 CCLOAD_PASS_RESET，否则每次重启都会重置。")
		log.Print("================================================================")
	}

	// 2. 两步验证重置逃生通道（手机+恢复码全部丢失时解锁）
	if os.Getenv("CCLOAD_2FA_RESET") == "1" {
		if err := store.DeleteAdmin2FA(ctx); err != nil {
			log.Printf("[WARN]  重置两步验证失败: %v", err)
		} else {
			log.Print("================================================================")
			log.Print("[警告] 两步验证绑定已通过 CCLOAD_2FA_RESET 删除！")
			log.Print("[警告] 请立即从环境变量中移除 CCLOAD_2FA_RESET。")
			log.Print("================================================================")
		}
	}

	// 3. DB 有哈希 → 正常模式
	hashStr, err := store.GetAdminPasswordHash(ctx)
	if err != nil {
		log.Fatalf("FATAL: 读取管理密码失败: %v", err)
	}
	if hashStr != "" {
		if os.Getenv("CCLOAD_PASS") != "" {
			log.Print("[INFO] 管理密码已由数据库管理，环境变量 CCLOAD_PASS 已忽略（建议从配置中移除）")
		} else {
			log.Print("[INFO] 管理密码已从数据库加载（bcrypt哈希）")
		}
		return []byte(hashStr), ""
	}

	// 4. 播种：老部署升级或首次用环境变量启动
	if pass := os.Getenv("CCLOAD_PASS"); pass != "" {
		hash := HashAdminPassword(pass)
		if err := store.SaveAdminPasswordHash(ctx, string(hash)); err != nil {
			log.Fatalf("FATAL: 密码播种入库失败: %v", err)
		}
		log.Print("[INFO] 管理密码已从 CCLOAD_PASS 迁移至数据库（bcrypt哈希）")
		log.Print("[INFO] 现在可以从环境变量/docker-compose.yml 中安全移除 CCLOAD_PASS 了")
		return hash, ""
	}

	// 5. Setup 模式：等待引导页初始化
	token := generateSetupToken()
	log.Print("================================================================")
	log.Print("[初始化] 尚未设置管理密码，已进入初始化引导模式")
	log.Printf("[初始化] Setup Token: %s", token)
	log.Print("[初始化] 请访问 Web 页面，凭上方令牌完成管理密码设置")
	log.Print("================================================================")
	return nil, token
}

// ============================================================================
// Setup / 改密码 Handlers（AuthService 方法）
// ============================================================================

// validateNewPassword 新密码规则校验
func validateNewPassword(password string) string {
	if len(password) < minPasswordLength {
		return "密码长度至少 8 位"
	}
	return ""
}

// HandleSetup 首访初始化：验证 Setup Token 后设置管理密码
// POST /setup  {setup_token, password}
// 仅 Setup 模式下有效；成功后直接签发会话（设完即登录）
func (s *AuthService) HandleSetup(c *gin.Context) {
	clientIP := c.ClientIP()

	// 速率限制：防 Setup Token 爆破
	if !s.loginRateLimiter.AllowAttempt(clientIP) {
		lockoutTime := s.loginRateLimiter.GetLockoutTime(clientIP)
		RespondErrorWithData(c, http.StatusTooManyRequests, "尝试次数过多，请稍后再试", gin.H{
			"lockout_seconds": lockoutTime,
		})
		return
	}

	if s.HasPassword() {
		RespondErrorMsg(c, http.StatusForbidden, "系统已初始化，请直接登录")
		return
	}

	var req struct {
		SetupToken string `json:"setup_token" binding:"required"`
		Password   string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "请填写初始化令牌和密码")
		return
	}

	// 常数时间比较 Setup Token（输入归一化：去空格、统一大写）
	input := strings.ToUpper(strings.TrimSpace(req.SetupToken))
	s.passwordMux.RLock()
	expected := s.setupToken
	s.passwordMux.RUnlock()
	if expected == "" || subtle.ConstantTimeCompare([]byte(input), []byte(expected)) != 1 {
		log.Printf("[WARN]  初始化令牌错误: IP=%s", clientIP)
		RespondErrorMsg(c, http.StatusUnauthorized, "初始化令牌错误，请查看容器启动日志获取")
		return
	}

	if msg := validateNewPassword(req.Password); msg != "" {
		RespondErrorMsg(c, http.StatusBadRequest, msg)
		return
	}

	// bcrypt 落库 + 热更新内存，退出 Setup 模式
	hash := HashAdminPassword(req.Password)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.SaveAdminPasswordHash(ctx, string(hash)); err != nil {
		log.Printf("[ERROR] 初始化密码入库失败: %v", err)
		RespondErrorMsg(c, http.StatusInternalServerError, "internal error")
		return
	}

	s.passwordMux.Lock()
	s.passwordHash = hash
	s.setupToken = ""
	s.passwordMux.Unlock()

	s.loginRateLimiter.RecordSuccess(clientIP)
	log.Printf("[INFO] 初始化完成：管理密码已设置（IP=%s）", clientIP)

	// 设完即登录：直接签发会话，前端跳转首页
	s.issueSession(c, clientIP)
}

// HandleChangePassword 修改管理密码（需登录）
// POST /admin/password/change  {old_password, new_password, totp_code}
// 校验链：旧密码 → (2FA已激活时强制)验证码 → 新密码规则 → 落库 → 吊销全部会话
func (s *AuthService) HandleChangePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required"`
		TotpCode    string `json:"totp_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "请填写当前密码和新密码")
		return
	}

	// 1. 验证旧密码
	s.passwordMux.RLock()
	currentHash := s.passwordHash
	s.passwordMux.RUnlock()
	if err := bcrypt.CompareHashAndPassword(currentHash, []byte(req.OldPassword)); err != nil {
		RespondErrorMsg(c, http.StatusUnauthorized, "当前密码错误")
		return
	}

	// 2. 已绑定两步验证时强制验证（防止会话被盗后被改密码夺取账户）
	rec, err := s.load2FA()
	if err != nil {
		log.Printf("[ERROR] 加载2FA配置失败: %v", err)
		RespondErrorMsg(c, http.StatusInternalServerError, "internal error")
		return
	}
	if rec.IsActive() {
		if req.TotpCode == "" {
			RespondErrorMsg(c, http.StatusBadRequest, "已开启两步验证，请输入动态验证码")
			return
		}
		if !check2FACode(rec, req.TotpCode, s.tokenEncryptionKey) {
			RespondErrorMsg(c, http.StatusUnauthorized, "验证码错误")
			return
		}
		// 持久化防重放时间片 / 已消费的恢复码
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := s.store.SaveAdmin2FA(ctx, rec); err != nil {
			log.Printf("[WARN]  保存2FA状态失败: %v", err)
		}
		cancel()
	}

	// 3. 新密码规则
	if msg := validateNewPassword(req.NewPassword); msg != "" {
		RespondErrorMsg(c, http.StatusBadRequest, msg)
		return
	}
	if req.NewPassword == req.OldPassword {
		RespondErrorMsg(c, http.StatusBadRequest, "新密码不能与当前密码相同")
		return
	}

	// 4. 落库 + 热更新内存
	hash := HashAdminPassword(req.NewPassword)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.SaveAdminPasswordHash(ctx, string(hash)); err != nil {
		log.Printf("[ERROR] 新密码入库失败: %v", err)
		RespondErrorMsg(c, http.StatusInternalServerError, "internal error")
		return
	}

	s.passwordMux.Lock()
	s.passwordHash = hash
	s.passwordMux.Unlock()

	// 5. 吊销全部会话（含当前会话），所有端需用新密码重新登录
	s.RevokeAllSessions()
	clearAdminSessionCookie(c)

	log.Printf("[INFO] 管理密码已修改，全部会话已吊销（IP=%s）", c.ClientIP())
	RespondJSON(c, http.StatusOK, gin.H{"message": "密码已修改，请重新登录"})
}

// RevokeAllSessions 吊销全部管理员会话（内存 + 数据库）
func (s *AuthService) RevokeAllSessions() {
	s.tokensMux.Lock()
	s.validTokens = make(map[string]time.Time)
	s.tokensMux.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.DeleteAllAdminSessions(ctx); err != nil {
		log.Printf("[WARN]  清除数据库会话失败: %v", err)
	}
}
