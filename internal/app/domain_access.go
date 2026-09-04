package app

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const domainAccessRulesKey = "domain_access_rules"

type domainAccessRule struct {
	Host string `json:"host"`
	Mode string `json:"mode"`
}

func parseDomainAccessRules(raw string) ([]domainAccessRule, error) {
	var rules []domainAccessRule
	if strings.TrimSpace(raw) == "" {
		return rules, nil
	}
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, fmt.Errorf("must be a JSON array")
	}
	if len(rules) > 100 {
		return nil, fmt.Errorf("at most 100 addresses are allowed")
	}
	seen := make(map[string]struct{}, len(rules))
	for i := range rules {
		host, err := normalizeAccessTarget(rules[i].Host)
		if err != nil {
			return nil, err
		}
		rules[i].Host = host
		if rules[i].Mode != "web" && rules[i].Mode != "api" && rules[i].Mode != "both" {
			return nil, fmt.Errorf("invalid mode for %s", host)
		}
		if _, ok := seen[host]; ok {
			return nil, fmt.Errorf("duplicate address: %s", host)
		}
		seen[host] = struct{}{}
	}
	return rules, nil
}

func normalizeAccessTarget(value string) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(value))
	if ip := net.ParseIP(raw); ip != nil {
		if strings.Contains(ip.String(), ":") {
			return "[" + ip.String() + "]", nil
		}
		return ip.String(), nil
	}
	if len(raw) > 2 && raw[0] == '[' && raw[len(raw)-1] == ']' {
		if ip := net.ParseIP(raw[1 : len(raw)-1]); ip != nil {
			return "[" + ip.String() + "]", nil
		}
	}
	if raw == "" || strings.Contains(raw, "://") || strings.ContainsAny(raw, "/?#@") {
		return "", fmt.Errorf("invalid address: %s", value)
	}
	u, err := url.Parse("//" + raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" {
		return "", fmt.Errorf("invalid address: %s", value)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if !validAccessHost(host) {
		return "", fmt.Errorf("invalid address: %s", value)
	}
	port := u.Port()
	if port == "" && strings.Contains(raw, ":") {
		return "", fmt.Errorf("invalid port: %s", value)
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid port: %s", value)
		}
		return net.JoinHostPort(host, strconv.Itoa(n)), nil
	}
	return host, nil
}

func validAccessHost(host string) bool {
	if host == "localhost" || net.ParseIP(host) != nil {
		return true
	}
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func splitAccessTarget(target string) (string, string) {
	u, err := url.Parse("//" + target)
	if err != nil {
		return "", ""
	}
	return strings.ToLower(u.Hostname()), u.Port()
}

func domainAccessMode(rules []domainAccessRule, target string) string {
	if len(rules) == 0 {
		return "both"
	}
	for _, rule := range rules {
		if rule.Host == target {
			return rule.Mode
		}
	}
	targetHost, _ := splitAccessTarget(target)
	for _, rule := range rules {
		ruleHost, rulePort := splitAccessTarget(rule.Host)
		if rulePort == "" && ruleHost == targetHost {
			return rule.Mode
		}
	}
	return ""
}

func isProxyAPIPath(reqPath string) bool {
	return reqPath == "/v1" || strings.HasPrefix(reqPath, "/v1/") || reqPath == "/v1beta" || strings.HasPrefix(reqPath, "/v1beta/")
}

func (s *Server) domainAccessGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.configService == nil {
			c.Next()
			return
		}
		rules, err := parseDomainAccessRules(s.configService.GetString(domainAccessRulesKey, "[]"))
		if err != nil {
			c.Next() // 数据库被手工写坏时保持旧行为，避免锁死管理入口
			return
		}

		target, err := normalizeAccessTarget(c.Request.Host)
		if err != nil {
			target = ""
		}
		mode := domainAccessMode(rules, target)
		apiRequest := isProxyAPIPath(c.Request.URL.Path)
		if mode == "both" || mode == "api" && (apiRequest || c.Request.URL.Path == "/health") || mode == "web" && !apiRequest {
			c.Next()
			return
		}

		message := "此域名未绑定到当前服务"
		status := http.StatusMisdirectedRequest
		if mode == "api" {
			message, status = "此域名仅用于 API 请求", http.StatusForbidden
		} else if mode == "web" {
			message, status = "此域名仅用于网页访问", http.StatusForbidden
		}
		if apiRequest {
			c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"message": message, "type": "domain_access_denied"}})
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Data(status, "text/html; charset=utf-8", []byte(domainAccessErrorPage))
		c.Abort()
	}
}

type domainAccessTestRequest struct {
	Host   string `json:"host"`
	Scheme string `json:"scheme"`
}

func (s *Server) HandleTestDomainAccess(c *gin.Context) {
	var req domainAccessTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "请求格式无效")
		return
	}
	target, err := normalizeAccessTarget(req.Host)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}
	rules, err := parseDomainAccessRules(s.configService.GetString(domainAccessRulesKey, "[]"))
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "域名访问规则无效")
		return
	}
	var mode string
	for _, rule := range rules {
		if rule.Host == target {
			mode = rule.Mode
			break
		}
	}
	if mode == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "请先保存这条访问规则")
		return
	}

	schemes := []string{"https", "http"}
	if req.Scheme == "http" {
		schemes = []string{"http", "https"}
	} else if req.Scheme != "" && req.Scheme != "https" {
		RespondErrorMsg(c, http.StatusBadRequest, "仅支持 HTTP 或 HTTPS 测试")
		return
	}
	transport := http.DefaultTransport
	if s.client != nil && s.client.Transport != nil {
		transport = s.client.Transport
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	var failures []string
	for _, scheme := range schemes {
		address := scheme + "://" + target
		response, err := client.Get(address + "/health")
		if err != nil {
			failures = append(failures, scheme+": "+err.Error())
			continue
		}
		var health APIResponse[struct {
			Status  string `json:"status"`
			Service string `json:"service"`
		}]
		err = json.NewDecoder(response.Body).Decode(&health)
		response.Body.Close()
		if response.StatusCode == http.StatusOK && err == nil && health.Success && health.Data.Status == "ok" && health.Data.Service == "ccLoad" {
			RespondJSON(c, http.StatusOK, gin.H{"address": address, "mode": mode})
			return
		}
		failures = append(failures, fmt.Sprintf("%s: 未返回 ccLoad 健康响应 (HTTP %d)", scheme, response.StatusCode))
	}
	RespondErrorMsg(c, http.StatusBadGateway, "连接测试失败："+strings.Join(failures, "；"))
}

const domainAccessErrorPage = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>无法访问网页</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f8fafc;color:#0f172a;font:16px/1.6 system-ui,sans-serif}.card{width:min(440px,calc(100% - 48px));padding:40px;border:1px solid #e2e8f0;border-radius:16px;background:#fff;box-shadow:0 12px 32px #0f172a12}h1{margin:0 0 8px;font-size:24px}p{margin:0;color:#475569}code{display:block;margin-top:20px;padding:12px;border-radius:8px;background:#f1f5f9;color:#334155}@media(prefers-color-scheme:dark){body{background:#0f172a;color:#f8fafc}.card{border-color:#334155;background:#1e293b}p{color:#cbd5e1}code{background:#0f172a;color:#e2e8f0}}</style></head><body><main class="card"><h1>无法通过此域名访问网页</h1><p>当前域名未配置网页访问权限，请改用已绑定的网页域名打开管理后台。</p><code>API endpoints: /v1/* · /v1beta/*</code></main></body></html>`
