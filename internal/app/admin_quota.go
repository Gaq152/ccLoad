package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ccLoad/internal/model"

	"github.com/dop251/goja"
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

	// 检查是否需要执行反爬挑战
	if qc.ChallengeMode == model.ChallengeModeAcwScV2 {
		statusCode, headers, body, err := s.fetchWithChallenge(ctx, client, req, qc)
		if err != nil {
			RespondErrorMsg(c, http.StatusBadGateway, "challenge request failed: "+err.Error())
			return
		}

		RespondJSON(c, http.StatusOK, gin.H{
			"status_code": statusCode,
			"headers":     headers,
			"body":        body,
		})
		return
	}

	// 普通请求模式（无挑战）
	resp, err := client.Do(req)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadGateway, "upstream request failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// 读取响应体（限制大小，防止OOM）
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 最大1MB
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "failed to read response body: "+err.Error())
		return
	}

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
			strings.HasPrefix(ipv4Part, "169.254.") ||
			strings.HasPrefix(ipv4Part, "172.16.") || strings.HasPrefix(ipv4Part, "172.17.") ||
			strings.HasPrefix(ipv4Part, "172.18.") || strings.HasPrefix(ipv4Part, "172.19.") ||
			strings.HasPrefix(ipv4Part, "172.2") || strings.HasPrefix(ipv4Part, "172.30.") ||
			strings.HasPrefix(ipv4Part, "172.31.") {
			return fmt.Errorf("IPv4-mapped private addresses not allowed")
		}
	}

	return nil
}

// ==================== 反爬挑战模块 ====================
// 用于处理 acw_sc__v2 类型的动态 Cookie 挑战

// fetchWithChallenge 带反爬挑战的请求流程
// 1. 先发送一个 GET 请求获取挑战页面（HTML）
// 2. 提取并执行页面中的 JS 脚本，获取动态 Cookie
// 3. 合并原有 Cookie 和动态 Cookie，重新发送真正的 API 请求
func (s *Server) fetchWithChallenge(ctx context.Context, client *http.Client, originalReq *http.Request, qc *model.QuotaConfig) (int, map[string]string, string, error) {
	// 解析 URL 以获取挑战页面地址（通常是相同域名的根路径或 API 路径）
	parsedURL, err := url.Parse(qc.RequestURL)
	if err != nil {
		return 0, nil, "", fmt.Errorf("invalid request URL: %v", err)
	}

	// 挑战页面 URL（使用 API 相同的 URL）
	challengeURL := qc.RequestURL

	// 构建挑战请求（GET）
	challengeReq, err := http.NewRequestWithContext(ctx, "GET", challengeURL, nil)
	if err != nil {
		return 0, nil, "", fmt.Errorf("failed to create challenge request: %v", err)
	}

	// 复制原始请求头（除了 Content-Type 和 Cookie）
	for key, values := range originalReq.Header {
		lowerKey := strings.ToLower(key)
		// 保留认证相关头部
		if lowerKey == "cookie" || lowerKey == "new-api-user" || lowerKey == "authorization" || lowerKey == "user-agent" || lowerKey == "accept" {
			for _, v := range values {
				challengeReq.Header.Add(key, v)
			}
		}
	}

	// 提取原始请求中的 Cookie
	originalCookies := originalReq.Header.Get("Cookie")

	log.Printf("[Challenge] 开始反爬挑战，URL: %s", challengeURL)

	// 第一次请求：获取挑战页面
	resp, err := client.Do(challengeReq)
	if err != nil {
		return 0, nil, "", fmt.Errorf("challenge request failed: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 最大 1MB
	if err != nil {
		return 0, nil, "", fmt.Errorf("failed to read challenge response: %v", err)
	}

	bodyStr := string(bodyBytes)

	// 检查是否需要挑战（如果直接返回 JSON 或非 HTML，说明不需要挑战）
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		// 不需要挑战，直接返回结果
		log.Printf("[Challenge] 无需挑战，直接返回 JSON 响应")
		headers := extractResponseHeaders(resp)
		return resp.StatusCode, headers, bodyStr, nil
	}

	// 检查是否是 acw_sc__v2 挑战页面
	if !strings.Contains(bodyStr, "acw_sc__v2") {
		// 不是挑战页面，可能是其他错误
		log.Printf("[Challenge] 响应不包含 acw_sc__v2 挑战，返回原始响应")
		headers := extractResponseHeaders(resp)
		return resp.StatusCode, headers, bodyStr, nil
	}

	log.Printf("[Challenge] 检测到 acw_sc__v2 挑战页面，开始提取脚本")

	// 提取并执行脚本获取动态 Cookie
	dynamicCookie, err := extractAndExecuteScript(bodyStr)
	if err != nil {
		return 0, nil, "", fmt.Errorf("failed to extract challenge cookie: %v", err)
	}

	log.Printf("[Challenge] 成功获取动态 Cookie: %s", truncateCookie(dynamicCookie))

	// 合并 Cookie
	var mergedCookies string
	if originalCookies != "" {
		mergedCookies = originalCookies + "; " + dynamicCookie
	} else {
		mergedCookies = dynamicCookie
	}

	// 构建真正的 API 请求
	method := strings.ToUpper(qc.GetRequestMethod())
	var bodyReader io.Reader
	if method == "POST" && qc.RequestBody != "" {
		bodyReader = bytes.NewBufferString(qc.RequestBody)
	}

	apiReq, err := http.NewRequestWithContext(ctx, method, qc.RequestURL, bodyReader)
	if err != nil {
		return 0, nil, "", fmt.Errorf("failed to create API request: %v", err)
	}

	// 复制原始请求头
	for key, values := range originalReq.Header {
		lowerKey := strings.ToLower(key)
		if lowerKey == "cookie" {
			continue // Cookie 将被替换
		}
		for _, v := range values {
			apiReq.Header.Add(key, v)
		}
	}

	// 设置合并后的 Cookie
	apiReq.Header.Set("Cookie", mergedCookies)
	// 设置 Referer（有些反爬检查 Referer）
	apiReq.Header.Set("Referer", fmt.Sprintf("%s://%s/", parsedURL.Scheme, parsedURL.Host))

	log.Printf("[Challenge] 发送带动态 Cookie 的 API 请求")

	// 发送真正的 API 请求
	apiResp, err := client.Do(apiReq)
	if err != nil {
		return 0, nil, "", fmt.Errorf("API request failed: %v", err)
	}
	defer apiResp.Body.Close()

	apiBodyBytes, err := io.ReadAll(io.LimitReader(apiResp.Body, 1<<20))
	if err != nil {
		return 0, nil, "", fmt.Errorf("failed to read API response: %v", err)
	}

	headers := extractResponseHeaders(apiResp)
	log.Printf("[Challenge] 挑战成功，API 返回状态码: %d", apiResp.StatusCode)

	return apiResp.StatusCode, headers, string(apiBodyBytes), nil
}

// extractAndExecuteScript 从 HTML 中提取脚本并执行，获取 acw_sc__v2 Cookie
func extractAndExecuteScript(html string) (string, error) {
	// 查找包含 acw_sc__v2 的 <script> 标签
	// 匹配模式：<script>...acw_sc__v2...</script>
	scriptPattern := regexp.MustCompile(`(?is)<script[^>]*>(.*?acw_sc__v2.*?)</script>`)
	matches := scriptPattern.FindAllStringSubmatch(html, -1)

	if len(matches) == 0 {
		return "", errors.New("no acw_sc__v2 script found in HTML")
	}

	// 尝试每个匹配的脚本
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		scriptContent := match[1]

		// 跳过太短的脚本（可能是误匹配）
		if len(scriptContent) < 50 {
			continue
		}

		// 尝试执行脚本
		cookie, err := executeScriptForCookie(scriptContent)
		if err == nil && cookie != "" {
			return cookie, nil
		}
		log.Printf("[Challenge] 脚本执行失败，尝试下一个: %v", err)
	}

	return "", errors.New("failed to extract cookie from any script")
}

// executeScriptForCookie 在沙箱中执行 JS 脚本，捕获 document.cookie 赋值
func executeScriptForCookie(scriptContent string) (string, error) {
	vm := goja.New()

	// 设置超时（5 秒）
	time.AfterFunc(5*time.Second, func() {
		vm.Interrupt("script execution timeout")
	})

	var cookieValue string

	// 创建沙箱 document 对象
	document := vm.NewObject()
	_ = document.DefineAccessorProperty("cookie",
		vm.ToValue(func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(cookieValue)
		}),
		vm.ToValue(func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) > 0 {
				cookieValue = call.Arguments[0].String()
			}
			return goja.Undefined()
		}),
		goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = vm.Set("document", document)

	// 创建沙箱 location 对象
	location := vm.NewObject()
	_ = location.Set("href", "")
	_ = location.Set("hostname", "")
	_ = location.Set("pathname", "")
	_ = location.Set("reload", func() {})
	_ = vm.Set("location", location)

	// 创建沙箱 navigator 对象
	navigator := vm.NewObject()
	_ = navigator.Set("userAgent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	_ = vm.Set("navigator", navigator)

	// 创建沙箱 window 对象（循环引用）
	window := vm.NewObject()
	_ = window.Set("document", document)
	_ = window.Set("location", location)
	_ = window.Set("navigator", navigator)
	_ = vm.Set("window", window)

	// 提供常用的浏览器 API 空实现
	_ = vm.Set("setTimeout", func(fn goja.Callable, delay int64) {
		// 立即执行（简化处理）
		if fn != nil {
			_, _ = fn(goja.Undefined())
		}
	})
	_ = vm.Set("setInterval", func(fn goja.Callable, delay int64) {})
	_ = vm.Set("clearTimeout", func(id int64) {})
	_ = vm.Set("clearInterval", func(id int64) {})
	_ = vm.Set("console", vm.NewObject())
	_ = vm.Set("alert", func(msg string) {})

	// 执行脚本
	_, err := vm.RunString(scriptContent)
	if err != nil {
		// 检查是否是超时中断
		if strings.Contains(err.Error(), "timeout") {
			return "", errors.New("script execution timeout")
		}
		return "", fmt.Errorf("script execution failed: %v", err)
	}

	// 提取 cookie 名=值 部分（去掉 path、expires 等）
	if cookieValue != "" {
		parts := strings.Split(cookieValue, ";")
		return strings.TrimSpace(parts[0]), nil
	}

	return "", errors.New("no cookie produced by script")
}

// extractResponseHeaders 提取常用响应头
func extractResponseHeaders(resp *http.Response) map[string]string {
	headers := make(map[string]string)
	for _, key := range []string{"Content-Type", "X-RateLimit-Remaining", "X-RateLimit-Limit"} {
		if val := resp.Header.Get(key); val != "" {
			headers[key] = val
		}
	}
	return headers
}

// truncateCookie 截断 Cookie 用于日志输出
func truncateCookie(cookie string) string {
	if len(cookie) > 40 {
		return cookie[:20] + "..." + cookie[len(cookie)-10:]
	}
	return cookie
}
