package app

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sync"

	"github.com/bytedance/sonic"
)

// KiroFingerprint Kiro 设备指纹
type KiroFingerprint struct {
	// 基础信息
	SDKVersion  string `json:"sdkVersion"`  // aws-sdk-js 版本
	OSType      string `json:"osType"`      // darwin/windows/linux
	OSVersion   string `json:"osVersion"`   // 操作系统版本
	NodeVersion string `json:"nodeVersion"` // Node.js 版本
	KiroVersion string `json:"kiroVersion"` // Kiro IDE 版本
	KiroHash    string `json:"kiroHash"`    // 64位十六进制随机码（核心标识）

	// 地理和语言
	Locale   string `json:"locale"`   // 语言 (zh-CN/en-US等)
	Timezone string `json:"timezone"` // 时区

	// 扩展指纹维度
	AcceptLanguage string `json:"acceptLanguage"` // Accept-Language 头
	AcceptEncoding string `json:"acceptEncoding"` // Accept-Encoding 头
	Platform       string `json:"platform"`       // 平台标识 (Win32/MacIntel/Linux x86_64)
}

// FingerprintManager 指纹管理器（全局单例）
type FingerprintManager struct {
	mu sync.RWMutex
}

var (
	globalFingerprintManager *FingerprintManager
	fingerprintOnce          sync.Once
)

// SDK 版本选项
var sdkVersions = []string{
	"1.0.20", "1.0.21", "1.0.22", "1.0.23", "1.0.24",
	"1.0.25", "1.0.26", "1.0.27",
}

// 操作系统配置
type osProfile struct {
	osType    string
	versions  []string
	locales   []string
	timezones []string
	platform  string
}

var osProfiles = []osProfile{
	{
		osType:    "darwin",
		versions:  []string{"23.0.0", "23.1.0", "23.5.0", "24.0.0", "24.1.0", "24.5.0", "24.6.0", "25.0.0"},
		locales:   []string{"en-US", "en-GB", "zh-CN", "zh-TW", "ja-JP", "ko-KR"},
		timezones: []string{"America/Los_Angeles", "America/New_York", "Europe/London", "Asia/Shanghai", "Asia/Tokyo"},
		platform:  "MacIntel",
	},
	{
		osType:    "windows",
		versions:  []string{"10.0.19041", "10.0.19042", "10.0.19043", "10.0.22000", "10.0.22621", "10.0.22631"},
		locales:   []string{"en-US", "en-GB", "zh-CN", "zh-TW", "ja-JP", "ko-KR"},
		timezones: []string{"America/Los_Angeles", "America/New_York", "America/Chicago", "Europe/London", "Asia/Shanghai"},
		platform:  "Win32",
	},
	{
		osType:    "linux",
		versions:  []string{"5.15.0", "5.19.0", "6.1.0", "6.2.0", "6.5.0", "6.6.0", "6.8.0"},
		locales:   []string{"en-US", "en-GB", "zh-CN", "de-DE", "ru-RU"},
		timezones: []string{"UTC", "America/New_York", "Europe/Berlin", "Asia/Shanghai"},
		platform:  "Linux x86_64",
	},
}

// Node.js 版本选项
var nodeVersions = []string{
	"18.17.0", "18.18.0", "18.19.0", "18.20.0",
	"20.10.0", "20.11.0", "20.12.0", "20.14.0", "20.15.0", "20.16.0", "20.17.0", "20.18.0",
	"22.0.0", "22.1.0", "22.2.0",
}

// Kiro 版本选项
var kiroVersions = []string{
	"0.3.0", "0.3.1", "0.3.2", "0.3.3",
	"0.4.0", "0.5.0", "0.6.0", "0.7.0", "0.8.0",
}

// Accept-Language 模板
var acceptLanguageTemplates = map[string][]string{
	"en-US": {"en-US,en;q=0.9", "en-US,en;q=0.9,zh-CN;q=0.8", "en-US,en;q=0.8"},
	"en-GB": {"en-GB,en;q=0.9,en-US;q=0.8", "en-GB,en;q=0.9"},
	"zh-CN": {"zh-CN,zh;q=0.9,en;q=0.8", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7"},
	"zh-TW": {"zh-TW,zh;q=0.9,en;q=0.8", "zh-TW,zh-CN;q=0.9,zh;q=0.8,en;q=0.7"},
	"ja-JP": {"ja-JP,ja;q=0.9,en;q=0.8", "ja,en-US;q=0.9,en;q=0.8"},
	"ko-KR": {"ko-KR,ko;q=0.9,en;q=0.8", "ko,en-US;q=0.9,en;q=0.8"},
	"de-DE": {"de-DE,de;q=0.9,en;q=0.8", "de,en-US;q=0.9,en;q=0.8"},
	"ru-RU": {"ru-RU,ru;q=0.9,en;q=0.8", "ru,en;q=0.9"},
}

// Accept-Encoding 选项
var acceptEncodings = []string{
	"gzip, deflate, br",
	"br, gzip, deflate",
	"gzip, deflate, br, zstd",
	"gzip, deflate",
}

// GetFingerprintManager 获取全局指纹管理器
func GetFingerprintManager() *FingerprintManager {
	fingerprintOnce.Do(func() {
		globalFingerprintManager = &FingerprintManager{}
	})
	return globalFingerprintManager
}

// GenerateFingerprint 生成新的随机指纹
func (fm *FingerprintManager) GenerateFingerprint() (*KiroFingerprint, error) {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// 随机选择操作系统配置
	osProfile, err := randomChoice(osProfiles)
	if err != nil {
		return nil, err
	}

	locale, err := randomChoice(osProfile.locales)
	if err != nil {
		return nil, err
	}

	timezone, err := randomChoice(osProfile.timezones)
	if err != nil {
		return nil, err
	}

	osVersion, err := randomChoice(osProfile.versions)
	if err != nil {
		return nil, err
	}

	sdkVersion, err := randomChoice(sdkVersions)
	if err != nil {
		return nil, err
	}

	nodeVersion, err := randomChoice(nodeVersions)
	if err != nil {
		return nil, err
	}

	kiroVersion, err := randomChoice(kiroVersions)
	if err != nil {
		return nil, err
	}

	// 根据 locale 选择 Accept-Language
	acceptLangOptions := acceptLanguageTemplates[locale]
	if acceptLangOptions == nil {
		acceptLangOptions = acceptLanguageTemplates["en-US"]
	}
	acceptLang, err := randomChoice(acceptLangOptions)
	if err != nil {
		return nil, err
	}

	acceptEncoding, err := randomChoice(acceptEncodings)
	if err != nil {
		return nil, err
	}

	// 生成 64 位十六进制 Hash
	kiroHash, err := generateHash()
	if err != nil {
		return nil, err
	}

	fp := &KiroFingerprint{
		SDKVersion:     sdkVersion,
		OSType:         osProfile.osType,
		OSVersion:      osVersion,
		NodeVersion:    nodeVersion,
		KiroVersion:    kiroVersion,
		KiroHash:       kiroHash,
		Locale:         locale,
		Timezone:       timezone,
		AcceptLanguage: acceptLang,
		AcceptEncoding: acceptEncoding,
		Platform:       osProfile.platform,
	}

	return fp, nil
}

// ParseFingerprint 从 JSON 字符串解析指纹
func (fm *FingerprintManager) ParseFingerprint(jsonStr string) (*KiroFingerprint, error) {
	if jsonStr == "" {
		return nil, nil
	}

	var fp KiroFingerprint
	if err := sonic.UnmarshalString(jsonStr, &fp); err != nil {
		return nil, fmt.Errorf("parse fingerprint: %w", err)
	}

	return &fp, nil
}

// ToJSON 将指纹序列化为 JSON 字符串
func (fp *KiroFingerprint) ToJSON() (string, error) {
	data, err := sonic.Marshal(fp)
	if err != nil {
		return "", fmt.Errorf("serialize fingerprint: %w", err)
	}
	return string(data), nil
}

// BuildUserAgent 构建 User-Agent 头
func (fp *KiroFingerprint) BuildUserAgent() string {
	return fmt.Sprintf(
		"aws-sdk-js/%s ua/2.1 os/%s#%s lang/js md/nodejs#%s api/codewhispererstreaming#%s m/E KiroIDE-%s-%s",
		fp.SDKVersion, fp.OSType, fp.OSVersion, fp.NodeVersion, fp.SDKVersion, fp.KiroVersion, fp.KiroHash,
	)
}

// BuildAmzUserAgent 构建 x-amz-user-agent 头
func (fp *KiroFingerprint) BuildAmzUserAgent() string {
	return fmt.Sprintf(
		"aws-sdk-js/%s KiroIDE-%s-%s",
		fp.SDKVersion, fp.KiroVersion, fp.KiroHash,
	)
}

// GetSummary 获取指纹摘要信息（用于日志/展示）
func (fp *KiroFingerprint) GetSummary() string {
	return fmt.Sprintf("%s/%s Kiro-%s Hash-%s", fp.OSType, fp.OSVersion, fp.KiroVersion, fp.KiroHash[:8])
}

// ============================================================================
// 辅助函数
// ============================================================================

// randomChoice 从切片中随机选择一个元素（密码学安全）
func randomChoice[T any](options []T) (T, error) {
	var zero T
	if len(options) == 0 {
		return zero, fmt.Errorf("empty options")
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(options))))
	if err != nil {
		return zero, err
	}

	return options[n.Int64()], nil
}

// generateHash 生成 64 位十六进制随机码
func generateHash() (string, error) {
	bytes := make([]byte, 32) // 32 字节 = 64 位十六进制
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}

	const hexChars = "0123456789abcdef"
	hash := make([]byte, 64)
	for i, b := range bytes {
		hash[i*2] = hexChars[b>>4]
		hash[i*2+1] = hexChars[b&0x0f]
	}

	return string(hash), nil
}
