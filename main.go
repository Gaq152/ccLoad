package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ccLoad/internal/app"
	"ccLoad/internal/storage"
	"ccLoad/internal/storage/redis"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// defaultTrustedProxies 默认可信代理（私有网段 + 共享地址空间）
var defaultTrustedProxies = []string{
	"10.0.0.0/8",     // Class A 私有 (RFC 1918)
	"172.16.0.0/12",  // Class B 私有 (RFC 1918)
	"192.168.0.0/16", // Class C 私有 (RFC 1918)
	"100.64.0.0/10",  // 共享地址空间 (RFC 6598, 运营商级NAT/CGNAT)
	"127.0.0.0/8",    // Loopback
	"::1/128",        // IPv6 Loopback
}

// getTrustedProxies 获取可信代理配置
// 环境变量 TRUSTED_PROXIES: 逗号分隔的 CIDR，"none" 表示不信任任何代理
// 未设置时返回私有网段默认值
func getTrustedProxies() []string {
	v := os.Getenv("TRUSTED_PROXIES")
	if v == "" {
		return defaultTrustedProxies
	}
	if v == "none" {
		return nil
	}
	var proxies []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			proxies = append(proxies, p)
		}
	}
	if len(proxies) == 0 {
		return nil
	}
	return proxies
}

func main() {
	// 优先读取.env文件
	if err := godotenv.Load(); err != nil {
		log.Printf("No .env file found: %v", err)
	}

	// 设置Gin运行模式
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode) // 生产模式
	}

	// 初始化Redis同步客户端 (可选功能)
	redisURL := os.Getenv("REDIS_URL")
	redisSync, err := redis.NewRedisSync(redisURL)
	if err != nil {
		log.Fatalf("Redis初始化失败: %v", err)
	}
	defer redisSync.Close()

	if redisSync.IsEnabled() {
		log.Printf("Redis同步已启用")
	} else {
		log.Printf("Redis同步未配置")
	}

	// 使用工厂函数创建存储实例（自动识别MySQL/SQLite）
	// [FIX] 2025-12：初始化逻辑（迁移→恢复→启动同步）已收敛到 NewStore()
	store, err := storage.NewStore(redisSync)
	if err != nil {
		log.Fatalf("存储初始化失败: %v", err)
	}

	// 渠道仅从数据库管理与读取；不再从本地文件初始化。

	srv := app.NewServer(store)

	// 创建Gin引擎
	r := gin.New()

	// 配置可信代理，防止 X-Forwarded-For 伪造绕过登录限速
	// TRUSTED_PROXIES 环境变量：逗号分隔的 CIDR 列表，设为 "none" 则不信任任何代理
	// 未配置时默认信任私有网段（适用于内网反向代理场景）
	trustedProxies := getTrustedProxies()
	if trustedProxies == nil {
		if err := r.SetTrustedProxies(nil); err != nil {
			log.Fatalf("[FATAL] 设置可信代理失败: %v", err)
		}
		log.Printf("[CONFIG] 可信代理: 无 (直接暴露)")
	} else {
		if err := r.SetTrustedProxies(trustedProxies); err != nil {
			log.Fatalf("[FATAL] 设置可信代理失败: %v", err)
		}
		log.Printf("[CONFIG] 可信代理: %v", trustedProxies)
	}

	// 添加基础中间件
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// 注册路由
	srv.SetupRoutes(r)

	// session清理循环在NewServer中已启动，避免重复启动

	addr := ":8080"
	if v := os.Getenv("PORT"); v != "" {
		if !strings.HasPrefix(v, ":") {
			v = ":" + v
		}
		addr = v
	}

	// 使用http.Server支持优雅关闭
	writeTimeout := srv.GetWriteTimeout()
	httpServer := &http.Server{
		Addr:    addr,
		Handler: r,

		// ✅ 深度防御：传输层超时保护（抵御slowloris等慢速攻击）
		// 即使绕过应用层并发控制，也会在HTTP层被杀死
		ReadHeaderTimeout: 5 * time.Second,   // 防止慢速发送header（slowloris攻击）
		ReadTimeout:       120 * time.Second, // 防止慢速发送body（兼容长请求）
		WriteTimeout:      writeTimeout,      // 防止慢速读取响应（兼容流式输出）
		IdleTimeout:       60 * time.Second,  // 防止keep-alive连接占用fd
	}
	log.Printf("[CONFIG] HTTP WriteTimeout: %v", writeTimeout)

	// 启动HTTP服务器（在goroutine中）
	go func() {
		log.Printf("listening on %s", addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP服务器启动失败: %v", err)
		}
	}()

	// 监听系统信号，实现优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// ✅ 停止信号监听,释放signal.Notify创建的后台goroutine
	signal.Stop(quit)
	close(quit)

	log.Println("收到关闭信号，正在优雅关闭服务器...")

	// 先通知 SSE 连接关闭，让长连接主动断开
	srv.PrepareShutdown()
	// 给 SSE 连接一点时间断开
	time.Sleep(100 * time.Millisecond)

	// 设置5秒超时用于HTTP服务器关闭
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 关闭HTTP服务器
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP服务器关闭超时: %v，强制关闭连接", err)
		// 超时后强制关闭，防止streaming连接阻塞退出
		_ = httpServer.Close()
	}

	// 关闭Server后台任务（设置10秒超时）
	taskShutdownCtx, taskCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer taskCancel()

	if err := srv.Shutdown(taskShutdownCtx); err != nil {
		log.Printf("Server后台任务关闭错误: %v", err)
	}

	log.Println("✅ 服务器已优雅关闭")
}
