package model

// Admin2FA 状态常量
const (
	Admin2FAStatusPending = 0 // 待激活：已生成 secret 但未回填验证码确认，不影响登录
	Admin2FAStatusActive  = 1 // 已激活：登录需要 TOTP 验证码
)

// Admin2FA 管理员两步验证(TOTP)配置
// 单管理员场景，admin_2fa 表恒为单行(id=1)
type Admin2FA struct {
	Secret          string   `json:"-"`                // TOTP secret(base32)；SecretEncrypted=true 时为 AES-256-GCM 加密后的 base64
	SecretEncrypted bool     `json:"secret_encrypted"` // secret 是否加密存储(取决于 CCLOAD_TOKEN_KEY 是否配置)
	Status          int      `json:"status"`           // 0=待激活 1=已激活
	RecoveryCodes   []string `json:"-"`                // 恢复码 SHA256 哈希列表(用掉即移除，与加密密钥无关，作为换密钥后的逃生通道)
	LastUsedStep    int64    `json:"-"`                // 最近成功验证的 TOTP 时间片(防 30 秒窗口内重放)
	CreatedAt       int64    `json:"created_at"`       // 创建时间(Unix秒)
	ActivatedAt     int64    `json:"activated_at"`     // 激活时间(Unix秒，0=未激活)
}

// IsActive 是否已激活(登录需要两步验证)
func (a *Admin2FA) IsActive() bool {
	return a != nil && a.Status == Admin2FAStatusActive
}
