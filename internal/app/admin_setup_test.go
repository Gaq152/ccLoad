package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"ccLoad/internal/model"
	"ccLoad/internal/storage"
	"ccLoad/internal/util"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// newSetupTestStore 创建独立的测试数据库
func newSetupTestStore(t *testing.T) storage.Store {
	t.Helper()
	store, err := storage.CreateSQLiteStore(filepath.Join(t.TempDir(), "test.db"), nil)
	if err != nil {
		t.Fatalf("创建测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// clearPassEnv 清空密码相关环境变量（t.Setenv 自动在测试结束后恢复）
func clearPassEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CCLOAD_PASS", "")
	t.Setenv("CCLOAD_PASS_RESET", "")
	t.Setenv("CCLOAD_2FA_RESET", "")
}

func TestResolveAdminPassword(t *testing.T) {
	t.Run("无哈希无环境变量进入Setup模式", func(t *testing.T) {
		store := newSetupTestStore(t)
		clearPassEnv(t)

		hash, token := resolveAdminPassword(store)
		if hash != nil {
			t.Fatal("Setup模式下哈希应为nil")
		}
		// token 格式：XXXX-XXXX-XXXX（去易混淆字符集）
		if !regexp.MustCompile(`^[2-9A-HJKMNP-Z]{4}-[2-9A-HJKMNP-Z]{4}-[2-9A-HJKMNP-Z]{4}$`).MatchString(token) {
			t.Fatalf("Setup Token格式错误: %s", token)
		}
	})

	t.Run("CCLOAD_PASS播种入库", func(t *testing.T) {
		store := newSetupTestStore(t)
		clearPassEnv(t)
		t.Setenv("CCLOAD_PASS", "seed-password-123")

		hash, token := resolveAdminPassword(store)
		if token != "" {
			t.Fatal("播种后不应进入Setup模式")
		}
		if bcrypt.CompareHashAndPassword(hash, []byte("seed-password-123")) != nil {
			t.Fatal("返回的哈希应匹配环境变量密码")
		}
		// DB 已落库
		dbHash, err := store.GetAdminPasswordHash(context.Background())
		if err != nil || dbHash == "" {
			t.Fatalf("播种后DB应有哈希: err=%v", err)
		}
	})

	t.Run("DB哈希优先于环境变量", func(t *testing.T) {
		store := newSetupTestStore(t)
		clearPassEnv(t)
		// 先落库一个哈希
		dbHash := HashAdminPassword("db-password")
		if err := store.SaveAdminPasswordHash(context.Background(), string(dbHash)); err != nil {
			t.Fatalf("预置哈希失败: %v", err)
		}
		// 环境变量给不同密码 → 应被忽略
		t.Setenv("CCLOAD_PASS", "env-password-ignored")

		hash, _ := resolveAdminPassword(store)
		if bcrypt.CompareHashAndPassword(hash, []byte("db-password")) != nil {
			t.Fatal("DB哈希应优先，环境变量应被忽略")
		}
	})

	t.Run("CCLOAD_PASS_RESET强制重置并吊销会话", func(t *testing.T) {
		store := newSetupTestStore(t)
		clearPassEnv(t)
		ctx := context.Background()
		// 预置旧密码和一个会话
		_ = store.SaveAdminPasswordHash(ctx, string(HashAdminPassword("old-password")))
		_ = store.CreateAdminSession(ctx, "some-session-token", time.Now().Add(time.Hour))

		t.Setenv("CCLOAD_PASS_RESET", "reset-password-456")
		hash, _ := resolveAdminPassword(store)

		if bcrypt.CompareHashAndPassword(hash, []byte("reset-password-456")) != nil {
			t.Fatal("重置后哈希应匹配新密码")
		}
		sessions, _ := store.LoadAllSessions(ctx)
		if len(sessions) != 0 {
			t.Fatalf("重置后会话应全部吊销，剩余 %d 个", len(sessions))
		}
	})

	t.Run("CCLOAD_2FA_RESET删除两步验证绑定", func(t *testing.T) {
		store := newSetupTestStore(t)
		clearPassEnv(t)
		ctx := context.Background()
		_ = store.SaveAdminPasswordHash(ctx, string(HashAdminPassword("some-password")))
		_ = store.SaveAdmin2FA(ctx, &model.Admin2FA{
			Secret: "TESTSECRET", Status: model.Admin2FAStatusActive, CreatedAt: time.Now().Unix(),
		})

		t.Setenv("CCLOAD_2FA_RESET", "1")
		resolveAdminPassword(store)

		rec, _ := store.GetAdmin2FA(ctx)
		if rec != nil {
			t.Fatal("2FA绑定应已被删除")
		}
	})
}

// newTestAuthService 创建带速率限制器的测试 AuthService
func newTestAuthService(t *testing.T, store storage.Store, passwordHash []byte, setupToken string) *AuthService {
	t.Helper()
	limiter := util.NewLoginRateLimiter()
	svc := NewAuthService(passwordHash, setupToken, limiter, store, nil, nil)
	t.Cleanup(func() {
		svc.Close()
		limiter.Stop()
	})
	return svc
}

// postJSON 构造 gin 测试上下文发起 POST 请求
func postJSON(t *testing.T, handler gin.HandlerFunc, path string, payload any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body, _ := json.Marshal(payload)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", path, strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)

	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

func TestHandleSetup(t *testing.T) {
	store := newSetupTestStore(t)
	svc := newTestAuthService(t, store, nil, "ABCD-EFGH-JKMN")

	t.Run("错误令牌拒绝", func(t *testing.T) {
		w, _ := postJSON(t, svc.HandleSetup, "/setup", gin.H{
			"setup_token": "WRONG-TOKEN-XX", "password": "new-password-123",
		})
		if w.Code != 401 {
			t.Fatalf("错误令牌应返回401, got %d", w.Code)
		}
	})

	t.Run("弱密码拒绝", func(t *testing.T) {
		w, _ := postJSON(t, svc.HandleSetup, "/setup", gin.H{
			"setup_token": "ABCD-EFGH-JKMN", "password": "short",
		})
		if w.Code != 400 {
			t.Fatalf("弱密码应返回400, got %d", w.Code)
		}
	})

	t.Run("正确令牌完成初始化并签发会话", func(t *testing.T) {
		// 令牌输入做了归一化：小写+空格也应通过
		w, resp := postJSON(t, svc.HandleSetup, "/setup", gin.H{
			"setup_token": " abcd-efgh-jkmn ", "password": "new-password-123",
		})
		if w.Code != 200 {
			t.Fatalf("初始化应成功, got %d: %v", w.Code, resp)
		}
		data := resp["data"].(map[string]any)
		if data["token"] == "" {
			t.Fatal("初始化成功应直接签发会话token")
		}
		if !svc.HasPassword() {
			t.Fatal("初始化后应退出Setup模式")
		}
		// DB 已落库
		dbHash, _ := store.GetAdminPasswordHash(context.Background())
		if bcrypt.CompareHashAndPassword([]byte(dbHash), []byte("new-password-123")) != nil {
			t.Fatal("DB哈希应匹配新密码")
		}
	})

	t.Run("已初始化后再次调用拒绝", func(t *testing.T) {
		w, _ := postJSON(t, svc.HandleSetup, "/setup", gin.H{
			"setup_token": "ABCD-EFGH-JKMN", "password": "another-password",
		})
		if w.Code != 403 {
			t.Fatalf("已初始化应返回403, got %d", w.Code)
		}
	})
}

func TestHandleChangePassword(t *testing.T) {
	newSvc := func(t *testing.T) (*AuthService, storage.Store) {
		store := newSetupTestStore(t)
		_ = store.SaveAdminPasswordHash(context.Background(), string(HashAdminPassword("old-password-123")))
		svc := newTestAuthService(t, store, HashAdminPassword("old-password-123"), "")
		return svc, store
	}

	t.Run("旧密码错误拒绝", func(t *testing.T) {
		svc, _ := newSvc(t)
		w, _ := postJSON(t, svc.HandleChangePassword, "/admin/password/change", gin.H{
			"old_password": "wrong-password", "new_password": "new-password-456",
		})
		if w.Code != 401 {
			t.Fatalf("旧密码错误应返回401, got %d", w.Code)
		}
	})

	t.Run("新密码太短拒绝", func(t *testing.T) {
		svc, _ := newSvc(t)
		w, _ := postJSON(t, svc.HandleChangePassword, "/admin/password/change", gin.H{
			"old_password": "old-password-123", "new_password": "short",
		})
		if w.Code != 400 {
			t.Fatalf("弱密码应返回400, got %d", w.Code)
		}
	})

	t.Run("已绑定2FA时缺验证码拒绝", func(t *testing.T) {
		svc, store := newSvc(t)
		_ = store.SaveAdmin2FA(context.Background(), &model.Admin2FA{
			Secret: genTestSecret(t), Status: model.Admin2FAStatusActive, CreatedAt: time.Now().Unix(),
		})
		w, _ := postJSON(t, svc.HandleChangePassword, "/admin/password/change", gin.H{
			"old_password": "old-password-123", "new_password": "new-password-456",
		})
		if w.Code != 400 {
			t.Fatalf("缺2FA验证码应返回400, got %d", w.Code)
		}
	})

	t.Run("修改成功且吊销全部会话", func(t *testing.T) {
		svc, store := newSvc(t)
		ctx := context.Background()
		_ = store.CreateAdminSession(ctx, "session-a", time.Now().Add(time.Hour))

		w, _ := postJSON(t, svc.HandleChangePassword, "/admin/password/change", gin.H{
			"old_password": "old-password-123", "new_password": "new-password-456",
		})
		if w.Code != 200 {
			t.Fatalf("修改应成功, got %d", w.Code)
		}
		// 新哈希生效（内存热更新）
		svc.passwordMux.RLock()
		newHash := svc.passwordHash
		svc.passwordMux.RUnlock()
		if bcrypt.CompareHashAndPassword(newHash, []byte("new-password-456")) != nil {
			t.Fatal("内存哈希应已热更新为新密码")
		}
		// 会话全部吊销
		sessions, _ := store.LoadAllSessions(ctx)
		if len(sessions) != 0 {
			t.Fatalf("会话应全部吊销，剩余 %d 个", len(sessions))
		}
	})

	t.Run("已绑定2FA时正确验证码通过", func(t *testing.T) {
		svc, store := newSvc(t)
		secret := genTestSecret(t)
		_ = store.SaveAdmin2FA(context.Background(), &model.Admin2FA{
			Secret: secret, Status: model.Admin2FAStatusActive, CreatedAt: time.Now().Unix(),
		})
		w, resp := postJSON(t, svc.HandleChangePassword, "/admin/password/change", gin.H{
			"old_password": "old-password-123",
			"new_password": "new-password-456",
			"totp_code":    genCodeAt(t, secret, time.Now()),
		})
		if w.Code != 200 {
			t.Fatalf("带正确2FA码修改应成功, got %d: %v", w.Code, resp)
		}
	})
}
