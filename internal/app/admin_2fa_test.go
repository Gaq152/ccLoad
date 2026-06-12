package app

import (
	"strings"
	"testing"
	"time"

	"ccLoad/internal/crypto"
	"ccLoad/internal/model"

	"github.com/pquerna/otp/totp"
)

// genTestSecret 生成测试用 TOTP secret
func genTestSecret(t *testing.T) string {
	t.Helper()
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "ccLoad-test",
		AccountName: "admin",
		Period:      totpPeriod,
		Digits:      totpDigits,
	})
	if err != nil {
		t.Fatalf("生成TOTP key失败: %v", err)
	}
	return key.Secret()
}

// genCodeAt 计算指定时间的 TOTP 验证码
func genCodeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
		Period: totpPeriod,
		Digits: totpDigits,
	})
	if err != nil {
		t.Fatalf("生成验证码失败: %v", err)
	}
	return code
}

func TestVerifyTOTPStep(t *testing.T) {
	secret := genTestSecret(t)
	now := time.Now()

	t.Run("正确验证码通过", func(t *testing.T) {
		code := genCodeAt(t, secret, now)
		step, ok := verifyTOTPStep(secret, code, now, 0)
		if !ok {
			t.Fatal("正确验证码应当通过")
		}
		if step != now.Unix()/totpPeriod {
			t.Errorf("命中时间片错误: got %d, want %d", step, now.Unix()/totpPeriod)
		}
	})

	t.Run("错误验证码拒绝", func(t *testing.T) {
		code := genCodeAt(t, secret, now)
		// 构造一个必然不同的6位码
		wrong := "000000"
		if wrong == code {
			wrong = "000001"
		}
		if _, ok := verifyTOTPStep(secret, wrong, now, 0); ok {
			t.Fatal("错误验证码不应通过")
		}
	})

	t.Run("前一时间片容差通过", func(t *testing.T) {
		prevCode := genCodeAt(t, secret, now.Add(-totpPeriod*time.Second))
		if _, ok := verifyTOTPStep(secret, prevCode, now, 0); !ok {
			t.Fatal("±1窗口内的上一片验证码应当通过（时钟偏差容忍）")
		}
	})

	t.Run("同码重放拒绝", func(t *testing.T) {
		code := genCodeAt(t, secret, now)
		step, ok := verifyTOTPStep(secret, code, now, 0)
		if !ok {
			t.Fatal("首次验证应当通过")
		}
		// 用已记录的时间片再次验证同一个码 → 拒绝
		if _, ok := verifyTOTPStep(secret, code, now, step); ok {
			t.Fatal("同一时间片的验证码重放应当被拒绝")
		}
	})
}

func TestRecoveryCodes(t *testing.T) {
	plain, hashes, err := generateRecoveryCodes()
	if err != nil {
		t.Fatalf("生成恢复码失败: %v", err)
	}
	if len(plain) != recoveryCodeCount || len(hashes) != recoveryCodeCount {
		t.Fatalf("恢复码数量错误: plain=%d hashes=%d", len(plain), len(hashes))
	}

	t.Run("格式校验", func(t *testing.T) {
		for _, code := range plain {
			if len(code) != 11 || code[5] != '-' {
				t.Errorf("恢复码格式错误: %s（应为 XXXXX-XXXXX）", code)
			}
		}
	})

	t.Run("正确恢复码消费成功且一次性", func(t *testing.T) {
		rec := &model.Admin2FA{RecoveryCodes: append([]string(nil), hashes...)}
		if !consumeRecoveryCode(rec, plain[0]) {
			t.Fatal("正确恢复码应当通过")
		}
		if len(rec.RecoveryCodes) != recoveryCodeCount-1 {
			t.Fatalf("消费后应移除哈希: got %d", len(rec.RecoveryCodes))
		}
		// 同一个码第二次使用 → 拒绝
		if consumeRecoveryCode(rec, plain[0]) {
			t.Fatal("已消费的恢复码不应再次通过")
		}
	})

	t.Run("小写输入归一化通过", func(t *testing.T) {
		rec := &model.Admin2FA{RecoveryCodes: append([]string(nil), hashes...)}
		if !consumeRecoveryCode(rec, strings.ToLower(plain[1])) {
			t.Fatal("小写形式的恢复码应当归一化后通过")
		}
	})

	t.Run("错误恢复码拒绝", func(t *testing.T) {
		rec := &model.Admin2FA{RecoveryCodes: append([]string(nil), hashes...)}
		if consumeRecoveryCode(rec, "XXXXX-XXXXX") {
			t.Fatal("错误恢复码不应通过")
		}
	})
}

func TestEncryptDecrypt2FASecret(t *testing.T) {
	secret := genTestSecret(t)

	t.Run("无密钥明文存储", func(t *testing.T) {
		stored, encrypted, err := encrypt2FASecret(secret, nil)
		if err != nil {
			t.Fatalf("加密失败: %v", err)
		}
		if encrypted || stored != secret {
			t.Fatal("无密钥时应明文存储")
		}
		rec := &model.Admin2FA{Secret: stored, SecretEncrypted: encrypted}
		got, err := decrypt2FASecret(rec, nil)
		if err != nil || got != secret {
			t.Fatalf("明文roundtrip失败: got=%s err=%v", got, err)
		}
	})

	t.Run("有密钥加密roundtrip", func(t *testing.T) {
		key := crypto.DeriveKey("test-key")
		stored, encrypted, err := encrypt2FASecret(secret, key)
		if err != nil {
			t.Fatalf("加密失败: %v", err)
		}
		if !encrypted || stored == secret {
			t.Fatal("有密钥时应加密存储")
		}
		rec := &model.Admin2FA{Secret: stored, SecretEncrypted: encrypted}
		got, err := decrypt2FASecret(rec, key)
		if err != nil || got != secret {
			t.Fatalf("加密roundtrip失败: got=%s err=%v", got, err)
		}
	})

	t.Run("密钥更换解密失败", func(t *testing.T) {
		key := crypto.DeriveKey("old-key")
		stored, _, err := encrypt2FASecret(secret, key)
		if err != nil {
			t.Fatalf("加密失败: %v", err)
		}
		rec := &model.Admin2FA{Secret: stored, SecretEncrypted: true}
		if _, err := decrypt2FASecret(rec, crypto.DeriveKey("new-key")); err == nil {
			t.Fatal("密钥更换后解密应当失败")
		}
	})
}

func TestCheck2FACode(t *testing.T) {
	secret := genTestSecret(t)
	plain, hashes, err := generateRecoveryCodes()
	if err != nil {
		t.Fatalf("生成恢复码失败: %v", err)
	}

	newRec := func() *model.Admin2FA {
		return &model.Admin2FA{
			Secret:        secret,
			Status:        model.Admin2FAStatusActive,
			RecoveryCodes: append([]string(nil), hashes...),
		}
	}

	t.Run("TOTP路径", func(t *testing.T) {
		rec := newRec()
		code := genCodeAt(t, secret, time.Now())
		if !check2FACode(rec, code, nil) {
			t.Fatal("正确TOTP码应当通过")
		}
		if rec.LastUsedStep == 0 {
			t.Fatal("通过后应记录时间片（防重放）")
		}
		// 同码立即重放 → 拒绝
		if check2FACode(rec, code, nil) {
			t.Fatal("同码重放应当被拒绝")
		}
	})

	t.Run("恢复码路径", func(t *testing.T) {
		rec := newRec()
		if !check2FACode(rec, plain[0], nil) {
			t.Fatal("正确恢复码应当通过")
		}
		if len(rec.RecoveryCodes) != recoveryCodeCount-1 {
			t.Fatal("恢复码应被消费")
		}
	})

	t.Run("错误输入拒绝", func(t *testing.T) {
		rec := newRec()
		if check2FACode(rec, "abcdef", nil) {
			t.Fatal("非法输入不应通过")
		}
	})
}
