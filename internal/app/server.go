package app

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"ccLoad/internal/config"
	"ccLoad/internal/cooldown"
	"ccLoad/internal/model"
	"ccLoad/internal/storage"
	"ccLoad/internal/util"
	"ccLoad/internal/validator"

	"github.com/gin-gonic/gin"
)

type Server struct {
	// ============================================================================
	// 服务层
	// ============================================================================
	authService   *AuthService   // 认证授权服务
	logService    *LogService    // 日志管理服务
	configService *ConfigService // 配置管理服务

	// ============================================================================
	// 核心字段
	// ============================================================================
	store            storage.Store
	channelCache     *storage.ChannelCache // 高性能渠道缓存层
	keySelector      *KeySelector          // Key选择器（多Key支持）
	cooldownManager  *cooldown.Manager     // 统一冷却管理器
	validatorManager *validator.Manager    // 渠道验证器管理器
	client     *http.Client // HTTP客户端（HTTP/2）
	kiroClient *http.Client // Kiro 专用客户端（utls + HTTP/1.1）

	// 异步统计（有界队列，避免每请求起goroutine）
	tokenStatsCh        chan tokenStatsUpdate
	tokenStatsDropCount atomic.Int64

	// 运行时配置（启动时从数据库加载，修改后重启生效）
	maxKeyRetries    int           // 单个渠道内最大Key重试次数
	firstByteTimeout time.Duration // 上游首字节超时（流式请求）
	nonStreamTimeout time.Duration // 非流式请求超时
	// 模型匹配配置（启动时从数据库加载，修改后重启生效）
	modelLookupStripDateSuffix bool // 未命中时去除末尾-YYYYMMDD日期后缀再匹配渠道（优先精确匹配）
	modelFuzzyMatch            bool // 未命中时启用模糊匹配（子串匹配+版本排序）

	// 登录速率限制器（用于传递给AuthService）
	loginRateLimiter *util.LoginRateLimiter

	// 并发控制
	concurrencySem chan struct{} // 信号量：限制最大并发请求数（防止goroutine爆炸）
	maxConcurrency int           // 最大并发数（默认1000）

	// 后台服务
	endpointTester   *EndpointTester       // 后台端点测速服务
	cooldownService  *CooldownService      // 冷却事件 SSE 广播服务
	activeReqManager *activeRequestManager // 活跃请求管理器
	monitorService   *MonitorService       // 请求监控服务
	traceStore       *storage.TraceStore   // 追踪数据存储（独立数据库）

	// 优雅关闭机制
	shutdownCh     chan struct{}  // 关闭信号channel
	shutdownDone   chan struct{}  // Shutdown完成信号（幂等）
	isShuttingDown atomic.Bool    // shutdown标志，防止向已关闭channel写入
	wg             sync.WaitGroup // 等待所有后台goroutine结束
}

func NewServer(store storage.Store) *Server {
	// 初始化ConfigService（优先从数据库加载配置,环境变量作Fallback）
	configService := NewConfigService(store)
	if err := configService.LoadDefaults(context.Background()); err != nil {
		log.Fatalf("❌ ConfigService初始化失败: %v", err)
	}
	log.Print("[INFO] ConfigService已加载系统配置（支持Web界面管理）")

	// 管理员密码：仅从环境变量读取（安全考虑：密码不应存储在数据库中）
	password := os.Getenv("CCLOAD_PASS")
	if password == "" {
		log.Print("❌ 未设置 CCLOAD_PASS，出于安全原因程序将退出。请设置强管理员密码后重试。")
		os.Exit(1)
	}

	log.Printf("[INFO] 管理员密码已从环境变量加载（长度: %d 字符）", len(password))
	log.Print("[INFO] API访问令牌将从数据库动态加载（支持Web界面管理）")

	// 从ConfigService读取运行时配置（启动时加载一次，修改后重启生效）
	// 配置验证已移至 ConfigService 的带约束 API（SRP）
	maxKeyRetries := configService.GetIntMin("max_key_retries", config.DefaultMaxKeyRetries, 1)

	// 超时配置（固定值，不再支持Web管理）
	firstByteTimeout := time.Duration(0)  // 流式请求首字节超时（0=禁用）
	nonStreamTimeout := 120 * time.Second // 非流式请求超时

	logRetentionDays := configService.GetInt("log_retention_days", 7)
	statsRetentionDays := configService.GetInt("stats_retention_days", 365)

	// 冷却时间配置
	cooldownMode := configService.GetString("cooldown_mode", "exponential")
	cooldownFixedInterval := configService.GetIntMin("cooldown_fixed_interval", 30, 1)
	util.SetCooldownConfig(cooldownMode, cooldownFixedInterval)

	// 模型匹配配置（启动时加载，修改后重启生效）
	modelLookupStripDateSuffix := configService.GetBool("model_lookup_strip_date_suffix", true)
	if modelLookupStripDateSuffix {
		log.Print("[INFO] 已启用模型日期后缀回退匹配：未命中时忽略末尾-YYYYMMDD日期后缀进行匹配（优先精确匹配）")
	}

	modelFuzzyMatch := configService.GetBool("model_fuzzy_match", false)
	if modelFuzzyMatch {
		log.Print("[INFO] 已启用模型模糊匹配：未命中时进行子串匹配并按版本排序选择最新模型")
	}

	// 最大并发数保留环境变量读取（启动参数，不支持Web管理）
	maxConcurrency := config.DefaultMaxConcurrency
	if concEnv := os.Getenv("CCLOAD_MAX_CONCURRENCY"); concEnv != "" {
		if val, err := strconv.Atoi(concEnv); err == nil && val > 0 {
			maxConcurrency = val
		}
	}

	// 构建HTTP Transport（使用统一函数，消除DRY违反）
	// TLS证书验证始终开启（安全默认值）
	transport := buildHTTPTransport(false)
	log.Print("[INFO] HTTP/2已启用（头部压缩+多路复用，HTTPS自动协商）")

	// 构建 Kiro 专用 Transport（utls 指纹伪装 + HTTP/1.1）
	kiroTransport := buildKiroHTTPTransport()
	log.Print("[INFO] Kiro TLS指纹伪装已启用（Chrome utls + HTTP/1.1）")

	s := &Server{
		store:            store,
		configService:    configService,
		loginRateLimiter: util.NewLoginRateLimiter(),

		// 运行时配置（启动时加载，修改后重启生效）
		maxKeyRetries:              maxKeyRetries,
		firstByteTimeout:           firstByteTimeout,
		nonStreamTimeout:           nonStreamTimeout,
		modelLookupStripDateSuffix: modelLookupStripDateSuffix,
		modelFuzzyMatch:            modelFuzzyMatch,

		// HTTP客户端
		client: &http.Client{
			Transport: transport,
			Timeout:   0, // 不设置全局超时，避免中断长时间任务
		},

		// Kiro 专用客户端（utls 指纹伪装 + HTTP/1.1）
		kiroClient: &http.Client{
			Transport: kiroTransport,
			Timeout:   0,
		},

		// 并发控制：使用信号量限制最大并发请求数
		concurrencySem: make(chan struct{}, maxConcurrency),
		maxConcurrency: maxConcurrency,

		// 初始化优雅关闭机制
		shutdownCh:   make(chan struct{}),
		shutdownDone: make(chan struct{}),

		// Token统计队列（避免每请求起goroutine）
		tokenStatsCh: make(chan tokenStatsUpdate, config.DefaultTokenStatsBufferSize),
	}

	// 初始化高性能缓存层（60秒TTL，避免数据库性能杀手查询）
	s.channelCache = storage.NewChannelCache(store, 60*time.Second)

	// 初始化冷却管理器（统一管理渠道级和Key级冷却）
	// 传入Server作为configGetter，利用缓存层查询渠道配置
	s.cooldownManager = cooldown.NewManager(store, s)

	// 初始化冷却事件 SSE 广播服务
	s.cooldownService = NewCooldownService(s.shutdownCh, &s.isShuttingDown)

	// 设置冷却事件回调（用于 SSE 推送）
	s.cooldownManager.SetCooldownCallbacks(
		s.cooldownService.BroadcastChannelCooldown,
		s.cooldownService.BroadcastKeyCooldown,
	)

	// 初始化渠道验证器管理器（支持88code套餐验证等扩展规则）
	s.validatorManager = validator.NewManager()

	// 初始化Key选择器（移除store依赖，避免重复查询）
	s.keySelector = NewKeySelector()

	// 初始化活跃请求管理器（用于追踪进行中的请求）
	s.activeReqManager = newActiveRequestManager()

	// ============================================================================
	// 创建服务层（仅保留有价值的服务）
	// ============================================================================

	// 1. LogService（负责日志管理）
	s.logService = NewLogService(
		store,
		config.DefaultLogBufferSize,
		config.DefaultLogWorkers,
		logRetentionDays,   // 日志保留天数（启动时读取，修改后重启生效）
		statsRetentionDays, // 统计数据保留天数
		s.shutdownCh,
		&s.isShuttingDown,
		&s.wg,
	)
	// 启动日志 Workers
	s.logService.StartWorkers()

	// 启动时补全历史统计数据（从日志聚合到 daily_stats 表）
	// 同步执行，确保在清理循环启动前完成聚合，避免数据丢失
	s.logService.BackfillDailyStats(context.Background())

	// 仅当保留天数>0时启动清理协程（-1表示永久保留，不清理）
	if logRetentionDays > 0 {
		s.logService.StartCleanupLoop()
	}

	// 2. AuthService（负责认证授权）
	// 初始化时自动从数据库加载API访问令牌
	s.authService = NewAuthService(
		password,
		s.loginRateLimiter,
		store, // 传入store用于热更新令牌
	)

	// 启动Token统计Worker（有界队列：性能可控，Shutdown可等待）
	s.wg.Add(1)
	go s.tokenStatsWorker()

	// 启动后台清理协程（Token 认证）
	s.wg.Add(1)
	go s.tokenCleanupLoop() // 定期清理过期Token

	// 启动 OAuth Token 定时刷新服务（Codex/Gemini 官方预设）
	s.wg.Add(1)
	go s.oauthRefreshLoop()

	// 启动后台端点测速服务（0=禁用）
	autoTestInterval := configService.GetInt("auto_test_endpoints_interval", 30)
	s.endpointTester = NewEndpointTester(s, autoTestInterval)
	s.endpointTester.Start()

	// 初始化请求监控服务（使用独立数据库）
	traceDBPath := filepath.Join("data", "debug_traces.db")
	traceStore, err := storage.NewTraceStore(traceDBPath)
	if err != nil {
		log.Printf("[WARN] 请求监控存储初始化失败: %v（监控功能不可用）", err)
	} else {
		s.traceStore = traceStore
		s.monitorService = NewMonitorService(traceStore, s.shutdownCh)
		// 从配置恢复监控开关状态
		if configService.GetBool("monitor_enabled", false) {
			s.monitorService.SetEnabled(true)
		}
		log.Print("[INFO] 请求监控服务已初始化")
	}

	// 启动时加载模型定价缓存（从 DB → 内存 sync.Map）
	s.loadPricingCache()

	return s

}

// ================== 缓存辅助函数 ==================

func (s *Server) getChannelCache() *storage.ChannelCache {
	if s == nil {
		return nil
	}
	return s.channelCache
}

// buildHTTPTransport 构建HTTP Transport（DRY：统一配置逻辑）
// 参数:
//   - skipTLSVerify: 是否跳过TLS证书验证
func buildHTTPTransport(skipTLSVerify bool) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   config.HTTPDialTimeout,
		KeepAlive: config.HTTPKeepAliveInterval,
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				_ = setTCPNoDelay(fd)
			})
		},
	}

	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment, // 支持 HTTPS_PROXY/HTTP_PROXY/NO_PROXY
		MaxIdleConns:        config.HTTPMaxIdleConns,
		MaxIdleConnsPerHost: config.HTTPMaxIdleConnsPerHost,
		IdleConnTimeout:     90 * time.Second, // 空闲连接90秒后关闭，避免僵尸连接
		MaxConnsPerHost:     config.HTTPMaxConnsPerHost,
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: config.HTTPTLSHandshakeTimeout,
		DisableCompression:  false,
		DisableKeepAlives:   false,
		ForceAttemptHTTP2:   true, // 启用标准库 HTTP/2（HTTPS 自动协商）
		TLSClientConfig: &tls.Config{
			ClientSessionCache: tls.NewLRUClientSessionCache(config.TLSSessionCacheSize),
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: skipTLSVerify,
		},
	}

	return transport // HTTP/2 已通过 ForceAttemptHTTP2 启用
}

// NOTE: 这些缓存fallback函数存在重复逻辑，可使用泛型重构（Go 1.18+）
// 当前设计选择：保持简单直接，避免过度抽象（YAGNI）

// GetConfig 获取渠道配置（实现cooldown.ConfigGetter接口）
// 优先使用缓存层（60秒TTL），降级到数据库查询
func (s *Server) GetConfig(ctx context.Context, channelID int64) (*model.Config, error) {
	if cache := s.getChannelCache(); cache != nil {
		return cache.GetConfig(ctx, channelID)
	}
	return s.store.GetConfig(ctx, channelID)
}

func (s *Server) GetEnabledChannelsByModel(ctx context.Context, model string) ([]*model.Config, error) {
	if cache := s.getChannelCache(); cache != nil {
		if channels, err := cache.GetEnabledChannelsByModel(ctx, model); err == nil {
			return channels, nil
		}
	}
	return s.store.GetEnabledChannelsByModel(ctx, model)
}

func (s *Server) GetEnabledChannelsByType(ctx context.Context, channelType string) ([]*model.Config, error) {
	if cache := s.getChannelCache(); cache != nil {
		if channels, err := cache.GetEnabledChannelsByType(ctx, channelType); err == nil {
			return channels, nil
		}
	}
	return s.store.GetEnabledChannelsByType(ctx, channelType)
}

func (s *Server) getAPIKeys(ctx context.Context, channelID int64) ([]*model.APIKey, error) {
	if cache := s.getChannelCache(); cache != nil {
		if keys, err := cache.GetAPIKeys(ctx, channelID); err == nil {
			return keys, nil
		}
	}
	return s.store.GetAPIKeys(ctx, channelID)
}

func (s *Server) getAllChannelCooldowns(ctx context.Context) (map[int64]time.Time, error) {
	if cache := s.getChannelCache(); cache != nil {
		if cooldowns, err := cache.GetAllChannelCooldowns(ctx); err == nil {
			return cooldowns, nil
		}
	}
	return s.store.GetAllChannelCooldowns(ctx)
}

func (s *Server) getAllKeyCooldowns(ctx context.Context) (map[int64]map[int]time.Time, error) {
	if cache := s.getChannelCache(); cache != nil {
		if cooldowns, err := cache.GetAllKeyCooldowns(ctx); err == nil {
			return cooldowns, nil
		}
	}
	return s.store.GetAllKeyCooldowns(ctx)
}

// InvalidateChannelListCache 使渠道列表缓存失效
// 在渠道CRUD操作后调用，确保缓存一致性
func (s *Server) InvalidateChannelListCache() {
	if cache := s.getChannelCache(); cache != nil {
		cache.InvalidateCache()
	}
}

// InvalidateAPIKeysCache 使指定渠道的 API Keys 缓存失效
// 在渠道Key更新后调用，确保缓存一致性
func (s *Server) InvalidateAPIKeysCache(channelID int64) {
	if cache := s.getChannelCache(); cache != nil {
		cache.InvalidateAPIKeysCache(channelID)
	}
}

// InvalidateAllAPIKeysCache 使所有 API Keys 缓存失效
// 在批量导入操作后调用，确保缓存一致性
func (s *Server) InvalidateAllAPIKeysCache() {
	if cache := s.getChannelCache(); cache != nil {
		cache.InvalidateAllAPIKeysCache()
	}
}

func (s *Server) invalidateCooldownCache() {
	if cache := s.getChannelCache(); cache != nil {
		cache.InvalidateCooldownCache()
	}
}

// invalidateChannelRelatedCache 统一失效渠道相关的所有缓存
// 在渠道CRUD、冷却状态变更后调用
func (s *Server) invalidateChannelRelatedCache(channelID int64) {
	s.InvalidateChannelListCache()
	s.InvalidateAPIKeysCache(channelID)
	s.invalidateCooldownCache()
}

// GetWriteTimeout 返回建议的 HTTP WriteTimeout
// 确保传输层超时不小于业务层非流式超时，避免长流被HTTP层过早切断
func (s *Server) GetWriteTimeout() time.Duration {
	const minWriteTimeout = 120 * time.Second
	if s.nonStreamTimeout > minWriteTimeout {
		return s.nonStreamTimeout
	}
	return minWriteTimeout
}

// SetupRoutes - 新的路由设置函数，适配Gin
func (s *Server) SetupRoutes(r *gin.Engine) {
	// 公开访问的API（代理服务）- 需要 API 认证
	// 透明代理：统一处理所有 /v1/* 端点，支持所有HTTP方法
	apiV1 := r.Group("/v1")
	apiV1.Use(s.authService.RequireAPIAuth())
	{
		apiV1.Any("/*path", s.HandleProxyRequest)
	}
	apiV1Beta := r.Group("/v1beta")
	apiV1Beta.Use(s.authService.RequireAPIAuth())
	{
		apiV1Beta.Any("/*path", s.HandleProxyRequest)
	}

	// 健康检查（公开访问，无需认证，K8s liveness/readiness probe）
	r.GET("/health", s.HandleHealth)

	// 公开访问的API（首页仪表盘数据）
	// [SECURITY NOTE] /public/* 端点故意不做认证，用于首页展示。
	// 如需隐藏运营数据，可添加 s.authService.RequireTokenAuth() 中间件。
	public := r.Group("/public")
	{
		public.GET("/summary", s.HandlePublicSummary)
		public.GET("/channel-types", s.HandleGetChannelTypes)
		public.GET("/models", s.HandlePublicModels) // 获取所有渠道支持的模型列表
	}

	// 登录相关（公开访问）
	r.POST("/login", s.authService.HandleLogin)
	r.POST("/logout", s.authService.HandleLogout)

	// 需要身份验证的admin APIs（使用Token认证）
	admin := r.Group("/admin")
	admin.Use(s.authService.RequireTokenAuth())
	admin.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Next()
	})
	{
		// 渠道管理
		admin.GET("/channels", s.HandleChannels)
		admin.POST("/channels", s.HandleChannels)
		admin.GET("/channels/export", s.HandleExportChannelsCSV)
		admin.POST("/channels/import", s.HandleImportChannelsCSV)
		admin.POST("/channels/reorder", s.HandleReorderChannels) // 批量更新渠道排序（拖拽排序）
		admin.GET("/channels/:id", s.HandleChannelByID)
		admin.PUT("/channels/:id", s.HandleChannelByID)
		admin.DELETE("/channels/:id", s.HandleChannelByID)
		admin.GET("/channels/:id/keys", s.HandleChannelKeys)
		admin.POST("/channels/models/fetch", s.HandleFetchModelsPreview) // 临时渠道配置获取模型列表
		admin.POST("/models/cheapest", s.HandleSelectCheapestModel)      // 选择最低计费模型（用于测试默认选择）
		admin.GET("/channels/:id/models/fetch", s.HandleFetchModels)     // 获取渠道可用模型列表(新增)
		admin.POST("/channels/:id/models", s.HandleAddModels)            // 添加渠道模型
		admin.DELETE("/channels/:id/models", s.HandleDeleteModels)       // 删除渠道模型
		admin.POST("/channels/:id/test", s.HandleChannelTest)
		admin.POST("/channels/:id/cooldown", s.HandleSetChannelCooldown)
		admin.POST("/channels/:id/keys/:keyIndex/cooldown", s.HandleSetKeyCooldown)
		admin.DELETE("/channels/:id/keys/:keyIndex", s.HandleDeleteAPIKey)

		// 端点管理（多URL支持）
		admin.GET("/channels/:id/endpoints", s.HandleChannelEndpoints)
		admin.PUT("/channels/:id/endpoints", s.HandleChannelEndpoints)
		admin.POST("/channels/:id/endpoints/test", s.HandleTestEndpoints)
		admin.PUT("/channels/:id/endpoints/active", s.HandleSetActiveEndpoint)
		admin.GET("/endpoints/status", s.HandleEndpointsStatus) // 测速状态（前端倒计时）

		// 渠道用量监控
		admin.POST("/channels/:id/quota/fetch", s.handleQuotaFetch)
		admin.GET("/quota/fetch-all", s.handleQuotaFetchAll) // 批量用量查询（SSE）

		// OAuth Token 代理（用于 Codex 渠道 OAuth 流程）
		admin.POST("/oauth/token", s.HandleOAuthToken)
		admin.POST("/oauth/pkce", s.HandleGeneratePKCE)

		// Kiro Token 刷新
		admin.POST("/kiro/refresh", s.HandleKiroRefresh)
		// Kiro 获取邮箱
		admin.POST("/kiro/email", s.HandleKiroGetEmail)
		// Kiro 生成设备指纹
		admin.GET("/kiro/fingerprint/generate", s.HandleKiroGenerateFingerprint)

		// 统计分析
		admin.GET("/logs", s.HandleErrors)
		admin.GET("/metrics", s.HandleMetrics)
		admin.GET("/stats", s.HandleStats)
		admin.GET("/cooldown/stats", s.HandleCooldownStats)
		admin.GET("/cache/stats", s.HandleCacheStats)
		admin.GET("/models", s.HandleGetModels)

		// 渠道健康监控（第三方数据代理）
		admin.GET("/channel-health-proxy", s.handleChannelHealthProxy)

		// API访问令牌管理
		admin.GET("/auth-tokens", s.HandleListAuthTokens)
		admin.POST("/auth-tokens", s.HandleCreateAuthToken)
		admin.PUT("/auth-tokens/:id", s.HandleUpdateAuthToken)
		admin.DELETE("/auth-tokens/:id", s.HandleDeleteAuthToken)
		admin.GET("/auth-tokens/:id/channels", s.HandleGetTokenChannels) // 获取令牌渠道配置（2025-12新增）
		admin.PUT("/auth-tokens/:id/channels", s.HandleSetTokenChannels) // 设置令牌渠道配置（2025-12新增）

		// 模型定价管理
		admin.GET("/pricing", s.HandleListModelPricing)
		admin.POST("/pricing", s.HandleCreateModelPricing)
		admin.PUT("/pricing/:id", s.HandleUpdateModelPricing)
		admin.DELETE("/pricing/:id", s.HandleDeleteModelPricing)
		admin.POST("/pricing/defaults", s.HandleImportDefaultPricing)

		// 系统配置管理
		admin.GET("/settings", s.AdminListSettings)
		admin.GET("/settings/:key", s.AdminGetSetting)
		admin.PUT("/settings/:key", s.AdminUpdateSetting)
		admin.POST("/settings/:key/reset", s.AdminResetSetting)
		admin.POST("/settings/batch", s.AdminBatchUpdateSettings)

		// 日志实时推送（SSE）
		admin.GET("/logs/stream", s.HandleLogSSE)
		admin.GET("/logs/active", s.HandleActiveRequests)

		// 冷却事件实时推送（SSE）
		admin.GET("/cooldown/stream", s.HandleCooldownSSE)

		// 请求监控
		admin.GET("/monitor/status", s.HandleMonitorStatus)
		admin.POST("/monitor/toggle", s.HandleMonitorToggle)
		admin.GET("/monitor/stream", s.HandleMonitorSSE)
		admin.GET("/monitor/traces", s.HandleMonitorList)
		admin.GET("/monitor/traces/:id", s.HandleMonitorDetail)
		admin.DELETE("/monitor/traces", s.HandleMonitorClear)
		admin.GET("/monitor/stats", s.HandleMonitorStats)
	}

	// 静态文件服务（安全）：使用框架自带的静态文件路由，自动做路径清理，防止目录遍历
	// 等价于 http.FileServer，避免手工拼接路径导致的 /web/../ 泄露
	r.Static("/web", "./web")

	// 默认首页重定向
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/web/index.html")
	})
}

// 说明：已改为使用 r.Static("/web", "./web") 提供静态文件服务，
// 该实现会自动进行路径清理和越界防护，避免目录遍历风险。

// Token清理循环（定期清理过期Token）
// 支持优雅关闭
func (s *Server) tokenCleanupLoop() {
	defer func() {
		log.Print("[DEBUG] tokenCleanupLoop 退出")
		s.wg.Done()
	}()

	ticker := time.NewTicker(config.TokenCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.shutdownCh:
			// 优先检查shutdown信号,快速响应关闭
			// 移除shutdown时的额外清理,避免潜在的死锁或延迟
			// Token清理不是关键路径,可以在下次启动时清理过期Token
			return
		case <-ticker.C:
			s.authService.CleanExpiredTokens()
		}
	}
}

// AddLogAsync 异步添加日志（委托给LogService处理）
// 在代理请求完成后调用，记录请求日志
func (s *Server) AddLogAsync(entry *model.LogEntry) {
	// 委托给 LogService 处理日志写入
	s.logService.AddLogAsync(entry)
}

// getModelsByChannelType 获取指定渠道类型的去重模型列表
func (s *Server) getModelsByChannelType(ctx context.Context, channelType string) ([]string, error) {
	// 直接查询数据库（KISS原则，避免过度设计）
	channels, err := s.store.GetEnabledChannelsByType(ctx, channelType)
	if err != nil {
		return nil, err
	}
	modelSet := make(map[string]struct{})
	for _, cfg := range channels {
		for _, modelName := range cfg.Models {
			modelSet[modelName] = struct{}{}
		}
	}
	models := make([]string, 0, len(modelSet))
	for name := range modelSet {
		models = append(models, name)
	}
	return models, nil
}

// getAllModels 获取所有启用渠道的去重模型列表
func (s *Server) getAllModels(ctx context.Context) ([]string, error) {
	channels, err := s.store.ListConfigs(ctx)
	if err != nil {
		return nil, err
	}
	modelSet := make(map[string]struct{})
	for _, cfg := range channels {
		if !cfg.Enabled {
			continue
		}
		for _, modelName := range cfg.Models {
			modelSet[modelName] = struct{}{}
		}
	}
	models := make([]string, 0, len(modelSet))
	for name := range modelSet {
		models = append(models, name)
	}
	return models, nil
}

// [INFO] 修复：handleChannelKeys 路由处理器(2025-10新架构支持)
// GET /admin/channels/:id/keys - 获取渠道的所有API Keys
func (s *Server) HandleChannelKeys(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid channel id")
		return
	}
	s.handleGetChannelKeys(c, id)
}

// 优雅关闭Server
// PrepareShutdown 预关闭：关闭 shutdownCh 通知所有 SSE 连接断开
// 应在 httpServer.Shutdown() 之前调用，让长连接主动断开
func (s *Server) PrepareShutdown() {
	if s.isShuttingDown.Swap(true) {
		return // 已经在关闭中
	}
	log.Print("🛑 正在通知 SSE 连接关闭...")
	close(s.shutdownCh)
}

// Shutdown 优雅关闭Server，等待所有后台goroutine完成
// 参数ctx用于控制最大等待时间，超时后强制退出
// 返回值：nil表示成功，context.DeadlineExceeded表示超时
func (s *Server) Shutdown(ctx context.Context) error {
	// 检查是否已经完成关闭（幂等）
	select {
	case <-s.shutdownDone:
		return nil
	default:
	}

	// 如果 PrepareShutdown 没被调用，这里关闭 shutdownCh
	if !s.isShuttingDown.Swap(true) {
		close(s.shutdownCh)
	}
	defer close(s.shutdownDone)

	log.Print("🛑 正在关闭Server，等待后台任务完成...")

	// 停止后台端点测速服务
	if s.endpointTester != nil {
		s.endpointTester.Stop()
	}

	// 关闭冷却事件 SSE 服务
	if s.cooldownService != nil {
		s.cooldownService.Shutdown()
	}

	// 停止LoginRateLimiter的cleanupLoop
	if s.loginRateLimiter != nil {
		s.loginRateLimiter.Stop()
	}

	// 关闭AuthService的后台worker
	if s.authService != nil {
		s.authService.Close()
	}

	// 使用channel等待所有goroutine完成
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	// 等待完成或超时
	var err error
	select {
	case <-done:
		log.Print("[INFO] Server优雅关闭完成")
	case <-ctx.Done():
		log.Print("[WARN]  Server关闭超时，部分后台任务可能未完成")
		err = ctx.Err()
	}

	// 无论成功还是超时，都要关闭数据库连接
	// 先关闭追踪存储（独立数据库）
	if s.traceStore != nil {
		if closeErr := s.traceStore.Close(); closeErr != nil {
			log.Printf("[WARN] 关闭追踪数据库失败: %v", closeErr)
		}
	}

	// 再关闭主数据库连接
	if closer, ok := s.store.(interface{ Close() error }); ok {
		if closeErr := closer.Close(); closeErr != nil {
			log.Printf("❌ 关闭数据库连接失败: %v", closeErr)
		}
	}

	return err
}
