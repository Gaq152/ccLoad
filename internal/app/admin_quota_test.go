package app

import (
	"testing"
)

// TestValidateQuotaURL_IPv4MappedIPv6 测试 IPv4-mapped IPv6 地址的 SSRF 校验
// 验证 172.16.0.0/12 私网段的完整覆盖
func TestValidateQuotaURL_IPv4MappedIPv6(t *testing.T) {
	testCases := []struct {
		name      string
		url       string
		shouldErr bool
		reason    string
	}{
		// === 应该被拦截的私网地址 ===
		{
			name:      "IPv4-mapped-172.16.0.1-起始",
			url:       "http://[::ffff:172.16.0.1]/api/quota",
			shouldErr: true,
			reason:    "172.16.0.0/12 起始地址应该被拦截",
		},
		{
			name:      "IPv4-mapped-172.20.0.1-中间",
			url:       "http://[::ffff:172.20.0.1]/api/quota",
			shouldErr: true,
			reason:    "172.20.x.x 属于 172.16.0.0/12 私网段",
		},
		{
			name:      "IPv4-mapped-172.25.100.50-中间",
			url:       "http://[::ffff:172.25.100.50]/api/quota",
			shouldErr: true,
			reason:    "172.25.x.x 属于 172.16.0.0/12 私网段",
		},
		{
			name:      "IPv4-mapped-172.31.255.255-结束",
			url:       "http://[::ffff:172.31.255.255]/api/quota",
			shouldErr: true,
			reason:    "172.16.0.0/12 结束地址应该被拦截",
		},
		{
			name:      "IPv4-mapped-10.0.0.1",
			url:       "http://[::ffff:10.0.0.1]/api/quota",
			shouldErr: true,
			reason:    "10.0.0.0/8 私网段",
		},
		{
			name:      "IPv4-mapped-192.168.1.1",
			url:       "http://[::ffff:192.168.1.1]/api/quota",
			shouldErr: true,
			reason:    "192.168.0.0/16 私网段",
		},
		{
			name:      "IPv4-mapped-127.0.0.1",
			url:       "http://[::ffff:127.0.0.1]/api/quota",
			shouldErr: true,
			reason:    "127.0.0.0/8 本地回环",
		},
		{
			name:      "IPv4-mapped-169.254.1.1",
			url:       "http://[::ffff:169.254.1.1]/api/quota",
			shouldErr: true,
			reason:    "169.254.0.0/16 链路本地地址",
		},

		// === 应该被放行的公网地址 ===
		{
			name:      "IPv4-mapped-172.2.0.1-公网",
			url:       "http://[::ffff:172.2.0.1]/api/quota",
			shouldErr: false,
			reason:    "172.2.x.x 是公网段，不应该被拦截",
		},
		{
			name:      "IPv4-mapped-172.15.0.1-边界前",
			url:       "http://[::ffff:172.15.0.1]/api/quota",
			shouldErr: false,
			reason:    "172.15.x.x 在 172.16.0.0/12 之前",
		},
		{
			name:      "IPv4-mapped-172.32.0.1-边界后",
			url:       "http://[::ffff:172.32.0.1]/api/quota",
			shouldErr: false,
			reason:    "172.32.x.x 在 172.16.0.0/12 之后",
		},
		{
			name:      "IPv4-mapped-8.8.8.8-公网DNS",
			url:       "http://[::ffff:8.8.8.8]/api/quota",
			shouldErr: false,
			reason:    "8.8.8.8 是公网地址",
		},
		{
			name:      "IPv4-mapped-1.1.1.1-公网DNS",
			url:       "http://[::ffff:1.1.1.1]/api/quota",
			shouldErr: false,
			reason:    "1.1.1.1 是公网地址",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateQuotaURL(tc.url)
			if tc.shouldErr {
				if err == nil {
					t.Errorf("%s: 期望被拦截但通过了校验", tc.reason)
				}
			} else {
				if err != nil {
					t.Errorf("%s: 期望通过但被拦截了，错误: %v", tc.reason, err)
				}
			}
		})
	}
}

// TestValidateQuotaURL_BasicSSRF 测试基本的 SSRF 防护
func TestValidateQuotaURL_BasicSSRF(t *testing.T) {
	testCases := []struct {
		name      string
		url       string
		shouldErr bool
	}{
		// 应该被拦截
		{"localhost", "http://localhost/api", true},
		{"127.0.0.1", "http://127.0.0.1/api", true},
		{"0.0.0.0", "http://0.0.0.0/api", true},
		{"10.0.0.1", "http://10.0.0.1/api", true},
		{"192.168.1.1", "http://192.168.1.1/api", true},
		{"172.16.0.1", "http://172.16.0.1/api", true},
		{"169.254.169.254", "http://169.254.169.254/latest/meta-data", true},
		{"IPv6-loopback", "http://[::1]/api", true},
		{"IPv6-unspecified", "http://[::]/api", true},
		{"IPv6-ULA", "http://[fc00::1]/api", true},
		{"IPv6-link-local", "http://[fe80::1]/api", true},

		// 应该被放行
		{"公网域名", "https://api.anthropic.com/v1/messages", false},
		{"公网IP", "http://8.8.8.8/api", false},
		{"172.2.x.x公网", "http://172.2.0.1/api", false},
		{"172.32.x.x公网", "http://172.32.0.1/api", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateQuotaURL(tc.url)
			if tc.shouldErr && err == nil {
				t.Errorf("期望被拦截但通过了: %s", tc.url)
			}
			if !tc.shouldErr && err != nil {
				t.Errorf("期望通过但被拦截了: %s, 错误: %v", tc.url, err)
			}
		})
	}
}

// TestValidateQuotaURL_InvalidFormats 测试无效格式的 URL
func TestValidateQuotaURL_InvalidFormats(t *testing.T) {
	testCases := []struct {
		name string
		url  string
	}{
		{"空URL", ""},
		{"无协议", "api.example.com/path"},
		{"非HTTP协议", "ftp://example.com/file"},
		{"包含用户信息", "http://user:pass@example.com/api"},
		{"无效URL格式", "http://[invalid"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateQuotaURL(tc.url)
			if err == nil {
				t.Errorf("期望返回错误但通过了: %s", tc.url)
			}
		})
	}
}
