package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"ccLoad/internal/model"

	"github.com/gin-gonic/gin"
)

// QuotaFetchRequest 用量查询请求（可选，用于测试未保存的配置）
type QuotaFetchRequest struct {
	QuotaConfig *model.QuotaConfig `json:"quota_config,omitempty"`
}

// handleQuotaFetch 代理渠道用量查询请求
// POST /admin/channels/:id/quota/fetch
//
// 功能：
//  1. 优先使用请求体中的 quota_config（测试模式）
//  2. 无请求体时，读取数据库中渠道的 quota_config（正常轮询模式）
//  3. 构建HTTP请求（URL、Headers、Body）
//  4. 执行请求（5秒超时）
//  5. 返回原始响应给前端
func (s *Server) handleQuotaFetch(c *gin.Context) {
	// 解析渠道ID（0 表示测试模式，仅支持请求体配置）
	idStr := c.Param("id")
	channelID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid channel id")
		return
	}

	var qc *model.QuotaConfig

	// 尝试从请求体获取配置（测试模式）
	var fetchReq QuotaFetchRequest
	if err := c.ShouldBindJSON(&fetchReq); err == nil && fetchReq.QuotaConfig != nil {
		// 使用请求体中的配置
		qc = fetchReq.QuotaConfig
	} else if channelID > 0 {
		// 无请求体，从数据库读取（正常轮询模式）
		config, err := s.store.GetConfig(c.Request.Context(), channelID)
		if err != nil {
			RespondErrorMsg(c, http.StatusNotFound, "channel not found")
			return
		}

		// [FIX] 渠道禁用不阻止用量查询（手动刷新仍可用）
		// 禁用渠道只影响前端自动轮询，不影响手动刷新

		if config.QuotaConfig == nil || !config.QuotaConfig.Enabled {
			RespondErrorMsg(c, http.StatusBadRequest, "quota monitoring not enabled for this channel")
			return
		}
		qc = config.QuotaConfig

		// [FIX] 官方预设：检查并刷新过期的 Token
		tokenRefreshed := false // 标记是否刷新了 Token
		switch config.Preset {
		case "kiro":
			// Kiro 预设：刷新 Token
			apiKeys, err := s.getAPIKeys(c.Request.Context(), channelID)
			if err == nil && len(apiKeys) > 0 {
				firstKey := apiKeys[0]

				// 检查 Token 是否需要刷新
				if IsKiroTokenExpiringSoon(firstKey.TokenExpiresAt) {
					// 构建认证配置
					var kiroConfig *KiroAuthConfig

					// 尝试从 IDToken 解析 IdC 认证信息
					if firstKey.IDToken != "" {
						kiroConfig = ParseKiroAuthConfig(firstKey.IDToken)
					}

					// 如果 IDToken 解析失败，使用 Social 方式
					if kiroConfig == nil && firstKey.RefreshToken != "" {
						kiroConfig = &KiroAuthConfig{
							AuthType:     KiroAuthMethodSocial,
							RefreshToken: firstKey.RefreshToken,
						}
					}

					if kiroConfig != nil {
						// 尝试刷新 Token
						newAccessToken, newExpiresAt, err := s.RefreshKiroTokenIfNeeded(
							c.Request.Context(),
							channelID,
							0, // keyIndex
							kiroConfig,
							firstKey.AccessToken,
							firstKey.TokenExpiresAt,
						)
						if err == nil && newAccessToken != "" {
							// 刷新成功，更新 RequestHeaders 中的 Authorization
							if qc.RequestHeaders == nil {
								qc.RequestHeaders = make(map[string]string)
							}
							qc.RequestHeaders["Authorization"] = "Bearer " + newAccessToken
							tokenRefreshed = true
							log.Printf("[INFO] [Kiro Quota] Token 已自动刷新 (channel=%d, expiresAt=%d)", channelID, newExpiresAt)
						} else {
							log.Printf("[WARN] [Kiro Quota] Token 刷新失败 (channel=%d): %v", channelID, err)
						}
					}
				}
			}

		case "official":
			// Codex/Gemini 预设：刷新 Token
			apiKeys, err := s.getAPIKeys(c.Request.Context(), channelID)
			if err == nil && len(apiKeys) > 0 {
				firstKey := apiKeys[0]
				channelType := config.GetChannelType()

				// Codex 预设
				if channelType == "codex" {
					_, oauthToken, isOAuth := ParseAPIKeyOrOAuth(firstKey.IDToken)
					if isOAuth && oauthToken != nil && oauthToken.IsTokenExpiringSoon() {
						newAccessToken, _, err := s.RefreshCodexTokenIfNeeded(
							c.Request.Context(),
							channelID,
							0, // keyIndex
							oauthToken,
						)
						if err == nil && newAccessToken != "" {
							// 刷新成功，更新 RequestHeaders 中的 Authorization
							if qc.RequestHeaders == nil {
								qc.RequestHeaders = make(map[string]string)
							}
							qc.RequestHeaders["Authorization"] = "Bearer " + newAccessToken
							tokenRefreshed = true
							log.Printf("[INFO] [Codex Quota] Token 已自动刷新 (channel=%d)", channelID)
						} else {
							log.Printf("[WARN] [Codex Quota] Token 刷新失败 (channel=%d): %v", channelID, err)
						}
					}
				}

				// Gemini 预设
				if channelType == "gemini" {
					_, oauthToken, isOAuth := ParseGeminiAPIKeyOrOAuth(firstKey.IDToken)
					if isOAuth && oauthToken != nil && oauthToken.IsTokenExpiringSoon() {
						newAccessToken, _, err := s.RefreshGeminiTokenIfNeeded(
							c.Request.Context(),
							channelID,
							0, // keyIndex
							oauthToken,
						)
						if err == nil && newAccessToken != "" {
							// 刷新成功，更新 RequestHeaders 中的 Authorization
							if qc.RequestHeaders == nil {
								qc.RequestHeaders = make(map[string]string)
							}
							qc.RequestHeaders["Authorization"] = "Bearer " + newAccessToken
							tokenRefreshed = true
							log.Printf("[INFO] [Gemini Quota] Token 已自动刷新 (channel=%d)", channelID)
						} else {
							log.Printf("[WARN] [Gemini Quota] Token 刷新失败 (channel=%d): %v", channelID, err)
						}
					}
				}
			}
		}

		// 如果刷新了 Token，需要保存更新后的 QuotaConfig 到数据库
		if tokenRefreshed {
			config.QuotaConfig = qc
			_, err := s.store.UpdateConfig(c.Request.Context(), channelID, config)
			if err != nil {
				log.Printf("[WARN] [Quota] 保存更新后的 QuotaConfig 失败 (channel=%d): %v", channelID, err)
			} else {
				log.Printf("[INFO] [Quota] 已保存更新后的 Token 到 QuotaConfig (channel=%d)", channelID)
			}
		}
	} else {
		// channelID=0 且无请求体配置
		RespondErrorMsg(c, http.StatusBadRequest, "quota_config is required for test mode")
		return
	}

	// 验证请求URL（安全检查：防止SSRF）
	if qc.RequestURL == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "quota config missing request_url")
		return
	}

	// URL安全验证
	if err := validateQuotaURL(qc.RequestURL); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid request_url: "+err.Error())
		return
	}

	// 验证HTTP方法
	method := strings.ToUpper(qc.GetRequestMethod())
	if method != "GET" && method != "POST" {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid request method, only GET/POST allowed")
		return
	}

	// acw_sc__v2 反爬模式：直接调用外部服务完成用量查询
	if qc.ChallengeMode == "acw_sc__v2" {
		s.handleAcwScV2QuotaFetch(c, qc)
		return
	}

	// 构建HTTP请求
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var bodyReader io.Reader
	if method == "POST" && qc.RequestBody != "" {
		// 限制请求体大小（最大64KB）
		if len(qc.RequestBody) > 64*1024 {
			RespondErrorMsg(c, http.StatusBadRequest, "request_body too large (max 64KB)")
			return
		}
		bodyReader = bytes.NewBufferString(qc.RequestBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, qc.RequestURL, bodyReader)
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "failed to create request: "+err.Error())
		return
	}

	// 设置默认请求头（模拟浏览器以降低被 Cloudflare 拦截概率）
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")

	// 添加自定义请求头（可覆盖默认值，但过滤敏感头）
	blockedHeaders := map[string]bool{
		"host":              true,
		"content-length":    true,
		"transfer-encoding": true,
		"connection":        true,
		"upgrade":           true,
		"te":                true,
		"trailer":           true,
	}
	for key, value := range qc.RequestHeaders {
		lowerKey := strings.ToLower(key)
		// 阻止敏感头和 Proxy-* 头
		if blockedHeaders[lowerKey] || strings.HasPrefix(lowerKey, "proxy-") {
			continue
		}
		req.Header.Set(key, value)
	}

	// 执行请求（禁用自动重定向和自动解压，防止重定向到内网地址和 gzip 炸弹）
	// TLS 配置：兼容更多服务器（包括 Cloudflare 等）
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false, // 保持证书验证
		CipherSuites: []uint16{
			// TLS 1.3 密码套件由 Go 自动选择，无需指定
			// TLS 1.2 兼容性密码套件
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		},
		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
			tls.CurveP384,
		},
	}

	client := &http.Client{
		Timeout: 10 * time.Second, // 增加超时时间
		Transport: &http.Transport{
			DisableCompression: true,      // 禁用自动解压
			Proxy:              nil,       // 禁用代理
			TLSClientConfig:    tlsConfig,
			ForceAttemptHTTP2:  true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 不跟随重定向
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// 读取响应体（限制大小，防止OOM，支持 gzip 解压）
	bodyBytes, err := readResponseBody(resp)
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "failed to read response body: "+err.Error())
		return
	}

	// 注意：acw_sc__v2 反爬挑战检测逻辑已废弃
	// 当 ChallengeMode="acw_sc__v2" 时，在第 216 行已提前返回，使用外部服务方式处理
	// 原有的本地挑战检测代码（第 316-363 行）已删除

	// 提取响应头（只保留常用的）
	respHeaders := make(map[string]string)
	for _, key := range []string{"Content-Type", "X-RateLimit-Remaining", "X-RateLimit-Limit"} {
		if val := resp.Header.Get(key); val != "" {
			respHeaders[key] = val
		}
	}

	RespondJSON(c, http.StatusOK, gin.H{
		"status_code": resp.StatusCode,
		"headers":     respHeaders,
		"body":        string(bodyBytes),
	})
}

// handleAcwScV2QuotaFetch 处理 acw_sc__v2 反爬模式的用量查询
// 直接调用外部 Deno 服务完成挑战处理和用量查询
func (s *Server) handleAcwScV2QuotaFetch(c *gin.Context, qc *model.QuotaConfig) {
	serviceURL := os.Getenv("ANYROUTER_COOKIE_SERVICE")
	if serviceURL == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "ANYROUTER_COOKIE_SERVICE 环境变量未设置")
		return
	}

	// 从请求头配置中提取 Cookie 和 New-Api-User
	cookieHeader := ""
	userID := ""
	for key, value := range qc.RequestHeaders {
		switch strings.ToLower(key) {
		case "cookie":
			cookieHeader = value
		case "new-api-user":
			userID = value
		}
	}

	if cookieHeader == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "缺少 Cookie 请求头配置")
		return
	}
	if userID == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "缺少 New-Api-User 请求头配置")
		return
	}

	// 从 Cookie 中提取 session（剔除 acw_sc__v2）
	session := extractSessionFromCookie(cookieHeader)
	if session == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "Cookie 中未找到 session")
		return
	}

	// 解析目标 URL 获取路径
	parsedURL, err := url.Parse(qc.RequestURL)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "无效的请求 URL")
		return
	}
	targetPath := parsedURL.Path
	if targetPath == "" {
		targetPath = "/api/user/self"
	}

	// 调用外部服务的 /api/quota 端点
	reqURL := strings.TrimSuffix(serviceURL, "/") + "/api/quota"

	reqBody := map[string]string{
		"session": session,
		"user_id": userID,
		"target":  targetPath,
	}
	reqBodyBytes, _ := json.Marshal(reqBody)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(reqBodyBytes))
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "创建请求失败: "+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadGateway, "请求外部服务失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "读取响应失败: "+err.Error())
		return
	}

	// 解析外部服务响应
	var extResp struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   *string         `json:"error"`
	}
	if err := json.Unmarshal(bodyBytes, &extResp); err != nil {
		RespondErrorMsg(c, http.StatusBadGateway, "解析外部服务响应失败: "+err.Error())
		return
	}

	if !extResp.Success {
		errMsg := "外部服务返回错误"
		if extResp.Error != nil {
			errMsg = *extResp.Error
		}
		RespondErrorMsg(c, http.StatusBadGateway, errMsg)
		return
	}

	// 返回与原有格式一致的响应
	RespondJSON(c, http.StatusOK, gin.H{
		"status_code": 200,
		"headers":     map[string]string{"Content-Type": "application/json"},
		"body":        string(extResp.Data),
	})
}

// extractSessionFromCookie 从 Cookie 字符串中提取 session 值，自动剔除 acw_sc__v2
// 支持格式：
//   - "session=xxx"
//   - "session=xxx; acw_sc__v2=yyy"
//   - "acw_sc__v2=yyy; session=xxx"
//   - "other=aaa; session=xxx; acw_sc__v2=yyy"
func extractSessionFromCookie(cookieHeader string) string {
	// 按分号分割
	parts := strings.Split(cookieHeader, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		// 跳过 acw_sc__v2
		if strings.HasPrefix(part, "acw_sc__v2=") {
			continue
		}
		// 提取 session
		if strings.HasPrefix(part, "session=") {
			return strings.TrimPrefix(part, "session=")
		}
	}
	return ""
}

// validateQuotaURL 验证用量查询URL的安全性（防止SSRF）
// 注意：此函数基于字符串匹配，无法防御 DNS 重绑定攻击
// 完整的 SSRF 防护需要在 DialContext 中检查解析后的 IP
func validateQuotaURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format")
	}

	// 只允许 http/https 协议
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http/https schemes allowed")
	}

	// 不允许空 host
	if u.Host == "" {
		return fmt.Errorf("host is required")
	}

	// 不允许包含用户信息
	if u.User != nil {
		return fmt.Errorf("userinfo not allowed in URL")
	}

	// 提取主机名（去掉端口和方括号）
	hostname := u.Hostname()
	lowerHost := strings.ToLower(hostname)

	// === IPv4 本地和私有地址 ===

	// 阻止 localhost
	if lowerHost == "localhost" || lowerHost == "0.0.0.0" {
		return fmt.Errorf("localhost not allowed")
	}

	// 阻止 127.0.0.0/8 段
	if strings.HasPrefix(hostname, "127.") {
		return fmt.Errorf("localhost not allowed")
	}

	// 阻止内网地址段（RFC 1918）
	if strings.HasPrefix(hostname, "10.") ||
		strings.HasPrefix(hostname, "192.168.") {
		return fmt.Errorf("private network addresses not allowed")
	}

	// 阻止 172.16.0.0/12（172.16.x.x - 172.31.x.x）
	if strings.HasPrefix(hostname, "172.") {
		parts := strings.Split(hostname, ".")
		if len(parts) >= 2 {
			if second := parts[1]; len(second) <= 2 {
				// 简单检查：16-31
				if second == "16" || second == "17" || second == "18" || second == "19" ||
					second == "20" || second == "21" || second == "22" || second == "23" ||
					second == "24" || second == "25" || second == "26" || second == "27" ||
					second == "28" || second == "29" || second == "30" || second == "31" {
					return fmt.Errorf("private network addresses not allowed")
				}
			}
		}
	}

	// 阻止链路本地地址（169.254.x.x，包括云元数据服务）
	if strings.HasPrefix(hostname, "169.254.") {
		return fmt.Errorf("link-local addresses not allowed")
	}

	// 阻止 100.64.0.0/10 (CGNAT: 100.64.x.x - 100.127.x.x)
	if strings.HasPrefix(hostname, "100.") {
		parts := strings.Split(hostname, ".")
		if len(parts) >= 2 {
			if second := parts[1]; len(second) <= 3 {
				// 64-127 都属于 CGNAT
				if second == "64" || second == "65" || second == "66" || second == "67" ||
					second == "68" || second == "69" || second == "70" || second == "71" ||
					second == "72" || second == "73" || second == "74" || second == "75" ||
					second == "76" || second == "77" || second == "78" || second == "79" ||
					second == "80" || second == "81" || second == "82" || second == "83" ||
					second == "84" || second == "85" || second == "86" || second == "87" ||
					second == "88" || second == "89" || second == "90" || second == "91" ||
					second == "92" || second == "93" || second == "94" || second == "95" ||
					second == "96" || second == "97" || second == "98" || second == "99" ||
					second == "100" || second == "101" || second == "102" || second == "103" ||
					second == "104" || second == "105" || second == "106" || second == "107" ||
					second == "108" || second == "109" || second == "110" || second == "111" ||
					second == "112" || second == "113" || second == "114" || second == "115" ||
					second == "116" || second == "117" || second == "118" || second == "119" ||
					second == "120" || second == "121" || second == "122" || second == "123" ||
					second == "124" || second == "125" || second == "126" || second == "127" {
					return fmt.Errorf("CGNAT addresses not allowed")
				}
			}
		}
	}

	// === IPv6 地址 ===

	// 阻止 IPv6 loopback (::1)
	if lowerHost == "::1" || lowerHost == "0:0:0:0:0:0:0:1" {
		return fmt.Errorf("localhost not allowed")
	}

	// 阻止 IPv6 unspecified (::)
	if lowerHost == "::" || lowerHost == "0:0:0:0:0:0:0:0" {
		return fmt.Errorf("unspecified address not allowed")
	}

	// 阻止 IPv6 ULA (fc00::/7 = fc00:: - fdff::)
	if strings.HasPrefix(lowerHost, "fc") || strings.HasPrefix(lowerHost, "fd") {
		return fmt.Errorf("IPv6 ULA addresses not allowed")
	}

	// 阻止 IPv6 link-local (fe80::/10)
	if strings.HasPrefix(lowerHost, "fe8") || strings.HasPrefix(lowerHost, "fe9") ||
		strings.HasPrefix(lowerHost, "fea") || strings.HasPrefix(lowerHost, "feb") {
		return fmt.Errorf("IPv6 link-local addresses not allowed")
	}

	// 阻止 IPv4-mapped IPv6 地址 (::ffff:x.x.x.x)
	if strings.HasPrefix(lowerHost, "::ffff:") {
		ipv4Part := strings.TrimPrefix(lowerHost, "::ffff:")
		// 递归检查内嵌的 IPv4 地址
		if strings.HasPrefix(ipv4Part, "127.") ||
			strings.HasPrefix(ipv4Part, "10.") ||
			strings.HasPrefix(ipv4Part, "192.168.") ||
			strings.HasPrefix(ipv4Part, "169.254.") {
			return fmt.Errorf("IPv4-mapped private addresses not allowed")
		}

		// 检查 172.16.0.0/12 (172.16.x.x - 172.31.x.x)
		if strings.HasPrefix(ipv4Part, "172.") {
			parts := strings.Split(ipv4Part, ".")
			if len(parts) >= 2 {
				second, err := strconv.Atoi(parts[1])
				if err == nil && second >= 16 && second <= 31 {
					return fmt.Errorf("IPv4-mapped private addresses not allowed")
				}
			}
		}
	}

	return nil
}

// readResponseBody 读取 HTTP 响应体，自动处理 gzip 解压
// 限制最大读取 1MB，防止 OOM
func readResponseBody(resp *http.Response) ([]byte, error) {
	var reader io.Reader = resp.Body

	// 检查是否为 gzip 压缩
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip reader 创建失败: %v", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	return io.ReadAll(io.LimitReader(reader, 1<<20)) // 最大 1MB
}

// isAcwScV2Challenge 检测响应是否为 acw_sc__v2 反爬挑战页面
// 特征：Content-Type 为 text/html 且响应体包含 acw_sc__v2 关键字
func isAcwScV2Challenge(contentType string, body []byte) bool {
	if !strings.Contains(contentType, "text/html") {
		return false
	}
	// 检查响应体是否包含挑战脚本特征
	bodyStr := string(body)
	return strings.Contains(bodyStr, "acw_sc__v2") || strings.Contains(bodyStr, "arg1=")
}

// challengeCookieResponse 外部服务返回的 Cookie 响应格式
type challengeCookieResponse struct {
	Target string  `json:"target"`
	Cookie string  `json:"cookie"`
	Error  *string `json:"error"`
}

// fetchChallengeCookie 调用外部 Deno 服务获取 acw_sc__v2 动态 cookie
// 环境变量 ANYROUTER_COOKIE_SERVICE 指定服务地址
func fetchChallengeCookie(targetURL string) (string, error) {
	serviceURL := os.Getenv("ANYROUTER_COOKIE_SERVICE")
	if serviceURL == "" {
		return "", fmt.Errorf("ANYROUTER_COOKIE_SERVICE 环境变量未设置")
	}

	// 解析目标 URL 提取路径
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return "", fmt.Errorf("无效的目标 URL: %v", err)
	}

	// 构建服务请求 URL: {serviceURL}/debug-cookie?target={path}
	reqURL := strings.TrimSuffix(serviceURL, "/") + "/debug-cookie?target=" + url.QueryEscape(parsedURL.Path)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %v", err)
	}

	// 使用支持代理和宽松 TLS 的客户端（Deno Deploy 在某些代理下证书链验证可能失败）
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // 外部 Cookie 服务允许跳过验证
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求外部服务失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("外部服务返回错误状态码: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16)) // 最大64KB
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %v", err)
	}

	var cookieResp challengeCookieResponse
	if err := json.Unmarshal(body, &cookieResp); err != nil {
		return "", fmt.Errorf("解析响应失败: %v", err)
	}

	if cookieResp.Error != nil && *cookieResp.Error != "" {
		return "", fmt.Errorf("外部服务错误: %s", *cookieResp.Error)
	}

	if cookieResp.Cookie == "" {
		return "", fmt.Errorf("外部服务未返回 Cookie")
	}

	return cookieResp.Cookie, nil
}
