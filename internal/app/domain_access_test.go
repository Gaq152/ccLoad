package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ccLoad/internal/model"

	"github.com/gin-gonic/gin"
)

func TestDomainAccessRules(t *testing.T) {
	rules, err := parseDomainAccessRules(`[{"host":"WEB.Example.com.","mode":"web"},{"host":"127.0.0.1:8080","mode":"api"},{"host":"[::1]:8443","mode":"both"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if got := domainAccessMode(rules, "web.example.com"); got != "web" {
		t.Fatalf("web mode = %q", got)
	}
	if got := domainAccessMode(rules, "127.0.0.1:8080"); got != "api" {
		t.Fatalf("IP port mode = %q", got)
	}
	if got := domainAccessMode(rules, "[::1]:8443"); got != "both" {
		t.Fatalf("IPv6 port mode = %q", got)
	}
	if got, err := normalizeAccessTarget("LOCALHOST:080"); err != nil || got != "localhost:80" {
		t.Fatalf("normalized address = %q, %v", got, err)
	}
	if got := domainAccessMode(rules, "unknown.example.com"); got != "" {
		t.Fatalf("unknown mode = %q", got)
	}
	if !isProxyAPIPath("/v1/messages") || isProxyAPIPath("/admin/settings") {
		t.Fatal("API path classification failed")
	}
	if _, err := parseDomainAccessRules(`[{"host":"api.example.com/path","mode":"api"}]`); err == nil {
		t.Fatal("invalid domain accepted")
	}
	if _, err := parseDomainAccessRules(`[{"host":"127.0.0.1:99999","mode":"api"}]`); err == nil {
		t.Fatal("invalid port accepted")
	}
}

func TestDomainAccessGuard(t *testing.T) {
	s := &Server{configService: &ConfigService{cache: map[string]*model.SystemSetting{
		domainAccessRulesKey: {Value: `[{"host":"web.example.com","mode":"web"},{"host":"web.example.com:8443","mode":"api"},{"host":"127.0.0.1:8080","mode":"api"}]`},
	}}}
	r := gin.New()
	r.Use(s.domainAccessGuard())
	r.Any("/*path", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, test := range []struct {
		host, path string
		want       int
	}{
		{"web.example.com", "/", http.StatusNoContent},
		{"web.example.com", "/v1/messages", http.StatusForbidden},
		{"web.example.com:8443", "/", http.StatusForbidden},
		{"web.example.com:8443", "/v1/messages", http.StatusNoContent},
		{"127.0.0.1:8080", "/v1/messages", http.StatusNoContent},
		{"other.example.com", "/", http.StatusMisdirectedRequest},
	} {
		req := httptest.NewRequest(http.MethodGet, test.path, nil)
		req.Host = test.host
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if res.Code != test.want {
			t.Errorf("%s%s: got %d, want %d", test.host, test.path, res.Code, test.want)
		}
	}
}

func TestDomainAccessConnection(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"data":{"status":"ok","service":"ccLoad"}}`))
	}))
	defer target.Close()
	host := strings.TrimPrefix(target.URL, "http://")
	s := &Server{
		client: target.Client(),
		configService: &ConfigService{cache: map[string]*model.SystemSetting{
			domainAccessRulesKey: {Value: `[{"host":"` + host + `","mode":"api"}]`},
		}},
	}
	r := gin.New()
	r.POST("/test", s.HandleTestDomainAccess)
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(`{"host":"`+host+`","scheme":"http"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("connection test = %d: %s", res.Code, res.Body.String())
	}
}
