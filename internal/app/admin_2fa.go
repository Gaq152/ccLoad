package app

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"image/png"
	"log"
	"net/http"
	"strings"
	"time"

	"ccLoad/internal/crypto"
	"ccLoad/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTP 参数（与 Google Authenticator 等主流验证器默认值一致）
const (
	totpPeriod = 30 // 时间片长度(秒)
	totpSkew   = 1  // 容差窗口(±1个时间片，容忍手机与服务器时钟偏差)
	totpDigits = otp.DigitsSix

	recoveryCodeCount = 10 // 激活时生成的恢复码数量
)

// ============================================================================
// TOTP / 恢复码 工具函数（包级函数，Server 与 AuthService 共用）
// ============================================================================

// encrypt2FASecret 加密 TOTP secret（未配置 CCLOAD_TOKEN_KEY 时明文存储，与项目现有惯例一致）
func encrypt2FASecret(secret string, encKey []byte) (stored string, encrypted bool, err error) {
	if len(encKey) == 0 {
		return secret, false, nil
	}
	enc, err := crypto.Encrypt(secret, encKey)
	if err != nil {
		return "", false, fmt.Errorf("encrypt totp secret: %w", err)
	}
	return enc, true, nil
}

// decrypt2FASecret 解密 TOTP secret
// 解密失败（如更换了 CCLOAD_TOKEN_KEY）时返回错误，此时 TOTP 必然验证失败，
// 但恢复码基于 SHA256 哈希、与加密密钥无关，仍可用于登录和解绑（逃生通道）
func decrypt2FASecret(rec *model.Admin2FA, encKey []byte) (string, error) {
	if !rec.SecretEncrypted {
		return rec.Secret, nil
	}
	if len(encKey) == 0 {
		return "", fmt.Errorf("secret已加密但未配置CCLOAD_TOKEN_KEY，请用恢复码登录后重新绑定")
	}
	secret, err := crypto.Decrypt(rec.Secret, encKey)
	if err != nil {
		return "", fmt.Errorf("decrypt totp secret(密钥可能已更换，请用恢复码登录后重新绑定): %w", err)
	}
	return secret, nil
}

// verifyTOTPStep 校验 TOTP 验证码（±totpSkew 窗口），返回命中的时间片
// 防重放：命中时间片必须大于 lastUsedStep（同一个码在 30 秒窗口内不能使用两次）
func verifyTOTPStep(secret, code string, now time.Time, lastUsedStep int64) (matchedStep int64, ok bool) {
	curStep := now.Unix() / totpPeriod
	for offset := int64(-totpSkew); offset <= totpSkew; offset++ {
		step := curStep + offset
		expected, err := totp.GenerateCodeCustom(secret, time.Unix(step*totpPeriod, 0), totp.ValidateOpts{
			Period: totpPeriod,
			Digits: totpDigits,
		})
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			if step <= lastUsedStep {
				return 0, false // 重放拒绝
			}
			return step, true
		}
	}
	return 0, false
}

// generateRecoveryCodes 生成恢复码，返回明文列表（仅展示一次）和 SHA256 哈希列表（落库）
// 格式：XXXXX-XXXXX，字符集去除易混淆字符（0/O、1/I/L）
func generateRecoveryCodes() (plain []string, hashes []string, err error) {
	const charset = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	plain = make([]string, 0, recoveryCodeCount)
	hashes = make([]string, 0, recoveryCodeCount)

	for range recoveryCodeCount {
		buf := make([]byte, 10)
		if _, err := rand.Read(buf); err != nil {
			return nil, nil, fmt.Errorf("crypto/rand failed: %w", err)
		}
		chars := make([]byte, 10)
		for i, b := range buf {
			chars[i] = charset[int(b)%len(charset)]
		}
		code := string(chars[:5]) + "-" + string(chars[5:])
		plain = append(plain, code)
		hashes = append(hashes, model.HashToken(code))
	}
	return plain, hashes, nil
}

// consumeRecoveryCode 校验并消费恢复码（一次性，命中后从列表移除）
// 调用方负责将更新后的 rec 落库
func consumeRecoveryCode(rec *model.Admin2FA, code string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	hash := model.HashToken(normalized)
	for i, h := range rec.RecoveryCodes {
		if subtle.ConstantTimeCompare([]byte(h), []byte(hash)) == 1 {
			rec.RecoveryCodes = append(rec.RecoveryCodes[:i], rec.RecoveryCodes[i+1:]...)
			return true
		}
	}
	return false
}

// check2FACode 校验 TOTP 验证码或恢复码
// 6位纯数字按 TOTP 校验，其余按恢复码校验；通过后就地更新 rec（时间片/消费恢复码），由调用方落库
func check2FACode(rec *model.Admin2FA, code string, encKey []byte) bool {
	code = strings.TrimSpace(code)
	if len(code) == 6 && isAllDigits(code) {
		secret, err := decrypt2FASecret(rec, encKey)
		if err != nil {
			log.Printf("[WARN]  2FA secret解密失败: %v", err)
			return false
		}
		step, ok := verifyTOTPStep(secret, code, time.Now(), rec.LastUsedStep)
		if !ok {
			return false
		}
		rec.LastUsedStep = step
		return true
	}
	return consumeRecoveryCode(rec, code)
}

func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ============================================================================
// Admin API Handlers
// ============================================================================

// HandleGet2FAStatus 获取两步验证状态
// GET /admin/2fa/status
func (s *Server) HandleGet2FAStatus(c *gin.Context) {
	rec, err := s.store.GetAdmin2FA(c.Request.Context())
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	RespondJSON(c, http.StatusOK, gin.H{
		"enabled": rec.IsActive(),
		"pending": rec != nil && rec.Status == model.Admin2FAStatusPending,
	})
}

// HandleSetup2FA 生成绑定二维码（secret 以待激活状态落库，回填验证码确认后才生效）
// POST /admin/2fa/setup
func (s *Server) HandleSetup2FA(c *gin.Context) {
	ctx := c.Request.Context()

	rec, err := s.store.GetAdmin2FA(ctx)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	if rec.IsActive() {
		RespondErrorMsg(c, http.StatusBadRequest, "两步验证已开启，请先解绑")
		return
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "ccLoad",
		AccountName: "admin",
		Period:      totpPeriod,
		Digits:      totpDigits,
	})
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 生成二维码 PNG → base64 data URI（前端 <img> 直接展示，无需前端二维码库）
	img, err := key.Image(220, 220)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	stored, encrypted, err := encrypt2FASecret(key.Secret(), s.tokenEncryptionKey)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// 待激活记录（覆盖旧的 pending 记录）
	if err := s.store.SaveAdmin2FA(ctx, &model.Admin2FA{
		Secret:          stored,
		SecretEncrypted: encrypted,
		Status:          model.Admin2FAStatusPending,
		CreatedAt:       time.Now().Unix(),
	}); err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	RespondJSON(c, http.StatusOK, gin.H{
		"qr_image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
		"secret":   key.Secret(), // 手动输入备选（无法扫码时）
	})
}

// HandleActivate2FA 激活两步验证（回填验证码确认扫码成功）
// POST /admin/2fa/activate
func (s *Server) HandleActivate2FA(c *gin.Context) {
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "请输入验证码")
		return
	}

	ctx := c.Request.Context()
	rec, err := s.store.GetAdmin2FA(ctx)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	if rec == nil || rec.Status != model.Admin2FAStatusPending {
		RespondErrorMsg(c, http.StatusBadRequest, "没有待激活的绑定，请先生成二维码")
		return
	}

	secret, err := decrypt2FASecret(rec, s.tokenEncryptionKey)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	step, ok := verifyTOTPStep(secret, strings.TrimSpace(req.Code), time.Now(), rec.LastUsedStep)
	if !ok {
		RespondErrorMsg(c, http.StatusBadRequest, "验证码错误，请确认手机时间准确或重新扫码")
		return
	}

	plain, hashes, err := generateRecoveryCodes()
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	rec.Status = model.Admin2FAStatusActive
	rec.RecoveryCodes = hashes
	rec.LastUsedStep = step
	rec.ActivatedAt = time.Now().Unix()
	if err := s.store.SaveAdmin2FA(ctx, rec); err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	log.Printf("[INFO] 两步验证已激活")
	RespondJSON(c, http.StatusOK, gin.H{
		"recovery_codes": plain, // 明文仅此一次返回
	})
}

// HandleDisable2FA 解绑两步验证（已激活时需验证当前验证码或恢复码）
// POST /admin/2fa/disable
func (s *Server) HandleDisable2FA(c *gin.Context) {
	var req struct {
		Code string `json:"code"`
	}
	_ = c.ShouldBindJSON(&req) // pending 状态解绑无需验证码，body 可为空

	ctx := c.Request.Context()
	rec, err := s.store.GetAdmin2FA(ctx)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	if rec == nil {
		RespondErrorMsg(c, http.StatusBadRequest, "两步验证未开启")
		return
	}

	// 已激活的绑定必须验证（防止他人趁登录会话未失效时关闭2FA）
	if rec.IsActive() {
		if req.Code == "" {
			RespondErrorMsg(c, http.StatusBadRequest, "请输入验证码或恢复码")
			return
		}
		if !check2FACode(rec, req.Code, s.tokenEncryptionKey) {
			RespondErrorMsg(c, http.StatusBadRequest, "验证码错误")
			return
		}
	}

	if err := s.store.DeleteAdmin2FA(ctx); err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	log.Printf("[INFO] 两步验证已解绑")
	RespondJSON(c, http.StatusOK, gin.H{"message": "两步验证已解绑"})
}
